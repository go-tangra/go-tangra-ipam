//go:build integration

package repodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestSetDeviceBMCRef (024 T008): the reference column and its audit row are
// written in one tenant transaction, only the column changes, and RLS hides
// another tenant's device.
func TestSetDeviceBMCRef(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := repodb.New(st)
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()

	dev := store.Device{ID: store.NewID(), TenantID: tenantA, Name: "node-1", DeviceType: store.DevServer, Status: store.DevStActive,
		ManagementIP: "10.1.112.14", Contact: ""}
	if err := db.CreateDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	ref := "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"
	row := store.AuditRow{ID: store.NewID(), TenantID: tenantA, ActorKind: "user", ActorID: "u1", Action: "bmc_reference_set",
		SubjectKind: "device", SubjectID: dev.ID, Outcome: "ok", Detail: map[string]any{"reference": ref}}
	prev, err := db.SetDeviceBMCRef(ctx, tenantA, dev.ID, ref, row)
	if err != nil || prev != "" {
		t.Fatalf("set: %q %v", prev, err)
	}
	got, err := db.GetDevice(ctx, tenantA, dev.ID)
	if err != nil || got.IPMISecretRef != ref || got.Name != "node-1" || got.ManagementIP != "10.1.112.14" {
		t.Fatalf("device after set: %+v %v", got, err)
	}
	var n int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE action='bmc_reference_set' AND subject_id=$1", dev.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d %v", n, err)
	}

	// Another tenant cannot see or change the device; no audit row is written.
	other := row
	other.ID, other.TenantID = store.NewID(), tenantB
	if _, err := db.SetDeviceBMCRef(ctx, tenantB, dev.ID, "", other); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if _, err := db.SetDeviceBMCRef(ctx, tenantA, store.NewID(), ref, other); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE subject_id=$1", dev.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refused writes left audit rows: %d %v", n, err)
	}

	// Clear returns the previous reference.
	clr := row
	clr.ID, clr.Action = store.NewID(), "bmc_reference_cleared"
	if prev, err := db.SetDeviceBMCRef(ctx, tenantA, dev.ID, "", clr); err != nil || prev != ref {
		t.Fatalf("clear: %q %v", prev, err)
	}
	if got, _ := db.GetDevice(ctx, tenantA, dev.ID); got.IPMISecretRef != "" {
		t.Fatalf("not cleared: %q", got.IPMISecretRef)
	}
}
