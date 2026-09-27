package hostsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
)

func hw(serial string) *invclient.Hardware {
	return &invclient.Hardware{Schema: 2, BIOS: invclient.BIOSInfo{Version: "2.5"},
		Processors: []invclient.Processor{{SocketDesignation: "CPU1", Version: "Xeon", CoreCount: 12, ThreadCount: 24, SocketPopulated: true}},
		Disks:      []invclient.Disk{{Name: "nvme0n1", Serial: serial, SizeBytes: 10, MediaType: "nvme_ssd", Interface: "nvme"}}}
}

// TestApplyStoresAndAuditsHardware: the host sync stores the reported
// hardware, audits a change, keeps it for a report without hardware and
// rolls it back with a failed apply.
func TestApplyStoresAndAuditsHardware(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r1 := rep(tA, host1, "node-1", "10.0.0.5", t0.Add(-time.Minute), 1)
	r1.Hardware = hw("S1")
	f.inv.Put(r1)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	d := f.device(t, tA, "node-1")
	h, err := f.st.GetDeviceHardware(ctx, tA, d.ID)
	if err != nil || h.Summary.CPUCores != 12 || h.Profile.Disks[0].Serial != "S1" || f.audits("hardware_reported") != 1 {
		t.Fatalf("stored %+v %v", h, err)
	}
	if d.HardwareSummary == nil || d.HardwareSummary.DiskCount != 1 {
		t.Fatalf("summary %+v", d.HardwareSummary)
	}

	r2 := rep(tA, host1, "node-1", "10.0.0.5", t0, 2)
	r2.Hardware = hw("S2")
	f.inv.Put(r2)
	f.now = t0.Add(time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _ := f.st.GetDeviceHardware(ctx, tA, d.ID); h.Profile.Disks[0].Serial != "S2" || f.audits("hardware_updated") != 1 {
		t.Fatalf("updated %+v", h.Profile.Disks)
	}

	// Report without hardware: the row stays.
	f.inv.Put(rep(tA, host1, "node-1", "10.0.0.5", t0.Add(time.Minute), 3))
	f.now = t0.Add(2 * time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if h, err := f.st.GetDeviceHardware(ctx, tA, d.ID); err != nil || h.Profile.Disks[0].Serial != "S2" {
		t.Fatalf("absent hardware: %+v %v", h, err)
	}

	// A failing hardware write rolls the whole host back.
	r4 := rep(tA, host1, "node-1", "10.0.0.5", t0.Add(2*time.Minute), 4)
	r4.Hardware = hw("S4")
	f.inv.Put(r4)
	f.now = t0.Add(3 * time.Minute)
	f.st.FailNext("tx.ReplaceHardware")
	_ = f.r.Cycle(ctx)
	if h, _ := f.st.GetDeviceHardware(ctx, tA, d.ID); h.Profile.Disks[0].Serial != "S2" || f.audits("hardware_updated") != 1 {
		t.Fatal("failed hardware write must roll back")
	}
	// Loading the stored hardware fails -> host failed, nothing written.
	f.st.FailNext("tx.GetHardware")
	f.now = t0.Add(4 * time.Minute)
	_ = f.r.Cycle(ctx)
	if h, _ := f.st.GetDeviceHardware(ctx, tA, d.ID); h.Profile.Disks[0].Serial != "S2" {
		t.Fatal("failed load must not write")
	}
	// Other tenants never see the row.
	if _, err := f.st.GetDeviceHardware(ctx, tB, d.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
}
