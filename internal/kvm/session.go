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
	"context"
	"crypto/tls"
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

type bmcSession struct {
	sid     string
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

// SID returns a valid session id for the target, logging in if there is no
// cached session or it has expired.
func (m *sessionManager) SID(ctx context.Context, t bmcTarget) (string, error) {
	key := t.key()

	m.mu.Lock()
	if s, ok := m.sessions[key]; ok && m.now().Before(s.expires) {
		sid := s.sid
		m.mu.Unlock()
		return sid, nil
	}
	m.mu.Unlock()

	sid, err := m.login(ctx, t)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.sessions[key] = bmcSession{sid: sid, expires: m.now().Add(sessionTTL)}
	m.mu.Unlock()
	return sid, nil
}

// Invalidate drops any cached session for the target, forcing re-login on the
// next SID call. Used when the proxy detects an expired session.
func (m *sessionManager) Invalidate(t bmcTarget) {
	m.mu.Lock()
	delete(m.sessions, t.key())
	m.mu.Unlock()
}

// login performs the POST /cgi/login.cgi form login and extracts the SID cookie.
func (m *sessionManager) login(ctx context.Context, t bmcTarget) (string, error) {
	if t.Username == "" || t.Password == "" {
		return "", errors.New("kvm: missing BMC credentials")
	}
	form := url.Values{"name": {t.Username}, "pwd": {t.Password}}
	endpoint := "https://" + t.Host + "/cgi/login.cgi"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("kvm: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("kvm: login %s: %w", t.Host, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	// On success the BMC sets a non-empty SID cookie (it also clears any prior
	// SID with an expired empty one, so take the last non-empty value).
	sid := ""
	for _, c := range resp.Cookies() {
		if c.Name == "SID" && c.Value != "" {
			sid = c.Value
		}
	}
	if sid == "" {
		return "", fmt.Errorf("kvm: login %s failed (bad credentials?)", t.Host)
	}
	return sid, nil
}
