package hostsync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tA    = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tB    = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
	host1 = "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa01"
	host2 = "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa02"
)

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

type published struct {
	tenant, typ string
	payload     any
}

type recPub struct {
	mu  sync.Mutex
	evs []published
}

func (p *recPub) Publish(_ context.Context, tenantID, eventType string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evs = append(p.evs, published{tenantID, eventType, payload})
}

func (p *recPub) count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.evs {
		if e.typ == typ {
			n++
		}
	}
	return n
}

func digestOf(n int) string { return fmt.Sprintf("%064x", n) }

func rep(tenant, hostID, hostname, ip string, changed time.Time, digestN int) invclient.Report {
	return invclient.Report{
		TenantID: tenant, Host: invclient.Host{ID: hostID, Hostname: hostname, Status: "active", LastSeen: changed},
		SnapshotID: "snap", CollectedAt: changed, ChangedAt: changed, Digest: digestOf(digestN), OSFamily: "linux",
		Interfaces: []invclient.Interface{{Name: "eth0", MAC: "aa:bb:cc:00:00:" + hostID[len(hostID)-2:], Type: "ethernet", Up: true,
			Addresses: []invclient.Address{{Address: ip, PrefixLength: 24}}}},
		PrimaryIPv4:    ip,
		Virtualization: invclient.Virtualization{Role: "physical"},
		Updates:        invclient.UpdateState{Status: "unknown"},
	}
}

type fixture struct {
	st  *memstore.Mem
	inv *invclient.Fake
	pub *recPub
	r   *Runner
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{st: memstore.New(), inv: invclient.NewFake(), pub: &recPub{}, now: t0}
	f.r = New(f.st, f.inv, f.pub, Config{PollInterval: time.Minute, Workers: 2, ConflictMoves: 3, ConflictWindow: 24 * time.Hour}, nil, nil)
	f.r.SetClock(func() time.Time { return f.now })
	f.r.sleep = func(context.Context, time.Duration) {}
	return f
}

func (f *fixture) device(t *testing.T, tenant, name string) store.Device {
	t.Helper()
	l, _ := f.st.ListDevices(context.Background(), tenant, store.DeviceFilter{Query: name})
	for _, d := range l {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("device %q not found in %s", name, tenant)
	return store.Device{}
}

func (f *fixture) settings(t *testing.T, tenant string) store.HostSyncSettings {
	t.Helper()
	s, err := f.st.GetHostSyncSettings(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) audits(action string) int {
	n := 0
	for _, a := range f.st.Audit() {
		if a.Action == action {
			n++
		}
	}
	return n
}
