// Package dnscfg is the IPAM DNS-configuration service: one config per tenant
// (resolver list, timeout, system-fallback and reverse-DNS toggles) plus a live
// reverse-lookup probe. Get returns a sensible default when the tenant has never
// configured DNS. Test performs a best-effort reverse lookup of an IP within the
// tenant's configured timeout and reports the resolved name and the elapsed
// milliseconds; the resolver is injected through a seam so the probe is
// offline-testable. Authorization is coarse (tenant scope); Update requires
// admin.
package dnscfg

import (
	"context"
	"net"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// Defaults for a tenant that has never configured DNS.
const (
	DefaultTimeoutMs = 5000
)

// LookupFunc is the reverse-DNS seam: given a context and an IP, it returns the
// reverse names (as net.Resolver.LookupAddr does). Tests inject a deterministic
// implementation; production uses the system resolver.
type LookupFunc func(ctx context.Context, addr string) ([]string, error)

// Service manages DNS configuration.
type Service struct {
	st     repo.Store
	lookup LookupFunc
}

// New builds the service with the system resolver as the reverse-lookup seam.
func New(st repo.Store) *Service {
	return &Service{st: st, lookup: net.DefaultResolver.LookupAddr}
}

// SetLookup injects the reverse-lookup function (tests / alternate resolvers).
func (s *Service) SetLookup(fn LookupFunc) {
	if fn != nil {
		s.lookup = fn
	}
}

// defaultConfig is returned for a tenant with no stored DNS config.
func defaultConfig(tenantID string) store.DNSConfig {
	return store.DNSConfig{
		TenantID:             tenantID,
		TimeoutMs:            DefaultTimeoutMs,
		UseSystemDNSFallback: true,
		ReverseDNSEnabled:    true,
	}
}

// Get returns the caller's DNS config, or a default when none is stored.
func (s *Service) Get(ctx context.Context, subj authz.Subjects) (store.DNSConfig, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.DNSConfig{}, err
	}
	c, err := s.st.GetDNSConfig(ctx, subj.TenantID)
	if err == repo.ErrNotFound {
		return defaultConfig(subj.TenantID), nil
	}
	if err != nil {
		return store.DNSConfig{}, err
	}
	return c, nil
}

// Update upserts the caller's DNS config. Admin only. The tenant is pinned from
// the subject; a zero timeout falls back to the default.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, cfg store.DNSConfig) (store.DNSConfig, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.DNSConfig{}, err
	}
	if err := authz.RequireAdmin(subj); err != nil {
		return store.DNSConfig{}, err
	}
	cfg.TenantID = subj.TenantID
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = DefaultTimeoutMs
	}
	if err := s.st.UpsertDNSConfig(ctx, cfg); err != nil {
		return store.DNSConfig{}, err
	}
	return s.st.GetDNSConfig(ctx, subj.TenantID)
}

// Test performs a best-effort reverse-DNS lookup of testIP using the tenant's
// configured timeout. It returns the first reverse name (or empty when none
// resolves), the elapsed time in milliseconds, and any lookup error. A malformed
// IP is reported as an error without invoking the resolver.
func (s *Service) Test(ctx context.Context, subj authz.Subjects, testIP string) (hostname string, latencyMs int64, err error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return "", 0, err
	}
	if net.ParseIP(testIP) == nil {
		return "", 0, &net.ParseError{Type: "IP address", Text: testIP}
	}
	cfg, err := s.Get(ctx, subj)
	if err != nil {
		return "", 0, err
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultTimeoutMs * time.Millisecond
	}
	lctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	names, lookupErr := s.lookup(lctx, testIP)
	latencyMs = time.Since(start).Milliseconds()
	if lookupErr != nil {
		return "", latencyMs, lookupErr
	}
	if len(names) > 0 {
		hostname = names[0]
	}
	return hostname, latencyMs, nil
}
