// Package subnets is the IPAM subnet service: CRUD over a tenant's CIDR blocks,
// plus the derived-field, overlap and gateway validation that must hold before a
// subnet is stored, and the read-side rollups (per-subnet utilization, the
// parent/child tree and the tenant capacity stats).
//
// On write the service parses the CIDR through the pure ipnet library and stamps
// the network/broadcast/mask/prefix/version fields from it, refuses a block that
// overlaps an existing subnet (unless the caller opts into overlap) and refuses a
// gateway outside the block. A bad field surfaces as ValidationError; a duplicate
// name surfaces as repo.ErrConflict; deleting a subnet that still holds addresses
// requires force, else ErrNotEmpty. Authorization is coarse (the caller is scoped
// to its tenant); the concrete store enforces per-tenant RLS.
package subnets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/ipnet"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// Sentinel errors. ErrNotFound and ErrNotEmpty mask the store's equivalents so
// callers depend on this package, not repo. ValidationError is distinct so the
// transport can map a bad field to a 400.
var (
	ErrNotFound = errors.New("subnets: not found")
	ErrNotEmpty = errors.New("subnets: not empty")
)

// ValidationError is returned when a caller-supplied field fails validation
// (unparseable CIDR, overlapping block, gateway out of range).
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return "subnets: " + e.Msg }

// Service manages subnets.
type Service struct {
	st  repo.Store
	now func() time.Time
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st, now: time.Now} }

// SetClock injects the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// TreeNode is a subnet plus its nested children, used by GetTree.
type TreeNode struct {
	store.Subnet
	Children []*TreeNode `json:"children,omitempty"`
}

// Stats is the tenant-wide subnet capacity rollup returned by GetStats.
type Stats struct {
	TotalSubnets       int64   `json:"total_subnets"`
	TotalAddresses     int64   `json:"total_addresses"`
	UsedAddresses      int64   `json:"used_addresses"`
	AvailableAddresses int64   `json:"available_addresses"`
	ReservedAddresses  int64   `json:"reserved_addresses"`
	Utilization        float64 `json:"utilization"`
}

// Create validates in, stamps its derived CIDR fields and inserts it in the
// caller's tenant. When allowOverlap is false the block must not overlap any
// existing subnet. The created row is returned with its computed counts filled.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, in store.Subnet, allowOverlap bool) (store.Subnet, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Subnet{}, err
	}
	in.TenantID = subj.TenantID
	if in.Name == "" {
		return store.Subnet{}, ValidationError{Msg: "name required"}
	}
	if err := deriveCIDR(&in); err != nil {
		return store.Subnet{}, err
	}
	if err := checkGateway(in.CIDR, in.Gateway); err != nil {
		return store.Subnet{}, err
	}
	if !allowOverlap {
		if err := s.checkOverlap(ctx, subj.TenantID, in.CIDR, ""); err != nil {
			return store.Subnet{}, err
		}
	}
	if in.ID == "" {
		in.ID = store.NewID()
	}
	if in.Status == "" {
		in.Status = store.SubnetActive
	}
	if in.CreatedBy == "" {
		in.CreatedBy = subj.ActorID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.now()
	}
	if err := s.st.CreateSubnet(ctx, in); err != nil {
		return store.Subnet{}, mapErr(err)
	}
	return s.Get(ctx, subj, in.ID)
}

// Get returns one subnet with its used/available/utilization fields computed.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.Subnet, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Subnet{}, err
	}
	sub, err := s.st.GetSubnet(ctx, subj.TenantID, id)
	if err != nil {
		return store.Subnet{}, mapErr(err)
	}
	s.fill(ctx, subj.TenantID, &sub)
	return sub, nil
}

// List returns the caller's subnets matching f, each with computed counts.
func (s *Service) List(ctx context.Context, subj authz.Subjects, f store.SubnetFilter) ([]store.Subnet, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	subs, err := s.st.ListSubnets(ctx, subj.TenantID, f)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		s.fill(ctx, subj.TenantID, &subs[i])
	}
	return subs, nil
}

// Update revalidates and replaces a subnet. A missing name/CIDR is inherited
// from the stored row; created-by/created-at are preserved.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, in store.Subnet, allowOverlap bool) (store.Subnet, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Subnet{}, err
	}
	in.TenantID = subj.TenantID
	ex, err := s.st.GetSubnet(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.Subnet{}, mapErr(err)
	}
	if in.Name == "" {
		in.Name = ex.Name
	}
	if in.CIDR == "" {
		in.CIDR = ex.CIDR
	}
	if err := deriveCIDR(&in); err != nil {
		return store.Subnet{}, err
	}
	if err := checkGateway(in.CIDR, in.Gateway); err != nil {
		return store.Subnet{}, err
	}
	if !allowOverlap {
		if err := s.checkOverlap(ctx, subj.TenantID, in.CIDR, in.ID); err != nil {
			return store.Subnet{}, err
		}
	}
	in.CreatedBy = ex.CreatedBy
	in.CreatedAt = ex.CreatedAt
	if in.Status == "" {
		in.Status = ex.Status
	}
	if err := s.st.UpdateSubnet(ctx, in); err != nil {
		return store.Subnet{}, mapErr(err)
	}
	return s.Get(ctx, subj, in.ID)
}

// Delete removes a subnet. If it still holds addresses, force is required; else
// ErrNotEmpty. The guard is checked here so it is explicit regardless of store.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string, force bool) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	if !force {
		n, err := s.st.CountAddressesInSubnet(ctx, subj.TenantID, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrNotEmpty
		}
	}
	return mapErr(s.st.DeleteSubnet(ctx, subj.TenantID, id, force))
}

// GetTree returns the caller's subnets nested by parent_id. Roots are subnets
// with no parent (or whose parent is not in the tenant). Every node carries its
// computed counts; siblings are ordered by id.
func (s *Service) GetTree(ctx context.Context, subj authz.Subjects) ([]*TreeNode, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	subs, err := s.st.ListSubnets(ctx, subj.TenantID, store.SubnetFilter{})
	if err != nil {
		return nil, err
	}
	nodes := make(map[string]*TreeNode, len(subs))
	for i := range subs {
		s.fill(ctx, subj.TenantID, &subs[i])
		nodes[subs[i].ID] = &TreeNode{Subnet: subs[i]}
	}
	var roots []*TreeNode
	for _, n := range nodes {
		if n.ParentID != "" {
			if p, ok := nodes[n.ParentID]; ok {
				p.Children = append(p.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	sortNodes(roots)
	for _, n := range nodes {
		sortNodes(n.Children)
	}
	return roots, nil
}

// GetStats returns the tenant capacity rollup: subnet count, aggregate address
// capacity, allocated (used), free and reserved counts, and overall utilization.
func (s *Service) GetStats(ctx context.Context, subj authz.Subjects) (Stats, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return Stats{}, err
	}
	subs, err := s.st.ListSubnets(ctx, subj.TenantID, store.SubnetFilter{})
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, sub := range subs {
		st.TotalSubnets++
		var total int64
		if info, perr := ipnet.Parse(sub.CIDR); perr == nil {
			total = info.Total
		}
		used, cerr := s.st.CountAddressesInSubnet(ctx, subj.TenantID, sub.ID)
		if cerr != nil {
			return Stats{}, cerr
		}
		st.TotalAddresses += total
		st.UsedAddresses += used
	}
	reserved, err := s.st.ListAddresses(ctx, subj.TenantID, store.AddressFilter{Status: store.IPReserved})
	if err != nil {
		return Stats{}, err
	}
	st.ReservedAddresses = int64(len(reserved))
	if avail := st.TotalAddresses - st.UsedAddresses; avail > 0 {
		st.AvailableAddresses = avail
	}
	st.Utilization = ipnet.Utilization(st.UsedAddresses, st.TotalAddresses)
	return st, nil
}

// --- helpers ---

// fill computes the used/available/utilization fields of sub from the store.
func (s *Service) fill(ctx context.Context, tenantID string, sub *store.Subnet) {
	var total int64
	if info, err := ipnet.Parse(sub.CIDR); err == nil {
		total = info.Total
	}
	used, _ := s.st.CountAddressesInSubnet(ctx, tenantID, sub.ID)
	sub.TotalAddresses = total
	sub.UsedAddresses = used
	if avail := total - used; avail > 0 {
		sub.AvailableAddresses = avail
	} else {
		sub.AvailableAddresses = 0
	}
	sub.Utilization = ipnet.Utilization(used, total)
}

// deriveCIDR parses sub.CIDR and stamps the network/broadcast/mask/prefix/
// version/total fields, returning ValidationError on an unparseable CIDR.
func deriveCIDR(sub *store.Subnet) error {
	info, err := ipnet.Parse(sub.CIDR)
	if err != nil {
		return ValidationError{Msg: fmt.Sprintf("invalid cidr %q", sub.CIDR)}
	}
	sub.NetworkAddress = info.Network.String()
	sub.BroadcastAddr = info.Broadcast.String()
	sub.Mask = info.Mask.String()
	sub.PrefixLength = info.PrefixLen
	sub.IPVersion = info.Version
	sub.TotalAddresses = info.Total
	return nil
}

// checkGateway rejects a non-empty gateway that is not a usable host in cidr.
func checkGateway(cidr, gateway string) error {
	if gateway == "" {
		return nil
	}
	ok, err := ipnet.GatewayInRange(cidr, gateway)
	if err != nil || !ok {
		return ValidationError{Msg: fmt.Sprintf("gateway %q not in range of %q", gateway, cidr)}
	}
	return nil
}

// checkOverlap rejects a block that overlaps any existing subnet in the tenant,
// skipping selfID (the row being updated) and any malformed stored CIDR.
func (s *Service) checkOverlap(ctx context.Context, tenantID, cidr, selfID string) error {
	existing, err := s.st.AllSubnetCIDRs(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, ex := range existing {
		if ex.ID == selfID || ex.CIDR == "" {
			continue
		}
		ov, oerr := ipnet.Overlaps(cidr, ex.CIDR)
		if oerr != nil {
			continue
		}
		if ov {
			return ValidationError{Msg: fmt.Sprintf("cidr %q overlaps existing subnet %s (%s)", cidr, ex.ID, ex.CIDR)}
		}
	}
	return nil
}

func sortNodes(ns []*TreeNode) {
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
}

// mapErr masks the store's sentinels into this package's own.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, repo.ErrNotEmpty):
		return ErrNotEmpty
	default:
		return err
	}
}
