// Package vlans is the IPAM VLAN service: CRUD over the tenant's 802.1Q VLANs
// plus the list of subnets bound to a VLAN. Authorization is coarse (the caller
// is scoped to its tenant); the concrete store enforces per-tenant RLS and the
// unique (tenant,vlan_id)/(tenant,name) constraints. A VLAN id outside the valid
// 1..4094 range is rejected with a ValidationError before the store is touched;
// a duplicate surfaces as repo.ErrConflict; deleting a VLAN that still has
// subnets bound to it requires force, else repo.ErrNotEmpty.
package vlans

import (
	"context"
	"fmt"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// MinVlanID and MaxVlanID bound the usable 802.1Q VLAN id space (0 and 4095 are
// reserved).
const (
	MinVlanID = 1
	MaxVlanID = 4094
)

// ValidationError is returned when a caller-supplied field fails validation. It
// is distinct from repo.ErrConflict (a uniqueness clash) and repo.ErrNotEmpty (a
// delete guard) so callers can map it to a 400.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return "vlans: " + e.Msg }

// Service manages VLANs.
type Service struct {
	st repo.Store
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st} }

// validate checks the VLAN id range and required fields.
func validate(v store.Vlan) error {
	if v.VlanID < MinVlanID || v.VlanID > MaxVlanID {
		return ValidationError{Msg: fmt.Sprintf("vlan_id %d out of range (%d..%d)", v.VlanID, MinVlanID, MaxVlanID)}
	}
	if v.Name == "" {
		return ValidationError{Msg: "name required"}
	}
	return nil
}

// Create validates and inserts a VLAN in the caller's tenant. The tenant is
// taken from the subject; the id is assigned here so the created row can be
// returned with its computed subnet count.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, v store.Vlan) (store.Vlan, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Vlan{}, err
	}
	v.TenantID = subj.TenantID
	if err := validate(v); err != nil {
		return store.Vlan{}, err
	}
	if v.ID == "" {
		v.ID = store.NewID()
	}
	if v.Status == "" {
		v.Status = store.VlanActive
	}
	if v.CreatedBy == "" {
		v.CreatedBy = subj.ActorID()
	}
	if err := s.st.CreateVlan(ctx, v); err != nil {
		return store.Vlan{}, err
	}
	return s.st.GetVlan(ctx, subj.TenantID, v.ID)
}

// Get returns one VLAN in the caller's tenant.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.Vlan, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Vlan{}, err
	}
	return s.st.GetVlan(ctx, subj.TenantID, id)
}

// List returns the caller's VLANs matching f.
func (s *Service) List(ctx context.Context, subj authz.Subjects, f store.VlanFilter) ([]store.Vlan, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListVlans(ctx, subj.TenantID, f)
}

// Update validates and replaces a VLAN. The id and tenant are pinned from the
// stored row / subject; the created-by is preserved.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, v store.Vlan) (store.Vlan, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Vlan{}, err
	}
	v.TenantID = subj.TenantID
	if err := validate(v); err != nil {
		return store.Vlan{}, err
	}
	ex, err := s.st.GetVlan(ctx, subj.TenantID, v.ID)
	if err != nil {
		return store.Vlan{}, err
	}
	v.CreatedBy = ex.CreatedBy
	if v.Status == "" {
		v.Status = ex.Status
	}
	if err := s.st.UpdateVlan(ctx, v); err != nil {
		return store.Vlan{}, err
	}
	return s.st.GetVlan(ctx, subj.TenantID, v.ID)
}

// Delete removes a VLAN. If subnets are still bound to it, force is required;
// otherwise the store returns repo.ErrNotEmpty. The subnet guard is checked here
// too so the guard is explicit and consistent regardless of store behaviour.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string, force bool) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	if !force {
		subs, err := s.st.SubnetsForVlan(ctx, subj.TenantID, id)
		if err != nil {
			return err
		}
		if len(subs) > 0 {
			return repo.ErrNotEmpty
		}
	}
	return s.st.DeleteVlan(ctx, subj.TenantID, id, force)
}

// GetSubnets returns the subnets bound to a VLAN in the caller's tenant.
func (s *Service) GetSubnets(ctx context.Context, subj authz.Subjects, id string) ([]store.Subnet, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.SubnetsForVlan(ctx, subj.TenantID, id)
}
