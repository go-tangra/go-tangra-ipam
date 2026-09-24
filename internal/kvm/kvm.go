// Package kvm provides an authenticating reverse proxy to a device's BMC HTML5
// KVM console, so a platform admin can open a remote console from the platform
// UI without the BMC password ever reaching the browser.
//
// The authenticated caller (after a platform-admin check) calls StartSession to
// mint a short-lived, opaque token bound to the device's BMC host and the
// credentials fetched from warden. The unauthenticated /bmc/ proxy Handler
// validates that token, logs in to the BMC server-side (POST /cgi/login.cgi ->
// SID cookie), and mounts the BMC's own console under our origin, injecting the
// session on every proxied HTTP request and WebSocket upgrade. Because the BMC
// uses a self-signed certificate, an isolated http.Client / websocket.Dialer
// with InsecureSkipVerify is used ONLY here; the browser never receives the BMC
// credentials or the raw session.
package kvm

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// kvmCookie carries the session token across the BMC viewer's relative asset and
// WebSocket requests (which cannot carry a query parameter).
const kvmCookie = "freya_kvm"

// Default lifetimes.
const (
	defaultTokenTTL   = 60 * time.Second
	defaultSessionTTL = 15 * time.Minute
	loginTimeout      = 20 * time.Second
	proxyTimeout      = 30 * time.Second
)

// Creds are the BMC web login credentials. They come from warden at use time
// and are held only in the token store, server-side; they are never sent to the
// browser or logged.
type Creds struct {
	Username string
	Password string
}

// Manager mints console tokens and serves the /bmc/ reverse proxy. Safe for
// concurrent use.
type Manager struct {
	log        *slog.Logger
	now        func() time.Time
	tokenTTL   time.Duration
	sessionTTL time.Duration
	scheme     string // "https"; overridable in tests

	httpClient *http.Client
	wsDialer   *websocket.Dialer
	mux        *http.ServeMux

	mu       sync.Mutex
	tokens   map[string]tokenEntry
	sessions map[string]sessionEntry
}

type tokenEntry struct {
	deviceID string
	host     string
	creds    Creds
	expires  time.Time
}

type sessionEntry struct {
	cookie  string
	expires time.Time
}

// NewManager builds a KVM manager. A non-positive tokenTTL uses the default; a
// nil logger disables logging.
func NewManager(log *slog.Logger, tokenTTL time.Duration) *Manager {
	if tokenTTL <= 0 {
		tokenTTL = defaultTokenTTL
	}
	// Isolated transport that accepts the BMC's self-signed certificate. This
	// InsecureSkipVerify is confined to this client and never leaks elsewhere.
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMC web UIs use self-signed certs
		DisableKeepAlives: true,
	}
	m := &Manager{
		log:        log,
		now:        time.Now,
		tokenTTL:   tokenTTL,
		sessionTTL: defaultSessionTTL,
		scheme:     "https",
		httpClient: &http.Client{
			Timeout:       loginTimeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		wsDialer: &websocket.Dialer{
			TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMC web UIs use self-signed certs
			Subprotocols:     []string{"binary", "base64"},
			HandshakeTimeout: 12 * time.Second,
		},
		tokens:   map[string]tokenEntry{},
		sessions: map[string]sessionEntry{},
	}
	m.mux = http.NewServeMux()
	m.mux.HandleFunc("/bmc/{id}/__kvmws", m.handleWS)
	m.mux.HandleFunc("/bmc/{id}/{path...}", m.handleProxy)
	return m
}

// StartSession mints a short-lived token bound to the device's BMC host and
// credentials and returns the token plus the console bootstrap URL to open. The
// caller must have verified platform-admin authorization before calling.
func (m *Manager) StartSession(_ context.Context, deviceID, bmcHost string, creds Creds) (token, consoleURL string, err error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)

	m.mu.Lock()
	m.tokens[token] = tokenEntry{deviceID: deviceID, host: bmcHost, creds: creds, expires: m.now().Add(m.tokenTTL)}
	m.gcLocked()
	m.mu.Unlock()

	consoleURL = "/bmc/" + url.PathEscape(deviceID) + "/?kvmtoken=" + token
	return token, consoleURL, nil
}

// Handler returns the /bmc/ reverse-proxy handler. Mount it on the module HTTP
// server; access is gated by the session token, not the gateway.
func (m *Manager) Handler() http.Handler { return m.mux }

// resolve returns a token's binding if it is present and unexpired.
func (m *Manager) resolve(token string) (tokenEntry, bool) {
	if token == "" {
		return tokenEntry{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tokens[token]
	if !ok || !m.now().Before(e.expires) {
		return tokenEntry{}, false
	}
	return e, true
}

// gcLocked drops expired tokens and sessions. Caller holds the lock.
func (m *Manager) gcLocked() {
	now := m.now()
	for k, v := range m.tokens {
		if !now.Before(v.expires) {
			delete(m.tokens, k)
		}
	}
	for k, v := range m.sessions {
		if !now.Before(v.expires) {
			delete(m.sessions, k)
		}
	}
}

// tokenFor resolves the request's token (query param, then cookie) and checks it
// is bound to the path's device id.
func (m *Manager) tokenFor(r *http.Request) (tokenEntry, bool) {
	tok := r.URL.Query().Get("kvmtoken")
	if tok == "" {
		if c, err := r.Cookie(kvmCookie); err == nil {
			tok = c.Value
		}
	}
	e, ok := m.resolve(tok)
	if !ok || e.deviceID != r.PathValue("id") {
		return tokenEntry{}, false
	}
	return e, true
}

// auth returns a valid BMC session cookie for the target, logging in if needed.
func (m *Manager) auth(ctx context.Context, host string, creds Creds) (string, error) {
	key := host + "\x00" + creds.Username
	m.mu.Lock()
	if s, ok := m.sessions[key]; ok && m.now().Before(s.expires) {
		cookie := s.cookie
		m.mu.Unlock()
		return cookie, nil
	}
	m.mu.Unlock()

	cookie, err := m.login(ctx, host, creds)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.sessions[key] = sessionEntry{cookie: cookie, expires: m.now().Add(m.sessionTTL)}
	m.mu.Unlock()
	return cookie, nil
}

// login authenticates to the BMC web UI and returns the SID cookie value.
func (m *Manager) login(ctx context.Context, host string, creds Creds) (string, error) {
	form := url.Values{"name": {creds.Username}, "pwd": {creds.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.scheme+"://"+host+"/cgi/login.cgi", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer drain(resp)
	for _, c := range resp.Cookies() {
		if c.Name == "SID" && c.Value != "" {
			return "SID=" + c.Value, nil
		}
	}
	return "", &loginError{host: host, status: resp.StatusCode}
}

// handleProxy reverse-proxies the BMC console assets under our origin, injecting
// the server-side session cookie. The browser's request never carries the BMC
// credentials.
func (m *Manager) handleProxy(w http.ResponseWriter, r *http.Request) {
	e, ok := m.tokenFor(r)
	if !ok {
		http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	cookie, err := m.auth(ctx, e.host, e.creds)
	if err != nil {
		m.warn("kvm console login", "host", e.host, "err", err)
		http.Error(w, "could not log in to BMC", http.StatusBadGateway)
		return
	}

	// Persist the token as a cookie so the viewer's relative asset/WS requests
	// (no query param) stay authorized.
	if r.URL.Query().Get("kvmtoken") != "" {
		http.SetCookie(w, &http.Cookie{
			Name: kvmCookie, Value: r.URL.Query().Get("kvmtoken"),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}

	upstreamPath := "/" + r.PathValue("path")
	host, scheme := e.host, m.scheme
	proxy := &httputil.ReverseProxy{
		Transport: m.httpClient.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := pr.Out
			req.URL.Scheme = scheme
			req.URL.Host = host
			req.URL.Path = upstreamPath
			req.Host = host
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			req.Header.Del("Referer")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			m.warn("kvm console proxy", "host", host, "err", err)
			http.Error(w, "BMC console unreachable", http.StatusBadGateway)
		},
	}
	pctx, pcancel := context.WithTimeout(context.WithoutCancel(r.Context()), proxyTimeout)
	defer pcancel()
	proxy.ServeHTTP(w, r.WithContext(pctx))
}

var kvmUpgrader = websocket.Upgrader{
	Subprotocols:    []string{"binary"},
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(*http.Request) bool { return true }, // same-origin via the gateway proxy
}

// handleWS proxies the browser's KVM WebSocket to the BMC, attaching the
// server-side session cookie. The stream is relayed transparently both ways.
func (m *Manager) handleWS(w http.ResponseWriter, r *http.Request) {
	e, ok := m.tokenFor(r)
	if !ok {
		http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	cookie, err := m.auth(ctx, e.host, e.creds)
	if err != nil {
		m.warn("kvm ws login", "host", e.host, "err", err)
		http.Error(w, "could not log in to BMC", http.StatusBadGateway)
		return
	}

	wsScheme := "wss"
	if m.scheme == "http" {
		wsScheme = "ws"
	}
	hdr := http.Header{"Origin": {m.scheme + "://" + e.host}}
	if cookie != "" {
		hdr.Set("Cookie", cookie)
	}
	upstream, resp, err := m.wsDialer.DialContext(ctx, wsScheme+"://"+e.host+"/", hdr)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			m.invalidate(e.host, e.creds)
		}
		m.warn("kvm ws dial", "host", e.host, "err", err)
		http.Error(w, "could not open BMC console stream", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	client, err := kvmUpgrader.Upgrade(w, r, nil)
	if err != nil {
		m.warn("kvm ws upgrade", "host", e.host, "err", err)
		return
	}
	for _, c := range []*websocket.Conn{client, upstream} {
		_ = c.SetReadDeadline(time.Time{})
		_ = c.SetWriteDeadline(time.Time{})
	}
	proxyWebSocket(client, upstream)
}

// invalidate drops any cached BMC session for the target.
func (m *Manager) invalidate(host string, creds Creds) {
	m.mu.Lock()
	delete(m.sessions, host+"\x00"+creds.Username)
	m.mu.Unlock()
}

// proxyWebSocket relays messages between the browser and the BMC until either
// side closes, keeping the browser leg alive with periodic pings.
func proxyWebSocket(browser, bmc *websocket.Conn) {
	done := make(chan struct{}, 2)
	go copyWS(bmc, browser, done)
	go copyWS(browser, bmc, done)

	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = browser.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()

	<-done
	close(stop)
	_ = browser.Close()
	_ = bmc.Close()
	<-done
}

func copyWS(dst, src *websocket.Conn, done chan struct{}) {
	for {
		mt, data, err := src.ReadMessage()
		if err != nil {
			done <- struct{}{}
			return
		}
		if err := dst.WriteMessage(mt, data); err != nil {
			done <- struct{}{}
			return
		}
	}
}

func (m *Manager) warn(msg string, args ...any) {
	if m.log != nil {
		m.log.Warn(msg, args...)
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// loginError is a BMC login failure carrying the status without exposing creds.
type loginError struct {
	host   string
	status int
}

func (e *loginError) Error() string {
	return "kvm: login to " + e.host + " failed (no SID cookie)"
}
