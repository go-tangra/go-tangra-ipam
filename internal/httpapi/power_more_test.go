package httpapi

import (
	"errors"
	"testing"
)

// TestPowerNoBMCRef covers loadBMC's validation branch: a device with no
// ipmi_secret_ref (or no host) is refused 422 on every OOB route.
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
		if w := f.req(t, route.method, route.path, "admin", body); w.Code != 422 {
			t.Fatalf("%s %s: want 422, got %d %s", route.method, route.path, w.Code, w.Body)
		}
	}
}

// TestPowerMissingDevice covers loadBMC's not-found branch on a random id.
func TestPowerMissingDevice(t *testing.T) {
	f := newAPI(t)
	if w := f.req(t, "GET", p+"/devices/"+randUUID+"/power", "admin", ""); w.Code != 404 {
		t.Fatalf("power missing device: want 404, got %d %s", w.Code, w.Body)
	}
}

// TestPowerWardenMissingSecret covers loadBMC's warden-fetch failure: the
// device references a secret ref that warden does not hold -> 404.
func TestPowerWardenMissingSecret(t *testing.T) {
	f := newAPI(t)
	w := f.req(t, "POST", p+"/devices", "admin",
		`{"name":"ghost","device_type":"server","management_ip":"10.0.0.1","ipmi_secret_ref":"does-not-exist"}`)
	did, _ := decodeBody(t, w)["id"].(string)
	if w := f.req(t, "GET", p+"/devices/"+did+"/power", "admin", ""); w.Code != 404 {
		t.Fatalf("power missing secret: want 404, got %d %s", w.Code, w.Body)
	}
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

// TestPowerBMCError covers the failSvc default (500) branch when the BMC client
// returns an unmapped error for status/sensors/sel/action.
func TestPowerBMCError(t *testing.T) {
	f := newAPI(t)
	did := f.newDeviceWithBMC(t)
	f.bmc.Err = errors.New("bmc offline")

	if w := f.req(t, "GET", p+"/devices/"+did+"/power", "admin", ""); w.Code != 500 {
		t.Fatalf("power status err: want 500, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sensors", "admin", ""); w.Code != 500 {
		t.Fatalf("sensors err: want 500, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "GET", p+"/devices/"+did+"/sel", "admin", ""); w.Code != 500 {
		t.Fatalf("sel err: want 500, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "POST", p+"/devices/"+did+"/power", "admin", `{"action":"on"}`); w.Code != 500 {
		t.Fatalf("power action err: want 500, got %d %s", w.Code, w.Body)
	}
}
