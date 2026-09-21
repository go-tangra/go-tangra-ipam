package scan

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/scan/icmp"
	"github.com/go-freya/freya/services/ipam/internal/scan/snmp"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

// emptyTenantSubj is a caller with no tenant, which trips RequireTenant.
func emptyTenantSubj() authz.Subjects {
	return authz.Subjects{TenantID: "", UserID: "u1", ActorKind: authz.ActorUser, Roles: []string{authz.RoleAdmin}}
}

// --- New / constructor ---

func TestNewDefaultsNowWhenNil(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	// Pass a nil now func: the constructor must substitute time.Now.
	svc := New(m, icmp.NewFake(), icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), nil)
	if svc.now == nil {
		t.Fatal("now func should be defaulted, not nil")
	}
	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	if job.CreatedAt.IsZero() {
		t.Fatal("default now produced a zero CreatedAt")
	}
}

// --- ListScanJobs ---

func TestListScanJobs(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	mustSubnet(t, m, "t1", "s2", "10.0.1.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	j1, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan s1: %v", err)
	}
	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s2", Options{SkipReverseDNS: true}); err != nil {
		t.Fatalf("StartScan s2: %v", err)
	}

	all, err := svc.ListScanJobs(ctx, adminSubj("t1"), store.ScanFilter{})
	if err != nil {
		t.Fatalf("ListScanJobs: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}

	// Filter by subnet.
	bySubnet, err := svc.ListScanJobs(ctx, adminSubj("t1"), store.ScanFilter{SubnetID: "s1"})
	if err != nil {
		t.Fatalf("ListScanJobs subnet: %v", err)
	}
	if len(bySubnet) != 1 || bySubnet[0].ID != j1.ID {
		t.Fatalf("subnet filter = %+v, want only j1", bySubnet)
	}

	// Filter by status.
	byStatus, err := svc.ListScanJobs(ctx, adminSubj("t1"), store.ScanFilter{Status: store.ScanPending})
	if err != nil {
		t.Fatalf("ListScanJobs status: %v", err)
	}
	if len(byStatus) != 2 {
		t.Fatalf("pending = %d, want 2", len(byStatus))
	}

	// A different tenant sees none.
	other, err := svc.ListScanJobs(ctx, adminSubj("t2"), store.ScanFilter{})
	if err != nil {
		t.Fatalf("ListScanJobs t2: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("t2 saw %d jobs, want 0", len(other))
	}

	// A tenant-less caller is forbidden.
	if _, err := svc.ListScanJobs(ctx, emptyTenantSubj(), store.ScanFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// --- GetScanJob error paths ---

func TestGetScanJobErrors(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), &clock{t: time.Now().UTC()})

	if _, err := svc.GetScanJob(ctx, adminSubj("t1"), "does-not-exist"); err == nil {
		t.Fatal("expected error for missing job id")
	}
	if _, err := svc.GetScanJob(ctx, emptyTenantSubj(), "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// --- CancelScan error paths ---

func TestCancelScanErrors(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	// Missing id.
	if _, err := svc.CancelScan(ctx, adminSubj("t1"), "nope"); err == nil {
		t.Fatal("expected error cancelling missing job")
	}
	// Tenant-less caller forbidden.
	if _, err := svc.CancelScan(ctx, emptyTenantSubj(), "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}

	// UpdateScanJob failure is surfaced.
	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	m.FailNext("UpdateScanJob")
	if _, err := svc.CancelScan(ctx, adminSubj("t1"), job.ID); err == nil {
		t.Fatal("expected UpdateScanJob failure to surface")
	}
}

// --- StartScan error paths ---

func TestStartScanTenantlessForbidden(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), &clock{t: time.Now().UTC()})
	if _, err := svc.StartScan(ctx, emptyTenantSubj(), "s1", Options{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestStartScanCreateFailure(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)
	m.FailNext("CreateScanJob")
	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true}); err == nil {
		t.Fatal("expected CreateScanJob failure to surface")
	}
}

func TestStartScanBadCIDRParseError(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	// A subnet with an unparseable CIDR trips the ipnet.Parse error branch.
	if err := m.CreateSubnet(ctx, store.Subnet{
		ID: "bad", TenantID: "t1", Name: "bad", CIDR: "not-a-cidr", IPVersion: 4, Status: store.SubnetActive,
	}); err != nil {
		t.Fatalf("create subnet: %v", err)
	}
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)
	if _, err := svc.StartScan(ctx, adminSubj("t1"), "bad", Options{}); err == nil {
		t.Fatal("expected parse error for bad CIDR")
	}
}

// --- Run drains on context cancel ---

func TestRunReturnsOnContextCancel(t *testing.T) {
	m := memstore.New()
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), &clock{t: time.Now().UTC()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled -> Run returns immediately
	if err := svc.Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
}

// --- RunOnce claim failure ---

func TestRunOnceClaimError(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), &clock{t: time.Now().UTC()})
	m.FailNext("ClaimDueScanJobs")
	if _, err := svc.RunOnce(ctx, nil); err == nil {
		t.Fatal("expected ClaimDueScanJobs failure to surface")
	}
}

// --- processJob: reverse DNS naming path exercises defaultRevLookup ---

func TestRunOnceReverseDNS(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/30", 4) // .1 .. .2

	sweeper := icmp.NewFake("10.0.0.1")
	svc := newService(m, sweeper, snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	// SkipReverseDNS false -> processJob invokes the default reverse-DNS lookup.
	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: false}); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, _ := m.FindAddress(ctx, "t1", "10.0.0.1")
	if got.Address != "10.0.0.1" {
		t.Fatalf("expected alive host stored, got %+v", got)
	}
}

// --- processJob: upsert failure retries ---

func TestProcessJobUpsertFailureRetries(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/30", 4)

	sweeper := icmp.NewFake("10.0.0.1")
	cfg := testConfig()
	cfg.MaxRetries = 0
	svc := newService(m, sweeper, snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	m.FailNext("UpsertAddressByAddress")
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, _ := m.GetScanJob(ctx, "t1", job.ID)
	if got.Status != store.ScanFailed {
		t.Fatalf("status = %q, want failed (retries exhausted)", got.Status)
	}
}

// --- processJob: subnet vanished after claim ---

func TestProcessJobSubnetGone(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	cfg := testConfig()
	cfg.MaxRetries = 0
	svc := newService(m, icmp.NewFake("10.0.0.1"), snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, clk)

	// A claimed (scanning) job whose subnet does not exist: the GetSubnet reload
	// inside processJob fails, driving retryOrFail -> failed.
	job := store.IPScanJob{
		ID: "job-orphan", TenantID: "t1", SubnetID: "missing-subnet", Status: store.ScanScanning,
	}
	if err := m.CreateScanJob(ctx, job); err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	scanning, _ := m.GetScanJob(ctx, "t1", "job-orphan")

	svc.processJob(ctx, slog.Default(), scanning)
	got, _ := m.GetScanJob(ctx, "t1", "job-orphan")
	if got.Status != store.ScanFailed {
		t.Fatalf("status = %q, want failed (subnet load error)", got.Status)
	}
}

// --- snmpCreds: version defaulting and error/empty branches ---

func TestSNMPCredsVersionDefault(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/30", 4)
	sub, _ := m.GetSubnet(ctx, "t1", "s1")
	sub.SNMPSecretRef = "snmp-ref"
	sub.SNMPVersion = 0 // -> defaults to 2 inside snmpCreds
	_ = m.UpdateSubnet(ctx, sub)

	w := warden.NewFake()
	w.Put("snmp-ref", map[string]string{"community": "public"}, warden.SecretMeta{Name: "snmp"})

	creds, ok := svc(m, w, clk).snmpCreds(ctx, sub)
	if !ok {
		t.Fatal("expected creds ok")
	}
	if creds.Version != 2 {
		t.Fatalf("version = %d, want defaulted 2", creds.Version)
	}
}

func TestSNMPCredsMissingRef(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	s := svc(m, warden.NewFake(), clk)
	// Empty ref -> not ok.
	if _, ok := s.snmpCreds(ctx, store.Subnet{}); ok {
		t.Fatal("empty ref should be not-ok")
	}
	// Ref present but warden lookup fails -> not ok.
	if _, ok := s.snmpCreds(ctx, store.Subnet{SNMPSecretRef: "unknown"}); ok {
		t.Fatal("unknown ref should be not-ok")
	}
}

func TestDiscoverSNMPMissingCredsIsZero(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/30", 4)
	// EnableSNMP but the subnet has no credential ref -> discoverSNMP returns 0.
	sweeper := icmp.NewFake("10.0.0.1")
	disc := snmp.NewFake()
	disc.Set("10.0.0.1", snmp.DiscoveredDevice{SysName: "sw"})
	s := newService(m, sweeper, disc, warden.NewFake(), &recPub{}, testConfig(), clk)
	if _, err := s.StartScan(ctx, adminSubj("t1"), "s1", Options{EnableSNMP: true, SkipReverseDNS: true}); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	if _, err := s.RunOnce(ctx, nil); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, _ := m.ListDevices(ctx, "t1", store.DeviceFilter{})
	if len(got) != 0 {
		t.Fatalf("expected no devices without creds, got %d", len(got))
	}
}

// --- persistDevice: naming fallbacks and store error paths ---

func TestPersistDeviceNamingFallbacks(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	s := svc(m, warden.NewFake(), clk)

	dev := snmp.DiscoveredDevice{
		// SysName empty -> deviceName falls back to "device-<ip>".
		Interfaces: []snmp.Interface{{IfIndex: 7 /* Name empty -> "if-7" */}},
		Links:      []snmp.Link{{RemotePort: "aa:bb", Source: snmp.SourceSNMPFDB, IfIndex: 7}},
	}
	if err := s.persistDevice(ctx, "t1", "10.0.0.9", dev); err != nil {
		t.Fatalf("persistDevice: %v", err)
	}
	devs, _ := m.ListDevices(ctx, "t1", store.DeviceFilter{})
	if len(devs) != 1 || devs[0].Name != "device-10.0.0.9" {
		t.Fatalf("device name = %+v, want device-10.0.0.9", devs)
	}
	ifaces, _ := m.ListInterfaces(ctx, "t1", devs[0].ID)
	if len(ifaces) != 1 || ifaces[0].Name != "if-7" {
		t.Fatalf("iface name = %+v, want if-7", ifaces)
	}
}

func TestPersistDeviceErrors(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	s := svc(m, warden.NewFake(), clk)
	dev := snmp.DiscoveredDevice{
		SysName:    "sw",
		Interfaces: []snmp.Interface{{Name: "Gi0/1", IfIndex: 1}},
		Links:      []snmp.Link{{RemotePort: "aa", Source: snmp.SourceSNMPFDB, IfIndex: 1}},
	}

	// Device upsert failure.
	m.FailNext("UpsertDeviceByName")
	if err := s.persistDevice(ctx, "t1", "10.0.0.1", dev); err == nil {
		t.Fatal("expected device upsert error")
	}

	// Interface upsert failure (device succeeds first).
	m.FailNext("UpsertInterfaceByName")
	if err := s.persistDevice(ctx, "t1", "10.0.0.1", dev); err == nil {
		t.Fatal("expected interface upsert error")
	}

	// Link replace failure (device + interface succeed first).
	m.FailNext("ReplaceInterfaceLinks")
	if err := s.persistDevice(ctx, "t1", "10.0.0.1", dev); err == nil {
		t.Fatal("expected link replace error")
	}
}

// --- retryOrFail: cancelled-underneath short-circuits ---

func TestRetryOrFailCancelledUnderneath(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	s := svc(m, warden.NewFake(), clk)

	// Branch: the second GetScanJob (by job.ID) finds the job cancelled.
	cancelled := store.IPScanJob{
		ID: "job-c", TenantID: "t1", SubnetID: "sub-x", Status: store.ScanScanning,
	}
	if err := m.CreateScanJob(ctx, cancelled); err != nil {
		t.Fatalf("create: %v", err)
	}
	cur, _ := m.GetScanJob(ctx, "t1", "job-c")
	cur.Status = store.ScanCancelled
	_ = m.UpdateScanJob(ctx, cur)

	s.retryOrFail(ctx, nil, cur, errors.New("boom"))
	after, _ := m.GetScanJob(ctx, "t1", "job-c")
	if after.Status != store.ScanCancelled {
		t.Fatalf("status = %q, want cancelled preserved (no resurrection)", after.Status)
	}
}

// --- observeCancel: reload error returns false ---

func TestObserveCancelReloadError(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	s := svc(m, warden.NewFake(), &clock{t: time.Now().UTC()})
	// A job whose id is not in the store: GetScanJob errors -> observeCancel false.
	j := store.IPScanJob{ID: "ghost", TenantID: "t1"}
	if s.observeCancel(ctx, nil, &j) {
		t.Fatal("observeCancel should be false when reload errors")
	}
}

// --- timeout default ---

func TestTimeoutDefault(t *testing.T) {
	m := memstore.New()
	cfg := testConfig()
	cfg.TimeoutMs = 0
	s := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, &clock{t: time.Now().UTC()})
	if got := s.timeout(); got != time.Second {
		t.Fatalf("timeout = %v, want 1s default", got)
	}
	cfg.TimeoutMs = 250
	s2 := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, &clock{t: time.Now().UTC()})
	if got := s2.timeout(); got != 250*time.Millisecond {
		t.Fatalf("timeout = %v, want 250ms", got)
	}
}

// svc is a compact Service builder for tests that only need the store, warden
// and clock (sweeper/discoverer default to fakes).
func svc(m *memstore.Mem, w warden.Client, clk *clock) *Service {
	return newService(m, icmp.NewFake(), snmp.NewFake(), w, &recPub{}, testConfig(), clk)
}
