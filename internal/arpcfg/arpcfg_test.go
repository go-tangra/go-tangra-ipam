package arpcfg

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const dev = "0190f7c2-aaaa-7c1a-9b2e-00000000ab01"

func subj() authz.Subjects {
	return authz.Subjects{TenantID: "t1", UserID: "u1", ActorKind: authz.ActorUser}
}

func ptr[T any](v T) *T { return &v }

func TestUpdateAndGet(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	if err := m.CreateDevice(ctx, store.Device{ID: dev, TenantID: "t1", Name: "fw"}); err != nil {
		t.Fatal(err)
	}
	s := New(m)
	got, err := s.Get(ctx, subj())
	if err != nil || !got.Enabled || got.ProxyThreshold != 8 {
		t.Fatalf("defaults %+v %v", got, err)
	}
	got, err = s.Update(ctx, subj(), Input{Enabled: ptr(false), ExcludedDevices: []string{dev, dev}, ProxyThreshold: ptr(2)})
	if err != nil || got.Enabled || len(got.ExcludedDevices) != 1 || got.ProxyThreshold != 2 || got.UpdatedBy != "u1" {
		t.Fatalf("update %+v %v", got, err)
	}
	// A nil list clears the exclusions.
	if got, err = s.Update(ctx, subj(), Input{Enabled: ptr(true), ProxyThreshold: ptr(256)}); err != nil || len(got.ExcludedDevices) != 0 {
		t.Fatalf("clear %+v %v", got, err)
	}
}

func TestUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	s := New(m)
	var fe *FieldError
	for _, in := range []Input{
		{ProxyThreshold: ptr(8)},
		{Enabled: ptr(true)},
		{Enabled: ptr(true), ProxyThreshold: ptr(1)},
		{Enabled: ptr(true), ProxyThreshold: ptr(8), ExcludedDevices: make([]string, 257)},
		{Enabled: ptr(true), ProxyThreshold: ptr(8), ExcludedDevices: []string{"x"}},
		{Enabled: ptr(true), ProxyThreshold: ptr(8), ExcludedDevices: []string{dev}},
	} {
		if _, err := s.Update(ctx, subj(), in); !errors.As(err, &fe) || fe.Error() == "" {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if _, err := s.Get(ctx, authz.Subjects{}); err == nil {
		t.Fatal("no tenant")
	}
	if _, err := s.Update(ctx, authz.Subjects{}, Input{}); err == nil {
		t.Fatal("no tenant")
	}
	_ = m.CreateDevice(ctx, store.Device{ID: dev, TenantID: "t1", Name: "fw"})
	m.FailNext("PutARPSettings")
	if _, err := s.Update(ctx, subj(), Input{Enabled: ptr(true), ProxyThreshold: ptr(8)}); err == nil {
		t.Fatal("store failure hidden")
	}
}
