package locations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

func subj(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, UserID: "op", Roles: []string{"admin"}, ActorKind: authz.ActorUser}
}

func TestCRUDAndPath(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	root, err := svc.Create(ctx, s, store.Location{Name: "dc1", Code: "DC1", LocationType: store.LocDatacenter})
	if err != nil {
		t.Fatalf("Create root: %v", err)
	}
	if root.Path != "/dc1" || root.CreatedBy != "op" {
		t.Fatalf("root path/createdby: %+v", root)
	}
	child, err := svc.Create(ctx, s, store.Location{Name: "rack1", Code: "R1", LocationType: store.LocRack, ParentID: root.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.Path != "/dc1/rack1" {
		t.Fatalf("child path = %q", child.Path)
	}

	got, err := svc.Get(ctx, s, root.ID)
	if err != nil || got.ChildCount != 1 {
		t.Fatalf("Get root: %v childcount=%d", err, got.ChildCount)
	}

	list, err := svc.List(ctx, s, store.LocationFilter{})
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v n=%d", err, len(list))
	}
}

func TestUpdateReparentRecomputesPath(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	a, _ := svc.Create(ctx, s, store.Location{Name: "a", LocationType: store.LocSite})
	b, _ := svc.Create(ctx, s, store.Location{Name: "b", LocationType: store.LocSite})
	leaf, _ := svc.Create(ctx, s, store.Location{Name: "leaf", LocationType: store.LocRack, ParentID: a.ID})
	if leaf.Path != "/a/leaf" {
		t.Fatalf("initial path = %q", leaf.Path)
	}
	leaf.ParentID = b.ID
	up, err := svc.Update(ctx, s, leaf)
	if err != nil {
		t.Fatalf("Update reparent: %v", err)
	}
	if up.Path != "/b/leaf" {
		t.Fatalf("reparented path = %q", up.Path)
	}
}

func TestUpdateCycleRejected(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	a, _ := svc.Create(ctx, s, store.Location{Name: "a", LocationType: store.LocSite})
	b, _ := svc.Create(ctx, s, store.Location{Name: "b", LocationType: store.LocSite, ParentID: a.ID})
	// Make a a child of b -> cycle.
	a.ParentID = b.ID
	_, err := svc.Update(ctx, s, a)
	var ve locations.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError for cycle, got %v", err)
	}
}

func TestDeleteGuard(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	root, _ := svc.Create(ctx, s, store.Location{Name: "root", LocationType: store.LocRegion})
	_, _ = svc.Create(ctx, s, store.Location{Name: "kid", LocationType: store.LocCity, ParentID: root.ID})

	if err := svc.Delete(ctx, s, root.ID, false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete with children: want ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete(ctx, s, root.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
}

func TestConflictAndValidation(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	if _, err := svc.Create(ctx, s, store.Location{Name: "x", Code: "X", LocationType: store.LocSite}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Location{Name: "x", LocationType: store.LocSite}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup name: want ErrConflict, got %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Location{Name: "y", Code: "X", LocationType: store.LocSite}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup code: want ErrConflict, got %v", err)
	}
	if _, err := svc.Create(ctx, s, store.Location{LocationType: store.LocSite}); err == nil {
		t.Fatalf("empty name: want error")
	}
	// missing parent
	if _, err := svc.Create(ctx, s, store.Location{Name: "z", LocationType: store.LocSite, ParentID: "nope"}); err == nil {
		t.Fatalf("missing parent: want error")
	}
}

func TestGetTree(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	region, _ := svc.Create(ctx, s, store.Location{Name: "eu", LocationType: store.LocRegion})
	dc, _ := svc.Create(ctx, s, store.Location{Name: "dc", LocationType: store.LocDatacenter, ParentID: region.ID})
	_, _ = svc.Create(ctx, s, store.Location{Name: "r1", LocationType: store.LocRack, ParentID: dc.ID})
	_, _ = svc.Create(ctx, s, store.Location{Name: "r2", LocationType: store.LocRack, ParentID: dc.ID})
	// a second root
	_, _ = svc.Create(ctx, s, store.Location{Name: "us", LocationType: store.LocRegion})

	tree, err := svc.GetTree(ctx, s)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	if len(tree) != 2 {
		t.Fatalf("roots = %d, want 2", len(tree))
	}
	// find eu
	var eu *locations.Node
	for _, n := range tree {
		if n.Name == "eu" {
			eu = n
		}
	}
	if eu == nil || len(eu.Children) != 1 {
		t.Fatalf("eu subtree wrong: %+v", eu)
	}
	if eu.Children[0].Name != "dc" || len(eu.Children[0].Children) != 2 {
		t.Fatalf("dc children wrong: %+v", eu.Children[0])
	}
}

func TestForbidden(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.GetTree(ctx, bad); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("GetTree want ErrForbidden, got %v", err)
	}
	if _, err := svc.Create(ctx, bad, store.Location{Name: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Create want ErrForbidden, got %v", err)
	}
}

func TestErrorString(t *testing.T) {
	if (locations.ValidationError{Msg: "x"}).Error() != "locations: x" {
		t.Fatalf("Error() wrong")
	}
}

func TestForbiddenSweep(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.Get(ctx, bad, "x"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := svc.List(ctx, bad, store.LocationFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("List: %v", err)
	}
	if _, err := svc.Update(ctx, bad, store.Location{Name: "n"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Update: %v", err)
	}
	if err := svc.Delete(ctx, bad, "x", false); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Delete: %v", err)
	}
}

func TestUpdateNotFoundAndSelfParent(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	if _, err := svc.Update(ctx, s, store.Location{ID: "missing", Name: "n"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Update missing: %v", err)
	}
	// self-parent rejected by validate
	l, _ := svc.Create(ctx, s, store.Location{Name: "a", LocationType: store.LocSite})
	l.ParentID = l.ID
	if _, err := svc.Update(ctx, s, l); err == nil {
		t.Fatalf("self-parent: want error")
	}
	// delete non-forced on childless leaf works
	if err := svc.Delete(ctx, s, l.ID, false); err != nil {
		t.Fatalf("delete leaf: %v", err)
	}
}

func TestUpdateRenameRecomputesPath(t *testing.T) {
	m := memstore.New()
	svc := locations.New(m)
	ctx := context.Background()
	s := subj(store.NewID())
	l, _ := svc.Create(ctx, s, store.Location{Name: "old", LocationType: store.LocSite})
	l.Name = "new"
	up, err := svc.Update(ctx, s, l)
	if err != nil || up.Path != "/new" {
		t.Fatalf("rename path: %v %q", err, up.Path)
	}
	// no-op update keeps path
	up2, _ := svc.Update(ctx, s, up)
	if up2.Path != "/new" {
		t.Fatalf("noop path changed: %q", up2.Path)
	}
}
