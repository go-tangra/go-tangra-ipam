package addresses

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// errProber fails every probe call.
type errProber struct{}

func (errProber) Ping(context.Context, string) (bool, int, error) {
	return false, 0, errors.New("ping boom")
}
func (errProber) Scan(context.Context, string, []int, time.Duration) ([]int, error) {
	return nil, errors.New("scan boom")
}

func TestForbiddenAllMethods(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	bad := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}

	if _, err := svc.Create(ctx, bad, store.IPAddress{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("create: %v", err)
	}
	if _, err := svc.Get(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("get: %v", err)
	}
	if _, err := svc.Update(ctx, bad, store.IPAddress{ID: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("update: %v", err)
	}
	if err := svc.Delete(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("delete: %v", err)
	}
	if _, err := svc.Find(ctx, bad, "10.0.0.1"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("find: %v", err)
	}
	if _, err := svc.AllocateNext(ctx, bad, "x", "", nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("alloc: %v", err)
	}
	if _, err := svc.BulkAllocate(ctx, bad, "x", 1, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("bulk: %v", err)
	}
	if _, err := svc.PingAddress(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("ping: %v", err)
	}
	if _, err := svc.SuggestAvailableAddresses(ctx, bad, "x", 1, nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("suggest: %v", err)
	}
}

func TestSetClockAndNilPublisher(t *testing.T) {
	st := memstore.New()
	svc := New(st, nil, 0, 0) // nil publisher must be a safe no-op
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	if _, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.8", SubnetID: sid}); err != nil {
		t.Fatalf("create with nil pub: %v", err)
	}
	// Delete also publishes; nil pub must not panic.
	a, _ := svc.Find(ctx, subj(), "10.0.0.8")
	if err := svc.Delete(ctx, subj(), a.ID); err != nil {
		t.Fatalf("delete with nil pub: %v", err)
	}
}

func TestStoreErrorsSurface(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	a, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.11", SubnetID: sid})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A non-sentinel store error passes through mapErr untouched.
	st.FailNext("UpdateAddress")
	if _, err := svc.Update(ctx, subj(), store.IPAddress{ID: a.ID, Hostname: "h"}); err == nil {
		t.Error("want update error")
	} else if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		t.Errorf("unexpected sentinel mapping: %v", err)
	}

	st.FailNext("DeleteAddress")
	if err := svc.Delete(ctx, subj(), a.ID); err == nil {
		t.Error("want delete error")
	}

	st.FailNext("CreateAddress")
	if _, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.12", SubnetID: sid}); err == nil {
		t.Error("want create error")
	}
}

func TestProbeErrorsSkipCandidate(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	svc.SetProbers(errProber{}, errProber{})
	// Every probe errors, so no candidate is verified quiet: result is empty.
	got, err := svc.SuggestAvailableAddresses(ctx, subj(), sid, 2, nil)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no suggestions when probes error, got %v", got)
	}

	// PingAddress must surface the prober error.
	a, _ := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.20", SubnetID: sid})
	if _, err := svc.PingAddress(ctx, subj(), a.ID); err == nil {
		t.Error("want ping error")
	}
}

func TestAllocateInvalidStartFrom(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "10.0.0.1")
	// An unparseable startFrom makes ipLess return false, so allocation proceeds
	// from the lowest free host.
	a, err := svc.AllocateNext(ctx, subj(), sid, "not-an-ip", nil)
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if a.Address != "10.0.0.2" {
		t.Errorf("alloc = %q, want 10.0.0.2", a.Address)
	}
}

func TestBulkPartialThenExhausted(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	// /29 usable .1-.6; skipFirst=1,skipLast=1 -> .2-.5 = 4 free. Asking for 6
	// returns the 4 it can and then ErrNoAvailable.
	sid := makeSubnet(t, st, "10.0.0.0/29", "")
	out, err := svc.BulkAllocate(ctx, subj(), sid, 6, "h")
	if err != ErrNoAvailable {
		t.Fatalf("want ErrNoAvailable, got %v", err)
	}
	if len(out) != 4 {
		t.Errorf("partial allocations = %d, want 4", len(out))
	}
}
