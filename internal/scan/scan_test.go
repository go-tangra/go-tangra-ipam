package scan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/scan/icmp"
	"github.com/go-freya/freya/services/ipam/internal/scan/snmp"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

// --- test doubles ---

type recEvent struct {
	tenant  string
	typ     string
	payload any
}

type recPub struct {
	mu     sync.Mutex
	events []recEvent
}

func (p *recPub) Publish(_ context.Context, tenant, typ string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, recEvent{tenant, typ, payload})
}

func (p *recPub) count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int
	for _, e := range p.events {
		if e.typ == typ {
			n++
		}
	}
	return n
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func adminSubj(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, UserID: "u1", ActorKind: authz.ActorUser, Roles: []string{authz.RoleAdmin}}
}

func testConfig() Config {
	return Config{MaxHosts: 1024, Concurrency: 8, TimeoutMs: 100, Workers: 3, MaxRetries: 2}
}

// mustSubnet creates a subnet and returns its id.
func mustSubnet(t *testing.T, m *memstore.Mem, tenant, id, cidr string, version int) {
	t.Helper()
	if err := m.CreateSubnet(context.Background(), store.Subnet{
		ID: id, TenantID: tenant, Name: "net-" + id, CIDR: cidr, IPVersion: version, Status: store.SubnetActive,
	}); err != nil {
		t.Fatalf("create subnet: %v", err)
	}
}

func newService(m *memstore.Mem, sweeper icmp.Sweeper, disc snmp.Discoverer, w warden.Client, pub *recPub, cfg Config, clk *clock) *Service {
	return New(m, sweeper, icmp.NewFake(), disc, w, pub, cfg, clk.now)
}

// --- StartScan ---

func TestStartScanEnqueuesPending(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	pub := &recPub{}
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), pub, testConfig(), clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SkipReverseDNS: true})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	if job.Status != store.ScanPending {
		t.Fatalf("status = %q, want pending", job.Status)
	}
	if job.TotalAddresses != 6 { // /29 => 8 addrs, 6 usable
		t.Fatalf("total = %d, want 6", job.TotalAddresses)
	}
	if pub.count("ipam.scan.started") != 1 {
		t.Fatalf("scan.started not published")
	}
}

func TestStartScanRefusesIPv6(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	mustSubnet(t, m, "t1", "s6", "2001:db8::/64", 6)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	_, err := svc.StartScan(ctx, adminSubj("t1"), "s6", Options{})
	if !errors.Is(err, ErrIPv6) {
		t.Fatalf("err = %v, want ErrIPv6", err)
	}
}

func TestStartScanRefusesTooLarge(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	mustSubnet(t, m, "t1", "big", "10.0.0.0/20", 4) // 4094 usable
	cfg := testConfig()
	cfg.MaxHosts = 1024
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, cfg, clk)

	_, err := svc.StartScan(ctx, adminSubj("t1"), "big", Options{})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestStartScanRefusesActive(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{}); err != nil {
		t.Fatalf("first StartScan: %v", err)
	}
	_, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{})
	if !errors.Is(err, ErrActiveScan) {
		t.Fatalf("err = %v, want ErrActiveScan", err)
	}
}

func TestStartScanCrossTenantForbidden(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	// A caller scoped to t2 has TenantID t2; RequireTenant(subj, subj.TenantID)
	// passes, but the subnet lookup is under t2 and finds nothing.
	_, err := svc.StartScan(ctx, adminSubj("t2"), "s1", Options{})
	if err == nil {
		t.Fatalf("expected error scanning another tenant's subnet")
	}
}

// --- Cancel ---

func TestCancelScan(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	mustSubnet(t, m, "t1", "s1", "10.0.0.0/29", 4)
	svc := newService(m, icmp.NewFake(), snmp.NewFake(), warden.NewFake(), &recPub{}, testConfig(), clk)

	job, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{})
	if err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	cancelled, err := svc.CancelScan(ctx, adminSubj("t1"), job.ID)
	if err != nil {
		t.Fatalf("CancelScan: %v", err)
	}
	if cancelled.Status != store.ScanCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}
	// A cancelled job is no longer active, so a new scan may start.
	if _, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{}); err != nil {
		t.Fatalf("restart after cancel: %v", err)
	}
	// Cancelling a terminal job returns ErrTerminal.
	if _, err := svc.CancelScan(ctx, adminSubj("t1"), job.ID); !errors.Is(err, ErrTerminal) {
		t.Fatalf("err = %v, want ErrTerminal", err)
	}
}
