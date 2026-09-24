package dnscfg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/dnscfg"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func admin(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, ActorKind: authz.ActorUser, Roles: []string{"admin"}}
}

func TestGetDefault(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	ctx := context.Background()
	tenant := store.NewID()

	c, err := svc.Get(ctx, admin(tenant))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.TimeoutMs != dnscfg.DefaultTimeoutMs || !c.UseSystemDNSFallback || !c.ReverseDNSEnabled {
		t.Fatalf("default config wrong: %+v", c)
	}
}

func TestUpdateThenGet(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := admin(tenant)

	cfg := store.DNSConfig{DNSServers: []string{"1.1.1.1"}, TimeoutMs: 250, ReverseDNSEnabled: true}
	up, err := svc.Update(ctx, s, cfg)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.TimeoutMs != 250 || len(up.DNSServers) != 1 {
		t.Fatalf("updated config: %+v", up)
	}
	got, _ := svc.Get(ctx, s)
	if got.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("persisted: %+v", got)
	}

	// zero timeout defaults
	up2, _ := svc.Update(ctx, s, store.DNSConfig{TimeoutMs: 0})
	if up2.TimeoutMs != dnscfg.DefaultTimeoutMs {
		t.Fatalf("zero timeout not defaulted: %+v", up2)
	}
}

func TestUpdateRequiresAdmin(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	nonAdmin := authz.Subjects{TenantID: tenant, ActorKind: authz.ActorUser}
	if _, err := svc.Update(ctx, nonAdmin, store.DNSConfig{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestTestLookupSeam(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	tenant := store.NewID()
	s := admin(tenant)
	ctx := context.Background()

	called := ""
	svc.SetLookup(func(_ context.Context, addr string) ([]string, error) {
		called = addr
		return []string{"host.example.com."}, nil
	})
	name, _, err := svc.Test(ctx, s, "192.0.2.10")
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if name != "host.example.com." || called != "192.0.2.10" {
		t.Fatalf("name=%q called=%q", name, called)
	}
}

func TestTestNoName(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	s := admin(store.NewID())
	svc.SetLookup(func(_ context.Context, _ string) ([]string, error) { return nil, nil })
	name, _, err := svc.Test(context.Background(), s, "10.0.0.1")
	if err != nil || name != "" {
		t.Fatalf("empty name expected: name=%q err=%v", name, err)
	}
}

func TestTestLookupError(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	s := admin(store.NewID())
	sentinel := errors.New("boom")
	svc.SetLookup(func(_ context.Context, _ string) ([]string, error) { return nil, sentinel })
	_, _, err := svc.Test(context.Background(), s, "10.0.0.1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

func TestTestBadIP(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	s := admin(store.NewID())
	invoked := false
	svc.SetLookup(func(_ context.Context, _ string) ([]string, error) { invoked = true; return nil, nil })
	if _, _, err := svc.Test(context.Background(), s, "not-an-ip"); err == nil {
		t.Fatalf("bad ip: want error")
	}
	if invoked {
		t.Fatalf("resolver should not be invoked for bad ip")
	}
}

func TestForbidden(t *testing.T) {
	m := memstore.New()
	svc := dnscfg.New(m)
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.Get(context.Background(), bad); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Get want ErrForbidden, got %v", err)
	}
	if _, _, err := svc.Test(context.Background(), bad, "1.2.3.4"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Test want ErrForbidden, got %v", err)
	}
}
