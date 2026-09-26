package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const hsHost = "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa01"

func (f *apiFixture) reportHost(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	f.inv.Put(invclient.Report{TenantID: apiTenant, Host: invclient.Host{ID: hsHost, Hostname: "hv-01", Status: "active"},
		CollectedAt: now, ChangedAt: now, Digest: strings.Repeat("a", 64), OSFamily: "linux",
		Interfaces: []invclient.Interface{{Name: "eth0", MAC: "aa:bb:cc:00:00:01", Type: "ethernet", Up: true,
			Addresses: []invclient.Address{{Address: "10.40.0.5", PrefixLength: 24}}}},
		Guests:  []invclient.Guest{{ID: "101", Name: "vm-a", Kind: "vm", MACs: []string{"bc:24:11:00:00:01"}}},
		Updates: invclient.UpdateState{Status: "unknown"}})
	if err := f.runner.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	l, _ := f.mem.HostDevices(context.Background(), apiTenant)
	if len(l) != 1 {
		t.Fatal("host device not created")
	}
	return l[0].ID
}

func TestHostSyncSettingsRoutes(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "GET", p+"/host-sync/settings", "user", "")
	if w.Code != 200 || decodeBody(t, w)["enabled"] != true {
		t.Fatalf("get settings %d %s", w.Code, w.Body)
	}
	w = f.req(t, "PUT", p+"/host-sync/settings", "admin", `{"enabled":false,"full_interval_minutes":30,"excluded_interfaces":["docker*"]}`)
	if w.Code != 200 || decodeBody(t, w)["enabled"] != false || decodeBody(t, w)["updated_by"] != apiAdmin {
		t.Fatalf("put settings %d %s", w.Code, w.Body)
	}
	found := false
	for _, a := range f.mem.Audit() {
		if a.Action == "hostsync_settings_updated" && a.ActorKind == "user" && a.ActorID == apiAdmin {
			found = a.Detail["changes"] != nil
		}
	}
	if !found {
		t.Fatal("settings audit with before/after and the user actor")
	}
	for body, code := range map[string]int{
		`{"enabled":true,"full_interval_minutes":5,"excluded_interfaces":[]}`:                 400,
		`{"enabled":true,"full_interval_minutes":60,"excluded_interfaces":["a b"]}`:           400,
		`{"enabled":true,"full_interval_minutes":60,"excluded_interfaces":[],"surprise":true}`: 400,
		`not json`: 400,
		`{"enabled":true,"full_interval_minutes":60,"excluded_interfaces":["` + strings.Repeat("x", 17000) + `"]}`: 413,
	} {
		if w := f.req(t, "PUT", p+"/host-sync/settings", "admin", body); w.Code != code {
			t.Errorf("%.60s: %d want %d (%s)", body, w.Code, code, w.Body)
		}
	}
	// CSRF parameter required on writes.
	r := httptest.NewRequest("PUT", "https://localhost"+p+"/host-sync/settings", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer admin")
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rw, r)
	if rw.Code != 422 { // refused by the OpenAPI validator (validation_failed)
		t.Fatalf("missing CSRF: %d", rw.Code)
	}
	if w := f.req(t, "GET", p+"/host-sync/settings", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated")
	}
	w = f.req(t, "GET", p+"/host-sync/status", "user", "")
	if w.Code != 200 || decodeBody(t, w)["state"] != "disabled" {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/host-sync/resync", "admin", ""); w.Code != 409 || decodeBody(t, w)["reason"] != "host_sync_disabled" {
		t.Fatalf("resync while disabled %d %s", w.Code, w.Body)
	}
	_ = f.req(t, "PUT", p+"/host-sync/settings", "admin", `{"enabled":true,"full_interval_minutes":60,"excluded_interfaces":[]}`)
	if w := f.req(t, "POST", p+"/host-sync/resync", "admin", ""); w.Code != 202 || decodeBody(t, w)["scheduled"] != true {
		t.Fatalf("resync all %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/host-sync/resync", "admin", `{"x":1}`); w.Code != 400 {
		t.Fatalf("unknown field %d", w.Code)
	}
	if w := f.req(t, "POST", p+"/host-sync/resync", "admin", `{`+strings.Repeat(" ", 2000)+`}`); w.Code != 413 {
		t.Fatalf("oversized action body %d", w.Code)
	}
}

func TestDeviceHostSyncRoutes(t *testing.T) {
	f := newAPI(t)
	id := f.reportHost(t)
	w := f.req(t, "GET", p+"/devices/"+id+"/host-sync", "user", "")
	if w.Code != 200 || decodeBody(t, w)["source"] != "host_report" || decodeBody(t, w)["inventory_host_id"] != hsHost {
		t.Fatalf("device host sync %d %s", w.Code, w.Body)
	}
	w = f.req(t, "POST", p+"/devices/"+id+"/host-sync", "admin", "")
	if w.Code != 200 || decodeBody(t, w)["applied"] != true {
		t.Fatalf("resync %d %s", w.Code, w.Body)
	}
	w = f.req(t, "GET", p+"/devices/"+id+"/guests", "user", "")
	items, _ := decodeBody(t, w)["items"].([]any)
	if w.Code != 200 || len(items) != 1 || items[0].(map[string]any)["guest_ref"] != "101" {
		t.Fatalf("guests %d %s", w.Code, w.Body)
	}
	// Tenant scoping: another tenant sees nothing.
	if w := f.req(t, "GET", p+"/devices/"+id+"/guests", "other", ""); w.Code != 404 {
		t.Fatalf("cross-tenant guests %d", w.Code)
	}
	if w := f.req(t, "POST", p+"/devices/"+id+"/host-sync", "other", ""); w.Code != 404 {
		t.Fatalf("cross-tenant resync %d", w.Code)
	}
	// Manual device -> 409 not_host_reported.
	w = f.req(t, "POST", p+"/devices", "admin", `{"name":"manual-1"}`)
	mid, _ := decodeBody(t, w)["id"].(string)
	if w := f.req(t, "POST", p+"/devices/"+mid+"/host-sync", "admin", ""); w.Code != 409 || decodeBody(t, w)["reason"] != "not_host_reported" {
		t.Fatalf("manual resync %d %s", w.Code, w.Body)
	}
	// Inventory down -> 503.
	f.inv.Down = true
	if w := f.req(t, "POST", p+"/devices/"+id+"/host-sync", "admin", ""); w.Code != 503 || decodeBody(t, w)["reason"] != "temporarily_unavailable" {
		t.Fatalf("down %d %s", w.Code, w.Body)
	}
	f.inv.Down = false
	f.inv.Delete(apiTenant, hsHost)
	if w := f.req(t, "POST", p+"/devices/"+id+"/host-sync", "admin", ""); w.Code != 404 {
		t.Fatalf("host without report %d", w.Code)
	}
	// Disabled -> 409 host_sync_disabled.
	_ = f.req(t, "PUT", p+"/host-sync/settings", "admin", `{"enabled":false,"full_interval_minutes":60,"excluded_interfaces":[]}`)
	if w := f.req(t, "POST", p+"/devices/"+id+"/host-sync", "admin", ""); w.Code != 409 || decodeBody(t, w)["reason"] != "host_sync_disabled" {
		t.Fatalf("disabled %d %s", w.Code, w.Body)
	}
	// Filters on the list routes.
	w = f.req(t, "GET", p+"/devices?source=host_report&report_state=reported", "user", "")
	if l, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(l) != 1 {
		t.Fatalf("device filters %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices?source=agent", "user", ""); w.Code != 422 {
		t.Fatalf("filter enum %d", w.Code)
	}
	w = f.req(t, "GET", p+"/ip-addresses?conflict=false&report_state=reported", "user", "")
	if l, _ := decodeBody(t, w)["items"].([]any); w.Code != 200 || len(l) != 1 {
		t.Fatalf("address filters %d %s", w.Code, w.Body)
	}
}

func TestClearConflictRoute(t *testing.T) {
	f := newAPI(t)
	sid := f.createSubnet(t, "lan", "10.50.0.0/24")
	_ = f.mem.CreateAddress(context.Background(), store.IPAddress{ID: "0190f7c2-cccc-7c1a-9b2e-cccccccccccc", TenantID: apiTenant, Address: "10.50.0.9", SubnetID: sid})
	w := f.req(t, "POST", p+"/ip-addresses/0190f7c2-cccc-7c1a-9b2e-cccccccccccc/clear-conflict", "admin", "{}")
	if w.Code != 200 || decodeBody(t, w)["address"] != "10.50.0.9" {
		t.Fatalf("clear %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/ip-addresses/0190f7c2-cccc-7c1a-9b2e-cccccccccccc/clear-conflict", "other", ""); w.Code != 404 {
		t.Fatalf("cross-tenant clear %d", w.Code)
	}
}

func TestHostSyncRoutesWithoutService(t *testing.T) {
	f := newAPI(t)
	s, err := NewHandler(f.s.rt, WithVerifier(f.s.verifier))
	if err != nil {
		t.Fatal(err)
	}
	s.Register(Deps{})
	r := httptest.NewRequest("GET", "https://localhost"+p+"/host-sync/status", nil)
	r.Header.Set("Authorization", "Bearer user")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("no host sync configured: %d", w.Code)
	}
}
