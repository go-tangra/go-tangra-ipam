package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestPowerNoBMCRef: a device with no BMC reference is refused 409
// bmc_not_configured on every OOB route (024; was a bare 422).
func TestPowerNoBMCRef(t *testing.T) {
	f := newAPI(t)
	// Plain device: no management_ip, no ipmi_secret_ref.
	w := f.req(t, "POST", p+"/devices", "admin", `{"name":"plain","device_type":"server"}`)
	did, _ := decodeBody(t, w)["id"].(string)

	for _, route := range []struct{ method, path string }{
		{"GET", p + "/devices/" + did + "/power"},
		{"GET", p + "/devices/" + did + "/sensors"},
		{"GET", p + "/devices/" + did + "/sel"},
		{"POST", p + "/devices/" + did + "/kvm-session"},
	} {
		body := ""
		if route.method == "POST" && route.path[len(route.path)-5:] != "ssion" {
			body = `{"action":"on"}`
		}
		w := f.req(t, route.method, route.path, "admin", body)
		checkReason(t, w.Code, w.Body.String(), 409, "bmc_not_configured")
	}
}

// TestPowerMissingDevice covers loadBMC's not-found branch on a random id.
func TestPowerMissingDevice(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/devices/"+randUUID+"/power", "admin", ""); w.Code != 404 {
		t.Fatalf("power missing device: want 404, got %d %s", w.Code, w.Body)
	}
}

// TestPowerWardenMissingSecret: the device references a secret warden does
// not hold -> 409 bmc_secret_not_found.
func TestPowerWardenMissingSecret(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/devices", "admin",
		`{"name":"ghost","device_type":"server","management_ip":"10.0.0.1"}`)
	did, _ := decodeBody(t, w)["id"].(string)
	if _, err := f.mem.SetDeviceBMCRef(context.Background(), apiTenant, did, "01928f7e-3c1a-7b44-9d2e-00000000dead", store.AuditRow{}); err != nil {
		t.Fatal(err)
	}
	w = f.req(t, "GET", p+"/devices/"+did+"/power", "admin", "")
	checkReason(t, w.Code, w.Body.String(), 409, "bmc_secret_not_found")
}

// TestPowerUnknownAction covers ipmi.ErrUnknownAction -> 400 bad_request.
func TestPowerUnknownAction(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	if w := f.req(t, "POST", p+"/devices/"+did+"/power", "admin",
		`{"action":"frobnicate"}`); w.Code != 400 {
		t.Fatalf("unknown action: want 400, got %d %s", w.Code, w.Body)
	}
}

// TestPowerMalformedBody covers the POST /power DecodeJSON error branch after a
// successful bmcSetup.
func TestPowerMalformedBody(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	if w := f.req(t, "POST", p+"/devices/"+did+"/power", "admin",
		`{"unknown_field":true}`); w.Code != 400 {
		t.Fatalf("power malformed: want 400, got %d %s", w.Code, w.Body)
	}
}

// TestCatchAllNotFound covers the mux's "/" catch-all 404 handler for an
// undeclared path.
func TestCatchAllNotFound(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", "/no/such/route", "admin", ""); w.Code != 404 {
		t.Fatalf("catch-all: want 404, got %d %s", w.Code, w.Body)
	}
}

// TestPowerBMCError: an unclassified BMC failure is 502 bmc_error for
// status/sensors/sel/action.
func TestPowerBMCError(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	f.bmc.Err = errors.New("bmc offline")

	if w := f.req(t, "GET", p+"/devices/"+did+"/power", "admin", ""); w.Code != 502 {
		t.Fatalf("power status err: want 502, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sensors", "admin", ""); w.Code != 502 {
		t.Fatalf("sensors err: want 502, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sel", "admin", ""); w.Code != 502 {
		t.Fatalf("sel err: want 502, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/devices/"+did+"/power", "admin", `{"action":"on"}`); w.Code != 502 {
		t.Fatalf("power action err: want 502, got %d %s", w.Code, w.Body)
	}
}
