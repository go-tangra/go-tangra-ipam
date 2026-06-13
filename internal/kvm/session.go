// Package kvm provides an authenticating reverse proxy to a Supermicro BMC's
// built-in HTML5 KVM (H5Viewer), so a platform admin can open a full remote
// console from within the platform UI without ever handling the BMC password
// in the browser.
//
// The design (ported from sm-ipmiview): IPAM logs into the BMC web UI
// (POST /cgi/login.cgi -> SID cookie) using credentials fetched from Warden,
// then mounts the BMC's own noVNC+AST2100 viewer under our origin and proxies
// the KVM WebSocket. The browser runs the BMC's unmodified decoder.
package kvm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sessionTTL is how long a cached BMC SID is trusted before re-login.
const sessionTTL = 15 * time.Minute

// loginTimeout bounds a single BMC login round-trip.
const loginTimeout = 10 * time.Second

// bmcTarget identifies a BMC web endpoint and the credentials to log in with.
type bmcTarget struct {
	Host     string // BMC hostname/IP (HTTPS on :443 assumed)
	Username string
	Password string
}

func (t bmcTarget) key() string { return t.Host + "\x00" + t.Username }

// bmcAuth carries the credentials to attach to proxied requests: a Cookie
// header value (legacy SID, or the newer firmware's SSO) and, for Redfish-based
// firmware, the X-Auth-Token header.
type bmcAuth struct {
	cookie     string // e.g. "SID=xxx" or "SSO=yyy"
	xAuthToken string // Redfish X-Auth-Token (newer Supermicro firmware)
}

func (a bmcAuth) empty() bool { return a.cookie == "" && a.xAuthToken == "" }

type bmcSession struct {
	auth    bmcAuth
	expires time.Time
}

// sessionManager caches one authenticated BMC web session per (host, user).
// Safe for concurrent use.
type sessionManager struct {
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]bmcSession
}

// newSessionManager constructs a manager with a TLS-skip-verify client (BMC
// certs are self-signed) that does not follow redirects (so Set-Cookie is
// observable on the login response).
func newSessionManager() *sessionManager {
	return &sessionManager{
		client: &http.Client{
			Timeout: loginTimeout,
			Transport: &http.Transport{
				//nolint:gosec // BMC web UIs use self-signed certificates.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:      time.Now,
		sessions: make(map[string]bmcSession),
	}
}

// Auth returns valid session auth for the target, logging in if there is no
// cached session or it has expired.
func (m *sessionManager) Auth(ctx context.Context, t bmcTarget) (bmcAuth, error) {
	key := t.key()

	m.mu.Lock()
	if s, ok := m.sessions[key]; ok && m.now().Before(s.expires) {
		auth := s.auth
		m.mu.Unlock()
		return auth, nil
	}
	m.mu.Unlock()

	auth, err := m.login(ctx, t)
	if err != nil {
		return bmcAuth{}, err
	}

	m.mu.Lock()
	m.sessions[key] = bmcSession{auth: auth, expires: m.now().Add(sessionTTL)}
	m.mu.Unlock()
	return auth, nil
}

// Invalidate drops any cached session for the target, forcing re-login on the
// next Auth call. Used when the proxy detects an expired session.
func (m *sessionManager) Invalidate(t bmcTarget) {
	m.mu.Lock()
	delete(m.sessions, t.key())
	m.mu.Unlock()
}

// login authenticates to the BMC web UI, supporting both firmware generations:
// the legacy form login (POST /cgi/login.cgi -> SID cookie) and the newer
// Redfish session login (POST /redfish/v1/SessionService/Sessions -> X-Auth-Token
// + SSO cookie). It tries legacy first, then falls back to Redfish.
func (m *sessionManager) login(ctx context.Context, t bmcTarget) (bmcAuth, error) {
	if t.Username == "" || t.Password == "" {
		return bmcAuth{}, errors.New("kvm: missing BMC credentials")
	}

	auth, legacyErr := m.loginLegacy(ctx, t)
	if legacyErr == nil {
		return auth, nil
	}

	auth, redfishErr := m.loginRedfish(ctx, t)
	if redfishErr == nil {
		return auth, nil
	}
	return bmcAuth{}, fmt.Errorf("kvm: login %s failed: legacy=%v; redfish=%v", t.Host, legacyErr, redfishErr)
}

// loginLegacy is the older Supermicro form login -> SID cookie.
func (m *sessionManager) loginLegacy(ctx context.Context, t bmcTarget) (bmcAuth, error) {
	form := url.Values{"name": {t.Username}, "pwd": {t.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+t.Host+"/cgi/login.cgi", strings.NewReader(form.Encode()))
	if err != nil {
		return bmcAuth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.client.Do(req)
	if err != nil {
		return bmcAuth{}, err
	}
	defer drain(resp)

	for _, c := range resp.Cookies() {
		if c.Name == "SID" && c.Value != "" {
			return bmcAuth{cookie: "SID=" + c.Value}, nil
		}
	}
	return bmcAuth{}, fmt.Errorf("no SID cookie (status %d)", resp.StatusCode)
}

// loginRedfish is the newer Supermicro Redfish session login -> X-Auth-Token
// header plus a session cookie (SSO).
func (m *sessionManager) loginRedfish(ctx context.Context, t bmcTarget) (bmcAuth, error) {
	body, _ := json.Marshal(map[string]string{"UserName": t.Username, "Password": t.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+t.Host+"/redfish/v1/SessionService/Sessions", bytes.NewReader(body))
	if err != nil {
		return bmcAuth{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return bmcAuth{}, err
	}
	defer drain(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bmcAuth{}, fmt.Errorf("redfish session status %d", resp.StatusCode)
	}

	var auth bmcAuth
	auth.xAuthToken = resp.Header.Get("X-Auth-Token")
	// The session cookie the H5Viewer/KVM WebSocket authenticates with.
	var cookies []string
	for _, c := range resp.Cookies() {
		if c.Value != "" {
			cookies = append(cookies, c.Name+"="+c.Value)
		}
	}
	auth.cookie = strings.Join(cookies, "; ")

	if auth.empty() {
		return bmcAuth{}, fmt.Errorf("redfish login returned no token/cookie (status %d)", resp.StatusCode)
	}
	return auth, nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
