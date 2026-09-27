//go:build integration

package repodb_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

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
