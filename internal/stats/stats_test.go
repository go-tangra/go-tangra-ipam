package stats_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stats"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func seed(t *testing.T, m *memstore.Mem, tenant string) {
	t.Helper()
	ctx := context.Background()
	if err := m.CreateSubnet(ctx, store.Subnet{TenantID: tenant, Name: "n", CIDR: "10.0.0.0/30"}); err != nil {
		t.Fatalf("subnet: %v", err)
	}
	if err := m.CreateVlan(ctx, store.Vlan{TenantID: tenant, VlanID: 3, Name: "v"}); err != nil {
		t.Fatalf("vlan: %v", err)
	}
	if err := m.CreateDevice(ctx, store.Device{TenantID: tenant, Name: "d", DeviceType: store.DevServer}); err != nil {
		t.Fatalf("device: %v", err)
	}
	if err := m.CreateLocation(ctx, store.Location{TenantID: tenant, Name: "l", LocationType: store.LocSite}); err != nil {
		t.Fatalf("loc: %v", err)
	}
}

func TestTenant(t *testing.T) {
	m := memstore.New()
	svc := stats.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	seed(t, m, tenant)

	s := authz.Subjects{TenantID: tenant, ActorKind: authz.ActorUser, Roles: []string{"admin"}}
	st, err := svc.Tenant(ctx, s)
	if err != nil {
		t.Fatalf("Tenant: %v", err)
	}
	if st.TotalSubnets != 1 || st.TotalVlans != 1 || st.TotalDevices != 1 || st.TotalLocations != 1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.DevicesByType[store.DevServer] != 1 {
		t.Fatalf("devices by type: %+v", st.DevicesByType)
	}
}

func TestTenantForbidden(t *testing.T) {
	m := memstore.New()
	svc := stats.New(m)
	ctx := context.Background()
	if _, err := svc.Tenant(ctx, authz.Subjects{ActorKind: authz.ActorUser}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestSystem(t *testing.T) {
	m := memstore.New()
	svc := stats.New(m)
	ctx := context.Background()
	t1, t2 := store.NewID(), store.NewID()
	seed(t, m, t1)
	seed(t, m, t2)

	admin := authz.Subjects{TenantID: t1, ActorKind: authz.ActorUser, Roles: []string{"owner"}}
	all, err := svc.System(ctx, admin)
	if err != nil {
		t.Fatalf("System: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("tenants = %d, want 2", len(all))
	}
	if all[t1].TotalSubnets != 1 || all[t2].TotalDevices != 1 {
		t.Fatalf("per-tenant rollup wrong: %+v", all)
	}
}

func TestSystemForbidden(t *testing.T) {
	m := memstore.New()
	svc := stats.New(m)
	ctx := context.Background()
	nonAdmin := authz.Subjects{TenantID: store.NewID(), ActorKind: authz.ActorUser}
	if _, err := svc.System(ctx, nonAdmin); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestSystemViaSystemScope(t *testing.T) {
	m := memstore.New()
	svc := stats.New(m)
	ctx := context.Background()
	seed(t, m, store.NewID())
	sys := authz.Subjects{ActorKind: authz.ActorSystem}
	if _, err := svc.System(ctx, sys); err != nil {
		t.Fatalf("system scope System: %v", err)
	}
}
