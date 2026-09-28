// Package kvm provides an authenticating reverse proxy to a device's BMC HTML5
// KVM console, so a platform admin can open a remote console from the platform
// UI without the BMC password ever reaching the browser.
//
// The authenticated caller (after a platform-admin check) calls StartSession,
// which logs in to the BMC server-side with the credentials fetched from
// warden (so a refused login is answered with its reason before any console
// opens) and mints a short-lived, single-use start token bound to that BMC
// session. The unauthenticated /bmc/ proxy Handler exchanges the token, on its
// first use, for a console session (the freya_kvm cookie, scoped to
// /bmc/<device>/, valid WithConsoleSessionTTL) and mounts the BMC's own
// HTML5 viewer under the console origin, injecting the BMC session on every
// proxied HTTP request and WebSocket upgrade.
//
// Both Supermicro login generations are supported (session.go): the Redfish
// session login of newer firmware (X-Auth-Token) and the legacy form login
// (SID cookie). One BMC session per (BMC host, user) is shared by the consoles
// that use it and deleted on the BMC when the last of them ends, so consoles
// do not exhaust the BMC's few session slots.
//
// The console is served by the gateway's console listener on an origin of its
// own (portal feature 025, WithConsoleOrigin), never on the portal origin: the
// BMC's JavaScript is untrusted vendor code. When a console origin is set,
// StartSession returns an absolute URL on it and console WebSockets must come
// from it. Because the BMC
// uses a self-signed certificate, an isolated http.Client / websocket.Dialer
// with InsecureSkipVerify is used ONLY here; the browser never receives the BMC
// credentials (see injectBootstrap for the Redfish session token).
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
	defaultConsoleTTL = time.Hour // console session (freya_kvm)
	loginTimeout      = 20 * time.Second
	logoutTimeout     = 10 * time.Second
	proxyTimeout      = 30 * time.Second
	// reloginAfter: a BMC session answered with 401 is dropped (and a new
	// one logged in) only when it is at least this old, so a path the BMC
	// always refuses cannot cause a login storm.
	reloginAfter = 10 * time.Second
	// janitorInterval: how often Run ends expired tokens and consoles.
	janitorInterval = 30 * time.Second
)

var errSessionEnded = &loginError{kind: ErrLogin, detail: "console session ended"}

// Creds are the BMC web login credentials. They come from warden at use time
// and are held only in memory, server-side, while a console uses them; they
// are never sent to the browser or logged.
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
	consoleTTL time.Duration
	janitor    time.Duration
	scheme     string // "https"; overridable in tests
	// consoleOrigin is the gateway console listener's origin ("" = relative
	// console URLs and no WebSocket Origin check).
	consoleOrigin string

	httpClient *http.Client      // logins and logouts
	proxyRT    http.RoundTripper // proxied console requests (limitTransport)
	wsDialer   *websocket.Dialer
	mux        *http.ServeMux
	wg         sync.WaitGroup // background logouts

	mu       sync.Mutex
	tokens   map[string]tokenEntry  // single-use start tokens
	consoles map[string]tokenEntry  // console sessions (freya_kvm)
	sessions map[string]*bmcSession // BMC logins by host + user
	dead     []*bmcSession          // released sessions to log out after unlocking
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

// WithTransport replaces the HTTP transport to the BMCs (login, logout and
// proxied requests; nil keeps the default). Tests use it for a fake BMC.
func WithTransport(rt http.RoundTripper) Option {
	return func(m *Manager) {
		if rt != nil {
			m.httpClient.Transport = rt
		}
	}
}

// tokenEntry binds a start token or console session to a device and the BMC
// session it uses.
type tokenEntry struct {
	deviceID string
	sess     *bmcSession
	expires  time.Time
}

// bmcSession is one BMC web login shared by every token and console of the
// same (host, user). refs and creds are guarded by Manager.mu; the login
// state by mu, which also serialises logins. Lock order: mu before
// Manager.mu, never the reverse.
type bmcSession struct {
	key   string
	host  string
	refs  int
	creds Creds

	mu       sync.Mutex
	auth     bmcAuth
	loggedAt time.Time
	closed   bool
}

// NewManager builds a KVM manager. A non-positive tokenTTL uses the default; a
// nil logger disables logging.
func NewManager(log *slog.Logger, tokenTTL time.Duration, opts ...Option) *Manager {
	if tokenTTL <= 0 {
		tokenTTL = defaultTokenTTL
	}
	// Isolated transport that accepts the BMC's self-signed certificate. This
	// InsecureSkipVerify is confined to this client and never leaks elsewhere.
	// Keep-alives are off: old BMC web servers mishandle connection reuse.
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMC web UIs use self-signed certs
		DisableKeepAlives: true,
	}
	m := &Manager{
		log:        log,
		now:        time.Now,
		tokenTTL:   tokenTTL,
		consoleTTL: defaultConsoleTTL,
		janitor:    janitorInterval,
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
		sessions: map[string]*bmcSession{},
	}
	for _, o := range opts {
		o(m)
	}
	m.proxyRT = newLimitTransport(m.httpClient.Transport)
	m.mux = http.NewServeMux()
	m.mux.HandleFunc("/bmc/{id}/__kvmws", m.handleWS)
	m.mux.HandleFunc("/bmc/{id}/{path...}", m.handleProxy)
	return m
}

// StartSession logs in to the device's BMC (or reuses the live BMC session of
// the same host and user), mints a short-lived start token bound to it and
// returns the token plus the console bootstrap URL to open. A refused login
// is returned as its classified error (ErrAuthFailed, ErrTwoFactor,
// ErrSessionLimit, ErrUnreachable, ErrLogin). The caller must have verified
// platform-admin authorization before calling.
func (m *Manager) StartSession(ctx context.Context, deviceID, bmcHost string, creds Creds) (token, consoleURL string, err error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)

	m.mu.Lock()
	s := m.acquireLocked(bmcHost, creds)
	m.mu.Unlock()
	lctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	if _, err := m.sessionAuth(lctx, s); err != nil {
		m.mu.Lock()
		m.releaseLocked(s)
		m.unlockAndRetire()
		m.warn("kvm console login", "host", bmcHost, "err", err)
		return "", "", err
	}

	m.mu.Lock()
	m.tokens[token] = tokenEntry{deviceID: deviceID, sess: s, expires: m.now().Add(m.tokenTTL)}
	m.gcLocked()
	m.unlockAndRetire()

	consoleURL = m.consoleOrigin + "/bmc/" + url.PathEscape(deviceID) + "/" + consoleEntry + "&kvmtoken=" + token
	return token, consoleURL, nil
}

// Handler returns the /bmc/ reverse-proxy handler. Mount it on the module HTTP
// server; access is gated by the session token, not the gateway.
func (m *Manager) Handler() http.Handler { return m.mux }

// Run ends expired start tokens and consoles (and so their BMC sessions)
// until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.janitor)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.gc()
		}
	}
}

// Close ends every console and logs out of every BMC session, waiting for
// the logouts until ctx is done.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	for _, s := range m.sessions {
		m.dead = append(m.dead, s)
	}
	m.sessions = map[string]*bmcSession{}
	m.tokens = map[string]tokenEntry{}
	m.consoles = map[string]tokenEntry{}
	m.unlockAndRetire()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

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

// acquireLocked returns the BMC session of (host, user), creating it, and
// counts one more user of it. The latest credentials win (a rotated password
// is used by the next login). Caller holds m.mu.
func (m *Manager) acquireLocked(host string, creds Creds) *bmcSession {
	key := host + "\x00" + creds.Username
	s := m.sessions[key]
	if s == nil {
		s = &bmcSession{key: key, host: host}
		m.sessions[key] = s
	}
	s.refs++
	s.creds = creds
	return s
}

// releaseLocked drops one user of s; the last one queues it for logout.
// Caller holds m.mu.
func (m *Manager) releaseLocked(s *bmcSession) {
	s.refs--
	if s.refs > 0 {
		return
	}
	if m.sessions[s.key] == s {
		delete(m.sessions, s.key)
	}
	m.dead = append(m.dead, s)
}

// unlockAndRetire releases m.mu and logs out the sessions released under it.
func (m *Manager) unlockAndRetire() {
	dead := m.dead
	m.dead = nil
	m.mu.Unlock()
	for _, s := range dead {
		m.wg.Add(1)
		go func(s *bmcSession) {
			defer m.wg.Done()
			s.mu.Lock()
			a := s.auth
			s.auth, s.closed = bmcAuth{}, true
			s.mu.Unlock()
			if !a.empty() {
				m.logout(context.Background(), s.host, a)
			}
		}(s)
	}
}

// gc ends expired tokens and consoles.
func (m *Manager) gc() {
	m.mu.Lock()
	m.gcLocked()
	m.unlockAndRetire()
}

// gcLocked drops expired tokens and consoles, releasing their BMC sessions.
// Caller holds the lock (and retires m.dead after unlocking).
func (m *Manager) gcLocked() {
	now := m.now()
	for _, set := range []map[string]tokenEntry{m.tokens, m.consoles} {
		for k, v := range set {
			if !now.Before(v.expires) {
				delete(set, k)
				m.releaseLocked(v.sess)
			}
		}
	}
}

// sessionAuth returns the BMC session's credentials, logging in when there
// is no live login.
func (m *Manager) sessionAuth(ctx context.Context, s *bmcSession) (bmcAuth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return bmcAuth{}, errSessionEnded
	}
	if !s.auth.empty() {
		return s.auth, nil
	}
	m.mu.Lock()
	creds := s.creds
	m.mu.Unlock()
	a, err := m.login(ctx, s.host, creds)
	if err != nil {
		return bmcAuth{}, err
	}
	s.auth, s.loggedAt = a, m.now()
	if m.log != nil {
		m.log.Info("kvm bmc login", "host", s.host, "redfish", a.token != "", "cookie", a.cookie != "")
	}
	return a, nil
}

// invalidate drops the BMC login stale (answered 401) unless it was already
// replaced or is too fresh to blame, logging it out in the background. It
// reports whether a new login will be made.
func (m *Manager) invalidate(s *bmcSession, stale bmcAuth) bool {
	s.mu.Lock()
	if s.auth != stale || s.auth.empty() || m.now().Sub(s.loggedAt) < reloginAfter {
		s.mu.Unlock()
		return false
	}
	s.auth = bmcAuth{}
	s.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.logout(context.Background(), s.host, stale)
	}()
	return true
}

// exchange consumes a start token bound to device and opens a console
// session with the same binding. A token for another device is refused and
// left unused.
func (m *Manager) exchange(token, device string) (string, tokenEntry, bool) {
	m.mu.Lock()
	defer m.unlockAndRetire()
	e, ok := m.tokens[token]
	if !ok || !m.now().Before(e.expires) || e.deviceID != device {
		return "", tokenEntry{}, false
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", tokenEntry{}, false
	}
	delete(m.tokens, token)
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

// handleProxy reverse-proxies the BMC console under the console origin,
// injecting the server-side BMC session. The browser's own cookies and
// X-Auth-Token never reach the BMC; the BMC's cookies never reach the browser.
func (m *Manager) handleProxy(w http.ResponseWriter, r *http.Request) {
	e, ok := m.authorize(w, r)
	if !ok {
		m.refuse(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	auth, err := m.sessionAuth(ctx, e.sess)
	if err != nil {
		m.warn("kvm console login", "host", e.sess.host, "err", err)
		http.Error(w, loginFailureText(err), http.StatusBadGateway)
		return
	}

	upstreamPath := "/" + r.PathValue("path")
	host, scheme := e.sess.host, m.scheme
	proxy := &httputil.ReverseProxy{
		// A 401 means the BMC ended its session (idle timeout, the UI's own
		// logout): drop it, log in again and repeat a bodiless request once,
		// so the page never sees the expiry.
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			resp, err := m.proxyRT.RoundTrip(req)
			if err != nil || resp.StatusCode != http.StatusUnauthorized || !m.invalidate(e.sess, auth) {
				return resp, err
			}
			if req.Body != nil && req.Body != http.NoBody {
				return resp, nil // the next request logs in again
			}
			fresh, lerr := m.sessionAuth(req.Context(), e.sess)
			if lerr != nil {
				return resp, nil
			}
			drain(resp)
			auth = fresh
			retry := req.Clone(req.Context())
			setBMCAuth(retry.Header, auth)
			return m.proxyRT.RoundTrip(retry)
		}),
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := pr.Out
			req.URL.Scheme = scheme
			req.URL.Host = host
			req.URL.Path, req.URL.RawPath = upstreamPath, ""
			req.Host = host
			if q := req.URL.Query(); q.Has("kvmtoken") {
				q.Del("kvmtoken") // the start token is ours, not the BMC's
				req.URL.RawQuery = q.Encode()
			}
			setBMCAuth(req.Header, auth)
			// Uncompressed, so HTML pages can take the bootstrap script.
			req.Header.Set("Accept-Encoding", "identity")
			req.Header.Del("Referer")
			if req.Header.Get("Origin") != "" {
				req.Header.Set("Origin", scheme+"://"+host)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			// The BMC session stays server-side.
			resp.Header.Del("Set-Cookie")
			resp.Header.Del("X-Auth-Token")
			return injectBootstrap(resp, auth)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			m.warn("kvm console proxy", "host", host, "err", transportError(err))
			http.Error(w, "BMC console unreachable", http.StatusBadGateway)
		},
	}
	pctx, pcancel := context.WithTimeout(context.WithoutCancel(r.Context()), proxyTimeout)
	defer pcancel()
	proxy.ServeHTTP(w, r.WithContext(pctx))
}

// setBMCAuth replaces the browser's Cookie and X-Auth-Token headers with the
// server-side BMC session.
func setBMCAuth(h http.Header, auth bmcAuth) {
	h.Del("Cookie")
	h.Del("X-Auth-Token")
	if auth.cookie != "" {
		h.Set("Cookie", auth.cookie)
	}
	if auth.token != "" {
		h.Set("X-Auth-Token", auth.token)
	}
}

// transportFunc adapts a function to http.RoundTripper.
type transportFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

// handleWS relays the browser's KVM WebSocket to the BMC, attaching the
// server-side BMC session. The injected bootstrap passes the path the viewer
// asked for in "u" (default "/"). A BMC that answers 401 (session expired)
// gets one new login and a second dial.
func (m *Manager) handleWS(w http.ResponseWriter, r *http.Request) {
	e, ok := m.console(r)
	if !ok || !m.originAllowed(r) {
		m.refuse(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	auth, err := m.sessionAuth(ctx, e.sess)
	if err != nil {
		m.warn("kvm ws login", "host", e.sess.host, "err", err)
		http.Error(w, loginFailureText(err), http.StatusBadGateway)
		return
	}
	target := wsPath(r.URL.Query().Get("u"))
	upstream, resp, err := m.dialBMC(ctx, e.sess.host, target, auth)
	if err != nil && resp != nil && resp.StatusCode == http.StatusUnauthorized && m.invalidate(e.sess, auth) {
		if auth, err = m.sessionAuth(ctx, e.sess); err == nil {
			upstream, _, err = m.dialBMC(ctx, e.sess.host, target, auth)
		}
	}
	if err != nil {
		m.warn("kvm ws dial", "host", e.sess.host, "err", err)
		http.Error(w, "could not open BMC console stream", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	client, err := m.upgrader().Upgrade(w, r, nil)
	if err != nil {
		m.warn("kvm ws upgrade", "host", e.sess.host, "err", err)
		return
	}
	for _, c := range []*websocket.Conn{client, upstream} {
		_ = c.SetReadDeadline(time.Time{})
		_ = c.SetWriteDeadline(time.Time{})
	}
	proxyWebSocket(client, upstream)
}

// dialBMC opens the BMC's KVM WebSocket with the BMC session.
func (m *Manager) dialBMC(ctx context.Context, host, target string, auth bmcAuth) (*websocket.Conn, *http.Response, error) {
	wsScheme := "wss"
	if m.scheme == "http" {
		wsScheme = "ws"
	}
	hdr := http.Header{"Origin": {m.scheme + "://" + host}}
	if auth.cookie != "" {
		hdr.Set("Cookie", auth.cookie)
	}
	if auth.token != "" {
		hdr.Set("X-Auth-Token", auth.token)
	}
	c, resp, err := m.wsDialer.DialContext(ctx, wsScheme+"://"+host+target, hdr)
	if resp != nil && resp.Body != nil {
		drain(resp)
	}
	return c, resp, err
}

// wsPath is the BMC WebSocket path the viewer asked for: an absolute path
// (with query) on the BMC host, "/" for anything else.
func wsPath(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "" || p.Host != "" || !strings.HasPrefix(p.Path, "/") || strings.HasPrefix(p.Path, "//") {
		return "/"
	}
	return p.RequestURI()
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
