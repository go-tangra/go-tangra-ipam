package authz

import (
	"errors"
	"testing"
)

func TestIsAdmin(t *testing.T) {
	cases := []struct {
		name string
		s    Subjects
		want bool
	}{
		{"admin role", Subjects{Roles: []string{"admin"}}, true},
		{"owner role", Subjects{Roles: []string{"owner"}}, true},
		{"admin among many", Subjects{Roles: []string{"viewer", "admin"}}, true},
		{"owner among many", Subjects{Roles: []string{"viewer", "owner"}}, true},
		{"system actor", Subjects{ActorKind: ActorSystem}, true},
		{"platform-admin is not tenant admin", Subjects{Roles: []string{"platform-admin"}, ActorKind: ActorUser}, false},
		{"plain user", Subjects{Roles: []string{"viewer"}, ActorKind: ActorUser}, false},
		{"no roles", Subjects{ActorKind: ActorUser}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.IsAdmin(); got != tc.want {
				t.Errorf("IsAdmin()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestIsPlatformAdmin(t *testing.T) {
	cases := []struct {
		name string
		s    Subjects
		want bool
	}{
		{"platform-admin role", Subjects{Roles: []string{"platform-admin"}, ActorKind: ActorUser}, true},
		{"platform-admin among many", Subjects{Roles: []string{"viewer", "platform-admin"}, ActorKind: ActorUser}, true},
		{"admin also qualifies", Subjects{Roles: []string{"admin"}, ActorKind: ActorUser}, true},
		{"owner also qualifies", Subjects{Roles: []string{"owner"}, ActorKind: ActorUser}, true},
		{"system actor", Subjects{ActorKind: ActorSystem}, true},
		{"plain user", Subjects{Roles: []string{"viewer"}, ActorKind: ActorUser}, false},
		{"no roles service", Subjects{ActorKind: ActorService}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.IsPlatformAdmin(); got != tc.want {
				t.Errorf("IsPlatformAdmin()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestActorID(t *testing.T) {
	if got := (Subjects{UserID: "u1"}).ActorID(); got != "u1" {
		t.Errorf("ActorID()=%q want u1", got)
	}
	if got := (Subjects{ActorKind: ActorService}).ActorID(); got != ActorService {
		t.Errorf("ActorID()=%q want %q", got, ActorService)
	}
}

func TestRequireAdmin(t *testing.T) {
	if err := RequireAdmin(Subjects{Roles: []string{"admin"}}); err != nil {
		t.Errorf("admin refused: %v", err)
	}
	if err := RequireAdmin(Subjects{ActorKind: ActorSystem}); err != nil {
		t.Errorf("system refused: %v", err)
	}
	err := RequireAdmin(Subjects{Roles: []string{"viewer"}})
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin allowed: %v", err)
	}
}

func TestRequirePlatformAdmin(t *testing.T) {
	if err := RequirePlatformAdmin(Subjects{Roles: []string{"platform-admin"}, ActorKind: ActorUser}); err != nil {
		t.Errorf("platform-admin refused: %v", err)
	}
	if err := RequirePlatformAdmin(Subjects{ActorKind: ActorSystem}); err != nil {
		t.Errorf("system refused: %v", err)
	}
	// A tenant admin without platform-admin still qualifies (admin implies it).
	if err := RequirePlatformAdmin(Subjects{Roles: []string{"admin"}, ActorKind: ActorUser}); err != nil {
		t.Errorf("tenant admin refused: %v", err)
	}
	// A plain viewer must be refused from power/KVM/IPMI.
	err := RequirePlatformAdmin(Subjects{Roles: []string{"viewer"}, ActorKind: ActorUser})
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer allowed into platform-admin gate: %v", err)
	}
}

func TestRequireTenant(t *testing.T) {
	if err := RequireTenant(Subjects{TenantID: "t1"}, "t1"); err != nil {
		t.Errorf("same tenant refused: %v", err)
	}
	// admin does not get cross-tenant via RequireTenant unless system scope.
	err := RequireTenant(Subjects{TenantID: "t1", Roles: []string{"admin"}}, "t2")
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Errorf("admin cross-tenant should be refused here: %v", err)
	}
	// system scope may act cross-tenant.
	if err := RequireTenant(Subjects{TenantID: "t1", ActorKind: ActorSystem}, "t2"); err != nil {
		t.Errorf("system cross-tenant refused: %v", err)
	}
	// plain mismatch.
	if err := RequireTenant(Subjects{TenantID: "t1", ActorKind: ActorUser}, "t2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("tenant mismatch allowed: %v", err)
	}
	// empty tenant is always refused.
	if err := RequireTenant(Subjects{ActorKind: ActorSystem}, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("empty tenant allowed: %v", err)
	}
}
