package addresses

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func subj() authz.Subjects {
	return authz.Subjects{TenantID: "t1", UserID: "u1", ActorKind: authz.ActorUser}
}

// capturePub records published events for assertions.
type capturePub struct{ events []string }

func (c *capturePub) Publish(_ context.Context, _ string, eventType string, _ any) {
	c.events = append(c.events, eventType)
}

// fakePinger answers alive for a fixed set of addresses.
type fakePinger struct{ alive map[string]bool }

func (f *fakePinger) Ping(_ context.Context, ip string) (bool, int, error) {
	return f.alive[ip], 3, nil
}

// fakeScanner reports open ports for a fixed set of addresses.
type fakeScanner struct{ open map[string][]int }

func (f *fakeScanner) Scan(_ context.Context, ip string, _ []int, _ time.Duration) ([]int, error) {
	return f.open[ip], nil
}

func newSvc(t *testing.T) (*Service, *memstore.Mem, *capturePub) {
	t.Helper()
	st := memstore.New()
	pub := &capturePub{}
	return New(st, pub, 1, 1), st, pub
}

// makeSubnet inserts a subnet directly and returns its id.
func makeSubnet(t *testing.T, st *memstore.Mem, cidr, gateway string) string {
	t.Helper()
	s := store.Subnet{TenantID: "t1", Name: "n-" + cidr, CIDR: cidr, Gateway: gateway}
	if err := st.CreateSubnet(context.Background(), s); err != nil {
		t.Fatalf("create subnet: %v", err)
	}
	subs, _ := st.ListSubnets(context.Background(), "t1", store.SubnetFilter{})
	for _, x := range subs {
		if x.CIDR == cidr {
			return x.ID
		}
	}
	t.Fatal("subnet not found")
	return ""
}

func TestCRUDAndEvents(t *testing.T) {
	svc, _, pub := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, memFromSvc(svc), "10.0.0.0/24", "10.0.0.1")

	a, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.10", SubnetID: sid})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.CreatedBy != "u1" {
		t.Errorf("created_by = %q", a.CreatedBy)
	}

	got, err := svc.Get(ctx, subj(), a.ID)
	if err != nil || got.Address != "10.0.0.10" {
		t.Fatalf("get: %v %q", err, got.Address)
	}

	if _, err := svc.Update(ctx, subj(), store.IPAddress{ID: a.ID, Hostname: "web1"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	upd, _ := svc.Get(ctx, subj(), a.ID)
	if upd.Hostname != "web1" {
		t.Errorf("hostname = %q", upd.Hostname)
	}
	if upd.Address != "10.0.0.10" {
		t.Errorf("address not preserved: %q", upd.Address)
	}

	found, err := svc.Find(ctx, subj(), "10.0.0.10")
	if err != nil || found.ID != a.ID {
		t.Fatalf("find: %v", err)
	}

	if err := svc.Delete(ctx, subj(), a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, subj(), a.ID); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	want := []string{events.IPAddressCreated, events.IPAddressUpdated, events.IPAddressDeleted}
	if len(pub.events) != 3 {
		t.Fatalf("events = %v", pub.events)
	}
	for i, w := range want {
		if pub.events[i] != w {
			t.Errorf("event[%d] = %q want %q", i, pub.events[i], w)
		}
	}
}

// memFromSvc is a tiny bridge so makeSubnet can share the service's store.
func memFromSvc(s *Service) *memstore.Mem { return s.st.(*memstore.Mem) }

func TestAllocateNextDistinct(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "10.0.0.1")

	a1, err := svc.AllocateNext(ctx, subj(), sid, "", nil)
	if err != nil {
		t.Fatalf("alloc1: %v", err)
	}
	a2, err := svc.AllocateNext(ctx, subj(), sid, "", nil)
	if err != nil {
		t.Fatalf("alloc2: %v", err)
	}
	if a1.Address == a2.Address {
		t.Fatalf("allocated same address twice: %s", a1.Address)
	}
	// skipFirst=1 removes .0 net and .1(gw skipped) => first usable should be .2.
	if a1.Address != "10.0.0.2" {
		t.Errorf("first alloc = %q, want 10.0.0.2", a1.Address)
	}
	if a1.Status != store.IPActive || a1.AddressType != store.AddrHost {
		t.Errorf("status/type = %q/%q", a1.Status, a1.AddressType)
	}
}

func TestAllocateNextStartFromAndSkip(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")

	a, err := svc.AllocateNext(ctx, subj(), sid, "10.0.0.50", []string{"10.0.0.50"})
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if a.Address != "10.0.0.51" {
		t.Errorf("alloc = %q, want 10.0.0.51 (startFrom .50 but .50 skipped)", a.Address)
	}
}

// conflictOnceStore returns repo.ErrConflict from the first CreateAddress call
// (simulating a lost allocation race) and delegates every call thereafter.
type conflictOnceStore struct {
	*memstore.Mem
	tripped bool
}

func (c *conflictOnceStore) CreateAddress(ctx context.Context, a store.IPAddress) error {
	if !c.tripped {
		c.tripped = true
		return repo.ErrConflict
	}
	return c.Mem.CreateAddress(ctx, a)
}

func TestAllocateConflictRetry(t *testing.T) {
	mem := memstore.New()
	cstore := &conflictOnceStore{Mem: mem}
	svc := New(cstore, &capturePub{}, 1, 1)
	ctx := context.Background()
	sid := makeSubnet(t, mem, "10.0.0.0/24", "10.0.0.1")

	// First candidate is .2; the store rejects the first insert as a race, so the
	// allocator widens its exclude set and takes the next free address, .3.
	a, err := svc.AllocateNext(ctx, subj(), sid, "", nil)
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if a.Address != "10.0.0.3" {
		t.Fatalf("alloc after conflict = %q, want 10.0.0.3", a.Address)
	}
}

func TestAllocateExhausted(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	// /30 has hosts .1 and .2; skipFirst=1 and skipLast=1 remove both -> no free.
	sid := makeSubnet(t, st, "10.0.0.0/30", "")
	_, err := svc.AllocateNext(ctx, subj(), sid, "", nil)
	if err != ErrNoAvailable {
		t.Fatalf("want ErrNoAvailable, got %v", err)
	}
}

func TestBulkAllocate(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "10.0.0.1")

	out, err := svc.BulkAllocate(ctx, subj(), sid, 3, "host")
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d addresses", len(out))
	}
	seen := map[string]bool{}
	for i, a := range out {
		if seen[a.Address] {
			t.Fatalf("duplicate %s", a.Address)
		}
		seen[a.Address] = true
		if a.Hostname != "host"+strconv.Itoa(i) {
			t.Errorf("hostname[%d] = %q", i, a.Hostname)
		}
	}
	if _, err := svc.BulkAllocate(ctx, subj(), sid, 0, ""); err != nil {
		t.Errorf("bulk zero: %v", err)
	}
}

func TestPingAddress(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	a, _ := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.9", SubnetID: sid})

	// No prober: clean not-available result.
	res, err := svc.PingAddress(ctx, subj(), a.ID)
	if err != nil {
		t.Fatalf("ping (no prober): %v", err)
	}
	if res.Available {
		t.Errorf("want not available")
	}
	if res.Address != "10.0.0.9" {
		t.Errorf("address = %q", res.Address)
	}

	// With prober.
	svc.SetProbers(&fakePinger{alive: map[string]bool{"10.0.0.9": true}}, nil)
	res, err = svc.PingAddress(ctx, subj(), a.ID)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !res.Available || !res.Alive || res.RTTMs != 3 {
		t.Errorf("res = %+v", res)
	}
}

func TestSuggestAvailable(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "10.0.0.1")

	// DB-free path (no probers): first free candidates in order.
	got, err := svc.SuggestAvailableAddresses(ctx, subj(), sid, 2, nil)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 2 || got[0] != "10.0.0.2" {
		t.Fatalf("suggest db-free = %v", got)
	}

	// With probers: .2 alive, .3 has an open port -> both dropped; .4 suggested.
	svc.SetProbers(
		&fakePinger{alive: map[string]bool{"10.0.0.2": true}},
		&fakeScanner{open: map[string][]int{"10.0.0.3": {80}}},
	)
	got, err = svc.SuggestAvailableAddresses(ctx, subj(), sid, 1, nil)
	if err != nil {
		t.Fatalf("suggest probed: %v", err)
	}
	if len(got) != 1 || got[0] != "10.0.0.4" {
		t.Fatalf("suggest probed = %v", got)
	}

	if got, err := svc.SuggestAvailableAddresses(ctx, subj(), sid, 0, nil); err != nil || got != nil {
		t.Errorf("suggest zero = %v %v", got, err)
	}
}

func TestListRedactsOwner(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	// Insert an address carrying a sealed owner directly in the store.
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.7", SubnetID: sid, Owner: "secret-owner@example.com"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	list, err := svc.List(ctx, subj(), store.AddressFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, a := range list {
		if a.Owner != "" {
			t.Errorf("owner leaked in struct: %q", a.Owner)
		}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "secret-owner") {
		t.Errorf("owner leaked in JSON: %s", raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "\"owner\"") {
		t.Errorf("owner key present in JSON: %s", raw)
	}
}

func TestNotFoundPaths(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Get(ctx, subj(), "nope"); err != ErrNotFound {
		t.Errorf("get: %v", err)
	}
	if _, err := svc.Find(ctx, subj(), "10.9.9.9"); err != ErrNotFound {
		t.Errorf("find: %v", err)
	}
	if err := svc.Delete(ctx, subj(), "nope"); err != ErrNotFound {
		t.Errorf("delete: %v", err)
	}
	if _, err := svc.AllocateNext(ctx, subj(), "nope", "", nil); err != ErrNotFound {
		t.Errorf("alloc: %v", err)
	}
	if _, err := svc.SuggestAvailableAddresses(ctx, subj(), "nope", 1, nil); err != ErrNotFound {
		t.Errorf("suggest: %v", err)
	}
}

func TestDuplicateCreateConflict(t *testing.T) {
	svc, st, _ := newSvc(t)
	ctx := context.Background()
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	if _, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.5", SubnetID: sid}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := svc.Create(ctx, subj(), store.IPAddress{Address: "10.0.0.5", SubnetID: sid})
	if err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	// Ensure mapping is anchored to the repo sentinel too.
	if !isConflict(err) {
		t.Errorf("mapErr should surface conflict sentinel")
	}
}

func isConflict(err error) bool { return err == ErrConflict || err == repo.ErrConflict }

func TestForbidden(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	bad := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}
	if _, err := svc.List(ctx, bad, store.AddressFilter{}); err == nil {
		t.Error("want forbidden")
	}
}
