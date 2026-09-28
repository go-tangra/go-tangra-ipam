package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

const bmcRef2 = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e10"

// newDevice posts a device without BMC credentials and returns its id.
func (f *apiFixture) newDevice(t *testing.T, body string) string {
	t.Helper()
	w := f.req(t, "POST", p+"/devices", "admin", body)
	if w.Code != 201 {
		t.Fatalf("create device: %d %s", w.Code, w.Body)
	}
	id, _ := decodeBody(t, w)["id"].(string)
	return id
}

func (f *apiFixture) auditActions(subject string) []string {
	var out []string
	for _, r := range f.mem.Audit() {
		if r.SubjectID == subject {
			out = append(out, r.Action)
		}
	}
	return out
}

func checkReason(t *testing.T, got int, body string, code int, reason string) {
	t.Helper()
	if got != code || !strings.Contains(body, `"reason":"`+reason+`"`) {
		t.Fatalf("want %d %s, got %d %s", code, reason, got, body)
	}
}

// TestDeviceBMCReference (024 T016): status per viewer, set/change/clear with
// audit rows, and every refusal leaves the device untouched.
func TestDeviceBMCReference(t *testing.T) {
	f := newAPI(t)
	f.warden.Put(bmcRef2, warden.SecretMeta{Name: "other", Username: "root"}, "pw-2")
	did := f.newDevice(t, `{"name":"node-1","device_type":"server","management_ip":"10.1.112.14"}`)
	path := p + "/devices/" + did + "/bmc"

	w := f.req(t, "GET", path, "user", "")
	if w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	st := decodeBody(t, w)
	if st["configured"] != false || st["ready"] != false || st["reason"] != "bmc_not_configured" || st["address"] != "10.1.112.14" || st["address_source"] != "management_ip" {
		t.Fatalf("unconfigured status: %v", st)
	}

	w = f.req(t, "PUT", path, "admin", `{"reference":"`+bmcRef+`"}`)
	if w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	st = decodeBody(t, w)
	sec, _ := st["secret"].(map[string]any)
	if st["ready"] != true || st["reference"] != bmcRef || sec["name"] != "bmc-1" || sec["username"] != bmcUser {
		t.Fatalf("set status: %v", st)
	}
	if strings.Contains(w.Body.String(), bmcPass) {
		t.Fatal("password in response")
	}
	if a := f.auditActions(did); len(a) != 1 || a[0] != "bmc_reference_set" {
		t.Fatalf("audit %v", a)
	}
	// The device JSON carries the pointer; the device list shows it too.
	if w := f.req(t, "GET", p+"/devices/"+did, "user", ""); !strings.Contains(w.Body.String(), bmcRef) {
		t.Fatalf("device json: %s", w.Body)
	}

	// Another viewer without access in warden sees "forbidden", no metadata.
	f.warden.Deny("user", bmcRef)
	st = decodeBody(t, f.req(t, "GET", path, "user", ""))
	if st["access"] != "forbidden" || st["reason"] != "bmc_secret_forbidden" || st["secret"] != nil || st["configured"] != true {
		t.Fatalf("forbidden viewer: %v", st)
	}

	// Refusals: unreadable, unknown, malformed, warden down, other tenant.
	f.warden.Deny("admin", bmcRef2)
	w = f.req(t, "PUT", path, "admin", `{"reference":"`+bmcRef2+`"}`)
	checkReason(t, w.Code, w.Body.String(), 403, "bmc_secret_forbidden")
	w = f.req(t, "PUT", path, "admin", `{"reference":"01928f7e-3c1a-7b44-9d2e-000000000000"}`)
	checkReason(t, w.Code, w.Body.String(), 422, "bmc_secret_not_found")
	for _, body := range []string{`{"reference":"not-a-uuid"}`, `{}`} {
		w := f.req(t, "PUT", path, "admin", body)
		if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"reference"`) {
			t.Fatalf("bad reference %s: %d %s", body, w.Code, w.Body)
		}
	}
	w = f.req(t, "PUT", path, "admin", `{"reference":"`+bmcRef+`","x":1}`)
	checkReason(t, w.Code, w.Body.String(), 400, "malformed_body")
	w = f.req(t, "PUT", path, "admin", `not json`)
	checkReason(t, w.Code, w.Body.String(), 400, "malformed_body")
	f.warden.SetUnavailable(true)
	w = f.req(t, "PUT", path, "admin", `{"reference":"`+bmcRef+`"}`)
	checkReason(t, w.Code, w.Body.String(), 503, "warden_unavailable")
	st = decodeBody(t, f.req(t, "GET", path, "admin", ""))
	if st["access"] != "unavailable" || st["reason"] != "warden_unavailable" {
		t.Fatalf("unavailable status: %v", st)
	}
	f.warden.SetUnavailable(false)
	if w := f.req(t, "PUT", path, "other", `{"reference":"`+bmcRef+`"}`); w.Code != 404 {
		t.Fatalf("other tenant put: %d", w.Code)
	}
	if w := f.req(t, "GET", path, "other", ""); w.Code != 404 {
		t.Fatalf("other tenant get: %d", w.Code)
	}
	if w := f.req(t, "DELETE", path, "other", ""); w.Code != 404 {
		t.Fatalf("other tenant delete: %d", w.Code)
	}
	if d, _ := f.mem.GetDevice(context.Background(), apiTenant, did); d.IPMISecretRef != bmcRef {
		t.Fatalf("refusals changed the reference: %q", d.IPMISecretRef)
	}
	if a := f.auditActions(did); len(a) != 1 {
		t.Fatalf("refusals wrote audit rows: %v", a)
	}

	// Change, then clear (twice).
	// Change to a third secret (admin is denied bmcRef2).
	ref3 := "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e11"
	f.warden.Put(ref3, warden.SecretMeta{Name: "third"}, "pw-3")
	if w := f.req(t, "PUT", path, "admin", `{"reference":"`+ref3+`"}`); w.Code != 200 {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "DELETE", path, "admin", ""); w.Code != 204 {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "DELETE", path, "admin", ""); w.Code != 204 {
		t.Fatalf("clear again: %d %s", w.Code, w.Body)
	}
	if a := f.auditActions(did); strings.Join(a, ",") != "bmc_reference_set,bmc_reference_changed,bmc_reference_cleared" {
		t.Fatalf("audit %v", a)
	}
	if w := f.req(t, "GET", p+"/devices/"+randUUID+"/bmc", "admin", ""); w.Code != 404 {
		t.Fatalf("missing device: %d", w.Code)
	}
	if w := f.req(t, "GET", path, "", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

// TestDeviceBMCReportedAddress: without a management IP the address the agent
// reported on the bmc interface is used; the primary IP never is.
func TestDeviceBMCReportedAddress(t *testing.T) {
	f := newAPI(t)
	did := f.newDevice(t, `{"name":"node-1","device_type":"server","primary_ip":"10.1.111.20"}`)
	st := decodeBody(t, f.req(t, "GET", p+"/devices/"+did+"/bmc", "admin", ""))
	if st["address"] != nil || st["reason"] != "bmc_not_configured" {
		t.Fatalf("primary ip used: %v", st)
	}
	if err := f.mem.CreateAddress(context.Background(), store.IPAddress{TenantID: apiTenant, Address: "10.1.112.14", DeviceID: did, InterfaceName: "bmc"}); err != nil {
		t.Fatal(err)
	}
	if w := f.req(t, "PUT", p+"/devices/"+did+"/bmc", "admin", `{"reference":"`+bmcRef+`"}`); w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	st = decodeBody(t, f.req(t, "GET", p+"/devices/"+did+"/bmc", "admin", ""))
	if st["address"] != "10.1.112.14" || st["address_source"] != "reported" || st["ready"] != true {
		t.Fatalf("reported address: %v", st)
	}
}

// TestDeviceBodyCannotChangeBMCReference (024 T017): the device create/update
// body cannot set or change the reference; omitting it keeps it.
func TestDeviceBodyCannotChangeBMCReference(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/devices", "admin", `{"name":"x","device_type":"server","ipmi_secret_ref":"`+bmcRef+`"}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"ipmi_secret_ref"`) {
		t.Fatalf("create with ref: %d %s", w.Code, w.Body)
	}
	did := f.newDevice(t, `{"name":"node-1","device_type":"server","management_ip":"10.0.0.9"}`)
	if w := f.req(t, "PUT", p+"/devices/"+did+"/bmc", "admin", `{"reference":"`+bmcRef+`"}`); w.Code != 200 {
		t.Fatalf("set: %d", w.Code)
	}
	w = f.req(t, "PUT", p+"/devices/"+did, "admin", `{"name":"node-1","device_type":"server","ipmi_secret_ref":"`+bmcRef2+`"}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"field":"ipmi_secret_ref"`) {
		t.Fatalf("update with other ref: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/devices/"+did, "admin", `{"name":"node-1","device_type":"server","ipmi_secret_ref":"`+bmcRef+`"}`); w.Code != 200 {
		t.Fatalf("update with same ref: %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/devices/"+did, "admin", `{"name":"node-1b","device_type":"server"}`); w.Code != 200 {
		t.Fatalf("update without ref: %d %s", w.Code, w.Body)
	}
	if d, _ := f.mem.GetDevice(context.Background(), apiTenant, did); d.IPMISecretRef != bmcRef || d.Name != "node-1b" {
		t.Fatalf("reference lost: %+v", d)
	}
}
