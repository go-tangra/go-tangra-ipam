//go:build integration

package hostsync_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostsync"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func p95(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[(len(d)*95)/100]
}

// TestHostSync1000Hosts (T115, SC-006): 1000 hosts of one tenant are synced
// well inside 15 minutes, p95 of one host apply stays within 150 ms, and a
// concurrent device listing (the GET /devices path) keeps its p95 under
// 200 ms. Set HOSTSYNC_BENCH_HOSTS to change the fleet size.
func TestHostSync1000Hosts(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark")
	}
	db := openRepo(t)
	ctx := context.Background()
	const hosts = 1000
	inv := invclient.NewFake()
	now := time.Now().UTC()
	for i := 0; i < hosts; i++ {
		id := fmt.Sprintf("0190f7c2-bbbb-7c1a-9b2e-%012d", i)
		inv.Put(invclient.Report{
			TenantID: tenantA, Host: invclient.Host{ID: id, Hostname: fmt.Sprintf("node-%04d", i), SystemSerial: fmt.Sprintf("SN%06d", i), Status: "active", LastSeen: now},
			CollectedAt: now, ChangedAt: now.Add(time.Duration(i) * time.Millisecond), Digest: fmt.Sprintf("%064x", i+1), OSFamily: "linux",
			Interfaces: []invclient.Interface{
				{Name: "eth0", MAC: fmt.Sprintf("aa:bb:cc:%02x:%02x:01", i/256, i%256), Type: "ethernet", Up: true, SpeedBps: 1e10,
					Addresses: []invclient.Address{{Address: fmt.Sprintf("10.%d.%d.10", 100+i/250, i%250), PrefixLength: 24}}},
				{Name: "eth1", MAC: fmt.Sprintf("aa:bb:cc:%02x:%02x:02", i/256, i%256), Type: "ethernet", Up: true,
					Addresses: []invclient.Address{{Address: fmt.Sprintf("192.168.%d.%d", i/250, i%250+1), PrefixLength: 16}}},
				{Name: "docker0", Type: "bridge", Addresses: []invclient.Address{{Address: "172.17.0.1", PrefixLength: 16}}},
			},
			PrimaryIPv4:    fmt.Sprintf("10.%d.%d.10", 100+i/250, i%250),
			Virtualization: invclient.Virtualization{Role: "vm", Kind: "kvm"},
			Updates:        invclient.UpdateState{PackageManager: "apt", Status: "updates_available", RebootRequired: "false", AutomaticUpdates: "true"},
			PendingUpdates: []invclient.PendingUpdate{{Name: "openssl", InstalledVersion: "3.0.1", AvailableVersion: "3.0.2", Security: true}, {Name: "vim", InstalledVersion: "1", AvailableVersion: "2"}},
		})
	}
	r := hostsync.New(db, inv, &pub{}, hostsync.Config{PollInterval: time.Minute, Workers: 2, Pace: 0, ConflictMoves: 3, ConflictWindow: 24 * time.Hour}, nil, nil)
	s, _ := db.EnsureHostSyncSettings(ctx, tenantA)

	stop := make(chan struct{})
	var lmu sync.Mutex
	var listLat []time.Duration
	var lists int32
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			if _, err := db.ListDevices(ctx, tenantA, store.DeviceFilter{Limit: 50}); err == nil {
				lmu.Lock()
				listLat = append(listLat, time.Since(start))
				lmu.Unlock()
				atomic.AddInt32(&lists, 1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	var applyLat []time.Duration
	start := time.Now()
	err := inv.ListHostReports(ctx, tenantA, invclient.Filter{}, func(page []invclient.Report) error {
		for _, rep := range page {
			t0 := time.Now()
			if _, err := r.Apply(ctx, s, rep, hostsync.TriggerPoll, "bench"); err != nil {
				return err
			}
			applyLat = append(applyLat, time.Since(t0))
		}
		return nil
	})
	total := time.Since(start)
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	lmu.Lock()
	listP95 := p95(listLat)
	lmu.Unlock()
	applyP95 := p95(applyLat)
	t.Logf("%d hosts in %s (p95 apply %s); %d concurrent device listings, p95 %s", hosts, total, applyP95, lists, listP95)
	if total > 15*time.Minute {
		t.Fatalf("SC-006: %s > 15 min", total)
	}
	if applyP95 > 150*time.Millisecond {
		t.Errorf("p95 apply %s > 150 ms", applyP95)
	}
	if listP95 > 200*time.Millisecond {
		t.Errorf("p95 device listing %s > 200 ms under sync load", listP95)
	}
	devs, _ := db.HostDevices(ctx, tenantA)
	if len(devs) != hosts {
		t.Fatalf("%d devices", len(devs))
	}
}
