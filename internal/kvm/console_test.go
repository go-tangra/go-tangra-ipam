package kvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const consoleOrigin = "https://portal.example.org:8444"

// fakeBMC logs in (SID cookie), records what proxied requests carried, tries
// to set its own cookies on the browser and serves a WebSocket echo at "/".
type fakeBMC struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func newFakeBMC(t *testing.T) *fakeBMC {
	t.Helper()
	b := &fakeBMC{}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	b.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi/login.cgi" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-abc"})
			return
		}
		b.mu.Lock()
		b.seen = append(b.seen, r.Clone(context.Background()))
		b.mu.Unlock()
		if websocket.IsWebSocketUpgrade(r) {
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
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "refreshed"})
		http.SetCookie(w, &http.Cookie{Name: "vendor", Value: "x"})
		_, _ = w.Write([]byte("console-ok"))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeBMC) host() string { return strings.TrimPrefix(b.srv.URL, "https://") }

func (b *fakeBMC) last() *http.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen[len(b.seen)-1]
}

func get(m *Manager, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Origin", consoleOrigin)
	req.Header.Set("Referer", consoleOrigin+"/bmc/dev1/")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != kvmCookie {
		t.Fatalf("cookies to the browser: %v", rec.Header().Values("Set-Cookie"))
	}
	return cs[0]
}

func TestConsoleURL(t *testing.T) {
	m := NewManager(nil, time.Minute, WithConsoleOrigin(consoleOrigin))
	tok, u, err := m.StartSession(context.Background(), "dev 1", "10.0.0.9", Creds{Username: "a", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if u != consoleOrigin+"/bmc/dev%201/?kvmtoken="+tok {
		t.Fatalf("console url %q", u)
	}
	_, u, _ = NewManager(nil, 0).StartSession(context.Background(), "dev1", "h", Creds{})
	if !strings.HasPrefix(u, "/bmc/dev1/?kvmtoken=") {
		t.Fatalf("relative url %q", u)
	}
}

func TestStartTokenExchangedForConsoleSession(t *testing.T) {
	bmc := newFakeBMC(t)
	m := NewManager(nil, time.Minute, WithConsoleOrigin(consoleOrigin), WithConsoleSessionTTL(30*time.Minute))
	now := time.Now()
	m.now = func() time.Time { return now }
	tok, _, _ := m.StartSession(context.Background(), "dev1", bmc.host(), Creds{Username: "admin", Password: "pw"})

	rec := get(m, "/bmc/dev1/?kvmtoken="+tok, &http.Cookie{Name: kvmCookie, Value: "stale"}, &http.Cookie{Name: "__Host-session", Value: "portal"})
	if rec.Code != http.StatusOK || rec.Body.String() != "console-ok" {
		t.Fatalf("bootstrap %d %q", rec.Code, rec.Body.String())
	}
	c := sessionCookie(t, rec)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(c.Value) || c.Value == tok {
		t.Fatalf("session id %q", c.Value)
	}
	if c.Path != "/bmc/dev1/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 1800 {
		t.Fatalf("cookie attributes %+v", c)
	}
	// Only the server-side BMC session reaches the BMC; browser cookies,
	// Referer and the console Origin never do.
	in := bmc.last()
	if got := in.Header.Values("Cookie"); len(got) != 1 || got[0] != "SID=sid-abc" {
		t.Fatalf("cookies at the BMC %q", got)
	}
	if in.Header.Get("Referer") != "" || in.Header.Get("Origin") != "https://"+bmc.host() {
		t.Fatalf("referer %q origin %q", in.Header.Get("Referer"), in.Header.Get("Origin"))
	}

	// The start token is single-use.
	if rec := get(m, "/bmc/dev1/?kvmtoken="+tok); rec.Code != http.StatusForbidden {
		t.Fatalf("replayed token: %d", rec.Code)
	}
	// The session outlives the token...
	now = now.Add(10 * time.Minute)
	if rec := get(m, "/bmc/dev1/app.js", c); rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("session after token ttl: %d %v", rec.Code, rec.Header().Values("Set-Cookie"))
	}
	// ...is bound to its device...
	if rec := get(m, "/bmc/dev2/app.js", c); rec.Code != http.StatusForbidden {
		t.Fatalf("session on another device: %d", rec.Code)
	}
	// ...and ends with the session TTL.
	now = now.Add(21 * time.Minute)
	if rec := get(m, "/bmc/dev1/app.js", c); rec.Code != http.StatusForbidden {
		t.Fatalf("expired session: %d", rec.Code)
	}
	if rec := get(m, "/bmc/dev1/app.js", &http.Cookie{Name: kvmCookie, Value: "unknown"}); rec.Code != http.StatusForbidden {
		t.Fatalf("unknown session: %d", rec.Code)
	}
	if rec := get(m, "/bmc/dev1/app.js"); rec.Code != http.StatusForbidden {
		t.Fatalf("no credential: %d", rec.Code)
	}
}

func TestTokenForAnotherDeviceIsNotConsumed(t *testing.T) {
	bmc := newFakeBMC(t)
	m := NewManager(nil, time.Minute)
	tok, _, _ := m.StartSession(context.Background(), "dev1", bmc.host(), Creds{Username: "admin", Password: "pw"})
	if rec := get(m, "/bmc/dev2/?kvmtoken="+tok); rec.Code != http.StatusForbidden {
		t.Fatalf("other device: %d", rec.Code)
	}
	rec := get(m, "/bmc/dev1/?kvmtoken="+tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("own device after a refused use: %d", rec.Code)
	}
	if c := sessionCookie(t, rec); c.MaxAge != int(defaultConsoleTTL.Seconds()) {
		t.Fatalf("default session ttl %d", c.MaxAge)
	}
}

func wsServer(t *testing.T, m *Manager) string {
	t.Helper()
	front := httptest.NewServer(m.Handler())
	t.Cleanup(front.Close)
	return "ws" + strings.TrimPrefix(front.URL, "http") + "/bmc/dev1/__kvmws"
}

func consoleSession(t *testing.T, m *Manager, host string) *http.Cookie {
	t.Helper()
	tok, _, _ := m.StartSession(context.Background(), "dev1", host, Creds{Username: "admin", Password: "pw"})
	return sessionCookie(t, get(m, "/bmc/dev1/?kvmtoken="+tok))
}

func dialWS(url, origin string, c *http.Cookie) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if c != nil {
		h.Set("Cookie", c.Name+"="+c.Value)
	}
	return websocket.DefaultDialer.Dial(url, h)
}

func TestWebSocketNeedsSessionAndConsoleOrigin(t *testing.T) {
	bmc := newFakeBMC(t)
	m := NewManager(nil, time.Minute, WithConsoleOrigin(consoleOrigin))
	url := wsServer(t, m)
	c := consoleSession(t, m, bmc.host())

	if _, resp, err := dialWS(url, consoleOrigin, nil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no session: %v %v", resp, err)
	}
	for _, bad := range []string{"https://evil.example", "https://portal.example.org", "null", ""} {
		if conn, resp, err := dialWS(url, bad, c); err == nil {
			conn.Close()
			t.Fatalf("origin %q accepted", bad)
		} else if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q: %v %v", bad, resp, err)
		}
	}
	conn, _, err := dialWS(url, "https://PORTAL.example.org:8444", c)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("keys")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "keys" {
		t.Fatalf("echo %q %v", data, err)
	}
	if got := bmc.last().Header.Values("Cookie"); len(got) != 1 || got[0] != "SID=sid-abc" {
		t.Fatalf("cookies at the BMC websocket %q", got)
	}
}

// Without a console origin (legacy relative URLs) the Origin is not checked.
func TestWebSocketWithoutConsoleOrigin(t *testing.T) {
	bmc := newFakeBMC(t)
	m := NewManager(nil, time.Minute)
	url := wsServer(t, m)
	c := consoleSession(t, m, bmc.host())
	conn, _, err := dialWS(url, "https://anything.example", c)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestOptionsIgnoreInvalidValues(t *testing.T) {
	m := NewManager(nil, 0, WithConsoleOrigin("https://x.example/"), WithConsoleSessionTTL(-1))
	if m.consoleOrigin != "https://x.example" || m.consoleTTL != defaultConsoleTTL {
		t.Fatalf("origin %q ttl %v", m.consoleOrigin, m.consoleTTL)
	}
}
