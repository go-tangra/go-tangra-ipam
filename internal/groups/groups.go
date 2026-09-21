// Package groups is the IPAM grouping service. It manages two kinds of group in
// the caller's tenant: IP groups (named sets of address / range / subnet members
// used for policy and reachability checks) and host groups (named sets of
// devices). Beyond CRUD it answers the reachability question CheckIpInGroup —
// "which of these groups does this IP fall in?" — over the members with a pure,
// panic-free matcher (Matches). Authorization is coarse (the caller is scoped to
// its tenant); the store enforces per-tenant RLS and member uniqueness.
package groups

import (
	"context"
	"net"
	"strings"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/ipnet"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// ValidationError is returned when a caller-supplied field fails validation.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return "groups: " + e.Msg }

// Service manages IP and host groups.
type Service struct {
	st repo.Store
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st} }

// ---- IP groups

// CreateIPGroup validates and inserts an IP group in the caller's tenant.
func (s *Service) CreateIPGroup(ctx context.Context, subj authz.Subjects, g store.IPGroup) (store.IPGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPGroup{}, err
	}
	g.TenantID = subj.TenantID
	if g.Name == "" {
		return store.IPGroup{}, ValidationError{Msg: "name required"}
	}
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	if g.CreatedBy == "" {
		g.CreatedBy = subj.ActorID()
	}
	if err := s.st.CreateIPGroup(ctx, g); err != nil {
		return store.IPGroup{}, err
	}
	return s.st.GetIPGroup(ctx, subj.TenantID, g.ID)
}

// GetIPGroup returns one IP group in the caller's tenant.
func (s *Service) GetIPGroup(ctx context.Context, subj authz.Subjects, id string) (store.IPGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPGroup{}, err
	}
	return s.st.GetIPGroup(ctx, subj.TenantID, id)
}

// ListIPGroups returns the caller's IP groups (keyset paginated).
func (s *Service) ListIPGroups(ctx context.Context, subj authz.Subjects, limit int, cursorID string) ([]store.IPGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListIPGroups(ctx, subj.TenantID, limit, cursorID)
}

// UpdateIPGroup replaces an IP group's mutable fields.
func (s *Service) UpdateIPGroup(ctx context.Context, subj authz.Subjects, g store.IPGroup) (store.IPGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPGroup{}, err
	}
	g.TenantID = subj.TenantID
	if g.Name == "" {
		return store.IPGroup{}, ValidationError{Msg: "name required"}
	}
	ex, err := s.st.GetIPGroup(ctx, subj.TenantID, g.ID)
	if err != nil {
		return store.IPGroup{}, err
	}
	g.CreatedBy = ex.CreatedBy
	if g.Status == "" {
		g.Status = ex.Status
	}
	if err := s.st.UpdateIPGroup(ctx, g); err != nil {
		return store.IPGroup{}, err
	}
	return s.st.GetIPGroup(ctx, subj.TenantID, g.ID)
}

// DeleteIPGroup removes an IP group and its members.
func (s *Service) DeleteIPGroup(ctx context.Context, subj authz.Subjects, id string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	return s.st.DeleteIPGroup(ctx, subj.TenantID, id)
}

// AddIPGroupMember validates the member value against its type and inserts it.
func (s *Service) AddIPGroupMember(ctx context.Context, subj authz.Subjects, m store.IPGroupMember) (store.IPGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPGroupMember{}, err
	}
	m.TenantID = subj.TenantID
	if m.MemberType == "" {
		m.MemberType = store.MemberAddress
	}
	if err := validateMemberValue(m.MemberType, m.Value); err != nil {
		return store.IPGroupMember{}, err
	}
	if m.ID == "" {
		m.ID = store.NewID()
	}
	if err := s.st.AddIPGroupMember(ctx, m); err != nil {
		return store.IPGroupMember{}, err
	}
	return m, nil
}

// RemoveIPGroupMember deletes one member by id.
func (s *Service) RemoveIPGroupMember(ctx context.Context, subj authz.Subjects, memberID string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	return s.st.RemoveIPGroupMember(ctx, subj.TenantID, memberID)
}

// UpdateIPGroupMember validates and replaces a member's type/value/metadata.
func (s *Service) UpdateIPGroupMember(ctx context.Context, subj authz.Subjects, m store.IPGroupMember) (store.IPGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPGroupMember{}, err
	}
	m.TenantID = subj.TenantID
	if m.MemberType == "" {
		m.MemberType = store.MemberAddress
	}
	if err := validateMemberValue(m.MemberType, m.Value); err != nil {
		return store.IPGroupMember{}, err
	}
	if err := s.st.UpdateIPGroupMember(ctx, m); err != nil {
		return store.IPGroupMember{}, err
	}
	return m, nil
}

// ListIPGroupMembers returns a group's members ordered by sequence.
func (s *Service) ListIPGroupMembers(ctx context.Context, subj authz.Subjects, groupID string) ([]store.IPGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListIPGroupMembers(ctx, subj.TenantID, groupID)
}

// CheckIpInGroup returns the IP groups (from groupIDs, or all of the tenant's
// groups when groupIDs is empty) that contain ip — i.e. those with at least one
// member the ip matches. Matching is by member type: address is an exact match,
// range is an inclusive lo-hi span, subnet is CIDR containment.
func (s *Service) CheckIpInGroup(ctx context.Context, subj authz.Subjects, ip string, groupIDs []string) ([]store.IPGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	groups, members, err := s.st.AllIPGroupsWithMembers(ctx, subj.TenantID, groupIDs)
	if err != nil {
		return nil, err
	}
	var out []store.IPGroup
	for _, g := range groups {
		for _, m := range members[g.ID] {
			if Matches(m, ip) {
				out = append(out, g)
				break
			}
		}
	}
	return out, nil
}

// Matches reports whether ip satisfies member. It is pure and panic-free for any
// input: an unparseable ip, value, or member type simply yields false.
//   - address: exact IP equality (v4/v6 aware).
//   - range:   inclusive "lo-hi" span (a value with no dash is a single address).
//   - subnet:  CIDR containment.
func Matches(member store.IPGroupMember, ip string) bool {
	switch member.MemberType {
	case store.MemberAddress:
		a := net.ParseIP(strings.TrimSpace(member.Value))
		b := net.ParseIP(strings.TrimSpace(ip))
		return a != nil && b != nil && a.Equal(b)
	case store.MemberRange:
		return ipnet.InRange(member.Value, ip)
	case store.MemberSubnet:
		ok, err := ipnet.Contains(member.Value, ip)
		return err == nil && ok
	default:
		return false
	}
}

// validateMemberValue rejects a member whose value does not parse as its type.
func validateMemberValue(memberType, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return ValidationError{Msg: "member value required"}
	}
	switch memberType {
	case store.MemberAddress:
		if net.ParseIP(v) == nil {
			return ValidationError{Msg: "invalid address value: " + value}
		}
	case store.MemberRange:
		lo, hi, ok := strings.Cut(v, "-")
		if !ok || net.ParseIP(strings.TrimSpace(lo)) == nil || net.ParseIP(strings.TrimSpace(hi)) == nil {
			return ValidationError{Msg: "invalid range value (want lo-hi): " + value}
		}
	case store.MemberSubnet:
		if _, _, err := net.ParseCIDR(v); err != nil {
			return ValidationError{Msg: "invalid subnet value: " + value}
		}
	default:
		return ValidationError{Msg: "unknown member_type: " + memberType}
	}
	return nil
}

// ---- host groups

// CreateHostGroup validates and inserts a host group.
func (s *Service) CreateHostGroup(ctx context.Context, subj authz.Subjects, g store.HostGroup) (store.HostGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostGroup{}, err
	}
	g.TenantID = subj.TenantID
	if g.Name == "" {
		return store.HostGroup{}, ValidationError{Msg: "name required"}
	}
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	if g.CreatedBy == "" {
		g.CreatedBy = subj.ActorID()
	}
	if err := s.st.CreateHostGroup(ctx, g); err != nil {
		return store.HostGroup{}, err
	}
	return s.st.GetHostGroup(ctx, subj.TenantID, g.ID)
}

// GetHostGroup returns one host group.
func (s *Service) GetHostGroup(ctx context.Context, subj authz.Subjects, id string) (store.HostGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostGroup{}, err
	}
	return s.st.GetHostGroup(ctx, subj.TenantID, id)
}

// ListHostGroups returns the caller's host groups (keyset paginated).
func (s *Service) ListHostGroups(ctx context.Context, subj authz.Subjects, limit int, cursorID string) ([]store.HostGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListHostGroups(ctx, subj.TenantID, limit, cursorID)
}

// UpdateHostGroup replaces a host group's mutable fields.
func (s *Service) UpdateHostGroup(ctx context.Context, subj authz.Subjects, g store.HostGroup) (store.HostGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostGroup{}, err
	}
	g.TenantID = subj.TenantID
	if g.Name == "" {
		return store.HostGroup{}, ValidationError{Msg: "name required"}
	}
	ex, err := s.st.GetHostGroup(ctx, subj.TenantID, g.ID)
	if err != nil {
		return store.HostGroup{}, err
	}
	g.CreatedBy = ex.CreatedBy
	if g.Status == "" {
		g.Status = ex.Status
	}
	if err := s.st.UpdateHostGroup(ctx, g); err != nil {
		return store.HostGroup{}, err
	}
	return s.st.GetHostGroup(ctx, subj.TenantID, g.ID)
}

// DeleteHostGroup removes a host group and its members.
func (s *Service) DeleteHostGroup(ctx context.Context, subj authz.Subjects, id string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	return s.st.DeleteHostGroup(ctx, subj.TenantID, id)
}

// AddHostGroupMember adds a device to a host group.
func (s *Service) AddHostGroupMember(ctx context.Context, subj authz.Subjects, m store.HostGroupMember) (store.HostGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostGroupMember{}, err
	}
	m.TenantID = subj.TenantID
	if m.DeviceID == "" {
		return store.HostGroupMember{}, ValidationError{Msg: "device_id required"}
	}
	if m.ID == "" {
		m.ID = store.NewID()
	}
	if err := s.st.AddHostGroupMember(ctx, m); err != nil {
		return store.HostGroupMember{}, err
	}
	return m, nil
}

// RemoveHostGroupMember removes a device from a host group by member id.
func (s *Service) RemoveHostGroupMember(ctx context.Context, subj authz.Subjects, memberID string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	return s.st.RemoveHostGroupMember(ctx, subj.TenantID, memberID)
}

// UpdateHostGroupMember replaces a member's device/sequence.
func (s *Service) UpdateHostGroupMember(ctx context.Context, subj authz.Subjects, m store.HostGroupMember) (store.HostGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostGroupMember{}, err
	}
	m.TenantID = subj.TenantID
	if m.DeviceID == "" {
		return store.HostGroupMember{}, ValidationError{Msg: "device_id required"}
	}
	if err := s.st.UpdateHostGroupMember(ctx, m); err != nil {
		return store.HostGroupMember{}, err
	}
	return m, nil
}

// ListHostGroupMembers returns a host group's members (device summaries filled).
func (s *Service) ListHostGroupMembers(ctx context.Context, subj authz.Subjects, groupID string) ([]store.HostGroupMember, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListHostGroupMembers(ctx, subj.TenantID, groupID)
}

// ListDeviceHostGroups returns the host groups a device belongs to.
func (s *Service) ListDeviceHostGroups(ctx context.Context, subj authz.Subjects, deviceID string) ([]store.HostGroup, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListDeviceHostGroups(ctx, subj.TenantID, deviceID)
}
