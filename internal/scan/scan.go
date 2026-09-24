// Package scan is the IPAM active-discovery orchestration service. It owns the
// subnet-scan work queue: StartScan validates and enqueues a job (tenant-scoped,
// refusing IPv6, oversized and already-active subnets), and the executor worker
// pool (Run/RunOnce) claims due jobs, sweeps the subnet's own hosts for ICMP
// liveness, upserts the alive addresses, optionally runs SNMP device discovery,
// and publishes realtime progress — all through the Sweeper/Pinger/Discoverer,
// warden and Publisher interfaces so the orchestration and its authorization are
// unit-tested with fakes, without touching the network.
package scan

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// Sentinel errors surfaced to callers.
var (
	// ErrTooLarge means the subnet's usable host count exceeds the configured
	// max_hosts bound.
	ErrTooLarge = errors.New("scan: subnet too large to scan")
	// ErrIPv6 means the subnet is IPv6, which active scanning does not support.
	ErrIPv6 = errors.New("scan: IPv6 subnets are not supported for scanning")
	// ErrActiveScan means a pending/running scan already exists for the subnet.
	ErrActiveScan = errors.New("scan: a scan is already active for this subnet")
	// ErrTerminal means a cancel was requested for an already-finished job.
	ErrTerminal = errors.New("scan: job already finished")
)

// Config bounds the scan executor (mirrors config.Scan).
type Config struct {
	MaxHosts    int
	Concurrency int
	TimeoutMs   int
	Workers     int
	MaxRetries  int
}

// Options are the per-scan toggles chosen at StartScan time.
type Options struct {
	EnableSNMP      bool
	EnableDNSUpdate bool
	SkipReverseDNS  bool
}

// Service orchestrates subnet scans over a repo.Store and the active-ops
// clients. It is safe for concurrent use.
type Service struct {
	st      repo.Store
	sweeper icmp.Sweeper
	pinger  icmp.Pinger
	snmp    snmp.Discoverer
	warden  warden.Client
	pub     events.Publisher
	cfg     Config

	now       func() time.Time
	revLookup func(ctx context.Context, ip string) string
}

// New builds a scan Service. A nil now uses time.Now (UTC); a nil publisher
// must not be passed (use events.HubPublisher{} for a no-op).
func New(
	st repo.Store,
	sweeper icmp.Sweeper,
	pinger icmp.Pinger,
	disc snmp.Discoverer,
	w warden.Client,
	pub events.Publisher,
	cfg Config,
	now func() time.Time,
) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		st:        st,
		sweeper:   sweeper,
		pinger:    pinger,
		snmp:      disc,
		warden:    w,
		pub:       pub,
		cfg:       cfg,
		now:       now,
		revLookup: defaultRevLookup,
	}
}

// StartScan validates and enqueues a pending scan for a subnet. It refuses a
// cross-tenant caller, an already-active subnet, an IPv6 subnet, and a subnet
// whose usable host count exceeds max_hosts.
func (s *Service) StartScan(ctx context.Context, subj authz.Subjects, subnetID string, opts Options) (store.IPScanJob, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPScanJob{}, err
	}
	tenantID := subj.TenantID

	subnet, err := s.st.GetSubnet(ctx, tenantID, subnetID)
	if err != nil {
		return store.IPScanJob{}, err
	}

	info, err := ipnet.Parse(subnet.CIDR)
	if err != nil {
		return store.IPScanJob{}, err
	}
	if info.Version == 6 {
		return store.IPScanJob{}, ErrIPv6
	}
	usable := usableHosts(info)
	if usable > int64(s.cfg.MaxHosts) {
		return store.IPScanJob{}, ErrTooLarge
	}

	active, err := s.st.ActiveScanForSubnet(ctx, tenantID, subnetID)
	if err != nil {
		return store.IPScanJob{}, err
	}
	if active {
		return store.IPScanJob{}, ErrActiveScan
	}

	now := s.now()
	job := store.IPScanJob{
		ID:              store.NewID(),
		TenantID:        tenantID,
		SubnetID:        subnetID,
		Status:          store.ScanPending,
		TriggeredBy:     store.TriggerManual,
		TotalAddresses:  usable,
		MaxRetries:      s.cfg.MaxRetries,
		TimeoutMs:       s.cfg.TimeoutMs,
		Concurrency:     s.cfg.Concurrency,
		SkipReverseDNS:  opts.SkipReverseDNS,
		EnableSNMP:      opts.EnableSNMP,
		EnableDNSUpdate: opts.EnableDNSUpdate,
		CreatedBy:       subj.ActorID(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.st.CreateScanJob(ctx, job); err != nil {
		return store.IPScanJob{}, err
	}
	s.pub.Publish(ctx, tenantID, events.ScanStarted, events.ScanPayload(job.ID, subnetID, 0, 0))
	return s.st.GetScanJob(ctx, tenantID, job.ID)
}

// GetScanJob returns one job in the caller's tenant.
func (s *Service) GetScanJob(ctx context.Context, subj authz.Subjects, id string) (store.IPScanJob, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPScanJob{}, err
	}
	return s.st.GetScanJob(ctx, subj.TenantID, id)
}

// ListScanJobs lists jobs in the caller's tenant.
func (s *Service) ListScanJobs(ctx context.Context, subj authz.Subjects, f store.ScanFilter) ([]store.IPScanJob, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListScanJobs(ctx, subj.TenantID, f)
}

// CancelScan marks a pending/running job cancelled. A running worker observes
// the change cooperatively and stops without completing. Cancelling an
// already-finished job returns it unchanged with ErrTerminal.
func (s *Service) CancelScan(ctx context.Context, subj authz.Subjects, id string) (store.IPScanJob, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPScanJob{}, err
	}
	job, err := s.st.GetScanJob(ctx, subj.TenantID, id)
	if err != nil {
		return store.IPScanJob{}, err
	}
	if isTerminal(job.Status) {
		return job, ErrTerminal
	}
	now := s.now()
	job.Status = store.ScanCancelled
	job.StatusMessage = "cancelled"
	job.CompletedAt = &now
	if err := s.st.UpdateScanJob(ctx, job); err != nil {
		return store.IPScanJob{}, err
	}
	return s.st.GetScanJob(ctx, subj.TenantID, id)
}

// usableHosts returns the count of scannable host addresses in a parsed CIDR
// (excluding network/broadcast for IPv4 prefixes shorter than /31).
func usableHosts(info ipnet.Info) int64 {
	total := info.Total
	if info.Version == 4 && info.PrefixLen < 31 {
		total -= 2
		if total < 0 {
			total = 0
		}
	}
	return total
}

func isTerminal(status string) bool {
	switch status {
	case store.ScanCompleted, store.ScanFailed, store.ScanCancelled:
		return true
	default:
		return false
	}
}

// defaultRevLookup performs a bounded reverse-DNS lookup, returning "" on any
// failure. The bound keeps a scan from stalling on unresponsive resolvers.
func defaultRevLookup(ctx context.Context, ip string) string {
	c, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(c, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}
