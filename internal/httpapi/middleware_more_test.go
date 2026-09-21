package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-freya/freya/internal/testrt"
	"github.com/go-freya/freya/internal/testutil"
)

// TestMethodNotAllowed covers validate's non-ErrPathNotFound branch: a declared
// path with an undeclared method yields 405.
func TestMethodNotAllowed(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "PATCH", p+"/subnets", "admin", `{}`); w.Code != 405 {
		t.Fatalf("method not allowed: want 405, got %d %s", w.Code, w.Body)
	}
}

// TestBadQueryParamValidation covers validate's parse-error branch (a
// non-integer limit -> 400) and its validation-failed branch (an out-of-range
// limit that parses but violates the schema minimum -> 422).
func TestBadQueryParamValidation(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/subnets?limit=notanint", "admin", ""); w.Code != 400 {
		t.Fatalf("non-int limit: want 400, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets?limit=0", "admin", ""); w.Code != 422 {
		t.Fatalf("out-of-range limit: want 422, got %d %s", w.Code, w.Body)
	}
}

// TestOversizedBody covers the body-too-large (413) path for a normal route
// whose JSON body exceeds MaxBodyBytes.
func TestOversizedBody(t *testing.T) {
	f := newAPI(t)
	big := `{"name":"` + strings.Repeat("z", 70000) + `","cidr":"10.0.0.0/24"}`
	if w := f.req(t, "POST", p+"/subnets", "admin", big); w.Code != 413 {
		t.Fatalf("oversized body: want 413, got %d %s", w.Code, w.Body)
	}
}

// TestNilVerifier covers authenticate's verifier==nil branch: a protected route
// is refused 401 when no verifier is installed.
func TestNilVerifier(t *testing.T) {
	rt := testrt.New(t, testutil.MustCA("example.org"), "ipam")
	s, err := NewHandler(rt) // no WithVerifier
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://localhost"+p+"/subnets", nil)
	r.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("nil verifier: want 401, got %d %s", w.Code, w.Body)
	}
}

// TestCallerAndSubjectsUnauthenticated covers Caller and subjects when no
// identity is in the request context.
func TestCallerAndSubjectsUnauthenticated(t *testing.T) {
	r := httptest.NewRequest("GET", "https://localhost/x", nil)
	if _, err := Caller(r); err != ErrUnauthenticated {
		t.Fatalf("Caller: want ErrUnauthenticated, got %v", err)
	}
	if _, err := subjects(r); err != ErrUnauthenticated {
		t.Fatalf("subjects: want ErrUnauthenticated, got %v", err)
	}
}

// TestHandleUndeclared covers Handle/HandleFunc rejecting an undeclared route
// and MustHandle panicking on the same.
func TestHandleUndeclared(t *testing.T) {
	f := newAPI(t)
	if err := f.s.HandleFunc("GET", "/not/declared", func(_ http.ResponseWriter, _ *http.Request) {}); err == nil {
		t.Fatal("HandleFunc undeclared: want error")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustHandle undeclared: want panic")
		}
	}()
	f.s.MustHandle("GET", "/not/declared", func(_ http.ResponseWriter, _ *http.Request) {})
}

// TestSingleResourceGetSuccess covers the success WriteJSON on the single-item
// GETs that were previously only exercised as 404s.
func TestSingleResourceGetSuccess(t *testing.T) {
	f := newAPI(t)

	dw := f.req(t, "POST", p+"/devices", "admin", `{"name":"g-dev","device_type":"server"}`)
	did, _ := decodeBody(t, dw)["id"].(string)
	if w := f.req(t, "GET", p+"/devices/"+did, "admin", ""); w.Code != 200 {
		t.Fatalf("device get: %d %s", w.Code, w.Body)
	}

	lw := f.req(t, "POST", p+"/locations", "admin", `{"name":"g-loc","location_type":"datacenter"}`)
	lid, _ := decodeBody(t, lw)["id"].(string)
	if w := f.req(t, "GET", p+"/locations/"+lid, "admin", ""); w.Code != 200 {
		t.Fatalf("location get: %d %s", w.Code, w.Body)
	}
}
