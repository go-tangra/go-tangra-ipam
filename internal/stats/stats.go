// Package stats computes IPAM statistics rollups. A tenant caller gets its own
// rollup (subnet/address utilization, vlan/device/location counts, devices by
// type); an administrator can request the system-wide breakdown keyed by tenant.
// Authorization is coarse: the tenant rollup needs only tenant scope, the system
// rollup needs admin (or the system scope).
package stats

import (
	"context"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
)

// Service computes statistics.
type Service struct {
	st repo.Store
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st} }

// Tenant returns the caller's tenant statistics.
func (s *Service) Tenant(ctx context.Context, subj authz.Subjects) (repo.Stats, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return repo.Stats{}, err
	}
	return s.st.TenantStats(ctx, subj.TenantID)
}

// System returns per-tenant statistics keyed by tenant id. Admin only:
// non-admin callers get authz.ErrForbidden.
func (s *Service) System(ctx context.Context, subj authz.Subjects) (map[string]repo.Stats, error) {
	if err := authz.RequireAdmin(subj); err != nil {
		return nil, err
	}
	ids, err := s.st.TenantIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]repo.Stats, len(ids))
	for _, id := range ids {
		st, err := s.st.TenantStats(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, nil
}
