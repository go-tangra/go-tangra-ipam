package vlans_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
)

func subj(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, UserID: "u1", Roles: []string{"admin"}, ActorKind: authz.ActorUser}
}

func TestCreateGetListUpdateDelete(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	v, err := svc.Create(ctx, s, store.Vlan{VlanID: 100, Name: "prod"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.ID == "" || v.Status != store.VlanActive || v.CreatedBy != "u1" {
		t.Fatalf("unexpected created vlan: %+v", v)
	}

	got, err := svc.Get(ctx, s, v.ID)
	if err != nil || got.Name != "prod" {
		t.Fatalf("Get: %v %+v", err, got)
	}

	list, err := svc.List(ctx, s, store.VlanFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v n=%d", err, len(list))
	}

	got.Name = "prod2"
	got.Status = ""
	up, err := svc.Update(ctx, s, got)
	if err != nil || up.Name != "prod2" || up.Status != store.VlanActive {
		t.Fatalf("Update: %v %+v", err, up)
	}

	if err := svc.Delete(ctx, s, v.ID, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, s, v.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	for _, id := range []int{0, 4095, -1, 9000} {
		_, err := svc.Create(ctx, s, store.Vlan{VlanID: id, Name: "x"})
		var ve vlans.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("VlanID %d: want ValidationError, got %v", id, err)
		}
	}
	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 10}); err == nil {
		t.Fatalf("empty name: want error")
	}
	// boundaries ok
	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 1, Name: "lo"}); err != nil {
		t.Fatalf("VlanID 1: %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 4094, Name: "hi"}); err != nil {
		t.Fatalf("VlanID 4094: %v", err)
	}
}

func TestConflict(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 5, Name: "a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 5, Name: "b"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup vlan_id: want ErrConflict, got %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Vlan{VlanID: 6, Name: "a"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup name: want ErrConflict, got %v", err)
	}
}

func TestDeleteGuardAndGetSubnets(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	v, err := svc.Create(ctx, s, store.Vlan{VlanID: 20, Name: "vl"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.CreateSubnet(ctx, store.Subnet{TenantID: tenant, Name: "net", CIDR: "10.0.0.0/24", VlanID: v.ID}); err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	subs, err := svc.GetSubnets(ctx, s, v.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("GetSubnets: %v n=%d", err, len(subs))
	}

	if err := svc.Delete(ctx, s, v.ID, false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete with subnets: want ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete(ctx, s, v.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
}

func TestForbidden(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	other := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}
	if _, err := svc.Create(ctx, other, store.Vlan{VlanID: 1, Name: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if _, err := svc.List(ctx, other, store.VlanFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("List want ErrForbidden, got %v", err)
	}
}

func TestErrorString(t *testing.T) {
	e := vlans.ValidationError{Msg: "bad"}
	if e.Error() != "vlans: bad" {
		t.Fatalf("Error() = %q", e.Error())
	}
}

func TestForbiddenSweep(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.Get(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := svc.Update(ctx, bad, store.Vlan{VlanID: 1, Name: "n"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Update: %v", err)
	}
	if err := svc.Delete(ctx, bad, "x", false); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetSubnets(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("GetSubnets: %v", err)
	}
}

func TestUpdateNotFoundAndErrorInjection(t *testing.T) {
	m := memstore.New()
	svc := vlans.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	if _, err := svc.Update(ctx, s, store.Vlan{ID: "missing", VlanID: 3, Name: "n"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Update missing: %v", err)
	}
	// GetSubnets error injection during delete guard.
	v, _ := svc.Create(ctx, s, store.Vlan{VlanID: 7, Name: "g"})
	m.FailNext("DeleteVlan")
	if err := svc.Delete(ctx, s, v.ID, true); err == nil {
		t.Fatalf("expected injected DeleteVlan error")
	}
}
