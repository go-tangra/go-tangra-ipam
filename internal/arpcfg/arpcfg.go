// Package arpcfg is the per-tenant ARP settings service (feature 022, US3):
// administrators turn ARP collection off, exclude devices as ARP sources and
// tune the proxy-ARP threshold. Every change is audited with the user actor
// in the settings transaction.
package arpcfg

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// FieldError is a rejected input field (never echoes the value).
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return "arpcfg: " + e.Field + ": " + e.Msg }

// Input is the PUT body; every field is required.
type Input struct {
	Enabled         *bool    `json:"enabled"`
	ExcludedDevices []string `json:"excluded_devices"`
	ProxyThreshold  *int     `json:"proxy_threshold"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Service reads and writes the tenant's ARP settings.
type Service struct {
	st  repo.Store
	now func() time.Time
}

// New builds the service.
func New(st repo.Store) *Service {
	return &Service{st: st, now: func() time.Time { return time.Now().UTC() }}
}

// Get returns the caller tenant's settings (defaults when never saved).
func (s *Service) Get(ctx context.Context, subj authz.Subjects) (store.ARPSettings, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.ARPSettings{}, err
	}
	return s.st.GetARPSettings(ctx, subj.TenantID)
}

// Update validates and replaces the caller tenant's settings. Excluded
// devices must be existing devices of the tenant; duplicates collapse.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, in Input) (store.ARPSettings, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.ARPSettings{}, err
	}
	switch {
	case in.Enabled == nil:
		return store.ARPSettings{}, &FieldError{"enabled", "required"}
	case in.ProxyThreshold == nil:
		return store.ARPSettings{}, &FieldError{"proxy_threshold", "required"}
	case *in.ProxyThreshold < store.ARPMinProxyThreshold || *in.ProxyThreshold > store.ARPMaxProxyThreshold:
		return store.ARPSettings{}, &FieldError{"proxy_threshold", "must be between 2 and 256"}
	case len(in.ExcludedDevices) > store.ARPMaxExcludedDevices:
		return store.ARPSettings{}, &FieldError{"excluded_devices", "at most 256 devices"}
	}
	excluded := []string{}
	seen := map[string]bool{}
	for _, id := range in.ExcludedDevices {
		if !uuidRe.MatchString(id) {
			return store.ARPSettings{}, &FieldError{"excluded_devices", "not a device id"}
		}
		if seen[id] {
			continue
		}
		if _, err := s.st.GetDevice(ctx, subj.TenantID, id); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return store.ARPSettings{}, &FieldError{"excluded_devices", "unknown device"}
			}
			return store.ARPSettings{}, err
		}
		seen[id] = true
		excluded = append(excluded, id)
	}
	next := store.ARPSettings{TenantID: subj.TenantID, Enabled: *in.Enabled, ExcludedDevices: excluded,
		ProxyThreshold: *in.ProxyThreshold, UpdatedBy: subj.ActorID()}
	row, err := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: audit.ARPSettingsUpdated, ActorKind: audit.ActorUser,
		ActorID: subj.ActorID(), SubjectKind: audit.SubjectTenant, SubjectID: subj.TenantID, Outcome: audit.OutcomeOK,
		Details: map[string]any{"enabled": next.Enabled, "excluded_count": len(excluded), "proxy_threshold": next.ProxyThreshold}}, s.now())
	if err != nil {
		return store.ARPSettings{}, err
	}
	if err := s.st.PutARPSettings(ctx, next, row); err != nil {
		return store.ARPSettings{}, err
	}
	return s.st.GetARPSettings(ctx, subj.TenantID)
}
