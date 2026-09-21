package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-freya/freya/internal/testrt"
	"github.com/go-freya/freya/internal/testutil"
	"github.com/go-freya/freya/services/auth/pkg/authclient"

	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/backup"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/ipmi"
	"github.com/go-freya/freya/services/ipam/internal/kvm"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/stream"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

const (
	apiTenant = "11111111-1111-7111-8111-111111111111"
	apiAdmin  = "22222222-2222-7222-8222-222222222222"
	apiUser   = "33333333-3333-7333-8333-333333333333"

	bmcRef  = "bmc-ref-1"
	bmcUser = "bmcuser"
	bmcPass = "s3cr3tpw"
)

// fakeVerifier maps a bearer token to a fixed identity.
type fakeVerifier struct {
	ids map[string]authclient.Identity
}

func (f fakeVerifier) Verify(_ context.Context, token string) (authclient.Identity, error) {
	if id, ok := f.ids[token]; ok {
		return id, nil
	}
	return authclient.Identity{}, ErrUnauthenticated
}

// recPub records the event types published (a stand-in for the real hub).
type recPub struct {
	mu     sync.Mutex
	events []string
}

func (p *recPub) Publish(_ context.Context, _ string, eventType string, _ any) {
	p.mu.Lock()
	p.events = append(p.events, eventType)
	p.mu.Unlock()
}

type apiFixture struct {
	s      *Server
	mem    *memstore.Mem
	bmc    *ipmi.Fake
	warden *warden.Fake
}

func newAPI(t *testing.T) *apiFixture { return newAPIWith(t, nil) }

// newAPIWith builds a fully wired IPAM API over a fresh memstore. When hub is
// non-nil the SSE stream route is enabled.
func newAPIWith(t *testing.T, hub *stream.Hub) *apiFixture {
	t.Helper()
	mem := memstore.New()
	rt := testrt.New(t, testutil.MustCA("example.org"), "ipam")

	pub := &recPub{}
	wf := warden.NewFake()
	wf.Put(bmcRef, map[string]string{
		"username": bmcUser, "password": bmcPass, "protocol": "2.0", "port": "623",
	}, warden.SecretMeta{Name: "bmc-1", Description: "test BMC creds"})

	dns := dnscfg.New(mem)
	dns.SetLookup(func(_ context.Context, _ string) ([]string, error) {
		return []string{"host.example.org."}, nil
	})

	bmc := ipmi.NewFake()
	deps := Deps{
		Subnets:   subnets.New(mem),
		Addresses: addresses.New(mem, pub, 0, 0),
		Devices:   devices.New(mem),
		Vlans:     vlans.New(mem),
		Locations: locations.New(mem),
		Groups:    groups.New(mem),
		Stats:     stats.New(mem),
		Backup:    backup.New(mem),
		DNS:       dns,
		Scan:      scan.New(mem, nil, nil, nil, wf, pub, scan.Config{MaxHosts: 65536}, nil),
		BMC:       bmc,
		KVM:       kvm.NewManager(nil, 0),
		Warden:    wf,
		Hub:       hub,
	}

	v := fakeVerifier{ids: map[string]authclient.Identity{
		"admin": {UserID: apiAdmin, TenantID: apiTenant, Roles: []string{"admin"}},
		"user":  {UserID: apiUser, TenantID: apiTenant, Roles: []string{"user"}},
	}}
	s, err := NewHandler(rt, WithVerifier(v))
	if err != nil {
		t.Fatal(err)
	}
	s.Register(deps)
	return &apiFixture{s: s, mem: mem, bmc: bmc, warden: wf}
}

const p = "/api/ipam/v1"

// req drives one JSON request as the caller (empty tok => no Authorization).
// Mutating methods carry the CSRF header the OpenAPI validator requires.
func (f *apiFixture) req(t *testing.T, method, path, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://localhost"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	if method != "GET" {
		r.Header.Set("X-CSRF-Token", "t")
	}
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return m
}

// createSubnet posts a subnet and returns its id.
func (f *apiFixture) createSubnet(t *testing.T, name, cidr string) string {
	t.Helper()
	w := f.req(t, "POST", p+"/subnets", "admin", `{"name":"`+name+`","cidr":"`+cidr+`"}`)
	if w.Code != 201 {
		t.Fatalf("create subnet %s: want 201, got %d %s", cidr, w.Code, w.Body)
	}
	id, _ := decodeBody(t, w)["id"].(string)
	if id == "" {
		t.Fatalf("create subnet returned no id: %s", w.Body)
	}
	return id
}

func TestUnauthenticated(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/subnets", "", ""); w.Code != 401 {
		t.Fatalf("no token: want 401, got %d (%s)", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets", "bogus", ""); w.Code != 401 {
		t.Fatalf("bad token: want 401, got %d", w.Code)
	}
	// Public health check needs no token.
	if w := f.req(t, "GET", p+"/health", "", ""); w.Code != 200 {
		t.Fatalf("health: want 200, got %d %s", w.Code, w.Body)
	}
}

func TestSubnetsAllocateAndExhaust(t *testing.T) {
	f := newAPI(t)
	// A /30 has exactly two usable hosts (network and broadcast excluded).
	sid := f.createSubnet(t, "small", "10.40.0.0/30")

	alloc := func() (int, string) {
		w := f.req(t, "POST", p+"/ip-addresses/allocate", "admin", `{"subnet_id":"`+sid+`"}`)
		addr, _ := decodeBody(t, w)["address"].(string)
		return w.Code, addr
	}

	c1, a1 := alloc()
	c2, a2 := alloc()
	if c1 != 201 || c2 != 201 {
		t.Fatalf("allocate: want 201/201, got %d/%d", c1, c2)
	}
	if a1 == "" || a1 == a2 {
		t.Fatalf("expected two distinct addresses, got %q and %q", a1, a2)
	}

	// The subnet is now full: no address remains.
	if c3, _ := alloc(); c3 != 507 {
		t.Fatalf("exhausted subnet: want 507, got %d", c3)
	}

	// Creating an address that is already allocated is a 409 conflict.
	w := f.req(t, "POST", p+"/ip-addresses", "admin", `{"subnet_id":"`+sid+`","address":"`+a1+`"}`)
	if w.Code != 409 {
		t.Fatalf("duplicate address: want 409, got %d %s", w.Code, w.Body)
	}

	// List reflects the two allocations.
	w = f.req(t, "GET", p+"/ip-addresses?subnet_id="+sid, "admin", "")
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(items) != 2 {
		t.Fatalf("list addresses: %d len=%d %s", w.Code, len(items), w.Body)
	}
}

func TestDevicesInterfacesPackages(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/devices", "admin", `{"name":"srv-1","device_type":"server"}`)
	if w.Code != 201 {
		t.Fatalf("create device: %d %s", w.Code, w.Body)
	}
	did, _ := decodeBody(t, w)["id"].(string)

	// Interface create + list.
	w = f.req(t, "POST", p+"/devices/"+did+"/interfaces", "admin", `{"name":"eth0","mac_address":"aa:bb:cc:dd:ee:ff"}`)
	if w.Code != 201 {
		t.Fatalf("create interface: %d %s", w.Code, w.Body)
	}
	ifid, _ := decodeBody(t, w)["id"].(string)
	w = f.req(t, "GET", p+"/devices/"+did+"/interfaces", "admin", "")
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(items) != 1 {
		t.Fatalf("list interfaces: %d %s", w.Code, w.Body)
	}

	// Package sync + list.
	w = f.req(t, "POST", p+"/devices/"+did+"/packages/sync", "admin",
		`{"packages":[{"name":"openssl","current_version":"1.0","available_version":"1.1","needs_update":true,"is_security_update":true}]}`)
	if w.Code != 200 {
		t.Fatalf("sync packages: %d %s", w.Code, w.Body)
	}
	if decodeBody(t, w)["security"].(float64) != 1 {
		t.Fatalf("sync result: %s", w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+did+"/packages", "admin", "")
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(items) != 1 {
		t.Fatalf("list packages: %d %s", w.Code, w.Body)
	}

	// Delete the interface.
	if w := f.req(t, "DELETE", p+"/devices/"+did+"/interfaces/"+ifid, "admin", ""); w.Code != 204 {
		t.Fatalf("delete interface: want 204, got %d %s", w.Code, w.Body)
	}
}

func TestVlanLocationGroupCheck(t *testing.T) {
	f := newAPI(t)

	if w := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":100,"name":"prod"}`); w.Code != 201 {
		t.Fatalf("create vlan: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/locations", "admin", `{"name":"dc1","location_type":"datacenter"}`); w.Code != 201 {
		t.Fatalf("create location: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/locations/tree", "admin", ""); w.Code != 200 {
		t.Fatalf("location tree: %d %s", w.Code, w.Body)
	}

	// IP group + a single-address member.
	w := f.req(t, "POST", p+"/ip-groups", "admin", `{"name":"dns-servers"}`)
	if w.Code != 201 {
		t.Fatalf("create ip-group: %d %s", w.Code, w.Body)
	}
	gid, _ := decodeBody(t, w)["id"].(string)
	if w := f.req(t, "POST", p+"/ip-groups/"+gid+"/members", "admin", `{"member_type":"address","value":"10.50.0.5"}`); w.Code != 201 {
		t.Fatalf("add member: %d %s", w.Code, w.Body)
	}

	// Check that the address matches the group.
	w = f.req(t, "GET", p+"/ip-groups/check?ip=10.50.0.5", "admin", "")
	if w.Code != 200 {
		t.Fatalf("check ip: %d %s", w.Code, w.Body)
	}
	if m, _ := decodeBody(t, w)["matching_groups"].([]any); len(m) != 1 {
		t.Fatalf("expected one matching group, got %s", w.Body)
	}
}

func TestScanStartAndList(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "scan-net", "10.60.0.0/24")

	w := f.req(t, "POST", p+"/ip-scans", "admin", `{"subnet_id":"`+sid+`","enable_snmp":false}`)
	if w.Code != 202 {
		t.Fatalf("start scan: want 202, got %d %s", w.Code, w.Body)
	}
	job := decodeBody(t, w)
	if job["status"] != "pending" || job["subnet_id"] != sid {
		t.Fatalf("scan job: %s", w.Body)
	}

	w = f.req(t, "GET", p+"/ip-scans", "admin", "")
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(items) != 1 {
		t.Fatalf("list scans: %d %s", w.Code, w.Body)
	}
}

func TestStatsAndBackup(t *testing.T) {
	f := newAPI(t)
	f.createSubnet(t, "stat-net", "10.70.0.0/24")

	w := f.req(t, "GET", p+"/stats", "admin", "")
	if w.Code != 200 || decodeBody(t, w)["total_subnets"].(float64) != 1 {
		t.Fatalf("stats: %d %s", w.Code, w.Body)
	}

	// Export then import into a fresh service.
	w = f.req(t, "POST", p+"/backup/export", "admin", `{"include_secrets":false}`)
	if w.Code != 200 {
		t.Fatalf("export: %d %s", w.Code, w.Body)
	}
	backupJSON := w.Body.String()

	f2 := newAPI(t)
	w = f2.req(t, "POST", p+"/backup/import", "admin", `{"mode":"overwrite","backup":`+backupJSON+`}`)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}

	// A bad schema version is a 422.
	w = f2.req(t, "POST", p+"/backup/import", "admin", `{"mode":"skip","backup":{"schema_version":99}}`)
	if w.Code != 422 {
		t.Fatalf("bad schema: want 422, got %d %s", w.Code, w.Body)
	}
}

func TestDNSConfig(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/dns-config", "admin", ""); w.Code != 200 {
		t.Fatalf("dns get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/dns-config", "admin", `{"dns_servers":["1.1.1.1"],"timeout_ms":2000,"reverse_dns_enabled":true}`); w.Code != 200 {
		t.Fatalf("dns update: %d %s", w.Code, w.Body)
	}
	w := f.req(t, "POST", p+"/dns-config/test", "admin", `{"test_ip":"127.0.0.1"}`)
	if w.Code != 200 {
		t.Fatalf("dns test: %d %s", w.Code, w.Body)
	}
	if decodeBody(t, w)["hostname"] != "host.example.org." {
		t.Fatalf("dns test hostname: %s", w.Body)
	}
}

// newDeviceWithBMC creates a device carrying a BMC management IP and secret ref.
func (f *apiFixture) newDeviceWithBMC(t *testing.T) string {
	t.Helper()
	w := f.req(t, "POST", p+"/devices", "admin",
		`{"name":"oob-1","device_type":"server","management_ip":"10.99.0.10","ipmi_secret_ref":"`+bmcRef+`"}`)
	if w.Code != 201 {
		t.Fatalf("create oob device: %d %s", w.Code, w.Body)
	}
	id, _ := decodeBody(t, w)["id"].(string)
	return id
}

func TestPowerAuthorization(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)

	// Platform admin: power status succeeds and never leaks credentials.
	w := f.req(t, "GET", p+"/devices/"+did+"/power", "admin", "")
	if w.Code != 200 {
		t.Fatalf("power status (admin): want 200, got %d %s", w.Code, w.Body)
	}
	if decodeBody(t, w)["on"] != true {
		t.Fatalf("power status body: %s", w.Body)
	}
	assertNoCreds(t, w.Body.String())

	// A power action succeeds and reaches the BMC.
	w = f.req(t, "POST", p+"/devices/"+did+"/power", "admin", `{"action":"cycle"}`)
	if w.Code != 200 {
		t.Fatalf("power action (admin): want 200, got %d %s", w.Code, w.Body)
	}
	if len(f.bmc.Actions) != 1 || f.bmc.Actions[0] != "cycle" {
		t.Fatalf("BMC did not record the action: %v", f.bmc.Actions)
	}
	// The BMC received the credentials fetched from warden (never surfaced).
	if f.bmc.LastCreds.Username != bmcUser || f.bmc.LastCreds.Password != bmcPass {
		t.Fatalf("BMC creds not wired from warden")
	}

	// Sensors and SEL are also platform-admin reads.
	if w := f.req(t, "GET", p+"/devices/"+did+"/sensors", "admin", ""); w.Code != 200 {
		t.Fatalf("sensors: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sel", "admin", ""); w.Code != 200 {
		t.Fatalf("sel: %d %s", w.Code, w.Body)
	}

	// A plain tenant user is forbidden from every out-of-band operation.
	if w := f.req(t, "GET", p+"/devices/"+did+"/power", "user", ""); w.Code != 403 {
		t.Fatalf("power status (user): want 403, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/devices/"+did+"/power", "user", `{"action":"on"}`); w.Code != 403 {
		t.Fatalf("power action (user): want 403, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/devices/"+did+"/kvm-session", "user", ""); w.Code != 403 {
		t.Fatalf("kvm-session (user): want 403, got %d %s", w.Code, w.Body)
	}
}

func TestKVMSession(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)

	w := f.req(t, "POST", p+"/devices/"+did+"/kvm-session", "admin", "")
	if w.Code != 201 {
		t.Fatalf("kvm-session: want 201, got %d %s", w.Code, w.Body)
	}
	body := decodeBody(t, w)
	tok, _ := body["token"].(string)
	consoleURL, _ := body["console_url"].(string)
	if tok == "" {
		t.Fatalf("kvm-session returned no token: %s", w.Body)
	}
	if !strings.Contains(consoleURL, "/bmc/") || !strings.Contains(consoleURL, "kvmtoken=") {
		t.Fatalf("kvm-session console_url malformed: %s", w.Body)
	}
	assertNoCreds(t, w.Body.String())
}

// assertNoCreds fails if a response body contains any BMC credential or the raw
// secret reference — these must never cross the API boundary.
func assertNoCreds(t *testing.T, body string) {
	t.Helper()
	for _, secret := range []string{bmcUser, bmcPass} {
		if strings.Contains(body, secret) {
			t.Fatalf("credential %q leaked in response: %s", secret, body)
		}
	}
}

// TestRouteSmoke exercises the remaining mounted read/derived routes to confirm
// they are wired to their services and return the documented envelope shapes.
func TestRouteSmoke(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "smoke", "10.80.0.0/24")

	// Subnet get / tree / stats / synchronous scan.
	if w := f.req(t, "GET", p+"/subnets/"+sid, "admin", ""); w.Code != 200 {
		t.Fatalf("subnet get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets/tree", "admin", ""); w.Code != 200 {
		t.Fatalf("subnet tree: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets/"+sid+"/stats", "admin", ""); w.Code != 200 {
		t.Fatalf("subnet stats: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/subnets/"+sid+"/scan", "admin", `{}`); w.Code != 200 {
		t.Fatalf("subnet scan: %d %s", w.Code, w.Body)
	}
	// A missing subnet is a 404.
	if w := f.req(t, "GET", p+"/subnets/018f0000-0000-7000-8000-0000000000ff", "admin", ""); w.Code != 404 {
		t.Fatalf("missing subnet: want 404, got %d", w.Code)
	}

	// Address bulk-allocate, find, suggest, get/update/delete, ping.
	w := f.req(t, "POST", p+"/ip-addresses/bulk-allocate", "admin", `{"subnet_id":"`+sid+`","count":2,"prefix":"host"}`)
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 201 || len(items) != 2 {
		t.Fatalf("bulk-allocate: %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/ip-addresses/find?address=10.80.0.1", "admin", "")
	if w.Code != 200 {
		t.Fatalf("find address: %d %s", w.Code, w.Body)
	}
	aid, _ := decodeBody(t, w)["id"].(string)
	if w := f.req(t, "GET", p+"/ip-addresses/suggest?subnet_id="+sid+"&count=3", "admin", ""); w.Code != 200 {
		t.Fatalf("suggest: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/ip-addresses/"+aid, "admin", ""); w.Code != 200 {
		t.Fatalf("address get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/ip-addresses/"+aid, "admin", `{"subnet_id":"`+sid+`","address":"10.80.0.1","hostname":"renamed"}`); w.Code != 200 {
		t.Fatalf("address update: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/ip-addresses/"+aid+"/ping", "admin", ""); w.Code != 200 {
		t.Fatalf("ping: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "DELETE", p+"/ip-addresses/"+aid, "admin", ""); w.Code != 204 {
		t.Fatalf("address delete: %d %s", w.Code, w.Body)
	}

	// Warden secret metadata (never values).
	w = f.req(t, "GET", p+"/warden-secrets", "admin", "")
	if items, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(items) != 1 {
		t.Fatalf("warden list: %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/warden-secrets/"+bmcRef, "admin", "")
	if w.Code != 200 {
		t.Fatalf("warden get: %d %s", w.Code, w.Body)
	}
	assertNoCreds(t, w.Body.String())
	if w := f.req(t, "GET", p+"/warden-secrets/nope", "admin", ""); w.Code != 404 {
		t.Fatalf("warden get missing: want 404, got %d", w.Code)
	}

	// VLAN get + subnets; scan get + cancel; device addresses + host-groups.
	vw := f.req(t, "POST", p+"/vlans", "admin", `{"vlan_id":42,"name":"smoke-vlan"}`)
	vid, _ := decodeBody(t, vw)["id"].(string)
	if w := f.req(t, "GET", p+"/vlans/"+vid, "admin", ""); w.Code != 200 {
		t.Fatalf("vlan get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/vlans/"+vid+"/subnets", "admin", ""); w.Code != 200 {
		t.Fatalf("vlan subnets: %d %s", w.Code, w.Body)
	}

	sid2 := f.createSubnet(t, "smoke2", "10.81.0.0/24")
	sw := f.req(t, "POST", p+"/ip-scans", "admin", `{"subnet_id":"`+sid2+`"}`)
	jid, _ := decodeBody(t, sw)["id"].(string)
	if w := f.req(t, "GET", p+"/ip-scans/"+jid, "admin", ""); w.Code != 200 {
		t.Fatalf("scan get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/ip-scans/"+jid+"/cancel", "admin", ""); w.Code != 200 {
		t.Fatalf("scan cancel: %d %s", w.Code, w.Body)
	}

	dw := f.req(t, "POST", p+"/devices", "admin", `{"name":"smoke-dev","device_type":"switch"}`)
	did, _ := decodeBody(t, dw)["id"].(string)
	if w := f.req(t, "GET", p+"/devices/"+did+"/addresses", "admin", ""); w.Code != 200 {
		t.Fatalf("device addresses: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/host-groups", "admin", ""); w.Code != 200 {
		t.Fatalf("device host-groups: %d %s", w.Code, w.Body)
	}
}

func TestStreamWithoutHubIs501(t *testing.T) {
	f := newAPI(t) // no hub
	if w := f.req(t, "GET", p+"/stream", "admin", ""); w.Code != 501 {
		t.Fatalf("stream without hub: want 501, got %d %s", w.Code, w.Body)
	}
}

func TestStreamSSE(t *testing.T) {
	hub := stream.NewHub(stream.NewMemory(), stream.Config{}, nil)
	t.Cleanup(hub.Close)
	f := newAPIWith(t, hub)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "https://localhost"+p+"/stream", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("stream: want 200, got %d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: %q", ct)
	}
}
