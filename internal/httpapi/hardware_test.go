package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
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

// seedHardware creates a host-reported device with stored hardware through
// the host sync's transaction and returns the device id.
func (f *apiFixture) seedHardware(t *testing.T, tenant string) string {
	t.Helper()
	id := store.NewID()
	ctx := context.Background()
	if _, err := f.mem.EnsureHostSyncSettings(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	err := f.mem.ApplyHostReport(ctx, tenant, func(tx repo.HostTx) error {
		if err := tx.InsertDevice(store.Device{ID: id, Name: "node-" + id[len(id)-4:], DeviceType: "server", Status: "active",
			Source: store.SrcHostReport, InventoryHostID: store.NewID()}); err != nil {
			return err
		}
		return tx.ReplaceHardware(store.DeviceHardware{DeviceID: id, Digest: strings.Repeat("a", 64), ReportedAt: time.Unix(1_700_000_000, 0).UTC(),
			Profile: store.HardwareProfile{Schema: 2, BIOS: store.HardwareBIOS{Vendor: "<b>AMI</b>", Version: "2.5"},
				Disks: []store.HardwareDisk{{Name: "nvme0n1", SizeBytes: 5, Media: "nvme_ssd", Interface: "nvme"}}},
			Summary: store.HardwareSummary{CPUModel: "Xeon", CPUSockets: 2, CPUCores: 24, CPUThreads: 48, MemoryType: "DDR4", DiskCount: 1, DiskTotalBytes: 5}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeviceHardwareRead(t *testing.T) {
	f := newAPI(t)
	id := f.seedHardware(t, apiTenant)
	w := f.req(t, "GET", p+"/devices/"+id+"/hardware", "user", "")
	if w.Code != 200 {
		t.Fatalf("get hardware: %d %s", w.Code, w.Body)
	}
	b := decodeBody(t, w)
	if b["device_id"] != id || b["reported_at"] != "2023-11-14T22:13:20Z" || b["bios"].(map[string]any)["vendor"] != "<b>AMI</b>" ||
		b["summary"].(map[string]any)["cpu_cores"] != float64(24) || len(b["disks"].([]any)) != 1 {
		t.Fatalf("body %s", w.Body)
	}
	if _, ok := b["digest"]; ok {
		t.Fatal("digest is internal")
	}
	// Device read and list carry the summary; the filter selects.
	dv := decodeBody(t, f.req(t, "GET", p+"/devices/"+id, "user", ""))
	if dv["hardware_summary"].(map[string]any)["memory_type"] != "DDR4" {
		t.Fatalf("device summary %v", dv["hardware_summary"])
	}
	man := decodeBody(t, f.req(t, "POST", p+"/devices", "admin", `{"name":"manual-1","device_type":"server"}`))
	if _, ok := man["hardware_summary"]; ok {
		t.Fatal("manual device has no hardware summary")
	}
	with := decodeBody(t, f.req(t, "GET", p+"/devices?has_hardware=true", "user", ""))["items"].([]any)
	without := decodeBody(t, f.req(t, "GET", p+"/devices?has_hardware=false", "user", ""))["items"].([]any)
	if len(with) != 1 || with[0].(map[string]any)["id"] != id || len(without) != 1 || without[0].(map[string]any)["id"] != man["id"] {
		t.Fatalf("filter with=%v without=%v", with, without)
	}
	if w := f.req(t, "GET", p+"/devices?has_hardware=maybe", "user", ""); w.Code != 422 && w.Code != 400 {
		t.Fatalf("bad filter value: %d", w.Code)
	}
	// 404: device without hardware, unknown device, another tenant's device.
	if w := f.req(t, "GET", p+"/devices/"+man["id"].(string)+"/hardware", "user", ""); w.Code != 404 {
		t.Fatalf("no hardware: %d", w.Code)
	}
	if w := f.req(t, "GET", p+"/devices/"+store.NewID()+"/hardware", "user", ""); w.Code != 404 {
		t.Fatalf("unknown device: %d", w.Code)
	}
	if w := f.req(t, "GET", p+"/devices/"+id+"/hardware", "other", ""); w.Code != 404 {
		t.Fatalf("other tenant: %d", w.Code)
	}
	if w := f.req(t, "GET", p+"/devices/"+id+"/hardware", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := f.req(t, "PUT", p+"/devices/"+id+"/hardware", "admin", `{}`); w.Code != 405 && w.Code != 404 {
		t.Fatalf("hardware is read-only: %d", w.Code)
	}
}
