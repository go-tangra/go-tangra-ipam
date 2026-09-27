//go:build integration

package hostsync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	invv1 "github.com/go-tangra/go-tangra-inventory/sdk/v4/api/proto/inventory/v1"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostsync"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func hardwareProfile(diskSerial string) *invv1.HardwareProfile {
	return &invv1.HardwareProfile{
		Schema: 2,
		Bios:   &invv1.BIOSInfo{Vendor: "American Megatrends International, LLC.", Version: "2.5", ReleaseDate: "11/26/2025"},
		System: &invv1.SystemInfo{Manufacturer: "Supermicro", ProductName: "Super Server", SerialNumber: "SYS-1"},
		Processors: []*invv1.Processor{
			{SocketDesignation: "CPU1", Version: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz", CoreCount: 12, ThreadCount: 24, SocketPopulated: true},
			{SocketDesignation: "CPU2", Version: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz", CoreCount: 12, ThreadCount: 24, SocketPopulated: true},
		},
		Memory: &invv1.MemoryInfo{TotalPhysicalBytes: 32 << 30, SlotsTotal: 2, SlotsPopulated: 2,
			Array: &invv1.MemoryArray{ErrorCorrection: "Single-bit ECC"},
			Modules: []*invv1.MemoryModule{
				{DeviceLocator: "P1-DIMMA1", CapacityBytes: 16 << 30, MemoryType: "DDR4", Populated: true},
				{DeviceLocator: "P1-DIMMB1", CapacityBytes: 16 << 30, MemoryType: "DDR4", Populated: true},
			}},
		Disks:        []*invv1.Disk{{Name: "nvme0n1", Model: "PM9A3", Serial: diskSerial, SizeBytes: 3840755982336, MediaType: "nvme_ssd", Interface: "nvme"}},
		Filesystems:  []*invv1.Filesystem{{Mount: "/", Fs: "ext4", SizeBytes: 100 << 30, FreeBytes: 40 << 30, Disks: []string{"nvme0n1"}}},
		Availability: &invv1.HardwareAvailability{Smbios: "ok", Disks: "ok"},
	}
}

// TestHostSyncHardwareEndToEnd (T055): an in-process inventory
// HostReportService (inventory SDK with hardware) serves reports over the
// mesh; IPAM stores the hardware, audits a replaced disk, keeps the stored
// hardware for a report without it and serves devices of hosts without
// hardware unchanged (compatibility, FR-006).
func TestHostSyncHardwareEndToEnd(t *testing.T) {
	db := openRepo(t)
	ctx := context.Background()
	inv := &inventory{reports: map[string]map[string]*invv1.HostReport{}}
	cfg := hostsync.Config{PollInterval: time.Minute, Workers: 1, ConflictMoves: 3, ConflictWindow: 24 * time.Hour}
	r := hostsync.New(db, mesh(t, inv, "ipam"), &pub{}, cfg, nil, nil)

	withHW := report(tenantA, hostA, "node-1", 1_000_000, 1, "10.40.0.5/24")
	withHW.Hardware = hardwareProfile("S64-1")
	legacyHost := "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa09"
	inv.put(withHW)
	inv.put(report(tenantA, legacyHost, "old-agent", 1_000_000, 11, "10.40.0.9/24"))
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	devs, _ := db.HostDevices(ctx, tenantA)
	var node, old store.Device
	for _, d := range devs {
		switch d.InventoryHostID {
		case hostA:
			node = d
		case legacyHost:
			old = d
		}
	}
	if node.ID == "" || old.ID == "" {
		t.Fatalf("devices %+v", devs)
	}
	hw, err := db.GetDeviceHardware(ctx, tenantA, node.ID)
	if err != nil || hw.Profile.BIOS.Version != "2.5" || len(hw.Profile.Disks) != 1 || hw.Summary.CPUCores != 24 || hw.Summary.DiskCount != 1 {
		t.Fatalf("hardware %+v %v", hw, err)
	}
	if d, _ := db.GetDevice(ctx, tenantA, node.ID); d.HardwareSummary == nil || d.HardwareSummary.MemoryType != "DDR4" {
		t.Fatalf("summary on the device %+v", d.HardwareSummary)
	}
	if _, err := db.GetDeviceHardware(ctx, tenantA, old.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("legacy host must have no hardware: %v", err)
	}
	if countAudit(t, db, tenantA, "hardware_reported") != 1 {
		t.Fatal("hardware_reported audited once")
	}

	// A replaced disk: hardware_updated with the disk change.
	changed := report(tenantA, hostA, "node-1", 2_000_000, 2, "10.40.0.5/24")
	changed.Hardware = hardwareProfile("S64-2")
	inv.put(changed)
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if hw, _ := db.GetDeviceHardware(ctx, tenantA, node.ID); hw.Profile.Disks[0].Serial != "S64-2" {
		t.Fatalf("replaced disk not stored: %+v", hw.Profile.Disks)
	}
	if countAudit(t, db, tenantA, "hardware_updated") != 1 {
		t.Fatal("hardware_updated audited once")
	}

	// A report without hardware (older agent/inventory) keeps the stored row.
	noHW := report(tenantA, hostA, "node-1", 3_000_000, 3, "10.40.0.5/24")
	inv.put(noHW)
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if hw, err := db.GetDeviceHardware(ctx, tenantA, node.ID); err != nil || hw.Profile.Disks[0].Serial != "S64-2" {
		t.Fatalf("absent hardware must leave the row: %+v %v", hw, err)
	}
	if countAudit(t, db, tenantA, "hardware_updated") != 1 {
		t.Fatal("no audit for an absent hardware section")
	}
}
