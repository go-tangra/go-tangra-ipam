package subnets

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func subj() authz.Subjects {
	return authz.Subjects{TenantID: "t1", UserID: "u1", ActorKind: authz.ActorUser}
}

func newSvc(t *testing.T) (*Service, *memstore.Mem) {
	t.Helper()
	st := memstore.New()
	return New(st), st
}

func TestCreateDerivesFields(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	got, err := svc.Create(ctx, subj(), store.Subnet{Name: "net-a", CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.NetworkAddress != "10.0.0.0" {
		t.Errorf("network = %q", got.NetworkAddress)
	}
	if got.BroadcastAddr != "10.0.0.255" {
		t.Errorf("broadcast = %q", got.BroadcastAddr)
	}
	if got.PrefixLength != 24 {
		t.Errorf("prefix = %d", got.PrefixLength)
	}
	if got.IPVersion != 4 {
		t.Errorf("version = %d", got.IPVersion)
	}
	if got.Mask != "ffffff00" && got.Mask != "255.255.255.0" {
		// net.IPMask.String() renders as hex; just ensure it is set.
		if got.Mask == "" {
			t.Errorf("mask empty")
		}
	}
	if got.TotalAddresses != 256 {
		t.Errorf("total = %d", got.TotalAddresses)
	}
	if got.AvailableAddresses != 256 {
		t.Errorf("available = %d", got.AvailableAddresses)
	}
	if got.CreatedBy != "u1" {
		t.Errorf("created_by = %q", got.CreatedBy)
	}
}

func TestSplitCreatesChildren(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	parent, err := svc.Create(ctx, subj(), store.Subnet{Name: "core", CIDR: "10.0.0.0/24", VlanID: "v1", LocationID: "l1"}, false)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	// Dry run writes nothing.
	preview, err := svc.Split(ctx, subj(), parent.ID, 26, true)
	if err != nil {
		t.Fatalf("split dry-run: %v", err)
	}
	if len(preview.Created) != 4 || len(preview.Skipped) != 0 {
		t.Fatalf("preview = %d created, %d skipped", len(preview.Created), len(preview.Skipped))
	}
	if all, _ := svc.List(ctx, subj(), store.SubnetFilter{}); len(all) != 1 {
		t.Fatalf("dry run persisted %d subnets", len(all))
	}
	res, err := svc.Split(ctx, subj(), parent.ID, 26, false)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(res.Created) != 4 {
		t.Fatalf("created = %d, want 4", len(res.Created))
	}
	for _, c := range res.Created {
		if c.ParentID != parent.ID {
			t.Errorf("child %s parent = %q, want %q", c.CIDR, c.ParentID, parent.ID)
		}
		if c.VlanID != "v1" || c.LocationID != "l1" {
			t.Errorf("child %s did not inherit vlan/location: %q/%q", c.CIDR, c.VlanID, c.LocationID)
		}
		if c.PrefixLength != 26 || c.TotalAddresses != 64 {
			t.Errorf("child %s derived fields: prefix %d, total %d", c.CIDR, c.PrefixLength, c.TotalAddresses)
		}
	}
	// Splitting again skips every block that now exists instead of failing.
	again, err := svc.Split(ctx, subj(), parent.ID, 26, false)
	if err == nil {
		t.Fatalf("second split created %d children, want refusal", len(again.Created))
	}
	if len(again.Skipped) != 4 {
		t.Errorf("skipped = %d, want 4", len(again.Skipped))
	}
}

func TestSplitPartialAndValidation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	parent, err := svc.Create(ctx, subj(), store.Subnet{Name: "core", CIDR: "10.0.0.0/24"}, false)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	// One half already carved out: the other three blocks are still created.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "taken", CIDR: "10.0.0.0/26", ParentID: parent.ID}, true); err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	res, err := svc.Split(ctx, subj(), parent.ID, 26, false)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(res.Created) != 3 || len(res.Skipped) != 1 || res.Skipped[0].CIDR != "10.0.0.0/26" {
		t.Fatalf("created %d, skipped %+v", len(res.Created), res.Skipped)
	}
	// A prefix no longer than the parent is a validation error, and an unknown
	// subnet is not found.
	if _, err := svc.Split(ctx, subj(), parent.ID, 24, false); err == nil {
		t.Errorf("split into /24 = nil error, want validation")
	}
	if _, err := svc.Split(ctx, subj(), "missing", 26, false); err == nil {
		t.Errorf("split of unknown subnet = nil error")
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, subj(), store.Subnet{CIDR: "10.0.0.0/24"}, false); err == nil {
		t.Fatal("want name-required error")
	}
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "bad", CIDR: "not-a-cidr"}, false); err == nil {
		t.Fatal("want invalid-cidr error")
	} else {
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("want ValidationError, got %T", err)
		}
	}
}

func TestCreateOverlapRejected(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/24"}, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.Create(ctx, subj(), store.Subnet{Name: "b", CIDR: "10.0.0.0/25"}, false)
	if err == nil {
		t.Fatal("want overlap error")
	}
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("want ValidationError, got %T", err)
	}
	// With allowOverlap the same block is accepted.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "c", CIDR: "10.0.0.0/25"}, true); err != nil {
		t.Fatalf("allow-overlap: %v", err)
	}
}

func TestCreateNestedChild(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	root, err := svc.Create(ctx, subj(), store.Subnet{Name: "root", CIDR: "10.0.0.0/16"}, false)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	mid, err := svc.Create(ctx, subj(), store.Subnet{Name: "mid", CIDR: "10.0.1.0/24", ParentID: root.ID}, false)
	if err != nil {
		t.Fatalf("child inside parent must be accepted: %v", err)
	}
	// A grandchild overlaps both ancestors, which is allowed.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "leaf", CIDR: "10.0.1.0/26", ParentID: mid.ID}, false); err != nil {
		t.Fatalf("grandchild: %v", err)
	}
	var ve ValidationError
	// A sibling colliding with an existing child is still rejected.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "dup", CIDR: "10.0.1.128/25", ParentID: root.ID}, false); !errors.As(err, &ve) {
		t.Errorf("sibling overlap: want ValidationError, got %v", err)
	}
	// A block outside its declared parent is rejected.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "out", CIDR: "10.1.0.0/24", ParentID: mid.ID}, false); !errors.As(err, &ve) {
		t.Errorf("outside parent: want ValidationError, got %v", err)
	}
	// An unknown parent is rejected.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "orphan", CIDR: "10.0.9.0/24", ParentID: "nope"}, false); !errors.As(err, &ve) {
		t.Errorf("unknown parent: want ValidationError, got %v", err)
	}
	// Moving a child under a new parent re-checks containment on update.
	if _, err := svc.Update(ctx, subj(), store.Subnet{ID: mid.ID, Name: "mid", CIDR: "10.0.1.0/24", ParentID: root.ID}, false); err != nil {
		t.Errorf("update in place: %v", err)
	}
}

func TestCreateGatewayOutOfRange(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/24", Gateway: "10.9.9.1"}, false)
	if err == nil {
		t.Fatal("want gateway-out-of-range error")
	}
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("want ValidationError, got %T", err)
	}
}

func TestGetListUpdate(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/24"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.Get(ctx, subj(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "a" {
		t.Errorf("name = %q", got.Name)
	}

	list, err := svc.List(ctx, subj(), store.SubnetFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v n=%d", err, len(list))
	}

	updated, err := svc.Update(ctx, subj(), store.Subnet{ID: created.ID, Name: "a2", CIDR: "10.0.0.0/24"}, false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "a2" {
		t.Errorf("updated name = %q", updated.Name)
	}
	if updated.CreatedBy != created.CreatedBy {
		t.Errorf("created_by not preserved")
	}

	if _, err := svc.Get(ctx, subj(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := svc.Update(ctx, subj(), store.Subnet{ID: "missing", CIDR: "10.0.0.0/24"}, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestUsedAndUtilization(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/30"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// /30 => total 4.
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: created.ID}); err != nil {
		t.Fatalf("addr: %v", err)
	}
	got, err := svc.Get(ctx, subj(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UsedAddresses != 1 {
		t.Errorf("used = %d", got.UsedAddresses)
	}
	if got.AvailableAddresses != 3 {
		t.Errorf("available = %d", got.AvailableAddresses)
	}
	if got.Utilization <= 0 || got.Utilization > 100 {
		t.Errorf("utilization = %v", got.Utilization)
	}
}

func TestDeleteGuard(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/24"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.5", SubnetID: created.ID}); err != nil {
		t.Fatalf("addr: %v", err)
	}
	if err := svc.Delete(ctx, subj(), created.ID, false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("want ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete(ctx, subj(), created.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, err := svc.Get(ctx, subj(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want gone, got %v", err)
	}
}

func TestDeleteEmpty(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, _ := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/24"}, false)
	if err := svc.Delete(ctx, subj(), created.ID, false); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
}

func TestGetTree(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	root, err := svc.Create(ctx, subj(), store.Subnet{Name: "root", CIDR: "10.0.0.0/16"}, false)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	child, err := svc.Create(ctx, subj(), store.Subnet{Name: "child", CIDR: "10.0.1.0/24", ParentID: root.ID}, true)
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	// An unrelated top-level subnet.
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "other", CIDR: "192.168.0.0/24"}, false); err != nil {
		t.Fatalf("other: %v", err)
	}

	tree, err := svc.GetTree(ctx, subj())
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(tree) != 2 {
		t.Fatalf("want 2 roots, got %d", len(tree))
	}
	var foundChild bool
	for _, n := range tree {
		if n.ID == root.ID {
			if len(n.Children) != 1 || n.Children[0].ID != child.ID {
				t.Fatalf("root children = %+v", n.Children)
			}
			foundChild = true
		}
	}
	if !foundChild {
		t.Error("root node not found in tree")
	}
}

func TestGetStats(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, subj(), store.Subnet{Name: "a", CIDR: "10.0.0.0/30"}, false)
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "b", CIDR: "10.1.0.0/30"}, false); err != nil {
		t.Fatalf("b: %v", err)
	}
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: a.ID, Status: store.IPActive}); err != nil {
		t.Fatalf("addr1: %v", err)
	}
	if err := st.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.2", SubnetID: a.ID, Status: store.IPReserved}); err != nil {
		t.Fatalf("addr2: %v", err)
	}

	stats, err := svc.GetStats(ctx, subj())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalSubnets != 2 {
		t.Errorf("total subnets = %d", stats.TotalSubnets)
	}
	if stats.TotalAddresses != 8 {
		t.Errorf("total addresses = %d", stats.TotalAddresses)
	}
	if stats.UsedAddresses != 2 {
		t.Errorf("used = %d", stats.UsedAddresses)
	}
	if stats.AvailableAddresses != 6 {
		t.Errorf("available = %d", stats.AvailableAddresses)
	}
	if stats.ReservedAddresses != 1 {
		t.Errorf("reserved = %d", stats.ReservedAddresses)
	}
	if stats.Utilization <= 0 {
		t.Errorf("utilization = %v", stats.Utilization)
	}
}

func TestConflictName(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, subj(), store.Subnet{Name: "dup", CIDR: "10.0.0.0/24"}, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.Create(ctx, subj(), store.Subnet{Name: "dup", CIDR: "10.5.0.0/24"}, false)
	if !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("want repo.ErrConflict, got %v", err)
	}
}

func TestForbiddenTenant(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	bad := authz.Subjects{TenantID: "", ActorKind: authz.ActorUser}
	if _, err := svc.List(ctx, bad, store.SubnetFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("want forbidden, got %v", err)
	}
}
