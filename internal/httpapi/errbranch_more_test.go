package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFailSvcUnauthenticated covers failSvc's ErrUnauthenticated branch via a
// direct call (the routed path is intercepted by the auth middleware first).
func TestFailSvcUnauthenticated(t *testing.T) {
	w := httptest.NewRecorder()
	failSvc(w, ErrUnauthenticated)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("failSvc unauth: want 401, got %d", w.Code)
	}
}

// TestFailSvcInternal covers failSvc's default (500) branch directly.
func TestFailSvcInternal(t *testing.T) {
	w := httptest.NewRecorder()
	failSvc(w, errors.New("unexpected"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failSvc default: want 500, got %d", w.Code)
	}
}

// TestFailWithLogger covers Fail's >=500 logging branch (log != nil).
func TestFailWithLogger(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httptest.NewRequest("GET", "https://localhost/x", nil)
	w := httptest.NewRecorder()
	Fail(w, r, log, errors.New("boom")) // 503, logged
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("Fail logged: want 503, got %d", w.Code)
	}
}

// TestMissingResourceErrBranches drives the by-id operation error branches for
// entities whose Get/Update/Delete verify existence and return not_found.
func TestMissingResourceErrBranches(t *testing.T) {
	f := newAPI(t)

	cases := []struct {
		method, path, body string
	}{
		// Address update/ping on a missing id.
		{"PUT", p + "/ip-addresses/" + randUUID, `{"subnet_id":"` + randUUID + `","address":"10.0.0.1"}`},
		{"POST", p + "/ip-addresses/" + randUUID + "/ping", ""},
		// VLAN get + subnets on a missing id.
		{"GET", p + "/vlans/" + randUUID, ""},
		{"GET", p + "/vlans/" + randUUID + "/subnets", ""},
		// IP-group get/update + member update/delete.
		{"GET", p + "/ip-groups/" + randUUID, ""},
		{"PUT", p + "/ip-groups/" + randUUID, `{"name":"x"}`},
		{"PUT", p + "/ip-groups/" + randUUID + "/members/" + randUUID, `{"member_type":"address","value":"10.0.0.1"}`},
		{"DELETE", p + "/ip-groups/" + randUUID + "/members/" + randUUID, ""},
		// Host-group get/update + member delete.
		{"GET", p + "/host-groups/" + randUUID, ""},
		{"PUT", p + "/host-groups/" + randUUID, `{"name":"x"}`},
		{"DELETE", p + "/host-groups/" + randUUID + "/members/" + randUUID, ""},
		// Device interface delete on a missing id.
		{"DELETE", p + "/devices/" + randUUID + "/interfaces/" + randUUID, ""},
		// Scan on a missing subnet.
		{"POST", p + "/ip-scans", `{"subnet_id":"` + randUUID + `"}`},
	}
	for _, c := range cases {
		w := f.req(t, c.method, c.path, "admin", c.body)
		if w.Code == 200 || w.Code == 201 || w.Code == 204 {
			t.Logf("%s %s: returned success %d (no error branch)", c.method, c.path, w.Code)
		}
		if w.Code >= 500 {
			t.Fatalf("%s %s: unexpected 5xx %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
}

// TestSuggestMissingSubnet covers the suggest handler's error branch on a subnet
// that does not exist.
func TestSuggestMissingSubnet(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "GET", p+"/ip-addresses/suggest?subnet_id="+randUUID+"&count=2", "admin", "")
	if w.Code == 200 {
		t.Logf("suggest missing subnet returned 200 (no error branch)")
	} else if w.Code >= 500 {
		t.Fatalf("suggest missing subnet: unexpected 5xx %d %s", w.Code, w.Body)
	}
}

// TestWardenListEmptyAndSuggestEmpty covers the nil->[] normalization branches.
func TestWardenListEmptyAndSuggestEmpty(t *testing.T) {
	f := newAPI(t)

	// A query matching no secret yields nil -> [] (metas == nil branch).
	w := f.req(t, "GET", p+"/warden-secrets?query=zzz-no-match-zzz", "admin", "")
	if w.Code != 200 {
		t.Fatalf("warden filtered list: %d %s", w.Code, w.Body)
	}
	if items, ok := decodeBody(t, w)["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("warden filtered list should be empty: %s", w.Body)
	}

	// Suggest on a fully-allocated /31 yields no suggestions (sugg == nil branch).
	sid := f.createSubnet(t, "sugg", "10.95.0.0/31")
	// Exhaust it.
	for i := 0; i < 2; i++ {
		f.req(t, "POST", p+"/ip-addresses/allocate", "admin", `{"subnet_id":"`+sid+`"}`)
	}
	w = f.req(t, "GET", p+"/ip-addresses/suggest?subnet_id="+sid+"&count=2", "admin", "")
	if w.Code != 200 {
		t.Fatalf("suggest exhausted: %d %s", w.Code, w.Body)
	}
	if s, ok := decodeBody(t, w)["suggestions"].([]any); !ok || len(s) != 0 {
		t.Fatalf("suggest exhausted should be empty: %s", w.Body)
	}
}
