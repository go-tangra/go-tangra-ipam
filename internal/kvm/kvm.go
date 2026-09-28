// Package kvm provides an authenticating reverse proxy to a device's BMC HTML5
// KVM console, so a platform admin can open a remote console from the platform
// UI without the BMC password ever reaching the browser.
//
// The authenticated caller (after a platform-admin check) calls StartSession to
// mint a short-lived, single-use start token bound to the device's BMC host and
// the credentials fetched from warden. The unauthenticated /bmc/ proxy Handler
// exchanges that token, on its first use, for a console session (the
// freya_kvm cookie, scoped to /bmc/<device>/, valid WithConsoleSessionTTL),
// logs in to the BMC server-side (POST /cgi/login.cgi -> SID cookie), and
// mounts the BMC's own console under the console origin, injecting the BMC
// session on every proxied HTTP request and WebSocket upgrade.
//
// The console is served by the gateway's console listener on an origin of its
// own (portal feature 025, WithConsoleOrigin), never on the portal origin: the
// BMC's JavaScript is untrusted vendor code. When a console origin is set,
// StartSession returns an absolute URL on it and console WebSockets must come
// from it. Because the BMC
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

// kvmCookie carries the console session across the BMC viewer's relative
// asset and WebSocket requests (which cannot carry a query parameter). The
// gateway's console listener forwards only this cookie.
const kvmCookie = "freya_kvm"

// Default lifetimes.
const (
	defaultTokenTTL   = 60 * time.Second
	defaultSessionTTL = 15 * time.Minute // cached BMC login
	defaultConsoleTTL = time.Hour        // console session (freya_kvm)
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
	consoleTTL time.Duration
	scheme     string // "https"; overridable in tests
	// consoleOrigin is the gateway console listener's origin ("" = relative
	// console URLs and no WebSocket Origin check).
	consoleOrigin string

	httpClient *http.Client
	wsDialer   *websocket.Dialer
	mux        *http.ServeMux

	mu       sync.Mutex
	tokens   map[string]tokenEntry   // single-use start tokens
	consoles map[string]tokenEntry   // console sessions (freya_kvm)
	sessions map[string]sessionEntry // cached BMC logins
}

// Option tunes a Manager.
type Option func(*Manager)

// WithConsoleOrigin sets the gateway console listener's public origin
// (https://host[:port]); the configuration validates it.
func WithConsoleOrigin(origin string) Option {
	return func(m *Manager) { m.consoleOrigin = strings.ToLower(strings.TrimRight(origin, "/")) }
}

// WithConsoleSessionTTL sets how long a console session lasts after its
// start token was used (default 1h; non-positive keeps the default).
func WithConsoleSessionTTL(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.consoleTTL = d
		}
	}
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
func NewManager(log *slog.Logger, tokenTTL time.Duration, opts ...Option) *Manager {
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
		consoleTTL: defaultConsoleTTL,
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
		consoles: map[string]tokenEntry{},
		sessions: map[string]sessionEntry{},
	}
	for _, o := range opts {
		o(m)
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

	consoleURL = m.consoleOrigin + "/bmc/" + url.PathEscape(deviceID) + "/?kvmtoken=" + token
	return token, consoleURL, nil
}

// Handler returns the /bmc/ reverse-proxy handler. Mount it on the module HTTP
// server; access is gated by the session token, not the gateway.
func (m *Manager) Handler() http.Handler { return m.mux }

// resolve returns a start token's binding if it is present and unexpired
// (without consuming it).
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
	for k, v := range m.consoles {
		if !now.Before(v.expires) {
			delete(m.consoles, k)
		}
	}
	for k, v := range m.sessions {
		if !now.Before(v.expires) {
			delete(m.sessions, k)
		}
	}
}

// exchange consumes a start token bound to device and opens a console
// session with the same binding. A token for another device is refused and
// left unused.
func (m *Manager) exchange(token, device string) (string, tokenEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tokens[token]
	if !ok || !m.now().Before(e.expires) || e.deviceID != device {
		return "", tokenEntry{}, false
	}
	delete(m.tokens, token)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", tokenEntry{}, false
	}
	id := hex.EncodeToString(buf)
	e.expires = m.now().Add(m.consoleTTL)
	m.consoles[id] = e
	m.gcLocked()
	return id, e, true
}

// console returns the live console session named by the request's cookie,
// bound to the path's device.
func (m *Manager) console(r *http.Request) (tokenEntry, bool) {
	c, err := r.Cookie(kvmCookie)
	if err != nil || c.Value == "" {
		return tokenEntry{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.consoles[c.Value]
	if !ok || !m.now().Before(e.expires) || e.deviceID != r.PathValue("id") {
		return tokenEntry{}, false
	}
	return e, true
}

// authorize admits a request by its start token (first use: the console
// session cookie is set on w) or by its console session cookie.
func (m *Manager) authorize(w http.ResponseWriter, r *http.Request) (tokenEntry, bool) {
	tok := r.URL.Query().Get("kvmtoken")
	if tok == "" {
		return m.console(r)
	}
	device := r.PathValue("id")
	id, e, ok := m.exchange(tok, device)
	if !ok {
		return tokenEntry{}, false
	}
	http.SetCookie(w, &http.Cookie{
		Name: kvmCookie, Value: id, Path: "/bmc/" + url.PathEscape(device) + "/",
		MaxAge: int(m.consoleTTL / time.Second), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	return e, true
}

// originAllowed reports whether a console WebSocket may be opened from the
// request's Origin: the console origin when one is configured (CSWSH), any
// origin otherwise (legacy relative consoles).
func (m *Manager) originAllowed(r *http.Request) bool {
	if m.consoleOrigin == "" {
		return true
	}
	return strings.EqualFold(r.Header.Get("Origin"), m.consoleOrigin)
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
	e, ok := m.authorize(w, r)
	if !ok {
		m.refuse(w, r)
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
			// The browser's cookies (console session, anything else) never
			// reach the BMC: only the server-side BMC session does.
			req.Header.Del("Cookie")
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			req.Header.Del("Referer")
			if req.Header.Get("Origin") != "" {
				req.Header.Set("Origin", scheme+"://"+host)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			// The BMC session stays server-side.
			resp.Header.Del("Set-Cookie")
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

// upgrader relays the viewer's WebSocket; the Origin is checked in handleWS
// before any BMC contact (originAllowed).
func (m *Manager) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		Subprotocols:    []string{"binary"},
		ReadBufferSize:  16 * 1024,
		WriteBufferSize: 16 * 1024,
		CheckOrigin:     m.originAllowed,
	}
}

// refuse answers a request without a valid start token or console session.
// The token value is never logged.
func (m *Manager) refuse(w http.ResponseWriter, r *http.Request) {
	m.warn("kvm console refused", "device", r.PathValue("id"), "path", r.URL.Path)
	http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
}

// handleWS proxies the browser's KVM WebSocket to the BMC, attaching the
// server-side session cookie. The stream is relayed transparently both ways.
func (m *Manager) handleWS(w http.ResponseWriter, r *http.Request) {
	e, ok := m.console(r)
	if !ok || !m.originAllowed(r) {
		m.refuse(w, r)
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

	client, err := m.upgrader().Upgrade(w, r, nil)
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
