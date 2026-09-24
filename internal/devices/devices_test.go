package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func subj() authz.Subjects {
	return authz.Subjects{TenantID: "t1", UserID: "u1", ActorKind: authz.ActorUser}
}

func newSvc(t *testing.T) (*Service, *memstore.Mem) {
	t.Helper()
	st := memstore.New()
	return New(st), st
}

func TestDeviceCRUD(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	d, err := svc.Create(ctx, subj(), store.Device{Name: "sw1", DeviceType: store.DevSwitch})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Status != store.DevStActive || d.CreatedBy != "u1" {
		t.Errorf("defaults not set: %+v", d)
	}

	got, err := svc.Get(ctx, subj(), d.ID)
	if err != nil || got.Name != "sw1" {
		t.Fatalf("get: %v", err)
	}

	list, err := svc.List(ctx, subj(), store.DeviceFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v n=%d", err, len(list))
	}

	upd, err := svc.Update(ctx, subj(), store.Device{ID: d.ID, Name: "sw1b"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "sw1b" || upd.DeviceType != store.DevSwitch {
		t.Errorf("update lost fields: %+v", upd)
	}
	if upd.CreatedBy != d.CreatedBy {
		t.Errorf("created_by not preserved")
	}

	if _, err := svc.Get(ctx, subj(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeviceUniqueName(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, subj(), store.Device{Name: "dup"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.Create(ctx, subj(), store.Device{Name: "dup"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestDeviceDeleteGuard(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	d, err := svc.Create(ctx, subj(), store.Device{Name: "srv1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.5", DeviceID: d.ID}); err != nil {
		t.Fatalf("addr: %v", err)
	}
	if err := svc.Delete(ctx, subj(), d.ID, false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("want ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete(ctx, subj(), d.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, err := svc.Get(ctx, subj(), d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want gone, got %v", err)
	}
}

func TestDeleteEmptyDevice(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	d, _ := svc.Create(ctx, subj(), store.Device{Name: "srv2"})
	if err := svc.Delete(ctx, subj(), d.ID, false); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
}

func TestInterfaces(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	d, _ := svc.Create(ctx, subj(), store.Device{Name: "sw2"})

	i, err := svc.CreateInterface(ctx, subj(), d.ID, store.DeviceInterface{Name: "eth0", Enabled: true})
	if err != nil {
		t.Fatalf("create iface: %v", err)
	}
	if i.DeviceID != d.ID || i.TenantID != "t1" {
		t.Errorf("iface fields: %+v", i)
	}

	// Duplicate name on same device is a conflict.
	if _, err := svc.CreateInterface(ctx, subj(), d.ID, store.DeviceInterface{Name: "eth0"}); !errors.Is(err, ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}

	// Interface on a missing device is not-found.
	if _, err := svc.CreateInterface(ctx, subj(), "nope", store.DeviceInterface{Name: "eth9"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	list, err := svc.ListInterfaces(ctx, subj(), d.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list ifaces: %v n=%d", err, len(list))
	}

	if err := svc.DeleteInterface(ctx, subj(), i.ID); err != nil {
		t.Fatalf("delete iface: %v", err)
	}
	list, _ = svc.ListInterfaces(ctx, subj(), d.ID)
	if len(list) != 0 {
		t.Errorf("iface not deleted")
	}
	if err := svc.DeleteInterface(ctx, subj(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
}

func TestGetAddresses(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	d, _ := svc.Create(ctx, subj(), store.Device{Name: "srv3"})
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.6", DeviceID: d.ID, Owner: "secret@x"}); err != nil {
		t.Fatalf("addr: %v", err)
	}
	got, err := svc.GetAddresses(ctx, subj(), d.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("get addrs: %v n=%d", err, len(got))
	}
	if got[0].Owner != "" {
		t.Errorf("owner not redacted: %q", got[0].Owner)
	}
}

func TestPackages(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	d, _ := svc.Create(ctx, subj(), store.Device{Name: "srv4"})

	pkgs := []store.DevicePackage{
		{Name: "openssl", NeedsUpdate: true, IsSecurityUpdate: true, PackageManager: "apt"},
		{Name: "vim", NeedsUpdate: true, PackageManager: "apt"},
		{Name: "bash", PackageManager: "apt"},
	}
	res, err := svc.SyncPackages(ctx, subj(), d.ID, pkgs)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Total != 3 || res.Updates != 2 || res.Security != 1 {
		t.Errorf("counts = %+v", res)
	}

	all, err := svc.ListPackages(ctx, subj(), d.ID, nil, nil, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("list all: %v n=%d", err, len(all))
	}

	needs := true
	upd, err := svc.ListPackages(ctx, subj(), d.ID, &needs, nil, "")
	if err != nil || len(upd) != 2 {
		t.Fatalf("list needs-update: %v n=%d", err, len(upd))
	}

	sec := true
	secList, err := svc.ListPackages(ctx, subj(), d.ID, nil, &sec, "apt")
	if err != nil || len(secList) != 1 {
		t.Fatalf("list security: %v n=%d", err, len(secList))
	}

	// Device counts reflect the synced packages.
	dd, _ := svc.Get(ctx, subj(), d.ID)
	if dd.PackageUpdateCount != 2 || dd.SecurityUpdateCount != 1 {
		t.Errorf("device pkg counts = %d/%d", dd.PackageUpdateCount, dd.SecurityUpdateCount)
	}

	// Sync onto a missing device is not-found.
	if _, err := svc.SyncPackages(ctx, subj(), "nope", pkgs); !errors.Is(err, ErrNotFound) {
		t.Errorf("sync missing: %v", err)
	}
}

func TestSyncPackagesConflict(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	d, _ := svc.Create(ctx, subj(), store.Device{Name: "srv5"})
	// Duplicate package names are rejected by the store as a conflict.
	dup := []store.DevicePackage{{Name: "x"}, {Name: "x"}}
	if _, err := svc.SyncPackages(ctx, subj(), d.ID, dup); !errors.Is(err, ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}

func TestUpdateMissing(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Update(ctx, subj(), store.Device{ID: "missing", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestForbidden(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	bad := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}
	if _, err := svc.Create(ctx, bad, store.Device{Name: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("create: %v", err)
	}
	if _, err := svc.Get(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("get: %v", err)
	}
	if _, err := svc.List(ctx, bad, store.DeviceFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("list: %v", err)
	}
	if _, err := svc.Update(ctx, bad, store.Device{ID: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("update: %v", err)
	}
	if err := svc.Delete(ctx, bad, "x", false); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("delete: %v", err)
	}
	if _, err := svc.GetAddresses(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("get addrs: %v", err)
	}
	if _, err := svc.CreateInterface(ctx, bad, "x", store.DeviceInterface{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("create iface: %v", err)
	}
	if _, err := svc.ListInterfaces(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("list ifaces: %v", err)
	}
	if err := svc.DeleteInterface(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("delete iface: %v", err)
	}
	if _, err := svc.SyncPackages(ctx, bad, "x", nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("sync: %v", err)
	}
	if _, err := svc.ListPackages(ctx, bad, "x", nil, nil, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("list pkgs: %v", err)
	}
}

func TestSetClock(t *testing.T) {
	svc, _ := newSvc(t)
	svc.SetClock(nil)
	_ = svc
}
