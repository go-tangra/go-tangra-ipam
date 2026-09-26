package httpapi

import (
	"strings"
	"testing"
)

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
