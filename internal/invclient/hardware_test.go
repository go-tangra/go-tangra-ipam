package invclient

import (
	"context"
	"testing"
	"time"

	invv1 "github.com/go-tangra/go-tangra-inventory/sdk/v4/api/proto/inventory/v1"
)

// TestMeshCarriesHardware: the host report's hardware section and the 023
// truncation counters reach the IPAM report; a legacy report has none.
func TestMeshCarriesHardware(t *testing.T) {
	withHW := fullReport("h1")
	withHW.Hardware = &invv1.HardwareProfile{Schema: 2, Bios: &invv1.BIOSInfo{Version: "2.5"},
		Disks: []*invv1.Disk{{Name: "nvme0n1", MediaType: "nvme_ssd"}}}
	withHW.Truncated = &invv1.CollectionLimits{Disks: 1, MemorySlots: 2, MemoryArrays: 3, Processors: 4, Filesystems: 5}
	srv := &fakeServer{pages: [][]*invv1.HostReport{{withHW, fullReport("h2")}}}
	dial, _ := serve(t, srv)
	m := NewMesh(dial, time.Second, 10)
	var got []Report
	if err := m.ListHostReports(context.Background(), tA, Filter{}, func(p []Report) error { got = append(got, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	if got[0].Hardware == nil || got[0].Hardware.BIOS.Version != "2.5" || got[0].Hardware.Disks[0].Name != "nvme0n1" || got[1].Hardware != nil {
		t.Fatalf("hardware %+v / %+v", got[0].Hardware, got[1].Hardware)
	}
	if l := got[0].Truncated; l.Disks != 1 || l.MemorySlots != 2 || l.MemoryArrays != 3 || l.Processors != 4 || l.Filesystems != 5 {
		t.Fatalf("limits %+v", l)
	}
}
