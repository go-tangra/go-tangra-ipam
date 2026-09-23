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
		if err := s.checkOverlap(ctx, subj.TenantID, in.CIDR, "", in.ParentID); err != nil {
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

// SplitResult reports the outcome of Split: the blocks that were created and
// the ones skipped because they already exist (or overlap something).
type SplitResult struct {
	Parent  store.Subnet   `json:"parent"`
	Created []store.Subnet `json:"created"`
	Skipped []SkippedBlock `json:"skipped"`
}

// SkippedBlock is one child block Split did not create, with the reason.
type SkippedBlock struct {
	CIDR   string `json:"cidr"`
	Reason string `json:"reason"`
}

// Split subdivides a subnet into the child blocks of prefixLen it contains and
// creates them under it, inheriting its VLAN and location. A block that
// overlaps an existing subnet is skipped rather than failing the whole
// operation, so an already partially split parent can be split further. With
// dryRun nothing is written and the result only reports what would be created.
func (s *Service) Split(ctx context.Context, subj authz.Subjects, id string, prefixLen int, dryRun bool) (SplitResult, error) {
	parent, err := s.Get(ctx, subj, id)
	if err != nil {
		return SplitResult{}, err
	}
	blocks, err := ipnet.Subdivide(parent.CIDR, prefixLen)
	if err != nil {
		return SplitResult{}, ValidationError{Msg: fmt.Sprintf("cannot split %q into /%d: %v", parent.CIDR, prefixLen, err)}
	}
	existing, err := s.st.AllSubnetCIDRs(ctx, subj.TenantID)
	if err != nil {
		return SplitResult{}, err
	}
	res := SplitResult{Parent: parent}
	for i, block := range blocks {
		if taken, by := overlapsAny(block, parent.ID, existing); taken {
			res.Skipped = append(res.Skipped, SkippedBlock{CIDR: block, Reason: fmt.Sprintf("overlaps %s", by)})
			continue
		}
		child := store.Subnet{
			Name:        fmt.Sprintf("%s — %s", parent.Name, block),
			CIDR:        block,
			Description: fmt.Sprintf("split %d of %d from %s", i+1, len(blocks), parent.CIDR),
			ParentID:    parent.ID,
			VlanID:      parent.VlanID,
			LocationID:  parent.LocationID,
			Status:      store.SubnetActive,
		}
		if dryRun {
			if derr := deriveCIDR(&child); derr != nil {
				return SplitResult{}, derr
			}
			res.Created = append(res.Created, child)
			continue
		}
		// allowOverlap: the parent contains every block by construction and
		// sibling collisions were ruled out above.
		created, cerr := s.Create(ctx, subj, child, true)
		if cerr != nil {
			return SplitResult{}, cerr
		}
		res.Created = append(res.Created, created)
		existing = append(existing, store.Subnet{ID: created.ID, CIDR: created.CIDR})
	}
	if len(res.Created) == 0 {
		return res, ValidationError{Msg: fmt.Sprintf("no free /%d block inside %s", prefixLen, parent.CIDR)}
	}
	return res, nil
}

// overlapsAny reports whether block collides with a stored subnet other than
// the parent being split, and returns that subnet's CIDR.
func overlapsAny(block, parentID string, existing []store.Subnet) (bool, string) {
	for _, ex := range existing {
		if ex.ID == parentID || ex.CIDR == "" {
			continue
		}
		ov, err := ipnet.Overlaps(block, ex.CIDR)
		if err == nil && ov {
			return true, ex.CIDR
		}
	}
	return false, ""
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
		if err := s.checkOverlap(ctx, subj.TenantID, in.CIDR, in.ID, in.ParentID); err != nil {
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
func (s *Service) checkOverlap(ctx context.Context, tenantID, cidr, selfID, parentID string) error {
	existing, err := s.st.AllSubnetCIDRs(ctx, tenantID)
	if err != nil {
		return err
	}
	byID := make(map[string]store.Subnet, len(existing))
	for _, ex := range existing {
		byID[ex.ID] = ex
	}
	nested, err := nestedWith(byID, cidr, selfID, parentID)
	if err != nil {
		return err
	}
	for _, ex := range existing {
		if ex.ID == selfID || ex.CIDR == "" || nested[ex.ID] {
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

// nestedWith returns the IDs a block legitimately overlaps in the hierarchy:
// parentID and every subnet above it, plus every subnet below selfID (its own
// children, when it is being updated). The block must lie strictly inside its
// declared parent.
func nestedWith(byID map[string]store.Subnet, cidr, selfID, parentID string) (map[string]bool, error) {
	out := map[string]bool{}
	if parentID != "" {
		parent, ok := byID[parentID]
		if !ok {
			return nil, ValidationError{Msg: fmt.Sprintf("parent subnet %s not found", parentID)}
		}
		if !ipnet.Within(cidr, parent.CIDR) {
			return nil, ValidationError{Msg: fmt.Sprintf("cidr %q is not inside parent %s", cidr, parent.CIDR)}
		}
		for id := parentID; id != "" && !out[id]; id = byID[id].ParentID {
			out[id] = true
		}
	}
	if selfID == "" {
		return out, nil
	}
	for id, sub := range byID {
		seen := map[string]bool{}
		for p := sub.ParentID; p != "" && !seen[p]; p = byID[p].ParentID {
			if p == selfID {
				out[id] = true
				break
			}
			seen[p] = true
		}
	}
	return out, nil
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
