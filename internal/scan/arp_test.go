package scan

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// arpFixture: a network-devices subnet with a router whose ARP table covers
// two routed subnets (feature 022, US1 independent test).
func arpFixture(t *testing.T) (*memstore.Mem, *snmp.Fake, *clock) {
	t.Helper()
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s-net", "10.0.0.0/29", 4)
	mustSubnet(t, m, "t1", "s-srv", "10.1.0.0/24", 4)
	mustSubnet(t, m, "t1", "s-ipmi", "10.2.0.0/24", 4)
	setCreds(t, m, "t1", "s-net", snmpcred.Input{Version: 2, Community: "lab-community"})
	for _, a := range []store.IPAddress{
		{ID: "a-srv5", TenantID: "t1", SubnetID: "s-srv", Address: "10.1.0.5"},
		{ID: "a-ns1", TenantID: "t1", SubnetID: "s-srv", Address: "10.1.0.8", MACAddress: "52:54:00:00:00:08", MACSource: store.MACSourceAgent},
	} {
		if err := m.CreateAddress(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	disc := snmp.NewFake()
	disc.Set("10.0.0.1", snmp.DiscoveredDevice{SysName: "MikroTik", DeviceType: store.DevRouter,
		Interfaces: []snmp.Interface{{Name: "ether1", IfIndex: 1, MAC: "4c:5e:0c:00:00:01"}}})
	disc.SetARP("10.0.0.1", []snmp.ARPEntry{
		{IP: "10.1.0.5", MAC: "0a:5c:d2:f1:00:05", IfIndex: 2},
		{IP: "10.1.0.6", MAC: "0a:5c:d2:f1:00:06", IfIndex: 2},
		{IP: "10.2.0.7", MAC: "0a:5c:d2:f1:00:07", IfIndex: 3},
		{IP: "10.1.0.8", MAC: "0a:5c:d2:f1:00:08", IfIndex: 2},
		{IP: "192.168.50.1", MAC: "0a:5c:d2:f1:00:09", IfIndex: 4},
	})
	disc.SetARPPartial("10.0.0.1")
	return m, disc, clk
}

func runARPScan(t *testing.T, m *memstore.Mem, disc *snmp.Fake, clk *clock) store.IPScanJob {
	t.Helper()
	ctx := context.Background()
	svc := newService(m, icmp.NewFake("10.0.0.1"), disc, &recPub{}, testConfig(), clk)
	job, err := svc.StartScan(ctx, adminSubj("t1"), "s-net", Options{EnableSNMP: true, SkipReverseDNS: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetScanJob(ctx, "t1", job.ID)
	if err != nil || got.Status != store.ScanCompleted {
		t.Fatalf("job %+v %v", got, err)
	}
	return got
}

func TestScanAppliesARP(t *testing.T) {
	ctx := context.Background()
	m, disc, clk := arpFixture(t)
	job := runARPScan(t, m, disc, clk)

	if seen := disc.Seen(); len(seen) != 1 || !seen[0].CollectARP {
		t.Fatal("ARP collection not requested")
	}
	devs, _ := m.ListDevices(ctx, "t1", store.DeviceFilter{})
	if len(devs) != 1 {
		t.Fatalf("devices %+v", devs)
	}
	router := devs[0].ID
	a, _ := m.FindAddress(ctx, "t1", "10.1.0.5")
	if a.MACAddress != "0a:5c:d2:f1:00:05" || a.MACSource != store.MACSourceARP || a.MACSourceDeviceID != router || a.MACSeenAt == nil {
		t.Fatalf("filled %+v", a)
	}
	for ip, subnet := range map[string]string{"10.1.0.6": "s-srv", "10.2.0.7": "s-ipmi"} {
		c, err := m.FindAddress(ctx, "t1", ip)
		if err != nil || c.Origin != store.OriginARP || c.SubnetID != subnet || c.Status != store.IPActive {
			t.Fatalf("created %s %+v %v", ip, c, err)
		}
	}
	ns1, _ := m.FindAddress(ctx, "t1", "10.1.0.8")
	if ns1.MACAddress != "52:54:00:00:00:08" || ns1.MACSource != store.MACSourceAgent || ns1.MACConflict != "0a:5c:d2:f1:00:08" {
		t.Fatalf("agent MAC must stay, conflict visible: %+v", ns1)
	}
	if _, err := m.FindAddress(ctx, "t1", "192.168.50.1"); err == nil {
		t.Fatal("address created outside every subnet")
	}
	if job.ARPStatus != store.ARPRan || job.ARPDevices != 1 || job.ARPPartial != 1 || job.ARPEntries != 5 ||
		job.ARPApplied != 1 || job.ARPCreated != 2 || job.ARPConflicts != 1 || job.ARPIgnored[store.ARPIgnoredOutside] != 1 {
		t.Fatalf("job counters %+v", job)
	}
	var run, learned int
	for _, r := range m.Audit() {
		switch r.Action {
		case "arp_run":
			run++
			if r.SubjectID != job.ID || r.SubjectKind != "scan" || r.Detail["entries"] != int64(5) {
				t.Fatalf("arp_run %+v", r)
			}
		case "mac_learned":
			learned++
		}
	}
	if run != 1 || learned != 1 {
		t.Fatalf("audit arp_run=%d mac_learned=%d", run, learned)
	}
}

func TestScanARPDisabled(t *testing.T) {
	ctx := context.Background()
	m, disc, clk := arpFixture(t)
	s := store.DefaultARPSettings("t1")
	s.Enabled = false
	if err := m.PutARPSettings(ctx, s, store.AuditRow{Action: "arp_settings_updated"}); err != nil {
		t.Fatal(err)
	}
	job := runARPScan(t, m, disc, clk)
	if seen := disc.Seen(); len(seen) != 1 || seen[0].CollectARP {
		t.Fatal("ARP collected although disabled")
	}
	if job.ARPStatus != store.ARPDisabled || job.ARPEntries != 0 {
		t.Fatalf("job %+v", job)
	}
	if a, _ := m.FindAddress(ctx, "t1", "10.1.0.5"); a.MACAddress != "" {
		t.Fatalf("MAC changed while disabled %+v", a)
	}
}

func TestScanARPFailuresKeepScan(t *testing.T) {
	for _, method := range []string{"GetARPSettings", "ApplyARP", "NetworkMACs", "ListAddresses"} {
		t.Run(method, func(t *testing.T) {
			m, disc, clk := arpFixture(t)
			m.FailNext(method)
			job := runARPScan(t, m, disc, clk)
			if job.ARPStatus != store.ARPFailed {
				t.Fatalf("status %q", job.ARPStatus)
			}
		})
	}
}

func TestScanWithoutSNMPHasNoARP(t *testing.T) {
	ctx := context.Background()
	m, disc, clk := arpFixture(t)
	svc := newService(m, icmp.NewFake("10.0.0.1"), disc, &recPub{}, testConfig(), clk)
	job, err := svc.StartScan(ctx, adminSubj("t1"), "s-net", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetScanJob(ctx, "t1", job.ID)
	if got.ARPStatus != "" || len(disc.Seen()) != 0 {
		t.Fatalf("ARP without SNMP: %+v", got)
	}
}
