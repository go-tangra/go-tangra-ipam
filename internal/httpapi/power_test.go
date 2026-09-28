package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

type oobRoute struct{ method, suffix, body string }

var oobRoutes = []oobRoute{
	{"GET", "/power", ""},
	{"POST", "/power", `{"action":"cycle"}`},
	{"GET", "/sensors", ""},
	{"GET", "/sel", ""},
	{"POST", "/kvm-session", ""},
}

func (f *apiFixture) auditRows(action string) []store.AuditRow {
	var out []store.AuditRow
	for _, r := range f.mem.Audit() {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// TestOOBUsesWardenCredentialsPerUser (024 T029): every out-of-band route
// fetches the credentials from warden with the caller's token at the moment
// of the call (a rotation is picked up), passes them to the BMC, and uses the
// agent-reported BMC address when there is no management IP.
func TestOOBUsesWardenCredentialsPerUser(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	for _, rt := range oobRoutes {
		f.warden.ResetCalls()
		w := f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "admin", rt.body)
		if w.Code != 200 && w.Code != 201 {
			t.Fatalf("%s %s: %d %s", rt.method, rt.suffix, w.Code, w.Body)
		}
		calls := f.warden.Calls()
		if len(calls) != 1 || calls[0].Op != warden.OpCredentials || calls[0].Token != "admin" || calls[0].Ref != bmcRef {
			t.Fatalf("%s %s warden calls: %+v", rt.method, rt.suffix, calls)
		}
		if strings.Contains(w.Body.String(), bmcPass) {
			t.Fatalf("%s %s leaks the password", rt.method, rt.suffix)
		}
	}
	if f.bmc.LastHost != "10.99.0.10" || f.bmc.LastCreds.Username != bmcUser || f.bmc.LastCreds.Password != bmcPass ||
		f.bmc.LastCreds.Protocol != "2.0" || f.bmc.LastCreds.Port != 623 {
		t.Fatalf("BMC got host=%q user=%q proto=%q port=%d", f.bmc.LastHost, f.bmc.LastCreds.Username, f.bmc.LastCreds.Protocol, f.bmc.LastCreds.Port)
	}

	// Rotation in warden takes effect on the next action (nothing cached).
	f.warden.SetPassword(bmcRef, "rotated-pw")
	if w := f.req(t, "GET", p+"/devices/"+did+"/power", "admin", ""); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if f.bmc.LastCreds.Password != "rotated-pw" {
		t.Fatal("rotated password not used")
	}

	// Reported BMC address (no management IP).
	rep := f.newDevice(t, `{"name":"node-2","device_type":"server","primary_ip":"10.1.111.21"}`)
	if err := f.mem.CreateAddress(context.Background(), store.IPAddress{TenantID: apiTenant, Address: "10.1.112.15", DeviceID: rep, InterfaceName: "bmc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mem.SetDeviceBMCRef(context.Background(), apiTenant, rep, bmcRef, store.AuditRow{}); err != nil {
		t.Fatal(err)
	}
	if w := f.req(t, "GET", p+"/devices/"+rep+"/sensors", "admin", ""); w.Code != 200 || f.bmc.LastHost != "10.1.112.15" {
		t.Fatalf("reported address: %d host=%q", w.Code, f.bmc.LastHost)
	}
}

// TestOOBRefusedByWardenNeverContactsBMC (024 T029, SC-004): a platform admin
// whom warden refuses the secret gets bmc_secret_forbidden on every route and
// the BMC sees nothing; a tenant user is refused before warden is asked.
func TestOOBRefusedByWardenNeverContactsBMC(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	f.warden.Deny("admin", bmcRef)
	for _, rt := range oobRoutes {
		w := f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "admin", rt.body)
		checkReason(t, w.Code, w.Body.String(), 403, "bmc_secret_forbidden")
	}
	if f.bmc.Calls != 0 || len(f.bmc.Actions) != 0 {
		t.Fatalf("BMC contacted despite the refusal: calls=%d", f.bmc.Calls)
	}
	f.warden.ResetCalls()
	for _, rt := range oobRoutes {
		w := f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "user", rt.body)
		checkReason(t, w.Code, w.Body.String(), 403, "forbidden")
	}
	if len(f.warden.Calls()) != 0 {
		t.Fatalf("warden asked for a non platform-admin: %+v", f.warden.Calls())
	}
}

// TestOOBAudit (024 T030): power actions and KVM sessions are audit rows with
// their outcome; reads are not.
func TestOOBAudit(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	for _, rt := range oobRoutes {
		f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "admin", rt.body)
	}
	power := f.auditRows("power_action")
	if len(power) != 1 || power[0].Outcome != "ok" || power[0].Detail["action"] != "cycle" || power[0].Target != "10.99.0.10" ||
		power[0].SubjectKind != "power" || power[0].SubjectID != did || power[0].ActorID != apiAdmin {
		t.Fatalf("power audit: %+v", power)
	}
	if kvm := f.auditRows("kvm_session_started"); len(kvm) != 1 || kvm[0].Outcome != "ok" || kvm[0].SubjectKind != "kvm" {
		t.Fatalf("kvm audit: %+v", kvm)
	}

	f.warden.Deny("admin", bmcRef)
	f.req(t, "POST", p+"/devices/"+did+"/power", "admin", `{"action":"off"}`)
	f.req(t, "POST", p+"/devices/"+did+"/kvm-session", "admin", "")
	power = f.auditRows("power_action")
	if len(power) != 2 || power[1].Outcome != "refused" || power[1].Reason != "bmc_secret_forbidden" || power[1].Detail["action"] != "off" {
		t.Fatalf("refused power audit: %+v", power)
	}
	if kvm := f.auditRows("kvm_session_started"); len(kvm) != 2 || kvm[1].Outcome != "refused" {
		t.Fatalf("refused kvm audit: %+v", kvm)
	}

	f2 := newAPI(t)
	d2 := f2.newDeviceWithBMC(t)
	f2.bmc.Err = ipmi.ErrUnreachable
	f2.req(t, "POST", p+"/devices/"+d2+"/power", "admin", `{"action":"on"}`)
	if r := f2.auditRows("power_action"); len(r) != 1 || r[0].Outcome != "error" || r[0].Reason != "bmc_unreachable" {
		t.Fatalf("error power audit: %+v", r)
	}
	f2.bmc.Err = nil
	if w := f2.req(t, "POST", p+"/devices/"+d2+"/power", "admin", `{"action":"frobnicate"}`); w.Code != 400 {
		t.Fatalf("unknown action: %d", w.Code)
	}
	if r := f2.auditRows("power_action"); len(r) != 2 || r[1].Reason != "bad_request" {
		t.Fatalf("bad action audit: %+v", r)
	}
	if f2.bmc.Calls != 1 {
		t.Fatalf("unknown action reached the BMC: %d", f2.bmc.Calls)
	}
	for _, r := range f.mem.Audit() {
		if r.Action == "power_status" || strings.Contains(r.Action, "sensor") || strings.Contains(r.Action, "sel") {
			t.Fatalf("read audited: %+v", r)
		}
	}
}

// TestOOBReasons (024 T035): each failure cause has its documented status and
// reason on every out-of-band route; BMC-side reasons name the address.
func TestOOBReasons(t *testing.T) {
	type setup func(f *apiFixture) string
	withRef := func(f *apiFixture) string { return f.newDeviceWithBMC(t) }
	cases := []struct {
		name   string
		setup  setup
		status int
		reason string
		addr   bool
	}{
		{"not configured", func(f *apiFixture) string {
			return f.newDevice(t, `{"name":"n","device_type":"server","management_ip":"10.0.0.1"}`)
		}, 409, "bmc_not_configured", false},
		{"no address", func(f *apiFixture) string {
			id := f.newDevice(t, `{"name":"n","device_type":"server","primary_ip":"10.0.0.1"}`)
			_, _ = f.mem.SetDeviceBMCRef(context.Background(), apiTenant, id, bmcRef, store.AuditRow{})
			return id
		}, 409, "bmc_no_address", false},
		{"forbidden", func(f *apiFixture) string { f.warden.Deny("admin", bmcRef); return withRef(f) }, 403, "bmc_secret_forbidden", false},
		{"secret gone", func(f *apiFixture) string { f.warden.Remove(bmcRef); return withRef(f) }, 409, "bmc_secret_not_found", false},
		{"warden down", func(f *apiFixture) string { f.warden.SetUnavailable(true); return withRef(f) }, 503, "warden_unavailable", false},
		{"bmc unreachable", func(f *apiFixture) string {
			f.bmc.Err = fmt.Errorf("x: %w", ipmi.ErrUnreachable)
			return withRef(f)
		}, 504, "bmc_unreachable", true},
		{"bmc auth", func(f *apiFixture) string {
			f.bmc.Err = fmt.Errorf("%w: rakp2 authcode (password %s)", ipmi.ErrAuthFailed, bmcPass)
			return withRef(f)
		}, 502, "bmc_auth_failed", true},
		{"bmc error", func(f *apiFixture) string {
			f.bmc.Err = errors.New("completion code 0xc1 " + bmcPass)
			return withRef(f)
		}, 502, "bmc_error", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPI(t)
			did := tc.setup(f)
			for _, rt := range oobRoutes {
				if tc.addr && rt.suffix == "/kvm-session" {
					continue // the console logs in through the BMC web UI (TestKVMSessionLoginReasons)
				}
				w := f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "admin", rt.body)
				checkReason(t, w.Code, w.Body.String(), tc.status, tc.reason)
				if tc.addr != strings.Contains(w.Body.String(), `"address":"10.99.0.10"`) {
					t.Fatalf("%s %s address detail: %s", rt.method, rt.suffix, w.Body)
				}
				if strings.Contains(w.Body.String(), bmcPass) {
					t.Fatalf("%s %s leaks: %s", rt.method, rt.suffix, w.Body)
				}
			}
		})
	}
}

// TestOOBWithoutBMCService: the routes refuse when the BMC service is not wired.
func TestOOBWithoutBMCService(t *testing.T) {
	f := newAPIMut(t, nil, func(d *Deps) { d.BMCRefs = nil })
	did := f.newDeviceWithBMC(t)
	for _, path := range []string{"/bmc"} {
		w := f.req(t, "GET", p+"/devices/"+did+path, "admin", "")
		checkReason(t, w.Code, w.Body.String(), 503, "temporarily_unavailable")
	}
	for _, rt := range oobRoutes {
		w := f.req(t, rt.method, p+"/devices/"+did+rt.suffix, "admin", rt.body)
		checkReason(t, w.Code, w.Body.String(), 503, "temporarily_unavailable")
	}
}
