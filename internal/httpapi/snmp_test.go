package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
)

func snmpDevice(name, descr string) snmp.DiscoveredDevice {
	return snmp.DiscoveredDevice{SysName: name, SysDescr: descr}
}

const snmpCommunity = "c0mm-HTTP-S3CRET"

func (f *apiFixture) setSNMP(t *testing.T, id, body string) {
	t.Helper()
	if w := f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", body); w.Code != 200 {
		t.Fatalf("put snmp: %d %s", w.Code, w.Body)
	}
}

// TestSubnetSNMPRoutes (T020): GET/PUT status, validation naming the field,
// 404, body bound; responses never carry the community (SC-003).
func TestSubnetSNMPRoutes(t *testing.T) {
	f := newAPI(t)
	id := f.createSubnet(t, "mgmt", "10.1.112.0/24")

	w := f.req(t, "GET", p+"/subnets/"+id+"/snmp", "user", "")
	if w.Code != 200 {
		t.Fatalf("get none: %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, w)
	if b["own"] != nil || b["effective"].(map[string]any)["state"] != "none" {
		t.Fatalf("status none %s", w.Body)
	}

	w = f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":2,"community":"`+snmpCommunity+`"}`)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	b = decodeBody(t, w)
	if b["own"].(map[string]any)["version"] != float64(2) || b["effective"].(map[string]any)["state"] != "own" {
		t.Fatalf("status own %s", w.Body)
	}

	bodies := []string{w.Body.String()}
	for _, path := range []string{"/subnets/" + id + "/snmp", "/subnets", "/subnets/" + id, "/subnets/tree"} {
		w := f.req(t, "GET", p+path, "user", "")
		if w.Code != 200 {
			t.Fatalf("GET %s: %d", path, w.Code)
		}
		bodies = append(bodies, w.Body.String())
	}
	for _, body := range bodies {
		if strings.Contains(body, snmpCommunity) {
			t.Fatalf("community leaked: %s", body)
		}
	}
	if !strings.Contains(bodies[2], `"state":"own"`) {
		t.Fatalf("list lacks the summary: %s", bodies[2])
	}

	w = f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":2}`)
	if w.Code != 422 || decodeBody(t, w)["reason"] != "validation_failed" ||
		decodeBody(t, w)["detail"].(map[string]any)["field"] != "community" {
		t.Fatalf("validation: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":2,"community":"c","surprise":1}`); w.Code != 400 {
		t.Fatalf("unknown field: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":2,"community":"`+strings.Repeat("x", 5000)+`"}`); w.Code != 413 {
		t.Fatalf("body bound: %d %s", w.Code, w.Body)
	}
	missing := "018f3a2b-0000-7000-8000-00000000dead"
	if w := f.req(t, "PUT", p+"/subnets/"+missing+"/snmp", "admin", `{"version":2,"community":"c"}`); w.Code != 404 {
		t.Fatalf("missing put: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets/"+missing+"/snmp", "admin", ""); w.Code != 404 {
		t.Fatalf("missing get: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/subnets/"+id+"/snmp", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	// Another tenant cannot see the subnet's credentials.
	if w := f.req(t, "GET", p+"/subnets/"+id+"/snmp", "other", ""); w.Code != 404 {
		t.Fatalf("other tenant: %d %s", w.Code, w.Body)
	}
}

// TestSubnetPutKeepsSNMP (FR-005 over HTTP): editing the subnet with legacy
// snmp_* fields in the body keeps the credentials.
func TestSubnetPutKeepsSNMP(t *testing.T) {
	f := newAPI(t)
	id := f.createSubnet(t, "mgmt", "10.1.112.0/24")
	f.setSNMP(t, id, `{"version":2,"community":"`+snmpCommunity+`"}`)
	w := f.req(t, "PUT", p+"/subnets/"+id, "admin", `{"name":"mgmt2","cidr":"10.1.112.0/24","snmp_secret_ref":"x","snmp_version":3,"snmp":{"state":"none"}}`)
	if w.Code != 200 {
		t.Fatalf("put subnet: %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, w)
	if b["snmp_version"] != float64(2) || b["snmp_secret_ref"] != nil || b["snmp"].(map[string]any)["state"] != "own" {
		t.Fatalf("subnet after edit %s", w.Body)
	}
}

// TestSubnetSNMPv3Routes (T031): v3 over HTTP; responses carry level and
// protocols but never the user or passwords.
func TestSubnetSNMPv3Routes(t *testing.T) {
	f := newAPI(t)
	id := f.createSubnet(t, "core", "10.1.111.0/24")
	w := f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin",
		`{"version":3,"user":"labuser-X","security_level":"authPriv","auth_protocol":"SHA512","auth_password":"authpass-X1","priv_protocol":"AES192","priv_password":"privpass-X1"}`)
	if w.Code != 200 {
		t.Fatalf("put v3: %d %s", w.Code, w.Body)
	}
	own := decodeBody(t, w)["own"].(map[string]any)
	if own["security_level"] != "authPriv" || own["auth_protocol"] != "SHA512" || own["priv_protocol"] != "AES192" || own["weak"] != false {
		t.Fatalf("own %v", own)
	}
	g := f.req(t, "GET", p+"/subnets/"+id+"/snmp", "user", "")
	for _, body := range []string{w.Body.String(), g.Body.String()} {
		for _, v := range []string{"labuser-X", "authpass-X1", "privpass-X1"} {
			if strings.Contains(body, v) {
				t.Fatalf("%q leaked: %s", v, body)
			}
		}
	}
	w = f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":3,"user":"u","security_level":"authNoPriv","auth_protocol":"SHA","auth_password":"authpass1","priv_password":"privpass1"}`)
	if w.Code != 422 || decodeBody(t, w)["detail"].(map[string]any)["field"] != "priv_password" {
		t.Fatalf("priv with authNoPriv: %d %s", w.Code, w.Body)
	}
	w = f.req(t, "PUT", p+"/subnets/"+id+"/snmp", "admin", `{"version":3,"user":"u","security_level":"authNoPriv","auth_protocol":"SHA","auth_password":"short"}`)
	if w.Code != 422 || decodeBody(t, w)["detail"].(map[string]any)["field"] != "auth_password" {
		t.Fatalf("short password: %d %s", w.Code, w.Body)
	}
}

// TestSubnetSNMPTestRoute (T040): outcome in the body, address confined to
// the subnet (422 detail.field=address), 10 tests per user per minute, an
// audit row per test, never a credential in the response.
func TestSubnetSNMPTestRoute(t *testing.T) {
	f := newAPI(t)
	id := f.createSubnet(t, "mgmt", "10.1.112.0/24")
	w := f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.1.112.20"}`)
	if w.Code != 200 || decodeBody(t, w)["outcome"] != "no_credentials" {
		t.Fatalf("no creds: %d %s", w.Code, w.Body)
	}
	f.setSNMP(t, id, `{"version":2,"community":"`+snmpCommunity+`"}`)
	f.snmp.Set("10.1.112.20", snmpDevice("sw-core-1", "Cisco IOS"))
	w = f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.1.112.20"}`)
	b := decodeBody(t, w)
	if w.Code != 200 || b["outcome"] != "ok" || b["sys_name"] != "sw-core-1" || b["source_subnet_id"] != id || b["duration_ms"] == nil {
		t.Fatalf("ok: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), snmpCommunity) {
		t.Fatal("community in the test result")
	}
	w = f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.9.9.9"}`)
	if w.Code != 422 || decodeBody(t, w)["detail"].(map[string]any)["field"] != "address" {
		t.Fatalf("outside: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.1.112.20","x":1}`); w.Code != 400 {
		t.Fatalf("unknown field: %d", w.Code)
	}
	if w := f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"`+strings.Repeat("1", 2000)+`"}`); w.Code != 413 {
		t.Fatalf("body bound: %d", w.Code)
	}
	var tested int
	for _, a := range f.mem.Audit() {
		if a.Action == "snmp_credentials_tested" {
			tested++
		}
	}
	if tested != 2 {
		t.Fatalf("audit rows %d, want 2 (refused targets are not probed)", tested)
	}
	// Rate limit: 10 per user per minute, counting every request (5 above).
	codes := map[int]int{}
	for i := 0; i < 8; i++ {
		codes[f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.1.112.20"}`).Code]++
	}
	if codes[200] != 5 || codes[429] != 3 {
		t.Fatalf("rate limit: %v", codes)
	}
	w = f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "admin", `{"address":"10.1.112.20"}`)
	if w.Code != 429 || decodeBody(t, w)["reason"] != "rate_limited" {
		t.Fatalf("limited: %d %s", w.Code, w.Body)
	}
	// Another user has an own budget.
	if w := f.req(t, "POST", p+"/subnets/"+id+"/snmp/test", "user", `{"address":"10.1.112.20"}`); w.Code != 200 {
		t.Fatalf("other user: %d", w.Code)
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newRateLimiter(2, time.Minute, func() time.Time { return now })
	for i, want := range []bool{true, true, false} {
		if got := l.allow("a"); got != want {
			t.Fatalf("request %d: allow=%v, two per window", i+1, got)
		}
	}
	if !l.allow("b") {
		t.Fatal("per key")
	}
	now = now.Add(61 * time.Second)
	if !l.allow("a") {
		t.Fatal("window slides")
	}
	for i := 0; i < maxLimiterKeys; i++ {
		l.allow(fmt.Sprintf("k%d", i))
	}
	// Keys idle for a whole window are dropped once the map is full.
	now = now.Add(61 * time.Second)
	l.allow("fresh")
	if len(l.hits) != 1 {
		t.Fatalf("idle keys kept: %d", len(l.hits))
	}
}

// TestSubnetSNMPDeleteRoute (T048): DELETE clears (204), is idempotent, and
// the children fall back to inheriting.
func TestSubnetSNMPDeleteRoute(t *testing.T) {
	f := newAPI(t)
	parent := f.createSubnet(t, "site", "10.1.0.0/16")
	w := f.req(t, "POST", p+"/subnets", "admin", `{"name":"mgmt","cidr":"10.1.112.0/24","parent_id":"`+parent+`"}`)
	child, _ := decodeBody(t, w)["id"].(string)
	f.setSNMP(t, parent, `{"version":2,"community":"parent-comm"}`)
	f.setSNMP(t, child, `{"version":2,"community":"`+snmpCommunity+`"}`)
	if w := f.req(t, "DELETE", p+"/subnets/"+child+"/snmp", "admin", ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, f.req(t, "GET", p+"/subnets/"+child+"/snmp", "user", ""))
	if b["own"] != nil || b["effective"].(map[string]any)["state"] != "inherited" {
		t.Fatalf("after clear %v", b)
	}
	if w := f.req(t, "DELETE", p+"/subnets/"+child+"/snmp", "admin", ""); w.Code != 204 {
		t.Fatalf("idempotent delete: %d", w.Code)
	}
	if w := f.req(t, "DELETE", p+"/subnets/018f3a2b-0000-7000-8000-00000000dead/snmp", "admin", ""); w.Code != 404 {
		t.Fatalf("missing subnet: %d", w.Code)
	}
	cleared := 0
	for _, a := range f.mem.Audit() {
		if a.Action == "snmp_credentials_cleared" {
			cleared++
		}
	}
	if cleared != 1 {
		t.Fatalf("cleared audit rows %d", cleared)
	}
}
