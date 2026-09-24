package kvm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartSessionMintsTokenNoCredsLeak(t *testing.T) {
	m := NewManager(nil, time.Minute)
	const pw = "super-secret-pw"

	tok, consoleURL, err := m.StartSession(context.Background(), "dev1", "10.0.0.9", Creds{Username: "admin", Password: pw})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !strings.Contains(consoleURL, tok) || !strings.Contains(consoleURL, "dev1") {
		t.Fatalf("console url %q missing token/device", consoleURL)
	}
	if strings.Contains(consoleURL, pw) || strings.Contains(consoleURL, "admin") {
		t.Fatalf("console url leaks credentials: %q", consoleURL)
	}

	// The token resolves to its binding while valid...
	e, ok := m.resolve(tok)
	if !ok || e.deviceID != "dev1" || e.host != "10.0.0.9" {
		t.Fatalf("resolve = %+v ok=%v", e, ok)
	}
	// ...an unknown token does not...
	if _, ok := m.resolve("not-a-token"); ok {
		t.Fatal("unknown token resolved")
	}
	// ...and an expired token does not.
	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, ok := m.resolve(tok); ok {
		t.Fatal("expired token resolved")
	}
}

func TestConsoleProxyLogsInServerSide(t *testing.T) {
	const pw = "s3cr3t-bmc-pw"
	var (
		mu       sync.Mutex
		sawSID   bool
		leaked   bool
		loggedIn bool
	)

	bmc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi/login.cgi" {
			_ = r.ParseForm()
			if r.FormValue("name") == "admin" && r.FormValue("pwd") == pw {
				mu.Lock()
				loggedIn = true
				mu.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-abc"})
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Non-login (proxied browser) request: it must carry the server-injected
		// session cookie and must NOT carry the raw credentials.
		c, _ := r.Cookie("SID")
		hasSID := c != nil && c.Value == "sid-abc"
		if strings.Contains(r.URL.RawQuery, pw) || strings.Contains(r.Header.Get("Authorization"), pw) {
			mu.Lock()
			leaked = true
			mu.Unlock()
		}
		mu.Lock()
		sawSID = sawSID || hasSID
		mu.Unlock()
		fmt.Fprintf(w, "console-ok sid=%v", hasSID)
	}))
	defer bmc.Close()

	host := strings.TrimPrefix(bmc.URL, "https://")
	m := NewManager(nil, time.Minute)

	tok, _, err := m.StartSession(context.Background(), "dev1", host, Creds{Username: "admin", Password: pw})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/bmc/dev1/console.html?kvmtoken="+tok, nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "console-ok sid=true") {
		t.Fatalf("unexpected proxied body: %q", body)
	}
	if strings.Contains(body, pw) {
		t.Fatalf("BMC password leaked to browser body: %q", body)
	}
	mu.Lock()
	defer mu.Unlock()
	if !loggedIn {
		t.Fatal("server-side BMC login never happened")
	}
	if !sawSID {
		t.Fatal("proxied request did not carry the injected session cookie")
	}
	if leaked {
		t.Fatal("raw credentials leaked into the proxied upstream request")
	}
}

func TestProxyRejectsBadToken(t *testing.T) {
	m := NewManager(nil, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/bmc/dev1/console.html?kvmtoken=bogus", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestProxyRejectsTokenDeviceMismatch(t *testing.T) {
	m := NewManager(nil, time.Minute)
	tok, _, _ := m.StartSession(context.Background(), "dev1", "10.0.0.9", Creds{Username: "admin", Password: "x"})
	// Token minted for dev1 used against dev2's path.
	req := httptest.NewRequest(http.MethodGet, "/bmc/dev2/console.html?kvmtoken="+tok, nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
