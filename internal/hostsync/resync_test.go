package hostsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func admin(f *fixture, global bool) *Admin {
	a := NewAdmin(f.st, f.inv, f.r, global)
	a.now = func() time.Time { return f.now }
	return a
}

var userA = authz.Subjects{TenantID: tA, UserID: "u1", ActorKind: authz.ActorUser}

func TestResyncDeviceIgnoresDigest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	d := f.device(t, tA, "web-01")
	// Someone edits the address by hand; the digest is unchanged, so only a
	// forced re-sync puts it back.
	a, _ := f.st.FindAddress(ctx, tA, "10.0.0.5")
	a.InterfaceName = "hand"
	_ = f.st.UpdateAddress(ctx, a)
	ad := admin(f, true)
	res, err := ad.ResyncDevice(ctx, userA, d.ID)
	if err != nil || !res.Applied || res.Changes == 0 {
		t.Fatalf("resync %+v %v", res, err)
	}
	if a, _ = f.st.FindAddress(ctx, tA, "10.0.0.5"); a.InterfaceName != "eth0" {
		t.Fatal("forced re-sync applied")
	}
	st, _ := f.st.GetHostSyncDeviceState(ctx, tA, d.ID)
	if st.Trigger != "resync:u1" || f.audits("hostsync_resync_requested") != 1 {
		t.Fatalf("trigger %q", st.Trigger)
	}
	info, err := ad.DeviceHostSync(ctx, userA, d.ID)
	if err != nil || info.Source != store.SrcHostReport || info.InventoryHostID != host1 || info.Trigger != "resync:u1" || info.Issues == nil {
		t.Fatalf("info %+v %v", info, err)
	}
}

func TestResyncErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ad := admin(f, true)
	_ = f.st.CreateDevice(ctx, store.Device{ID: "manual", TenantID: tA, Name: "manual"})
	if _, err := ad.ResyncDevice(ctx, userA, "manual"); !errors.Is(err, ErrNotHostReported) {
		t.Fatalf("manual device: %v", err)
	}
	if _, err := ad.ResyncDevice(ctx, userA, "missing"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("missing device")
	}
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	d := f.device(t, tA, "web-01")
	f.inv.Down = true
	if _, err := ad.ResyncDevice(ctx, userA, d.ID); !errors.Is(err, invclient.ErrUnavailable) {
		t.Fatalf("inventory down: %v", err)
	}
	f.inv.Down = false
	s, _ := f.st.GetHostSyncSettings(ctx, tA)
	s.Enabled = false
	_ = f.st.UpdateHostSyncSettings(ctx, s, store.AuditRow{})
	if _, err := ad.ResyncDevice(ctx, userA, d.ID); !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if err := ad.ResyncAll(ctx, userA); !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatal("resync all disabled")
	}
	if _, err := admin(f, false).ResyncDevice(ctx, userA, d.ID); !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatal("global kill switch")
	}
	// Cross-tenant callers never reach another tenant's device.
	if _, err := ad.ResyncDevice(ctx, authz.Subjects{TenantID: tB, UserID: "x", ActorKind: authz.ActorUser}, d.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant resync")
	}
	if _, err := ad.ResyncDevice(ctx, authz.Subjects{UserID: "x", ActorKind: authz.ActorUser}, d.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("no tenant")
	}
}

func TestResyncAllForcesReconcile(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	ad := admin(f, true)
	if err := ad.ResyncAll(ctx, userA); err != nil {
		t.Fatal(err)
	}
	if !f.settings(t, tA).ReconcileRequested || f.audits("hostsync_resync_requested") != 1 {
		t.Fatal("reconcile requested")
	}
	f.now = t0.Add(time.Minute)
	before := f.settings(t, tA).LastReconcileAt
	if err := f.r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	s := f.settings(t, tA)
	if s.ReconcileRequested || !s.LastReconcileAt.After(*before) {
		t.Fatalf("reconcile ran %+v", s)
	}
}

func TestAdminSettingsAndStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ad := admin(f, true)
	s, err := ad.Settings(ctx, userA)
	if err != nil || !s.Enabled {
		t.Fatal(err)
	}
	for _, bad := range []SettingsInput{
		{Enabled: true, FullIntervalMinutes: 14}, {Enabled: true, FullIntervalMinutes: 1441},
		{Enabled: true, FullIntervalMinutes: 60, ExcludedInterfaces: []string{"bad pattern"}},
	} {
		if _, err := ad.UpdateSettings(ctx, userA, bad); !errors.Is(err, ErrValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	got, err := ad.UpdateSettings(ctx, userA, SettingsInput{Enabled: false, FullIntervalMinutes: 30})
	if err != nil || got.Enabled || got.FullIntervalMinutes != 30 || len(got.ExcludedInterfaces) != 0 || got.UpdatedBy != "u1" {
		t.Fatalf("%+v %v", got, err)
	}
	var row store.AuditRow
	for _, a := range f.st.Audit() {
		if a.Action == "hostsync_settings_updated" {
			row = a
		}
	}
	ch := row.Detail["changes"].(map[string]any)
	if row.ActorKind != "user" || row.ActorID != "u1" || ch["enabled"] == nil || ch["full_interval_minutes"] == nil || ch["excluded_interfaces"] == nil {
		t.Fatalf("settings audit %+v", row)
	}
	st, err := ad.Status(ctx, userA)
	if err != nil || st.State != store.HostSyncDisabled || st.Enabled {
		t.Fatalf("status %+v", st)
	}
	_, _ = ad.UpdateSettings(ctx, userA, SettingsInput{Enabled: true, FullIntervalMinutes: 60, ExcludedInterfaces: []string{"docker*"}})
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	st, _ = ad.Status(ctx, userA)
	if st.State != store.HostSyncOK || st.NextReconcileAt == nil || st.HostsReported != 1 || !st.Enabled {
		t.Fatalf("status %+v", st)
	}
	if st, _ = admin(f, false).Status(ctx, userA); st.State != store.HostSyncDisabled {
		t.Fatal("global switch shows disabled")
	}
	f.st.FailNext("HostSyncCounts")
	if _, err := ad.Status(ctx, userA); err == nil {
		t.Fatal("counts error")
	}
	f.st.FailNext("EnsureHostSyncSettings")
	if _, err := ad.UpdateSettings(ctx, userA, SettingsInput{Enabled: true, FullIntervalMinutes: 60}); err == nil {
		t.Fatal("store error")
	}
	if _, err := ad.Settings(ctx, authz.Subjects{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("no tenant")
	}
	if _, err := ad.UpdateSettings(ctx, authz.Subjects{}, SettingsInput{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("no tenant update")
	}
	if err := ad.ResyncAll(ctx, authz.Subjects{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("no tenant resync all")
	}
}

func TestAdminGuestsAndConflicts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ad := admin(f, true)
	hv := rep(tA, host1, "hv-01", "10.0.0.5", t0, 1)
	hv.Guests = []invclient.Guest{{ID: "101", Name: "vm-a", Kind: "vm", MACs: []string{"bc:24:11:00:00:01"}}}
	f.inv.Put(hv)
	_ = f.r.Cycle(ctx)
	d := f.device(t, tA, "hv-01")
	gl, err := ad.Guests(ctx, userA, d.ID)
	if err != nil || len(gl) != 1 || gl[0].GuestRef != "101" || gl[0].GuestDeviceID != "" {
		t.Fatalf("guests %+v %v", gl, err)
	}
	// The guest reports itself -> linked to its hypervisor.
	g := rep(tA, host2, "vm-a", "10.0.0.6", t0.Add(30*time.Second), 2)
	g.Interfaces[0].MAC = "bc:24:11:00:00:01"
	g.Virtualization = invclient.Virtualization{Role: "vm", Kind: "kvm"}
	f.inv.Put(g)
	f.now = t0.Add(time.Minute)
	_ = f.r.Cycle(ctx)
	gd := f.device(t, tA, "vm-a")
	if gd.HypervisorDeviceID != d.ID || gd.DeviceType != store.DevVM || gd.VirtualizationKind != "kvm" {
		t.Fatalf("guest device %+v", gd)
	}
	if gl, _ = ad.Guests(ctx, userA, d.ID); gl[0].GuestDeviceID != gd.ID || gl[0].GuestDeviceName != "vm-a" {
		t.Fatalf("matched guest %+v", gl)
	}
	if gl, _ = ad.Guests(ctx, userA, gd.ID); len(gl) != 0 || gl == nil {
		t.Fatal("empty list, not null")
	}
	if _, err := ad.Guests(ctx, authz.Subjects{TenantID: tB, ActorKind: authz.ActorUser}, d.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant guests")
	}
	a, _ := f.st.FindAddress(ctx, tA, "10.0.0.5")
	if _, err := ad.ClearConflict(ctx, userA, a.ID); err != nil || f.audits("address_conflict_cleared") != 1 {
		t.Fatal("clear conflict")
	}
	if _, err := ad.ClearConflict(ctx, authz.Subjects{}, a.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("no tenant clear")
	}
	if _, err := ad.DeviceHostSync(ctx, userA, "missing"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("missing device")
	}
	_ = f.st.CreateDevice(ctx, store.Device{ID: "m", TenantID: tA, Name: "manual"})
	if info, err := ad.DeviceHostSync(ctx, userA, "m"); err != nil || info.Source != store.SrcManual || info.Issues == nil {
		t.Fatalf("manual %+v %v", info, err)
	}
}

func TestResyncTriggerBounded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.inv.Put(rep(tA, host1, "web-01", "10.0.0.5", t0, 1))
	_ = f.r.Cycle(ctx)
	d := f.device(t, tA, "web-01")
	long := authz.Subjects{TenantID: tA, UserID: strings.Repeat("u", 200), ActorKind: authz.ActorUser}
	if _, err := admin(f, true).ResyncDevice(ctx, long, d.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.st.GetHostSyncDeviceState(ctx, tA, d.ID); len(st.Trigger) != 80 {
		t.Fatalf("trigger %d", len(st.Trigger))
	}
}
