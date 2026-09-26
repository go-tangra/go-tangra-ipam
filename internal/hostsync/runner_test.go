package hostsync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestPollAppliesNewTenantReports(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0.Add(-time.Minute), 1))
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	d := f.device(t, tA, "web-01")
	if d.Source != store.SrcHostReport || d.InventoryHostID != host1 || d.PrimaryIP != "10.0.0.5" || d.ReportState != store.RepReported {
		t.Fatalf("device %+v", d)
	}
	s := f.settings(t, tA)
	if s.Status != store.HostSyncOK || s.ChangedSince == nil || !s.ChangedSince.Equal(t0.Add(-time.Minute)) || s.HostsReported != 1 ||
		s.LastReconcileAt == nil || s.LastPollAt == nil {
		t.Fatalf("settings %+v", s)
	}
	for _, a := range []string{"device_created", "interface_created", "subnet_created", "address_created", "hostsync_run"} {
		if f.audits(a) == 0 {
			t.Errorf("audit %s missing", a)
		}
	}
	if f.pub.count("ipam.ip_address.created") != 1 || f.pub.count("ipam.hostsync.applied") != 1 {
		t.Fatalf("events %+v", f.pub.evs)
	}
	// Same digest again -> skipped, nothing new written.
	before := len(f.st.Audit())
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	f.now = t0.Add(time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.st.Audit()); got != before+1 { // only the run summary
		t.Fatalf("unchanged digest wrote %d rows", got-before)
	}
	// Address change -> moved/released follows.
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.6", t0.Add(time.Minute), 2))
	f.now = t0.Add(2 * time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := f.st.FindAddress(ctx, tA, "10.0.0.5")
	if a.ReportState != store.RepNotReported || a.DeviceID != "" || a.PreviousDeviceID != d.ID {
		t.Fatalf("released %+v", a)
	}
	if n, _ := f.st.FindAddress(ctx, tA, "10.0.0.6"); n.DeviceID != d.ID {
		t.Fatal("new address")
	}
}

func TestFailedHostKeepsWatermark(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0.Add(-2*time.Minute), 1))
	f.inv.Put(rep(tA, host2, "web-02", "10.0.0.6", t0.Add(-time.Minute), 2))
	f.st.FailNext("ApplyHostReport")
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	s := f.settings(t, tA)
	if s.HostsFailed != 1 || s.ChangedSince != nil {
		t.Fatalf("watermark must not pass the failed host: %+v", s)
	}
	f.device(t, tA, "web-02") // the other host was still applied
	// Next cycle retries the tenant and catches up.
	f.now = t0.Add(time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if s = f.settings(t, tA); s.HostsFailed != 0 || s.ChangedSince == nil || !s.ChangedSince.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("retry %+v", s)
	}
	f.device(t, tA, "web-01")
}

func TestReconcileMarksGoneHostsAndAppliesMismatches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0.Add(-time.Minute), 1))
	f.inv.Put(rep(tA, host2, "web-02", "10.0.0.6", t0.Add(-time.Minute), 2))
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	// host2 deleted in inventory, host1 retired.
	f.inv.Delete(tA, host2)
	retired := rep(tA, host1, "web-01", "10.0.0.5", t0.Add(-time.Minute), 1)
	retired.Host.Status = "retired"
	f.inv.Put(retired)
	f.now = t0.Add(2 * time.Hour) // reconcile due
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	d1, d2 := f.device(t, tA, "web-01"), f.device(t, tA, "web-02")
	if d1.ReportState != store.RepNotReported || d2.ReportState != store.RepNotReported {
		t.Fatalf("gone hosts: %s %s", d1.ReportState, d2.ReportState)
	}
	a, _ := f.st.FindAddress(ctx, tA, "10.0.0.6")
	if a.DeviceID != d2.ID || a.ReportState != store.RepNotReported {
		t.Fatalf("addresses keep the device link: %+v", a)
	}
	if f.audits("device_not_reported") != 2 {
		t.Fatal("device_not_reported audit")
	}
	// A report for the retired host again -> reported, new digest applied by reconcile.
	back := rep(tA, host1, "web-01", "10.0.0.5", t0.Add(3*time.Hour), 9)
	f.inv.Put(back)
	_ = f.st.RequestReconcile(ctx, tA, store.AuditRow{})
	f.now = t0.Add(4 * time.Hour)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.device(t, tA, "web-01"); d.ReportState != store.RepReported || d.ReportDigest != digestOf(9) {
		t.Fatalf("re-reported %+v", d)
	}
}

func TestInventoryDownDegradesAndBacksOff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	f.inv.Down = true
	if err := f.r.Cycle(ctx); !errors.Is(err, invclient.ErrUnavailable) {
		t.Fatal(err)
	}
	if s := f.settings(t, tA); s.Status != store.HostSyncDegraded || s.LastError != invclient.CodeUnavailable {
		t.Fatalf("degraded %+v", s)
	}
	calls := len(f.inv.Calls)
	_ = f.r.Cycle(ctx) // inside the back-off window: no call
	if len(f.inv.Calls) != calls {
		t.Fatal("back-off not honoured")
	}
	for i := 0; i < 8; i++ {
		_, next := f.r.Backoff()
		f.now = next
		_ = f.r.Cycle(ctx)
	}
	if n, next := f.r.Backoff(); n < 5 || next.Sub(f.now) != MaxBackoff {
		t.Fatalf("back-off grows to 10 min: %d %s", n, next.Sub(f.now))
	}
	if f.pub.count("ipam.hostsync.status") == 0 {
		t.Fatal("status event")
	}
	f.inv.Down = false
	_, next := f.r.Backoff()
	f.now = next
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.r.Backoff(); n != 0 || f.settings(t, tA).Status != store.HostSyncOK {
		t.Fatal("recovery resets the back-off and the status")
	}
	// Outdated inventory (Unimplemented).
	f.inv.Err = &invclient.Error{Code: invclient.CodeOutdated}
	_ = f.r.Cycle(ctx)
	if s := f.settings(t, tA); s.LastError != invclient.CodeOutdated {
		t.Fatalf("outdated %+v", s)
	}
}

func TestTenantListFailureMidRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	fl := &flaky{Fake: f.inv}
	f.r.inv = fl
	if err := f.r.Cycle(ctx); !errors.Is(err, invclient.ErrUnavailable) {
		t.Fatalf("mid-run failure: %v", err)
	}
	if s := f.settings(t, tA); s.Status != store.HostSyncDegraded {
		t.Fatal("degraded")
	}
	if l, _ := f.st.ListDevices(ctx, tA, store.DeviceFilter{}); len(l) != 0 {
		t.Fatal("nothing written")
	}
}

// flaky lists tenants but fails listing their reports.
type flaky struct{ *invclient.Fake }

func (f *flaky) ListHostReports(context.Context, string, invclient.Filter, func([]invclient.Report) error) error {
	return &invclient.Error{Code: invclient.CodeUnavailable}
}

func TestDisabledTenantAndIgnoredTenantIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	f.inv.Put(rep("not-a-uuid", host2, "x", "10.0.0.9", t0, 2))
	s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
	s.Enabled = false
	_ = f.st.UpdateHostSyncSettings(ctx, s, store.AuditRow{})
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.st.ListDevices(ctx, tA, store.DeviceFilter{}); len(l) != 0 {
		t.Fatal("disabled tenant written")
	}
	if f.settings(t, tA).Status != store.HostSyncDisabled {
		t.Fatal("status disabled")
	}
	if all, _ := f.st.ListHostSyncSettings(ctx); len(all) != 1 {
		t.Fatal("non-uuid tenant ids from inventory are ignored")
	}
}

func TestTenantsInParallelBounded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	f.inv.Put(rep(tB, host2, "web-02", "10.0.0.6", t0, 2))
	var inFlight, peak int32
	f.r.sleep = func(context.Context, time.Duration) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
	}
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if peak > 2 {
		t.Fatalf("peak %d > workers", peak)
	}
	f.device(t, tA, "web-01")
	f.device(t, tB, "web-02")
	// Tenant B's host never appears in tenant A.
	if l, _ := f.st.ListDevices(ctx, tA, store.DeviceFilter{Query: "web-02"}); len(l) != 0 {
		t.Fatal("cross-tenant")
	}
}

type countLinker struct{ n int32 }

func (c *countLinker) Correlate(context.Context, string) error {
	atomic.AddInt32(&c.n, 1)
	return errors.New("ignored")
}

func TestRunHooksAndLoop(t *testing.T) {
	f := newFixture(t)
	l := &countLinker{}
	f.r.SetLinker(l)
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.r.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&l.n) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if atomic.LoadInt32(&l.n) != 1 {
		t.Fatal("port correlation after a run with changes")
	}
}

// A host missing from the digest listing whose report still exists (inventory
// omits hosts without a snapshot in listings) is re-applied, not marked gone.
func TestReconcileConfirmsAbsentHosts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0.Add(-time.Minute), 1))
	_ = f.r.Cycle(ctx)
	f.r.inv = &hideListing{Fake: f.inv}
	_ = f.st.RequestReconcile(ctx, tA, store.AuditRow{})
	f.now = t0.Add(time.Minute)
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.device(t, tA, "web-01"); d.ReportState != store.RepReported {
		t.Fatal("a host with a report is never marked gone")
	}
	f.inv.Err = &invclient.Error{Code: invclient.CodeUnavailable}
	_ = f.st.RequestReconcile(ctx, tA, store.AuditRow{})
	f.inv.Err = nil
	f.r.inv = &hideListing{Fake: f.inv, getErr: &invclient.Error{Code: invclient.CodeUnavailable}}
	f.now = t0.Add(20 * time.Minute)
	if err := f.r.Cycle(ctx); err == nil {
		t.Fatal("confirmation failure surfaces")
	}
}

// hideListing lists no host reports (digest view) but still serves GetHostReport.
type hideListing struct {
	*invclient.Fake
	getErr error
}

func (h *hideListing) ListHostReports(ctx context.Context, tid string, f invclient.Filter, fn func([]invclient.Report) error) error {
	if f.Digest {
		return nil
	}
	return h.Fake.ListHostReports(ctx, tid, f, fn)
}

func (h *hideListing) GetHostReport(ctx context.Context, tid, hid string) (invclient.Report, error) {
	if h.getErr != nil {
		return invclient.Report{}, h.getErr
	}
	return h.Fake.GetHostReport(ctx, tid, hid)
}
