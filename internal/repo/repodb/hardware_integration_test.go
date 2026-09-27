//go:build integration

package repodb_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestDeviceHardwareApply: hardware replace + audit in the host transaction,
// rollback on failure, summary on device reads, has_hardware filter and
// tenant isolation of the host transaction (T054).
func TestDeviceHardwareApply(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for _, tid := range []string{tenantA, tenantB} {
		if _, err := db.EnsureHostSyncSettings(ctx, tid); err != nil {
			t.Fatal(err)
		}
	}
	devA, devA2 := store.NewID(), store.NewID()
	now := time.Now().UTC().Truncate(time.Second)
	hw := store.DeviceHardware{DeviceID: devA, Digest: strings.Repeat("a", 64), ReportedAt: now,
		Profile: store.HardwareProfile{Schema: 2, BIOS: store.HardwareBIOS{Version: "2.5"},
			Disks: []store.HardwareDisk{{Name: "nvme0n1", SizeBytes: 5, Media: "nvme_ssd", Interface: "nvme"}}},
		Summary: store.HardwareSummary{CPUModel: "Xeon", CPUSockets: 2, CPUCores: 24, CPUThreads: 48, MemoryTotalBytes: 32 << 30,
			MemoryType: "DDR4", MemorySlotsTotal: 16, MemorySlotsUsed: 16, DiskCount: 1, DiskTotalBytes: 5}}
	err := db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		for _, id := range []string{devA, devA2} {
			if e := tx.InsertDevice(store.Device{ID: id, Name: "srv-" + id[len(id)-4:], Status: "active", DeviceType: "server", Source: "host_report", InventoryHostID: store.NewID()}); e != nil {
				return e
			}
		}
		if h, e := tx.GetHardware(devA); e != nil || h != nil {
			t.Fatalf("no hardware yet: %+v %v", h, e)
		}
		if e := tx.ReplaceHardware(hw); e != nil {
			return e
		}
		return tx.AppendAudit(store.AuditRow{TenantID: tenantA, Action: "hardware_reported", ActorKind: "system", ActorID: "hostsync", SubjectKind: "device", SubjectID: devA, Outcome: "ok"})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDeviceHardware(ctx, tenantA, devA)
	if err != nil || got.Profile.BIOS.Version != "2.5" || got.Summary.CPUCores != 24 || !got.ReportedAt.Equal(now) || got.Digest != hw.Digest {
		t.Fatalf("stored %+v %v", got, err)
	}
	dev, _ := db.GetDevice(ctx, tenantA, devA)
	if dev.HardwareSummary == nil || dev.HardwareSummary.MemoryType != "DDR4" || !dev.HardwareSummary.ReportedAt.Equal(now) {
		t.Fatalf("device summary %+v", dev.HardwareSummary)
	}
	if with, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{HasHardware: "true"}); len(with) != 1 || with[0].ID != devA || with[0].HardwareSummary == nil {
		t.Fatalf("has_hardware=true %+v", with)
	}
	if without, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{HasHardware: "false"}); len(without) != 1 || without[0].ID != devA2 || without[0].HardwareSummary != nil {
		t.Fatalf("has_hardware=false %+v", without)
	}

	// A failing apply undoes the hardware replace and its audit row.
	failure := errors.New("forced failure")
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		h2 := hw
		h2.Profile.BIOS.Version, h2.Digest = "9.9", strings.Repeat("b", 64)
		if e := tx.ReplaceHardware(h2); e != nil {
			return e
		}
		if e := tx.AppendAudit(store.AuditRow{TenantID: tenantA, Action: "hardware_updated", ActorKind: "system", ActorID: "hostsync", SubjectKind: "device", SubjectID: devA, Outcome: "ok"}); e != nil {
			return e
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got, _ := db.GetDeviceHardware(ctx, tenantA, devA); got.Profile.BIOS.Version != "2.5" {
		t.Fatal("a failed apply must not keep the hardware")
	}
	var updated int
	_ = db.St.Tx(ctx, store.Scope{TenantID: tenantA}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE action='hardware_updated'").Scan(&updated)
	})
	if updated != 0 {
		t.Fatal("a failed apply must not keep the audit row")
	}

	// Tenant B can neither read nor write tenant A's hardware.
	err = db.ApplyHostReport(ctx, tenantB, func(tx repo.HostTx) error {
		if h, e := tx.GetHardware(devA); e != nil || h != nil {
			t.Fatalf("cross-tenant read: %+v %v", h, e)
		}
		h2 := hw
		h2.Profile.BIOS.Version = "forged"
		if e := tx.ReplaceHardware(h2); !errors.Is(e, repo.ErrNotFound) {
			t.Fatalf("cross-tenant write: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDeviceHardware(ctx, tenantB, devA); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant B reads tenant A hardware: %v", err)
	}
	if got, _ := db.GetDeviceHardware(ctx, tenantA, devA); got.Profile.BIOS.Version != "2.5" {
		t.Fatal("tenant B changed tenant A hardware")
	}
}

// TestDeviceHardwareMigration upgrades a database at 0008 holding devices to
// 0009 and checks the constraints, the cascade and RLS of
// ipam_device_hardware (feature 023, T023).
func TestDeviceHardwareMigration(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, adminDSN, 8); err != nil {
		t.Fatalf("migrate to 0008: %v", err)
	}
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	devA, devB := store.NewID(), store.NewID()
	for _, row := range [][2]string{{devA, tenantA}, {devB, tenantB}} {
		if _, err := admin.Exec(ctx, "INSERT INTO ipam_devices (id, tenant_id, name) VALUES ($1, $2, 'srv')", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatalf("migrate 0009: %v", err)
	}
	digest := strings.Repeat("a", 64)
	ins := func(dev, tenant, dg, profile string) error {
		_, err := admin.Exec(ctx, `INSERT INTO ipam_device_hardware (device_id, tenant_id, profile, digest, reported_at)
			VALUES ($1, $2, $3::jsonb, $4, now())`, dev, tenant, profile, dg)
		return err
	}
	if ins(devA, tenantA, "xyz", `{}`) == nil {
		t.Fatal("digest CHECK")
	}
	if ins(devA, tenantA, strings.Repeat("A", 64), `{}`) == nil {
		t.Fatal("digest CHECK (uppercase)")
	}
	big := fmt.Sprintf(`{"x":"%s"}`, strings.Repeat("y", 600<<10))
	if ins(devA, tenantA, digest, big) == nil {
		t.Fatal("profile size CHECK (> 512 KiB jsonb text)")
	}
	if err := ins(devA, tenantA, digest, `{"bios":{"version":"2.5"}}`); err != nil {
		t.Fatal(err)
	}
	if err := ins(devB, tenantB, digest, `{}`); err != nil {
		t.Fatal(err)
	}
	var cores, slots int
	var mtype string
	if err := admin.QueryRow(ctx, "SELECT cpu_cores, memory_slots_total, memory_type FROM ipam_device_hardware WHERE device_id=$1", devA).Scan(&cores, &slots, &mtype); err != nil || cores != 0 || slots != 0 || mtype != "" {
		t.Fatalf("defaults %d %d %q %v", cores, slots, mtype, err)
	}

	st, err := store.Open(ctx, appDSN, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	count := func(scope store.Scope) (n int) {
		if err := st.Tx(ctx, scope, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_device_hardware").Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if a, b, sys := count(store.Scope{TenantID: tenantA}), count(store.Scope{TenantID: tenantB}), count(store.Scope{System: true}); a != 1 || b != 1 || sys != 2 {
		t.Fatalf("RLS a=%d b=%d sys=%d", a, b, sys)
	}
	// Tenant A cannot write tenant B's row.
	if err := st.Tx(ctx, store.Scope{TenantID: tenantA}, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, "UPDATE ipam_device_hardware SET cpu_cores=99 WHERE device_id=$1", devB)
		if err == nil && ct.RowsAffected() != 0 {
			t.Error("RLS breach on update")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Cascade with the device.
	if _, err := admin.Exec(ctx, "DELETE FROM ipam_devices WHERE id=$1", devA); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = admin.QueryRow(ctx, "SELECT count(*) FROM ipam_device_hardware WHERE device_id=$1", devA).Scan(&n)
	if n != 0 {
		t.Fatal("hardware must cascade with the device")
	}
}
