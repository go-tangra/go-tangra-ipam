// Package authz is the IPAM access model. Authorization is coarse (no Zanzibar):
// a caller is scoped to a tenant and carries roles the gateway resolved for it.
// This package answers "may this caller act within this tenant?", "is this
// caller a tenant admin?" and "is this caller a platform admin?" — the last
// gates the privileged out-of-band operations (power control, KVM console,
// IPMI) that a mere tenant admin must not reach.
package authz

import (
	"errors"
	"fmt"
)

// ErrForbidden is returned when a caller lacks the required scope.
var ErrForbidden = errors.New("authz: forbidden")

// Actor kinds (closed set): a human user, a peer service on the mesh, or the
// trusted system scope used by worker/maintenance paths.
const (
	ActorUser    = "user"
	ActorService = "service"
	ActorSystem  = "system"
)

// Role names that confer elevated scope.
const (
	RoleAdmin         = "admin"
	RoleOwner         = "owner"
	RolePlatformAdmin = "platform-admin"
)

// Subjects is the authenticated caller: a tenant, an actor identity, and the
// roles the gateway resolved for it.
type Subjects struct {
	TenantID  string
	UserID    string
	Roles     []string
	ActorKind string // user | service | system
}

func (s Subjects) hasRole(role string) bool {
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the caller is a tenant super-user. The tenant "owner"
// and "admin" roles both confer full control; the system scope is likewise
// treated as admin so trusted worker paths pass admin gates.
func (s Subjects) IsAdmin() bool {
	if s.ActorKind == ActorSystem {
		return true
	}
	return s.hasRole(RoleAdmin) || s.hasRole(RoleOwner)
}

// IsPlatformAdmin reports whether the caller may perform privileged, out-of-band
// operations (power control, KVM, IPMI). The dedicated "platform-admin" role
// grants it; the tenant "admin"/"owner" roles and the system scope also satisfy
// it so operators and trusted worker paths are not locked out.
func (s Subjects) IsPlatformAdmin() bool {
	if s.ActorKind == ActorSystem {
		return true
	}
	return s.hasRole(RolePlatformAdmin) || s.hasRole(RoleAdmin) || s.hasRole(RoleOwner)
}

// ActorID is the user id, falling back to the actor kind for non-user callers.
func (s Subjects) ActorID() string {
	if s.UserID != "" {
		return s.UserID
	}
	return s.ActorKind
}

// RequireAdmin permits only tenant super-users (or the system scope). It guards
// tenant-wide administrative operations.
func RequireAdmin(s Subjects) error {
	if s.IsAdmin() {
		return nil
	}
	return fmt.Errorf("%w: admin required", ErrForbidden)
}

// RequirePlatformAdmin permits only platform admins (or the system scope). It
// guards the out-of-band power/KVM/IPMI operations.
func RequirePlatformAdmin(s Subjects) error {
	if s.IsPlatformAdmin() {
		return nil
	}
	return fmt.Errorf("%w: platform-admin required", ErrForbidden)
}

// RequireTenant ensures the caller may act on tenantID. A non-system caller may
// act only within its own tenant; the system scope may act cross-tenant. A bare
// RequireTenant refuses an empty or mismatched tenant.
func RequireTenant(s Subjects, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("%w: tenant required", ErrForbidden)
	}
	if s.TenantID == tenantID {
		return nil
	}
	if s.ActorKind == ActorSystem {
		return nil
	}
	return fmt.Errorf("%w: tenant mismatch", ErrForbidden)
}
