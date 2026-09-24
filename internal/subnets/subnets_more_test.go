package subnets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestValidationErrorString(t *testing.T) {
	e := ValidationError{Msg: "bad"}
	if e.Error() != "subnets: bad" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestSetClockUsed(t *testing.T) {
	svc, _ := newSvc(t)
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	ctx := context.Background()
	got, err := svc.Create(ctx, subj(), store.Subnet{Name: "clocked", CIDR: "10.2.0.0/24"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("created_at not stamped")
	}
}

func TestUpdateInheritsAndRevalidates(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, subj(), store.Subnet{Name: "u1", CIDR: "10.3.0.0/24", Gateway: "10.3.0.1"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Update with empty name/CIDR: both inherited from the stored row.
	upd, err := svc.Update(ctx, subj(), store.Subnet{ID: created.ID, Description: "desc"}, false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "u1" || upd.CIDR != "10.3.0.0/24" {
		t.Errorf("inherit failed: %+v", upd)
	}
	if upd.Description != "desc" {
		t.Errorf("description not set")
	}

	// Update to an out-of-range gateway is rejected.
	_, err = svc.Update(ctx, subj(), store.Subnet{ID: created.ID, CIDR: "10.3.0.0/24", Gateway: "10.9.9.9"}, false)
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("want ValidationError, got %v", err)
	}
}

func TestForbiddenAllSubnetMethods(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	bad := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}

	if _, err := svc.Create(ctx, bad, store.Subnet{}, false); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("create: %v", err)
	}
	if _, err := svc.Get(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("get: %v", err)
	}
	if _, err := svc.Update(ctx, bad, store.Subnet{ID: "x"}, false); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("update: %v", err)
	}
	if err := svc.Delete(ctx, bad, "x", false); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("delete: %v", err)
	}
	if _, err := svc.GetTree(ctx, bad); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("tree: %v", err)
	}
	if _, err := svc.GetStats(ctx, bad); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("stats: %v", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if err := svc.Delete(ctx, subj(), "missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
