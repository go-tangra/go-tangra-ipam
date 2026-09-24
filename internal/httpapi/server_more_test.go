package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra/v4/freyatest/testrt"
	"github.com/go-tangra/go-tangra/v4/freyatest/testutil"
)

// TestListRoutes exercises every collection GET body so its query-filter
// assembly runs (several are not otherwise reached by the smoke tests).
func TestListRoutes(t *testing.T) {
	f := newAPI(t)
	f.createSubnet(t, "list-net", "10.90.0.0/24")

	for _, path := range []string{
		p + "/subnets",
		p + "/subnets?vlan_id=&status=active&query=x&limit=10&cursor=",
		p + "/ip-addresses?status=allocated&address_type=host&limit=5",
		p + "/devices?device_type=server&status=active&manufacturer=acme&limit=5",
		p + "/vlans?status=active&domain=d&limit=5",
		p + "/locations?location_type=rack&country=US&status=active&limit=5",
		p + "/ip-groups?limit=5",
		p + "/host-groups?limit=5",
		p + "/ip-scans?status=pending&limit=5",
	} {
		if w := f.req(t, "GET", path, "admin", ""); w.Code != 200 {
			t.Fatalf("list %s: want 200, got %d %s", path, w.Code, w.Body)
		}
	}
}

// TestMalformedBodyOnMutations drives an unknown-field body through every
// mutating route so the DecodeJSON error branch (Fail -> 400) is exercised. The
// path ids need not exist because decode happens before the store lookup.
func TestMalformedBodyOnMutations(t *testing.T) {
	f := newAPI(t)
	bad := `{"totally_unknown_field":true}`

	cases := []struct{ method, path string }{
		{"POST", p + "/subnets"},
		{"PUT", p + "/subnets/" + randUUID},
		{"POST", p + "/subnets/" + randUUID + "/scan"},
		{"POST", p + "/ip-addresses"},
		{"POST", p + "/ip-addresses/allocate"},
		{"POST", p + "/ip-addresses/bulk-allocate"},
		{"PUT", p + "/ip-addresses/" + randUUID},
		{"POST", p + "/devices"},
		{"PUT", p + "/devices/" + randUUID},
		{"POST", p + "/devices/" + randUUID + "/interfaces"},
		{"POST", p + "/devices/" + randUUID + "/packages/sync"},
		{"POST", p + "/vlans"},
		{"PUT", p + "/vlans/" + randUUID},
		{"POST", p + "/locations"},
		{"PUT", p + "/locations/" + randUUID},
		{"POST", p + "/ip-groups"},
		{"PUT", p + "/ip-groups/" + randUUID},
		{"POST", p + "/ip-groups/" + randUUID + "/members"},
		{"PUT", p + "/ip-groups/" + randUUID + "/members/" + randUUID},
		{"POST", p + "/host-groups"},
		{"PUT", p + "/host-groups/" + randUUID},
		{"POST", p + "/host-groups/" + randUUID + "/members"},
		{"POST", p + "/ip-scans"},
		{"PUT", p + "/dns-config"},
		{"POST", p + "/dns-config/test"},
		{"POST", p + "/backup/export"},
		{"POST", p + "/backup/import"},
	}
	for _, c := range cases {
		if w := f.req(t, c.method, c.path, "admin", bad); w.Code != 400 {
			t.Fatalf("%s %s: want 400 malformed, got %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
}

// TestServerGetters covers the pure accessors that report the mounted contract.
func TestServerGetters(t *testing.T) {
	f := newAPI(t)
	s := f.s

	if len(s.Declared()) == 0 {
		t.Fatal("Declared: empty")
	}
	if len(s.Implemented()) == 0 {
		t.Fatal("Implemented: empty")
	}
	// Without a hub, only the SSE stream route is left unimplemented.
	for _, rt := range s.Missing() {
		if rt.Path != p+"/stream" {
			t.Fatalf("Missing: unexpected %v", rt)
		}
	}
	if !s.IsPublic("GET", p+"/health") {
		t.Fatal("health should be public")
	}
	if s.IsPublic("GET", p+"/subnets") {
		t.Fatal("subnets should not be public")
	}
	if s.Document() == nil {
		t.Fatal("Document: nil")
	}
	if s.Edge() != nil {
		t.Fatal("Edge: want nil for NewHandler")
	}
}

// TestRegisterKVM covers both the nil (no-op) and wired branches.
func TestRegisterKVM(t *testing.T) {
	f := newAPI(t)

	// nil KVM -> no-op, no /bmc/ route.
	f.s.RegisterKVM(http.NewServeMux(), Deps{})

	// Wired KVM -> mounts /bmc/.
	mux := http.NewServeMux()
	f.s.RegisterKVM(mux, Deps{KVM: kvm.NewManager(nil, 0)})
	r := httptest.NewRequest("GET", "https://localhost/bmc/x", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r) // route is mounted; handler runs
}

// TestRemoteHandler covers WithRemote and remoteHandler cache/404 branches.
func TestRemoteHandler(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":    {Data: []byte("<html></html>")},
		"manifest.json": {Data: []byte(`{"name":"app"}`)},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	rt := testrt.New(t, testutil.MustCA("example.org"), "ipam")
	v := fakeVerifier{ids: map[string]authclient.Identity{
		"admin": {UserID: apiAdmin, TenantID: apiTenant, Roles: []string{"admin"}},
	}}
	s, err := NewHandler(rt, WithVerifier(v), WithRemote(dist))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://localhost"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Immutable hashed asset.
	if w := get(RemotePrefix + "/assets/app.js"); w.Code != 200 {
		t.Fatalf("asset: want 200, got %d", w.Code)
	} else if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control: %q", cc)
	}
	// Never-cached top-level file.
	if w := get(RemotePrefix + "/manifest.json"); w.Code != 200 {
		t.Fatalf("manifest: want 200, got %d", w.Code)
	} else if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("manifest cache-control: %q", cc)
	}
	// Directory root -> 404.
	if w := get(RemotePrefix + "/"); w.Code != 404 {
		t.Fatalf("root: want 404, got %d", w.Code)
	}
	// Missing file -> 404.
	if w := get(RemotePrefix + "/nope.js"); w.Code != 404 {
		t.Fatalf("missing: want 404, got %d", w.Code)
	}
}
