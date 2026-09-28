package kvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	rfUser = "ADMIN"
	rfPass = "rf-s3cret-pw"
)

// rfBMC is a fake Supermicro BMC. mode "redfish" is the X12 generation
// (Redfish session login only; login.cgi answers without a SID), "legacy" the
// old one (no Redfish sessions; login.cgi -> SID), "both" an X11 that offers
// both and needs both. Proxied requests must carry the credential the mode
// requires, else 401.
type rfBMC struct {
	srv   *httptest.Server
	mode  string
	twoFA bool
	limit bool

	mu            sync.Mutex
	redfishLogins int
	legacyLogins  int
	legacyForm    url.Values
	loginCT       string
	loginBody     map[string]string
	deletes       []string // "path token"
	legacyLogouts int
	next          int
	valid         map[string]bool
	seen          []*http.Request
	wsPath        string
	wsHeader      http.Header
}

func newRF(t *testing.T, mode string) *rfBMC {
	t.Helper()
	b := &rfBMC{mode: mode, valid: map[string]bool{}}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	b.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case r.URL.Path == redfishSessions && r.Method == http.MethodPost:
			b.redfishLogins++
			if b.mode == "legacy" {
				http.NotFound(w, r)
				return
			}
			b.loginCT = r.Header.Get("Content-Type")
			b.loginBody = map[string]string{}
			_ = json.NewDecoder(r.Body).Decode(&b.loginBody)
			if b.limit {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"@Message.ExtendedInfo":[{"MessageId":"Base.1.10.SessionLimitExceeded"}]}}`))
				return
			}
			if b.loginBody["UserName"] != rfUser || b.loginBody["Password"] != rfPass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			b.next++
			tok := fmt.Sprintf("xtok-%d", b.next)
			b.valid[tok] = true
			w.Header().Set("X-Auth-Token", tok)
			w.Header().Set("Location", fmt.Sprintf("https://%s%s/%d", r.Host, redfishSessions, b.next))
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"Id":"%d","@odata.id":"%s/%d","Oem":{"Supermicro":{"TwoFAEnabled":%v}}}`, b.next, redfishSessions, b.next, b.twoFA)
			return
		case strings.HasPrefix(r.URL.Path, redfishSessions+"/") && r.Method == http.MethodDelete:
			tok := r.Header.Get("X-Auth-Token")
			b.deletes = append(b.deletes, r.URL.Path+" "+tok)
			delete(b.valid, tok)
			w.WriteHeader(http.StatusNoContent)
			return
		case r.URL.Path == "/cgi/login.cgi":
			b.legacyLogins++
			_ = r.ParseForm()
			b.legacyForm = r.PostForm
			if b.mode != "redfish" && r.PostForm.Get("name") == rfUser && r.PostForm.Get("pwd") == rfPass {
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-legacy"})
			}
			return
		case r.URL.Path == "/cgi/logout.cgi":
			b.legacyLogouts++
			return
		}
		b.seen = append(b.seen, r.Clone(context.Background()))
		if !b.authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if websocket.IsWebSocketUpgrade(r) {
			b.wsPath, b.wsHeader = r.URL.RequestURI(), r.Header.Clone()
			b.mu.Unlock()
			defer b.mu.Lock()
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				mt, data, err := c.ReadMessage()
				if err != nil {
					return
				}
				_ = c.WriteMessage(mt, data)
			}
		}
		w.Header().Set("X-Auth-Token", "echoed")
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "vendor"})
		switch {
		case strings.HasSuffix(r.URL.Path, ".js"):
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte("var x='<head>';"))
		case strings.HasSuffix(r.URL.Path, ".json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			_, _ = w.Write([]byte("<!DOCTYPE html><html>\n<HEAD lang=\"en\"><title>H5Viewer</title></head><body><header>x</header></body></html>"))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

// authorized is the mode's credential check (caller holds b.mu).
func (b *rfBMC) authorized(r *http.Request) bool {
	tokOK := b.valid[r.Header.Get("X-Auth-Token")]
	c, _ := r.Cookie("SID")
	sidOK := c != nil && c.Value == "sid-legacy"
	switch b.mode {
	case "redfish":
		return tokOK
	case "legacy":
		return sidOK
	}
	return tokOK && sidOK
}

func (b *rfBMC) host() string { return strings.TrimPrefix(b.srv.URL, "https://") }

func (b *rfBMC) revokeAll() {
	b.mu.Lock()
	b.valid = map[string]bool{}
	b.mu.Unlock()
}

func (b *rfBMC) snapshot() rfBMC {
	b.mu.Lock()
	defer b.mu.Unlock()
	return rfBMC{redfishLogins: b.redfishLogins, legacyLogins: b.legacyLogins, legacyForm: b.legacyForm,
		loginCT: b.loginCT, loginBody: b.loginBody, deletes: append([]string(nil), b.deletes...),
		legacyLogouts: b.legacyLogouts, seen: append([]*http.Request(nil), b.seen...), wsPath: b.wsPath, wsHeader: b.wsHeader}
}

func rfCreds() Creds { return Creds{Username: rfUser, Password: rfPass} }

// startConsole mints a start token and exchanges it on the bootstrap URL.
func startConsole(t *testing.T, m *Manager, host string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	_, u, err := m.StartSession(context.Background(), "dev1", host, rfCreds())
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	pu, _ := url.Parse(u)
	rec := get(m, pu.RequestURI())
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d %q", rec.Code, rec.Body.String())
	}
	return rec, sessionCookie(t, rec)
}

func TestRedfishLoginInjectsTokenAndBootstrap(t *testing.T) {
	b := newRF(t, "redfish")
	var logs bytes.Buffer
	m := NewManager(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), time.Minute, WithConsoleOrigin(consoleOrigin))
	defer func() {
		if strings.Contains(logs.String(), rfPass) || strings.Contains(logs.String(), "xtok-") || !strings.Contains(logs.String(), "kvm bmc login") {
			t.Errorf("log leaks or misses the login: %s", logs.String())
		}
	}()
	_, u, err := m.StartSession(context.Background(), "dev1", b.host(), rfCreds())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, consoleOrigin+"/bmc/dev1/cgi/url_redirect.cgi?url_name=man_ikvm_html5_bootstrap&kvmtoken=") {
		t.Fatalf("console url %q", u)
	}
	s := b.snapshot()
	// The login is the BMC's own Redfish session login, exactly as its
	// redfish.js sends it; the legacy form is still tried once (an X11 needs
	// its SID as well), the X12 answers it without one.
	if s.redfishLogins != 1 || s.loginCT != "application/json" || s.loginBody["UserName"] != rfUser || s.loginBody["Password"] != rfPass {
		t.Fatalf("redfish login %+v ct=%q", s.loginBody, s.loginCT)
	}
	if s.legacyLogins != 1 {
		t.Fatalf("legacy logins %d", s.legacyLogins)
	}
	pu, _ := url.Parse(u)
	rec := get(m, pu.RequestURI())
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap %d %q", rec.Code, rec.Body.String())
	}
	c := sessionCookie(t, rec)
	body := rec.Body.String()
	if n := strings.Count(body, "<script>"); n != 1 {
		t.Fatalf("bootstrap scripts %d: %s", n, body)
	}
	if !strings.Contains(body, `<HEAD lang="en"><script>`) || !strings.Contains(body, `sessionStorage.setItem('_x_auth',"xtok-1")`) ||
		!strings.Contains(body, `sessionStorage.setItem('_sess_idx',"1")`) || !strings.Contains(body, "__kvmws") {
		t.Fatalf("bootstrap not injected at the head: %s", body)
	}
	if rec.Header().Get("X-Auth-Token") != "" || rec.Header().Get("Content-Length") != fmt.Sprint(len(body)) {
		t.Fatalf("response headers %v", rec.Header())
	}
	in := b.snapshot().seen[0]
	if in.Header.Get("X-Auth-Token") != "xtok-1" || in.Header.Get("Cookie") != "" || in.Header.Get("Accept-Encoding") != "identity" {
		t.Fatalf("upstream headers %v", in.Header)
	}
	if in.URL.Query().Get("kvmtoken") != "" || in.URL.Query().Get("url_name") != "man_ikvm_html5_bootstrap" {
		t.Fatalf("upstream query %q", in.URL.RawQuery)
	}

	// Non-HTML passes through untouched; a browser X-Auth-Token is replaced.
	req := httptest.NewRequest(http.MethodGet, "/bmc/dev1/js/app.js", nil)
	req.AddCookie(c)
	req.Header.Set("X-Auth-Token", "browser-supplied")
	js := httptest.NewRecorder()
	m.Handler().ServeHTTP(js, req)
	if js.Code != http.StatusOK || js.Body.String() != "var x='<head>';" {
		t.Fatalf("js %d %q", js.Code, js.Body.String())
	}
	if got := b.snapshot().seen[1].Header.Values("X-Auth-Token"); len(got) != 1 || got[0] != "xtok-1" {
		t.Fatalf("token at the BMC %q", got)
	}
	if rec := get(m, "/bmc/dev1/redfish/v1/x.json", c); rec.Body.String() != `{"ok":true}` {
		t.Fatalf("json %q", rec.Body.String())
	}
}

func TestLegacyLoginFallsBackToSID(t *testing.T) {
	b := newRF(t, "legacy")
	m := NewManager(nil, time.Minute)
	rec, c := startConsole(t, m, b.host())
	s := b.snapshot()
	if s.redfishLogins != 1 || s.legacyLogins != 1 {
		t.Fatalf("logins redfish=%d legacy=%d", s.redfishLogins, s.legacyLogins)
	}
	if s.legacyForm.Get("check") != "00" || s.legacyForm.Get("name") != rfUser {
		t.Fatalf("legacy form %v", s.legacyForm)
	}
	body := rec.Body.String()
	if strings.Contains(body, "_x_auth") || !strings.Contains(body, "__kvmws") {
		t.Fatalf("legacy bootstrap %s", body)
	}
	if in := s.seen[0]; in.Header.Get("Cookie") != "SID=sid-legacy" || in.Header.Get("X-Auth-Token") != "" {
		t.Fatalf("upstream %v", in.Header)
	}
	// Ending the console logs the SID out.
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	m.gc()
	m.wg.Wait()
	if s := b.snapshot(); s.legacyLogouts != 1 || len(s.deletes) != 0 {
		t.Fatalf("logout legacy=%d deletes=%v", s.legacyLogouts, s.deletes)
	}
	_ = c
}

func TestBothGenerationsMergeCredentials(t *testing.T) {
	b := newRF(t, "both")
	m := NewManager(nil, time.Minute)
	_, c := startConsole(t, m, b.host())
	if rec := get(m, "/bmc/dev1/js/a.js", c); rec.Code != http.StatusOK {
		t.Fatalf("merged session refused: %d", rec.Code)
	}
	in := b.snapshot().seen[1]
	if in.Header.Get("X-Auth-Token") != "xtok-1" || in.Header.Get("Cookie") != "SID=sid-legacy" {
		t.Fatalf("merged headers %v", in.Header)
	}
}

func TestLoginFailuresAreClassified(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(b *rfBMC)
		creds   Creds
		want    error
		legacy  int
		deletes int
	}{
		{"wrong password", func(*rfBMC) {}, Creds{Username: rfUser, Password: "nope"}, ErrAuthFailed, 0, 0},
		{"no credentials", func(*rfBMC) {}, Creds{Username: rfUser}, ErrAuthFailed, 0, 0},
		{"two factor", func(b *rfBMC) { b.twoFA = true }, rfCreds(), ErrTwoFactor, 0, 1},
		{"session limit", func(b *rfBMC) { b.limit = true }, rfCreds(), ErrSessionLimit, 0, 0},
		{"legacy rejected", func(b *rfBMC) { b.mode = "legacy" }, Creds{Username: rfUser, Password: "nope"}, ErrLogin, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newRF(t, "redfish")
			tc.setup(b)
			var logs bytes.Buffer
			m := NewManager(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), time.Minute)
			tok, u, err := m.StartSession(context.Background(), "dev1", b.host(), tc.creds)
			if !errors.Is(err, tc.want) || tok != "" || u != "" {
				t.Fatalf("err %v (want %v) tok=%q", err, tc.want, tok)
			}
			m.wg.Wait()
			s := b.snapshot()
			if s.legacyLogins != tc.legacy || len(s.deletes) != tc.deletes {
				t.Fatalf("legacy=%d deletes=%v", s.legacyLogins, s.deletes)
			}
			if strings.Contains(err.Error(), rfPass) || strings.Contains(logs.String(), rfPass) || strings.Contains(logs.String(), "xtok-") {
				t.Fatalf("secret leaked: %v / %s", err, logs.String())
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.tokens) != 0 || len(m.sessions) != 0 {
				t.Fatalf("failed start left state: %d tokens %d sessions", len(m.tokens), len(m.sessions))
			}
		})
	}
}

func TestLoginUnreachable(t *testing.T) {
	b := newRF(t, "redfish")
	host := b.host()
	b.srv.Close()
	m := NewManager(nil, time.Minute)
	if _, _, err := m.StartSession(context.Background(), "dev1", host, rfCreds()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err %v", err)
	}
	if _, err := m.loginLegacy(context.Background(), host, rfCreds()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("legacy err %v", err)
	}
	m.scheme = "bad scheme"
	if _, err := m.loginRedfish(context.Background(), host, rfCreds()); !errors.Is(err, ErrLogin) {
		t.Fatalf("bad request %v", err)
	}
	if _, err := m.loginLegacy(context.Background(), host, rfCreds()); !errors.Is(err, ErrLogin) {
		t.Fatalf("bad legacy request %v", err)
	}
	m.logout(context.Background(), host, bmcAuth{token: "t", sessionPath: redfishSessions + "/1", cookie: "SID=x"})
}

func TestRedfishSessionDeletedWhenConsolesEnd(t *testing.T) {
	b := newRF(t, "redfish")
	m := NewManager(nil, time.Minute, WithConsoleSessionTTL(30*time.Minute))
	now := time.Now()
	m.now = func() time.Time { return now }
	_, c1 := startConsole(t, m, b.host())
	now = now.Add(10 * time.Minute)
	_, c2 := startConsole(t, m, b.host())
	if s := b.snapshot(); s.redfishLogins != 1 {
		t.Fatalf("consoles share one BMC session: %d logins", s.redfishLogins)
	}
	// The first console ends; the second still uses the BMC session.
	now = now.Add(25 * time.Minute)
	m.gc()
	m.wg.Wait()
	if s := b.snapshot(); len(s.deletes) != 0 {
		t.Fatalf("deleted while in use: %v", s.deletes)
	}
	if rec := get(m, "/bmc/dev1/js/a.js", c2); rec.Code != http.StatusOK {
		t.Fatalf("second console: %d", rec.Code)
	}
	if rec := get(m, "/bmc/dev1/js/a.js", c1); rec.Code != http.StatusForbidden {
		t.Fatalf("ended console: %d", rec.Code)
	}
	now = now.Add(10 * time.Minute)
	m.gc()
	m.wg.Wait()
	if s := b.snapshot(); len(s.deletes) != 1 || s.deletes[0] != redfishSessions+"/1 xtok-1" {
		t.Fatalf("deletes %v", s.deletes)
	}

	// An unused start token also releases its BMC session.
	if _, _, err := m.StartSession(context.Background(), "dev1", b.host(), rfCreds()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	m.gc()
	m.wg.Wait()
	if s := b.snapshot(); len(s.deletes) != 2 || s.deletes[1] != redfishSessions+"/2 xtok-2" {
		t.Fatalf("deletes %v", s.deletes)
	}
}

func TestCloseLogsOutAndRunCollects(t *testing.T) {
	b := newRF(t, "redfish")
	m := NewManager(nil, time.Minute)
	startConsole(t, m, b.host())
	m.Close(context.Background())
	if s := b.snapshot(); len(s.deletes) != 1 {
		t.Fatalf("close deletes %v", s.deletes)
	}
	// The manager still works after Close (a new BMC session).
	if _, _, err := m.StartSession(context.Background(), "dev1", b.host(), rfCreds()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Close(ctx) // an expired shutdown context does not block

	m2 := NewManager(nil, time.Millisecond)
	m2.janitor = 5 * time.Millisecond
	if _, _, err := m2.StartSession(context.Background(), "dev1", b.host(), rfCreds()); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	stopped := make(chan struct{})
	go func() { m2.Run(rctx); close(stopped) }()
	for len(b.snapshot().deletes) < 3 {
		select {
		case <-rctx.Done():
			t.Fatalf("janitor never collected: %v", b.snapshot().deletes)
		case <-time.After(5 * time.Millisecond):
		}
	}
	rcancel()
	<-stopped
	m.wg.Wait()
	m2.wg.Wait()
}

func TestExpiredBMCSessionIsRenewed(t *testing.T) {
	b := newRF(t, "redfish")
	m := NewManager(nil, time.Minute)
	now := time.Now()
	m.now = func() time.Time { return now }
	_, c := startConsole(t, m, b.host())
	b.revokeAll() // the BMC timed the session out
	// A 401 right after login does not trigger a re-login storm...
	if rec := get(m, "/bmc/dev1/js/a.js", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("fresh 401: %d", rec.Code)
	}
	if b.snapshot().redfishLogins != 1 {
		t.Fatal("re-logged in on a fresh session")
	}
	// ...an older session is dropped, and the request is repeated once with a
	// new login: the page never sees the expiry.
	now = now.Add(time.Minute)
	if rec := get(m, "/bmc/dev1/js/a.js", c); rec.Code != http.StatusOK {
		t.Fatalf("renewed: %d", rec.Code)
	}
	if in := b.snapshot().seen; in[len(in)-1].Header.Get("X-Auth-Token") != "xtok-2" {
		t.Fatalf("retry token %q", in[len(in)-1].Header.Get("X-Auth-Token"))
	}
	// A request with a body is not repeated.
	b.revokeAll()
	now = now.Add(time.Minute)
	req := httptest.NewRequest(http.MethodPost, "/bmc/dev1/cgi/op.cgi", strings.NewReader("a=1"))
	req.AddCookie(c)
	post := httptest.NewRecorder()
	m.Handler().ServeHTTP(post, req)
	if post.Code != http.StatusUnauthorized {
		t.Fatalf("post: %d", post.Code)
	}
	m.wg.Wait()
	s := b.snapshot()
	if s.redfishLogins != 2 || len(s.deletes) != 2 || s.deletes[0] != redfishSessions+"/1 xtok-1" {
		t.Fatalf("logins %d deletes %v", s.redfishLogins, s.deletes)
	}
}

func TestProxyLoginFailureAfterStart(t *testing.T) {
	b := newRF(t, "redfish")
	m := NewManager(nil, time.Minute)
	now := time.Now()
	m.now = func() time.Time { return now }
	_, c := startConsole(t, m, b.host())
	b.revokeAll()
	b.mu.Lock()
	b.limit = true
	b.mu.Unlock()
	now = now.Add(time.Minute)
	get(m, "/bmc/dev1/js/a.js", c) // 401 -> dropped
	rec := get(m, "/bmc/dev1/js/a.js", c)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "session limit") {
		t.Fatalf("login failure: %d %q", rec.Code, rec.Body.String())
	}
	u := wsServer(t, m)
	if _, resp, err := dialWS(u, "", c); err == nil || resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("ws login failure: %v %v", resp, err)
	}
}

func TestRedfishWebSocketCarriesToken(t *testing.T) {
	b := newRF(t, "redfish")
	m := NewManager(nil, time.Minute, WithConsoleOrigin(consoleOrigin))
	now := time.Now()
	m.now = func() time.Time { return now }
	_, c := startConsole(t, m, b.host())
	u := wsServer(t, m) + "?u=" + url.QueryEscape("/kvm?port=1")
	conn, _, err := dialWS(u, consoleOrigin, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("k")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "k" {
		t.Fatalf("echo %q %v", data, err)
	}
	conn.Close()
	s := b.snapshot()
	if s.wsPath != "/kvm?port=1" || s.wsHeader.Get("X-Auth-Token") != "xtok-1" || s.wsHeader.Get("Cookie") != "" {
		t.Fatalf("ws upstream %q %v", s.wsPath, s.wsHeader)
	}

	// The BMC dropped the session: the dial is retried once after a re-login.
	b.revokeAll()
	now = now.Add(time.Minute)
	conn, _, err = dialWS(wsServer(t, m), consoleOrigin, c)
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	conn.Close()
	if s := b.snapshot(); s.redfishLogins != 2 || s.wsHeader.Get("X-Auth-Token") != "xtok-2" || s.wsPath != "/" {
		t.Fatalf("redial logins %d header %v path %q", s.redfishLogins, s.wsHeader, s.wsPath)
	}
}

func TestWSPath(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/": "/", "/kvm?a=1": "/kvm?a=1", "//evil/x": "/", "https://evil/x": "/", "x": "/", "%zz": "/",
	} {
		if got := wsPath(in); got != want {
			t.Errorf("wsPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionPath(t *testing.T) {
	cases := []struct{ loc, odata, id, want string }{
		{"https://10.0.0.1" + redfishSessions + "/7", "", "", redfishSessions + "/7"},
		{"", redfishSessions + "/8", "", redfishSessions + "/8"},
		{"", "", "9", redfishSessions + "/9"},
		{"https://evil/other/1", "/redfish/v1/../../x", "a/../b", ""},
		{"%zz", "", "", ""},
		{redfishSessions + "/", "", "", ""},
	}
	for _, tc := range cases {
		if got := sessionPath(tc.loc, tc.odata, tc.id); got != tc.want {
			t.Errorf("sessionPath(%q,%q,%q) = %q, want %q", tc.loc, tc.odata, tc.id, got, tc.want)
		}
	}
	if got := sessionID(redfishSessions+"/12", ""); got != "12" {
		t.Errorf("sessionID %q", got)
	}
	if mergeCookies("a=1", "") != "a=1" || mergeCookies("", "b=2") != "b=2" || mergeCookies("a=1", "b=2") != "a=1; b=2" {
		t.Error("mergeCookies")
	}
	if transportError(errors.New("plain")) != "plain" {
		t.Error("transportError")
	}
}

func TestRedfishOddResponses(t *testing.T) {
	var status int
	var hdr, body string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hdr != "" {
			w.Header().Set("X-Auth-Token", hdr)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	m := NewManager(nil, time.Minute)
	for _, tc := range []struct {
		status    int
		hdr, body string
		want      error
	}{
		{http.StatusForbidden, "", "", ErrAuthFailed},
		{http.StatusInternalServerError, "", "", errUnsupported},
		{http.StatusOK, "", "{}", errUnsupported},
		{http.StatusServiceUnavailable, "", "SessionLimitExceeded", ErrSessionLimit},
	} {
		status, hdr, body = tc.status, tc.hdr, tc.body
		if _, err := m.loginRedfish(context.Background(), host, rfCreds()); !errors.Is(err, tc.want) {
			t.Errorf("status %d: %v, want %v", tc.status, err, tc.want)
		}
	}
	// Legacy 401/403 is an authentication failure.
	status = http.StatusUnauthorized
	if _, err := m.loginLegacy(context.Background(), host, rfCreds()); !errors.Is(err, ErrAuthFailed) {
		t.Errorf("legacy 401: %v", err)
	}
	// A token without a session id still works (no DELETE target).
	status, hdr, body = http.StatusOK, "t0k", "not json"
	a, err := m.loginRedfish(context.Background(), host, rfCreds())
	if err != nil || a.token != "t0k" || a.sessionPath != "" {
		t.Fatalf("token only: %+v %v", a, err)
	}
}

func TestInjectBootstrapEdges(t *testing.T) {
	mk := func(ct, enc, method, body string) *http.Response {
		r := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)),
			Request: httptest.NewRequest(method, "/", nil)}
		r.Header.Set("Content-Type", ct)
		if enc != "" {
			r.Header.Set("Content-Encoding", enc)
		}
		return r
	}
	read := func(r *http.Response) string { b, _ := io.ReadAll(r.Body); return string(b) }
	auth := bmcAuth{token: "tk</script>", sessionID: "3"}

	r := mk("text/html", "", http.MethodGet, "<p>no head</p>")
	if err := injectBootstrap(r, auth); err != nil {
		t.Fatal(err)
	}
	got := read(r)
	if !strings.HasPrefix(got, "<script>") || !strings.HasSuffix(got, "<p>no head</p>") || strings.Contains(got, "tk</script>") {
		t.Fatalf("prepend / escaping: %s", got)
	}
	for _, r := range []*http.Response{
		mk("text/html", "gzip", http.MethodGet, "<head>"),
		mk("text/html", "", http.MethodHead, "<head>"),
		mk("text/plain", "", http.MethodGet, "<head>"),
	} {
		if err := injectBootstrap(r, auth); err != nil || read(r) != "<head>" {
			t.Fatal("must not inject")
		}
	}
	big := "<head>" + strings.Repeat("x", maxInjectBytes)
	r = mk("text/html", "", http.MethodGet, big)
	if err := injectBootstrap(r, auth); err != nil || read(r) != big {
		t.Fatal("oversized page must stream through unchanged")
	}
	r = mk("text/html", "", http.MethodGet, "")
	r.Body = io.NopCloser(errReader{})
	if err := injectBootstrap(r, auth); err == nil {
		t.Fatal("read error not surfaced")
	}
	r = mk("text/html", "", http.MethodGet, "<head>")
	r.Request = nil
	if err := injectBootstrap(r, bmcAuth{}); err != nil || strings.Contains(read(r), "_x_auth") {
		t.Fatal("no token must not seed")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestLoginFailureText(t *testing.T) {
	for err, want := range map[error]string{
		ErrTwoFactor: "two-factor", ErrSessionLimit: "session limit", ErrAuthFailed: "rejected",
		ErrUnreachable: "did not answer", ErrLogin: "could not log in",
	} {
		if got := loginFailureText(err); !strings.Contains(got, want) {
			t.Errorf("%v: %q", err, got)
		}
	}
}

func TestLimitTransport(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	set := func(n int) { mu.Lock(); calls = n; mu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	lt := newLimitTransport(http.DefaultTransport)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := lt.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("retry: %v %v", resp, err)
	}
	resp.Body.Close()
	// Non-idempotent requests are not retried.
	set(0)
	req, _ = http.NewRequest(http.MethodPost, srv.URL, nil)
	if resp, err := lt.RoundTrip(req); err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("post: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	// Retries give up with the last failure.
	set(-100)
	req, _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	if resp, err := lt.RoundTrip(req); err != nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("exhausted: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	// A cancelled request stops waiting.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	set(0)
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := lt.RoundTrip(req); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	sem := lt.hostSem("busy")
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://busy/", nil)
	if _, err := lt.RoundTrip(req); err == nil {
		t.Fatal("blocked request succeeded")
	}
}

func TestWithTransport(t *testing.T) {
	var hit bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hit = true
		h := http.Header{}
		h.Set("X-Auth-Token", "t")
		return &http.Response{StatusCode: 201, Header: h, Body: io.NopCloser(strings.NewReader(`{"Id":"1"}`)), Request: r}, nil
	})
	m := NewManager(nil, 0, WithTransport(rt), WithTransport(nil))
	if _, _, err := m.StartSession(context.Background(), "d", "bmc.example", rfCreds()); err != nil || !hit {
		t.Fatalf("custom transport: %v hit=%v", err, hit)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestBootstrapFocusesViewerCanvas: the injected script gives the HTML5
// viewer's canvas a tabindex and focuses it (at load, on clicks and when the
// frame gains focus) — without it keyboard input never reaches the BMC.
func TestBootstrapFocusesViewerCanvas(t *testing.T) {
	s := bootstrapScript(bmcAuth{})
	for _, want := range []string{"getElementById('noVNC_canvas')", "setAttribute('tabindex','0')", "c.focus()",
		"addEventListener('mousedown'", "window.addEventListener('focus',fc)"} {
		if !strings.Contains(s, want) {
			t.Errorf("bootstrap script lacks %q", want)
		}
	}
}
