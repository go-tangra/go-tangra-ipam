package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestAddressMACProvenanceRoutes (022 T017): a MAC set through the API is
// manual; provenance, origin and link in the body are ignored.
func TestAddressMACProvenanceRoutes(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "srv", "10.20.0.0/24")
	w := f.req(t, "POST", p+"/ip-addresses", "admin", `{"subnet_id":"`+sid+`","address":"10.20.0.5","mac_address":"0A:5C:D2:F1:00:05",
		"mac_source":"arp","mac_source_device_id":"x","mac_conflict":"y","origin":"arp","link":{"switch_id":"s","port_id":"p","source":"lldp"}}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, w)
	if b["mac_source"] != "manual" || b["mac_address"] != "0a:5c:d2:f1:00:05" || b["origin"] != nil || b["link"] != nil ||
		b["mac_conflict"] != nil || b["mac_source_device_id"] != nil {
		t.Fatalf("create body %v", b)
	}
	id := b["id"].(string)
	w = f.req(t, "PUT", p+"/ip-addresses/"+id, "admin", `{"subnet_id":"`+sid+`","address":"10.20.0.5","mac_address":""}`)
	if b := decodeBody(t, w); w.Code != 200 || b["mac_source"] != nil || b["mac_address"] != nil {
		t.Fatalf("clear %d %v", w.Code, b)
	}
}

// TestAddressLinkRoutes (022 T024): the address link object, the addresses
// behind a switch port and the links of a device's bound addresses.
func TestAddressLinkRoutes(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	sid := f.createSubnet(t, "srv", "10.21.0.0/24")
	sw := store.Device{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa01", TenantID: apiTenant, Name: "MSW-RACK2", DeviceType: store.DevSwitch}
	host := store.Device{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa02", TenantID: apiTenant, Name: "ipmi-host"}
	for _, d := range []store.Device{sw, host} {
		if err := f.mem.CreateDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	port := store.DeviceInterface{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa03", TenantID: apiTenant, DeviceID: sw.ID, Name: "14"}
	if err := f.mem.CreateInterface(ctx, port); err != nil {
		t.Fatal(err)
	}
	a := store.IPAddress{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000aa04", TenantID: apiTenant, SubnetID: sid, Address: "10.21.0.7",
		Hostname: "ipmi-7", DeviceID: host.ID, MACAddress: "0a:5c:d2:f1:00:07", MACSource: store.MACSourceARP}
	if err := f.mem.CreateAddress(ctx, a); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	if err := f.mem.SetAddressLinks(ctx, apiTenant, []store.IPAddress{{ID: a.ID, Link: &store.AddressLink{SwitchID: sw.ID, PortID: port.ID,
		PortName: "14", VLAN: 30, Source: store.LinkSNMPFDB, LastSeen: &seen}}}, nil); err != nil {
		t.Fatal(err)
	}
	w := f.req(t, "GET", p+"/ip-addresses/"+a.ID, "user", "")
	link, _ := decodeBody(t, w)["link"].(map[string]any)
	if w.Code != 200 || link["switch_name"] != "MSW-RACK2" || link["port_name"] != "14" || link["vlan"] != float64(30) ||
		link["source"] != "snmp_fdb" || link["last_seen"] == nil {
		t.Fatalf("address link %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+sw.ID+"/interfaces", "user", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"behind_addresses":[{"address_id":"`+a.ID+`","address":"10.21.0.7","hostname":"ipmi-7"}]`) {
		t.Fatalf("behind addresses %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+host.ID+"/addresses", "user", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"switch_name":"MSW-RACK2"`) {
		t.Fatalf("device addresses %d %s", w.Code, w.Body)
	}
}

// TestARPSettingsRoutes (022 T030): defaults for readers, validated writes
// with 422 detail.field, audit, CSRF, body bound and tenant scoping.
func TestARPSettingsRoutes(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	w := f.req(t, "GET", p+"/arp/settings", "user", "")
	b := decodeBody(t, w)
	if w.Code != 200 || b["enabled"] != true || b["proxy_threshold"] != float64(8) || len(b["excluded_devices"].([]any)) != 0 {
		t.Fatalf("defaults %d %s", w.Code, w.Body)
	}
	dev := store.Device{ID: "0190f7c2-aaaa-7c1a-9b2e-00000000ab01", TenantID: apiTenant, Name: "fortigate", DeviceType: store.DevFirewall}
	if err := f.mem.CreateDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	w = f.req(t, "PUT", p+"/arp/settings", "admin", `{"enabled":false,"excluded_devices":["`+dev.ID+`","`+dev.ID+`"],"proxy_threshold":20}`)
	b = decodeBody(t, w)
	if w.Code != 200 || b["enabled"] != false || b["proxy_threshold"] != float64(20) || len(b["excluded_devices"].([]any)) != 1 || b["updated_by"] != apiAdmin {
		t.Fatalf("put %d %s", w.Code, w.Body)
	}
	found := false
	for _, a := range f.mem.Audit() {
		if a.Action == "arp_settings_updated" {
			found = a.ActorKind == "user" && a.ActorID == apiAdmin && a.SubjectKind == "tenant" && a.Detail["excluded_count"] == 1 &&
				a.Detail["proxy_threshold"] == 20 && a.Detail["enabled"] == false
		}
	}
	if !found {
		t.Fatal("arp_settings_updated audit")
	}
	many := make([]string, 257)
	for i := range many {
		many[i] = `"0190f7c2-aaaa-7c1a-9b2e-` + strings.Repeat("0", 9) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + `0"`
	}
	for body, field := range map[string]string{
		`{"enabled":true,"excluded_devices":[],"proxy_threshold":1}`:                                       "proxy_threshold",
		`{"enabled":true,"excluded_devices":[],"proxy_threshold":257}`:                                     "proxy_threshold",
		`{"enabled":true,"excluded_devices":["0190f7c2-aaaa-7c1a-9b2e-00000000ffff"],"proxy_threshold":8}`: "excluded_devices",
		`{"enabled":true,"excluded_devices":["not-a-uuid"],"proxy_threshold":8}`:                           "excluded_devices",
		`{"enabled":true,"excluded_devices":[` + strings.Join(many, ",") + `],"proxy_threshold":8}`:        "excluded_devices",
		`{"excluded_devices":[],"proxy_threshold":8}`:                                                      "enabled",
		`{"enabled":true,"excluded_devices":[]}`:                                                           "proxy_threshold",
	} {
		w := f.req(t, "PUT", p+"/arp/settings", "admin", body)
		if w.Code != 422 {
			t.Errorf("%.70s: %d %s", body, w.Code, w.Body)
			continue
		}
		b := decodeBody(t, w)
		d, _ := b["detail"].(map[string]any)
		if b["reason"] != "validation_failed" || d["field"] != field {
			t.Errorf("%.70s: %s", body, w.Body)
		}
	}
	if w := f.req(t, "PUT", p+"/arp/settings", "admin", `{"enabled":true,"excluded_devices":[],"proxy_threshold":8,"surprise":1}`); w.Code/100 != 4 {
		t.Errorf("unknown field accepted: %d", w.Code)
	}
	if w := f.req(t, "PUT", p+"/arp/settings", "admin", `{"enabled":true,"excluded_devices":[],"proxy_threshold":8,"x":"`+strings.Repeat("x", 17000)+`"}`); w.Code != 413 {
		t.Errorf("oversized body: %d", w.Code)
	}
	r := httptest.NewRequest("PUT", "https://localhost"+p+"/arp/settings", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer admin")
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rw, r)
	if rw.Code != 422 {
		t.Fatalf("missing CSRF: %d", rw.Code)
	}
	// Another tenant still sees its defaults; unauthenticated is refused.
	if w := f.req(t, "GET", p+"/arp/settings", "other", ""); w.Code != 200 || decodeBody(t, w)["enabled"] != true {
		t.Fatalf("tenant isolation %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/arp/settings", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated")
	}
}

// TestAddressMACSearch (022 T035): full or partial MACs in any common
// notation; anything else is 422 with detail.field = mac.
func TestAddressMACSearch(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "srv", "10.22.0.0/24")
	for ip, mac := range map[string]string{"10.22.0.5": "0a:5c:d2:f1:00:05", "10.22.0.6": "0a:5c:d2:f1:00:06", "10.22.0.7": ""} {
		if w := f.req(t, "POST", p+"/ip-addresses", "admin", `{"subnet_id":"`+sid+`","address":"`+ip+`","mac_address":"`+mac+`"}`); w.Code != 201 {
			t.Fatalf("seed %s %d", ip, w.Code)
		}
	}
	for q, want := range map[string]int{"0a5c": 2, "D2-F1": 2, "0a5c.d2f1.0005": 1, "0A:5C:D2:F1:00:06": 1, "f1%2000": 2, "ffff": 0} {
		w := f.req(t, "GET", p+"/ip-addresses?mac="+q, "user", "")
		items, _ := decodeBody(t, w)["items"].([]any)
		if w.Code != 200 || len(items) != want {
			t.Errorf("mac=%s: %d rows (want %d) %d", q, len(items), want, w.Code)
		}
	}
	for _, q := range []string{"a", "0a5cd2f1000500", "zz", "0a_5c", "%27"} {
		w := f.req(t, "GET", p+"/ip-addresses?mac="+q, "user", "")
		b := decodeBody(t, w)
		d, _ := b["detail"].(map[string]any)
		if w.Code != 422 || b["reason"] != "validation_failed" || d["field"] != "mac" {
			t.Errorf("mac=%s: %d %s", q, w.Code, w.Body)
		}
	}
}
