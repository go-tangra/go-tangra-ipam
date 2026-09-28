package kvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Login failure kinds. errors.Is on a StartSession error (or a proxied login
// failure) names the kind; the bmc package maps them to the documented
// reasons. The error text carries the BMC host and HTTP statuses only, never
// a credential or a session token.
var (
	ErrUnreachable  = errors.New("kvm: BMC unreachable")
	ErrAuthFailed   = errors.New("kvm: BMC rejected the credentials")
	ErrTwoFactor    = errors.New("kvm: BMC requires two-factor login")
	ErrSessionLimit = errors.New("kvm: BMC session limit reached")
	ErrLogin        = errors.New("kvm: BMC login failed")

	// errUnsupported: the BMC does not offer that login flow (the other one
	// is tried).
	errUnsupported = errors.New("kvm: login flow not offered")
)

// redfishSessions is the Redfish session collection (DSP0266).
const redfishSessions = "/redfish/v1/SessionService/Sessions"

// bmcAuth is what proxied requests carry to the BMC: a Cookie header value
// (the legacy SID, or cookies the Redfish login set) and, for Redfish-based
// firmware, the X-Auth-Token header. sessionPath/sessionID name the Redfish
// session to DELETE on logout.
type bmcAuth struct {
	cookie      string
	token       string
	sessionPath string
	sessionID   string
}

func (a bmcAuth) empty() bool { return a.cookie == "" && a.token == "" }

// loginError is a classified login failure.
type loginError struct {
	host   string
	kind   error
	detail string
}

func (e *loginError) Error() string { return "kvm: login to " + e.host + " failed: " + e.detail }

func (e *loginError) Unwrap() error { return e.kind }

// login authenticates to the BMC web UI, supporting both Supermicro firmware
// generations (ported from v3). Newer firmware (X12, web UI 1.8.x) no longer
// honours the login form: its JavaScript creates a Redfish session
// (POST /redfish/v1/SessionService/Sessions {"UserName","Password"}) and sends
// the X-Auth-Token on every call; older firmware authenticates with the SID
// cookie of POST /cgi/login.cgi; the X11 generation between them wants both.
// Redfish goes first and whatever each flow yields is merged. A Redfish
// answer that settles the outcome (credentials rejected, two-factor login,
// session limit, unreachable) stops there, so a wrong password costs one
// failed attempt, not two.
func (m *Manager) login(ctx context.Context, host string, creds Creds) (bmcAuth, error) {
	if creds.Username == "" || creds.Password == "" {
		return bmcAuth{}, &loginError{host, ErrAuthFailed, "no credentials"}
	}
	rf, rfErr := m.loginRedfish(ctx, host, creds)
	if rfErr != nil && !errors.Is(rfErr, errUnsupported) {
		return bmcAuth{}, rfErr
	}
	if rfErr == nil && strings.Contains(rf.cookie, "SID=") {
		return rf, nil
	}
	lg, lgErr := m.loginLegacy(ctx, host, creds)
	switch {
	case lgErr == nil:
		rf.cookie = mergeCookies(rf.cookie, lg.cookie)
		return rf, nil
	case rfErr == nil:
		return rf, nil // Redfish alone (X12 answers the form without a SID)
	case errors.Is(lgErr, ErrLogin):
		return bmcAuth{}, &loginError{host, ErrLogin, rfErr.(*loginError).detail + "; " + lgErr.(*loginError).detail}
	}
	return bmcAuth{}, lgErr
}

// loginRedfish creates a Redfish session the way the BMC's own redfish.js
// does (_doLogin): a JSON body {"UserName","Password"}; the token comes back
// in the X-Auth-Token header, the session id in the body ("Id") and its URI
// in Location / "@odata.id". A session pending a two-factor code is deleted
// again: the console cannot answer the code.
func (m *Manager) loginRedfish(ctx context.Context, host string, creds Creds) (bmcAuth, error) {
	body, _ := json.Marshal(map[string]string{"UserName": creds.Username, "Password": creds.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.scheme+"://"+host+redfishSessions, bytes.NewReader(body))
	if err != nil {
		return bmcAuth{}, &loginError{host, ErrLogin, "redfish request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return bmcAuth{}, &loginError{host, ErrUnreachable, "redfish: " + transportError(err)}
	}
	defer drain(resp)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	status := fmt.Sprintf("redfish status %d", resp.StatusCode)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	switch {
	case !ok && bytes.Contains(raw, []byte("SessionLimitExceeded")):
		return bmcAuth{}, &loginError{host, ErrSessionLimit, status}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return bmcAuth{}, &loginError{host, ErrAuthFailed, status}
	case !ok:
		return bmcAuth{}, &loginError{host, errUnsupported, status}
	}
	a := bmcAuth{token: resp.Header.Get("X-Auth-Token")}
	if a.token == "" {
		return bmcAuth{}, &loginError{host, errUnsupported, status + " without X-Auth-Token"}
	}
	var doc struct {
		ID    string `json:"Id"`
		OData string `json:"@odata.id"`
		Oem   struct {
			Supermicro struct {
				TwoFAEnabled bool `json:"TwoFAEnabled"`
			} `json:"Supermicro"`
		} `json:"Oem"`
	}
	_ = json.Unmarshal(raw, &doc)
	a.sessionPath = sessionPath(resp.Header.Get("Location"), doc.OData, doc.ID)
	a.sessionID = sessionID(a.sessionPath, doc.ID)
	var cookies []string
	for _, c := range resp.Cookies() {
		if c.Value != "" {
			cookies = append(cookies, c.Name+"="+c.Value)
		}
	}
	a.cookie = strings.Join(cookies, "; ")
	if doc.Oem.Supermicro.TwoFAEnabled {
		m.logout(context.WithoutCancel(ctx), host, a)
		return bmcAuth{}, &loginError{host, ErrTwoFactor, "two-factor authentication is enabled for the user"}
	}
	return a, nil
}

// loginLegacy is the form login -> SID cookie. check=00 is the hidden field
// of the ATEN login form that later firmware expects.
func (m *Manager) loginLegacy(ctx context.Context, host string, creds Creds) (bmcAuth, error) {
	form := url.Values{"name": {creds.Username}, "pwd": {creds.Password}, "check": {"00"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.scheme+"://"+host+"/cgi/login.cgi", strings.NewReader(form.Encode()))
	if err != nil {
		return bmcAuth{}, &loginError{host, ErrLogin, "legacy request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return bmcAuth{}, &loginError{host, ErrUnreachable, "legacy: " + transportError(err)}
	}
	defer drain(resp)
	for _, c := range resp.Cookies() {
		if c.Name == "SID" && c.Value != "" {
			return bmcAuth{cookie: "SID=" + c.Value}, nil
		}
	}
	status := fmt.Sprintf("legacy: no SID cookie (status %d)", resp.StatusCode)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return bmcAuth{}, &loginError{host, ErrAuthFailed, status}
	}
	return bmcAuth{}, &loginError{host, ErrLogin, status}
}

// logout ends a BMC web session, best-effort: the Redfish session is deleted
// (freeing one of the BMC's few session slots) and a legacy SID logged out.
func (m *Manager) logout(ctx context.Context, host string, a bmcAuth) {
	ctx, cancel := context.WithTimeout(ctx, logoutTimeout)
	defer cancel()
	send := func(method, p string) {
		// The host is the BMC we logged in to; p is fixed or a path
		// sessionPath confined to the Redfish session collection.
		req, err := http.NewRequestWithContext(ctx, method, m.scheme+"://"+host+p, nil) // #nosec G704 -- see above
		if err != nil {
			return
		}
		if a.token != "" {
			req.Header.Set("X-Auth-Token", a.token)
		}
		if a.cookie != "" {
			req.Header.Set("Cookie", a.cookie)
		}
		resp, err := m.httpClient.Do(req) // #nosec G704 -- BMC host and validated session path (above)
		if err != nil {
			m.warn("kvm bmc logout", "host", host, "err", transportError(err))
			return
		}
		drain(resp)
	}
	if a.sessionPath != "" {
		send(http.MethodDelete, a.sessionPath)
	}
	if strings.Contains(a.cookie, "SID=") {
		send(http.MethodGet, "/cgi/logout.cgi")
	}
}

// sessionPath is the Redfish session URI to delete: the path of Location,
// else "@odata.id", else the collection plus "Id". Only a clean path inside
// the session collection is accepted (it is sent to the BMC host we logged in
// to, never to a host a response names).
func sessionPath(location, odata, id string) string {
	cands := []string{"", odata}
	if u, err := url.Parse(location); err == nil {
		cands[0] = u.Path
	}
	if id != "" {
		cands = append(cands, redfishSessions+"/"+id)
	}
	for _, p := range cands {
		if strings.HasPrefix(p, redfishSessions+"/") && len(p) > len(redfishSessions)+1 && path.Clean(p) == p {
			return p
		}
	}
	return ""
}

// sessionID is the session's id as the BMC UI stores it (_sess_idx).
func sessionID(p, id string) string {
	if id != "" {
		return id
	}
	return path.Base(p)
}

// mergeCookies joins two Cookie header values, dropping empties.
func mergeCookies(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// transportError is a transport failure without the request URL.
func transportError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// loginFailureText is the plain-text body of a console request whose BMC
// login failed (shown inside the console frame).
func loginFailureText(err error) string {
	switch {
	case errors.Is(err, ErrTwoFactor):
		return "The BMC requires two-factor login - open its web UI directly."
	case errors.Is(err, ErrSessionLimit):
		return "The BMC session limit is reached - close other BMC sessions and try again."
	case errors.Is(err, ErrAuthFailed):
		return "The BMC rejected the credentials - check the Warden secret."
	case errors.Is(err, ErrUnreachable):
		return "The BMC did not answer."
	}
	return "could not log in to BMC"
}
