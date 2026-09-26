package hostsync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// A report served under tenant A but claiming tenant B (forged/misrouted) is
// rejected before any transaction; tenant B is never written.
func TestForgedTenantReportRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	forged := rep(tB, host1, "evil", "10.0.0.66", t0, 1)
	f.inv.Put(rep(tA, host2, "web-02", "10.0.0.5", t0, 2)) // makes tenant A listed
	f.r.inv = &mislabel{Fake: f.inv, extra: forged}
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tid := range []string{tA, tB} {
		if l, _ := f.st.ListDevices(ctx, tid, store.DeviceFilter{Query: "evil"}); len(l) != 0 {
			t.Fatalf("forged report written in %s", tid)
		}
	}
	f.device(t, tA, "web-02")
	if f.settings(t, tA).HostsFailed != 0 {
		t.Fatal("a rejected report is not a failure that blocks the watermark")
	}
	s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
	bad := rep(tA, "not-a-uuid", "x", "10.0.0.1", t0, 3)
	if _, err := f.r.Apply(ctx, s, bad, TriggerPoll, "r"); err != hostreport.ErrHostID {
		t.Fatalf("non-uuid host id: %v", err)
	}
}

// mislabel returns an extra report of another tenant inside tenant A's pages.
type mislabel struct {
	*invclient.Fake
	extra invclient.Report
}

func (m *mislabel) ListHostReports(ctx context.Context, tid string, f invclient.Filter, fn func([]invclient.Report) error) error {
	return m.Fake.ListHostReports(ctx, tid, f, func(p []invclient.Report) error {
		return fn(append(append([]invclient.Report(nil), p...), m.extra))
	})
}

// Hostile strings and oversized lists are bounded; stored names never carry
// control characters.
func TestHostileReportBounded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := rep(tA, host1, strings.Repeat("h", hostreport.MaxHostname), "10.0.0.5", t0, 1)
	r.Host.Manufacturer = "ACME\x1b[31m"
	r.Host.Model = strings.Repeat("m", 1000)
	for i := 0; i < 10*hostreport.MaxInterfaces; i++ {
		r.Interfaces = append(r.Interfaces, invclient.Interface{Name: fmt.Sprintf("if%d‮", i)})
	}
	for i := 0; i < 10*hostreport.MaxPackages; i++ {
		r.PendingUpdates = append(r.PendingUpdates, invclient.PendingUpdate{Name: fmt.Sprintf("p%d", i), AvailableVersion: "2"})
	}
	for i := 0; i < 10*hostreport.MaxGuests; i++ {
		r.Guests = append(r.Guests, invclient.Guest{ID: fmt.Sprint(i + 100), Kind: "vm", Name: "g\x00"})
	}
	r.Updates.Status = store.UpdAvailable
	s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
	res, err := f.r.Apply(ctx, s, r, TriggerPoll, "r")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := f.st.GetDevice(ctx, tA, res.DeviceID)
	if len(d.Name) != hostreport.MaxHostname || d.Manufacturer != "" || d.Model != "" {
		t.Fatalf("device %q %q %q", d.Name, d.Manufacturer, d.Model)
	}
	ifs, _ := f.st.ListInterfaces(ctx, tA, d.ID)
	if len(ifs) > hostreport.MaxInterfaces {
		t.Fatalf("%d interfaces", len(ifs))
	}
	pk, _ := f.st.ListDevicePackages(ctx, tA, d.ID, nil, nil, "")
	if len(pk) != hostreport.MaxPackages || d.GuestCount != hostreport.MaxGuests {
		t.Fatalf("packages %d guests %d", len(pk), d.GuestCount)
	}
	st, _ := f.st.GetHostSyncDeviceState(ctx, tA, d.ID)
	found := false
	for _, i := range st.Issues {
		found = found || (i.Field == "packages" && i.Reason == "truncated")
	}
	if !found || len(st.Issues) > hostreport.MaxIssues {
		t.Fatalf("issues %+v", st.Issues)
	}
}

// The same inventory host id linked in two tenants stays independent.
func TestSameHostIDPerTenantIndependent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-a", "10.0.0.5", t0, 1))
	f.inv.Put(rep(tB, host1, "web-b", "10.0.0.5", t0, 2))
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := f.device(t, tA, "web-a"), f.device(t, tB, "web-b")
	if a.ID == b.ID || a.InventoryHostID != b.InventoryHostID {
		t.Fatal("independent devices per tenant")
	}
	aa, _ := f.st.FindAddress(ctx, tA, "10.0.0.5")
	ab, _ := f.st.FindAddress(ctx, tB, "10.0.0.5")
	if aa.DeviceID != a.ID || ab.DeviceID != b.ID || aa.PreviousDeviceID != "" {
		t.Fatal("the same address in two tenants never moves across tenants")
	}
}

// Every store failure inside the apply aborts it and leaves nothing behind.
func TestApplyStoreFailuresRollBack(t *testing.T) {
	methods := []string{"DeviceByInventoryHost", "InsertDevice", "UpsertInterfaceReported", "CreateSubnetAuto",
		"InsertAddressReported", "ReplacePendingPackages", "ReplaceGuests", "SaveDeviceState", "AppendAudit"}
	for _, m := range methods {
		f := newFixture(t)
		ctx := context.Background()
		s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
		r := rep(tA, host1, "web-01", "10.0.0.5", t0, 1)
		r.Updates.Status = store.UpdAvailable
		r.PendingUpdates = []invclient.PendingUpdate{{Name: "p", AvailableVersion: "2"}}
		r.Guests = []invclient.Guest{{ID: "101", Kind: "vm"}}
		f.st.FailNext("tx." + m)
		if _, err := f.r.Apply(ctx, s, r, TriggerPoll, "run"); err == nil {
			t.Errorf("%s: failure not surfaced", m)
		}
		if l, _ := f.st.ListDevices(ctx, tA, store.DeviceFilter{}); len(l) != 0 || len(f.st.Audit()) != 0 {
			t.Errorf("%s: partial apply committed", m)
		}
	}
}

func TestApplyUpdateOpsAndHypervisorExecute(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
	hv := rep(tA, host1, "hv", "10.0.0.5", t0, 1)
	hv.Guests = []invclient.Guest{{ID: "101", Kind: "vm", MACs: []string{"bc:24:11:00:00:01"}}}
	if _, err := f.r.Apply(ctx, s, hv, TriggerPoll, "r"); err != nil {
		t.Fatal(err)
	}
	g := rep(tA, host2, "guest", "10.0.0.6", t0, 2)
	g.Interfaces[0].MAC = "bc:24:11:00:00:01"
	if _, err := f.r.Apply(ctx, s, g, TriggerPoll, "r"); err != nil {
		t.Fatal(err)
	}
	// Host re-reports without the guest: unlink + interface/address updates.
	hv2 := rep(tA, host1, "hv", "10.0.0.7", t0.Add(time.Minute), 3)
	hv2.Interfaces[0].SpeedBps = 1e9
	for _, m := range []string{"UpdateDeviceReported", "UpdateAddressReported", "SetHypervisor"} {
		f.st.FailNext("tx." + m)
		if _, err := f.r.Apply(ctx, s, hv2, TriggerPoll, "r"); err == nil {
			t.Errorf("%s: failure not surfaced", m)
		}
	}
	if _, err := f.r.Apply(ctx, s, hv2, TriggerPoll, "r"); err != nil {
		t.Fatal(err)
	}
	if d := f.device(t, tA, "guest"); d.HypervisorDeviceID != "" {
		t.Fatal("guest unlinked")
	}
	// A guest reporting again while its host lists it: set_guest_device failure.
	f.st.FailNext("tx.SetGuestDevice")
	if _, err := f.r.Apply(ctx, s, hv, TriggerPoll, "r"); err != nil {
		t.Fatal(err) // host links the guest itself (no set_guest_device op)
	}
}
