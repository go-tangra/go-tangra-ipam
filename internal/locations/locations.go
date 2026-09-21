// Package locations is the IPAM location service: CRUD over the tenant's
// physical-place tree (region → country → … → rack) plus a nested tree
// projection with rollup counts. Authorization is coarse (the caller is scoped
// to its tenant); the store enforces per-tenant RLS and the unique
// (tenant,name)/(tenant,code) constraints. Deleting a location that still has
// children (or bound devices/subnets/vlans) requires force, else the store
// returns repo.ErrNotEmpty. When a location's parent changes, its materialised
// path is recomputed from the new ancestor chain.
package locations

import (
	"context"
	"strings"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// ValidationError is returned when a caller-supplied field fails validation.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return "locations: " + e.Msg }

// Service manages locations.
type Service struct {
	st repo.Store
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st} }

// Node is a location plus its children — the nested projection GetTree returns.
type Node struct {
	store.Location
	Children []*Node `json:"children,omitempty"`
}

func validate(l store.Location) error {
	if l.Name == "" {
		return ValidationError{Msg: "name required"}
	}
	if l.ParentID != "" && l.ParentID == l.ID {
		return ValidationError{Msg: "location cannot be its own parent"}
	}
	return nil
}

// Create validates and inserts a location. The parent (if any) is validated to
// exist in the tenant, and the materialised path is computed from it.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, l store.Location) (store.Location, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Location{}, err
	}
	l.TenantID = subj.TenantID
	if err := validate(l); err != nil {
		return store.Location{}, err
	}
	if l.ID == "" {
		l.ID = store.NewID()
	}
	if l.Status == "" {
		l.Status = store.LocStActive
	}
	if l.CreatedBy == "" {
		l.CreatedBy = subj.ActorID()
	}
	path, err := s.computePath(ctx, subj.TenantID, l.ParentID, l.Name)
	if err != nil {
		return store.Location{}, err
	}
	l.Path = path
	if err := s.st.CreateLocation(ctx, l); err != nil {
		return store.Location{}, err
	}
	return s.st.GetLocation(ctx, subj.TenantID, l.ID)
}

// Get returns one location in the caller's tenant.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.Location, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Location{}, err
	}
	return s.st.GetLocation(ctx, subj.TenantID, id)
}

// List returns the caller's locations matching f.
func (s *Service) List(ctx context.Context, subj authz.Subjects, f store.LocationFilter) ([]store.Location, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListLocations(ctx, subj.TenantID, f)
}

// Update validates and replaces a location. If the parent changed, the path is
// recomputed from the new ancestor chain; a parent that would create a cycle is
// rejected with a ValidationError.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, l store.Location) (store.Location, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Location{}, err
	}
	l.TenantID = subj.TenantID
	if err := validate(l); err != nil {
		return store.Location{}, err
	}
	ex, err := s.st.GetLocation(ctx, subj.TenantID, l.ID)
	if err != nil {
		return store.Location{}, err
	}
	l.CreatedBy = ex.CreatedBy
	if l.Status == "" {
		l.Status = ex.Status
	}
	if l.ParentID != ex.ParentID || l.Name != ex.Name {
		if err := s.guardCycle(ctx, subj.TenantID, l.ID, l.ParentID); err != nil {
			return store.Location{}, err
		}
		path, err := s.computePath(ctx, subj.TenantID, l.ParentID, l.Name)
		if err != nil {
			return store.Location{}, err
		}
		l.Path = path
	} else {
		l.Path = ex.Path
	}
	if err := s.st.UpdateLocation(ctx, l); err != nil {
		return store.Location{}, err
	}
	return s.st.GetLocation(ctx, subj.TenantID, l.ID)
}

// Delete removes a location. Children (and bound devices/subnets/vlans) block a
// non-forced delete with repo.ErrNotEmpty.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string, force bool) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	if !force {
		l, err := s.st.GetLocation(ctx, subj.TenantID, id)
		if err != nil {
			return err
		}
		if l.ChildCount > 0 {
			return repo.ErrNotEmpty
		}
	}
	return s.st.DeleteLocation(ctx, subj.TenantID, id, force)
}

// GetTree returns the caller's locations as a forest of Nodes nested by
// parent_id. Roots are the locations whose parent is empty or points outside the
// returned set. Each node carries the store's rollup counts (children, devices,
// subnets, vlans). Ordering within a level is by id (uuid v7 → time order).
func (s *Service) GetTree(ctx context.Context, subj authz.Subjects) ([]*Node, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	// Page through all locations for the tenant.
	var all []store.Location
	f := store.LocationFilter{Limit: 500}
	for {
		page, err := s.st.ListLocations(ctx, subj.TenantID, f)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < f.Limit {
			break
		}
		f.CursorID = page[len(page)-1].ID
	}

	nodes := make(map[string]*Node, len(all))
	present := make(map[string]bool, len(all))
	for _, l := range all {
		nodes[l.ID] = &Node{Location: l}
		present[l.ID] = true
	}
	var roots []*Node
	for _, l := range all {
		n := nodes[l.ID]
		if l.ParentID != "" && present[l.ParentID] {
			p := nodes[l.ParentID]
			p.Children = append(p.Children, n)
			continue
		}
		roots = append(roots, n)
	}
	sortNodes(roots)
	for _, n := range nodes {
		sortNodes(n.Children)
	}
	return roots, nil
}

func sortNodes(ns []*Node) {
	for i := 1; i < len(ns); i++ {
		for j := i; j > 0 && ns[j-1].ID > ns[j].ID; j-- {
			ns[j-1], ns[j] = ns[j], ns[j-1]
		}
	}
}

// computePath builds the materialised path "/ancestor/.../name" by walking the
// parent chain. A missing parent is a ValidationError.
func (s *Service) computePath(ctx context.Context, tenantID, parentID, name string) (string, error) {
	if parentID == "" {
		return "/" + name, nil
	}
	parent, err := s.st.GetLocation(ctx, tenantID, parentID)
	if err != nil {
		if err == repo.ErrNotFound {
			return "", ValidationError{Msg: "parent location not found"}
		}
		return "", err
	}
	base := parent.Path
	if base == "" {
		base = "/" + parent.Name
	}
	return strings.TrimRight(base, "/") + "/" + name, nil
}

// guardCycle rejects a parent that is the node itself or one of its descendants.
func (s *Service) guardCycle(ctx context.Context, tenantID, id, parentID string) error {
	cur := parentID
	for i := 0; cur != "" && i < 256; i++ {
		if cur == id {
			return ValidationError{Msg: "parent change would create a cycle"}
		}
		p, err := s.st.GetLocation(ctx, tenantID, cur)
		if err != nil {
			if err == repo.ErrNotFound {
				return nil
			}
			return err
		}
		cur = p.ParentID
	}
	return nil
}
