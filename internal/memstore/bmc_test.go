package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestSetDeviceBMCRef (024 T007): the reference and its audit row are written
// together, the previous value is returned, and another tenant's device is
// invisible (no row written).
func TestSetDeviceBMCRef(t *testing.T) {
	ctx := context.Background()
	m := New()
	if err := m.CreateDevice(ctx, store.Device{ID: "d1", TenantID: "t1", Name: "node-1"}); err != nil {
		t.Fatal(err)
	}
	row := store.AuditRow{TenantID: "t1", Action: "bmc_reference_set", SubjectID: "d1"}
	prev, err := m.SetDeviceBMCRef(ctx, "t1", "d1", "ref-a", row)
	if err != nil || prev != "" {
		t.Fatalf("set: prev=%q err=%v", prev, err)
	}
	d, _ := m.GetDevice(ctx, "t1", "d1")
	if d.IPMISecretRef != "ref-a" {
		t.Fatalf("ref = %q", d.IPMISecretRef)
	}
	prev, err = m.SetDeviceBMCRef(ctx, "t1", "d1", "", store.AuditRow{TenantID: "t1", Action: "bmc_reference_cleared", SubjectID: "d1"})
	if err != nil || prev != "ref-a" {
		t.Fatalf("clear: prev=%q err=%v", prev, err)
	}
	if len(m.Audit()) != 2 || m.Audit()[0].Action != "bmc_reference_set" {
		t.Fatalf("audit = %+v", m.Audit())
	}
	if _, err := m.SetDeviceBMCRef(ctx, "t2", "d1", "ref-x", store.AuditRow{TenantID: "t2"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if _, err := m.SetDeviceBMCRef(ctx, "t1", "missing", "ref-x", store.AuditRow{TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}
	if len(m.Audit()) != 2 {
		t.Fatalf("refused writes left audit rows: %d", len(m.Audit()))
	}
	m.FailNext("SetDeviceBMCRef")
	if _, err := m.SetDeviceBMCRef(ctx, "t1", "d1", "ref-b", row); err == nil {
		t.Fatal("injected failure ignored")
	}
}
