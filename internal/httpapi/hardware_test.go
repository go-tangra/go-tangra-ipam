package httpapi

import (
	"testing"
)

// TestDeviceWritesRejectHardware: hardware is reported data (FR-008); a
// device create or update carrying hardware_summary or hardware is refused
// by the strict decoder.
func TestDeviceWritesRejectHardware(t *testing.T) {
	f := newAPI(t)
	for _, body := range []string{
		`{"name":"h1","device_type":"server","hardware_summary":{"cpu_model":"x"}}`,
		`{"name":"h1","device_type":"server","hardware":{"bios":{"version":"1"}}}`,
	} {
		if w := f.req(t, "POST", p+"/devices", "admin", body); w.Code != 400 {
			t.Fatalf("create with hardware: want 400, got %d %s", w.Code, w.Body)
		}
	}
	w := f.req(t, "POST", p+"/devices", "admin", `{"name":"h1","device_type":"server"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	id := decodeBody(t, w)["id"].(string)
	if w := f.req(t, "PUT", p+"/devices/"+id, "admin", `{"name":"h1","device_type":"server","hardware_summary":{"cpu_cores":1}}`); w.Code != 400 {
		t.Fatalf("update with hardware_summary: want 400, got %d %s", w.Code, w.Body)
	}
	if w := f.req(t, "PUT", p+"/devices/"+id, "admin", `{"name":"h1","device_type":"server","description":"ok"}`); w.Code != 200 {
		t.Fatalf("plain update: %d %s", w.Code, w.Body)
	}
}
