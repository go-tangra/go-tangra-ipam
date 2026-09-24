package scan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

func TestRunOnceSweepsAndCompletes(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4) // .1 .. .6

	sweeper := icmp.NewFake("10.0.0.1", "10.0.0.3")
	pub := &recPub{}
	svc := newService(m, sweeper, snmp.NewFake(), warden.NewFake(), pub, testConfig(), clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}

	n, err := svc.RunOnce(ctx, nil)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("claimed = %d, want 1", n)
	}

	got, err := svc.GetScanJob(ctx, adminSubj("t1"), job.ID)
	if err != nil {
		t.Fatalf("GetScanJob: %v", err)
	}
	if got.Status != store.ScanCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.AliveCount != 2 || got.NewCount != 2 {
		t.Fatalf("alive=%d new=%d, want 2/2", got.AliveCount, got.NewCount)
	}
	if got.ScannedCount != 6 {
		t.Fatalf("scanned = %d, want 6", got.ScannedCount)
	}

	// Alive addresses were upserted, dead ones were not.
	if _, err := m.FindAddress(ctx, "t1", "10.0.0.1"); err != nil {
		t.Fatalf("expected 10.0.0.1 upserted: %v", err)
	}
	if _, err := m.FindAddress(ctx, "t1", "10.0.0.2"); err == nil {
		t.Fatalf("did not expect dead host 10.0.0.2 to be stored")
	}

	if pub.count(events.IPAddressScanned) != 2 {
		t.Fatalf("scanned events = %d, want 2", pub.count(events.IPAddressScanned))
	}
	if pub.count(events.ScanCompleted) != 1 {
		t.Fatalf("scan.completed events = %d, want 1", pub.count(events.ScanCompleted))
	}
}

func TestRunOnceSNMPDiscovery(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)

	// Attach an SNMP credential reference to the subnet.
	sub, _ := m.GetSubnet(ctx, "t1", "s1")
	sub.SNMPSecretRef = "snmp-ref"
	sub.SNMPVersion = 2
	if err := m.UpdateSubnet(ctx, sub); err != nil {
		t.Fatalf("update subnet: %v", err)
	}
	w := warden.NewFake()
	w.Put("snmp-ref", map[string]string{"community": "public"}, warden.SecretMeta{Name: "snmp"})

	disc := snmp.NewFake()
	disc.Set("10.0.0.1", snmp.DiscoveredDevice{
		SysName:    "switch-1",
		DeviceType: store.DevSwitch,
		Interfaces: []snmp.Interface{{Name: "Gi0/1", IfIndex: 1, MAC: "aa:bb:cc:dd:ee:01"}},
		Links:      []snmp.Link{{RemotePort: "90:5a:08:be:ec:3e", Source: snmp.SourceSNMPFDB, VLAN: 10, IfIndex: 1}},
	})

	sweeper := icmp.NewFake("10.0.0.1")
	svc := newService(m, sweeper, disc, w, &recPub{}, testConfig(), clk)

	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{EnableSNMP: true, SkipReverseDNS: true}); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	devices, err := m.ListDevices(ctx, "t1", store.DeviceFilter{})
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "switch-1" {
		t.Fatalf("devices = %+v, want one named switch-1", devices)
	}
	ifaces, err := m.ListInterfaces(ctx, "t1", devices[0].ID)
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(ifaces) != 1 || ifaces[0].Name != "Gi0/1" {
		t.Fatalf("interfaces = %+v, want one Gi0/1", ifaces)
	}
	links, err := m.ListInterfaceLinks(ctx, "t1", ifaces[0].ID)
	if err != nil {
		t.Fatalf("ListInterfaceLinks: %v", err)
	}
	if len(links) != 1 || links[0].LinkSource != snmp.SourceSNMPFDB || links[0].LinkVlan != 10 {
		t.Fatalf("links = %+v, want one snmp_fdb vlan10", links)
	}
}

func TestProcessJobHonorsCancel(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)

	sweeper := icmp.NewFake("10.0.0.1")
	svc := newService(m, sweeper, snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	// Simulate a worker having claimed the job (scanning) that is cancelled
	// mid-flight before it reaches the completion step.
	scanning, _ := m.GetScanJob(ctx, "t1", job.ID)
	scanning.Status = store.ScanScanning
	_ = m.UpdateScanJob(ctx, scanning)
	if _, err := svc.CancelScan(ctx, adminSubj("t1"), job.ID); err != nil {
		t.Fatalf("CancelScan: %v", err)
	}

	svc.processJob(ctx, nil, scanning)

	got, _ := m.GetScanJob(ctx, "t1", job.ID)
	if got.Status != store.ScanCancelled {
		t.Fatalf("status = %q, want cancelled (not overwritten by completion)", got.Status)
	}
}

func TestRunOnceRetriesWithBackoffThenFails(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)

	sweeper := &icmp.Fake{Err: errors.New("boom")} // every sweep fails
	cfg := testConfig()
	cfg.MaxRetries = 2
	svc := newService(m, sweeper, snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}

	// Failure 1: back to pending, retry scheduled in the future.
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce#1: %v", err)
	}
	got, _ := m.GetScanJob(ctx, "t1", job.ID)
	if got.Status != store.ScanPending || got.RetryCount != 1 || got.NextRetryAt == nil {
		t.Fatalf("after fail 1: status=%q retry=%d next=%v", got.Status, got.RetryCount, got.NextRetryAt)
	}
	if !got.NextRetryAt.After(clk.t) {
		t.Fatalf("next_retry_at %v not after now %v", got.NextRetryAt, clk.t)
	}

	// Not yet due -> not reclaimed.
	if n, _ := svc.RunOnce(ctx, nil); n != 0 {
		t.Fatalf("claimed %d before backoff elapsed, want 0", n)
	}

	// Advance past the backoff: failure 2.
	clk.t = clk.t.Add(time.Minute)
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce#2: %v", err)
	}
	got, _ = m.GetScanJob(ctx, "t1", job.ID)
	if got.Status != store.ScanPending || got.RetryCount != 2 {
		t.Fatalf("after fail 2: status=%q retry=%d", got.Status, got.RetryCount)
	}

	// Advance again: failure 3 exhausts the budget -> failed.
	clk.t = clk.t.Add(time.Minute)
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce#3: %v", err)
	}
	got, _ = m.GetScanJob(ctx, "t1", job.ID)
	if got.Status != store.ScanFailed {
		t.Fatalf("after fail 3: status=%q, want failed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatalf("failed job should carry completed_at")
	}
}
