package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/subnets"
)

// pollInterval is how often Run polls the work queue for due jobs.
const pollInterval = time.Second

// baseBackoff is the first retry delay; it doubles per retry.
const baseBackoff = 5 * time.Second

// Run drives the worker pool until ctx is cancelled, polling the work queue and
// processing every claimed job. It always returns ctx.Err() on exit.
func (s *Service) Run(ctx context.Context, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := s.RunOnce(ctx, log); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("scan run cycle", "err", err)
			}
		}
	}
}

// RunOnce claims one batch of due jobs (system scope) and processes them
// concurrently, bounded by the worker count. It returns the number claimed. It
// is the deterministic unit-test entry point.
func (s *Service) RunOnce(ctx context.Context, log *slog.Logger) (int, error) {
	if log == nil {
		log = slog.Default()
	}
	workers := s.cfg.Workers
	if workers < 1 {
		workers = 1
	}
	jobs, err := s.st.ClaimDueScanJobs(ctx, s.now(), workers)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(job store.IPScanJob) {
			defer wg.Done()
			defer func() { <-sem }()
			s.processJob(ctx, log, job)
		}(job)
	}
	wg.Wait()
	return len(jobs), nil
}

// processJob runs the full discovery pipeline for one claimed (already-scanning)
// job: enumerate the subnet's hosts, sweep for liveness, upsert alive addresses,
// optionally run SNMP discovery, then mark completed. Failures are retried with
// backoff up to max_retries; a cancel is honored cooperatively.
func (s *Service) processJob(ctx context.Context, log *slog.Logger, job store.IPScanJob) {
	if log == nil {
		log = slog.Default()
	}
	// A cancel may have landed between the claim and now; never write progress
	// (which would clobber the cancelled status) once it has.
	if s.observeCancel(ctx, log, &job) {
		return
	}
	subnet, err := s.st.GetSubnet(ctx, job.TenantID, job.SubnetID)
	if err != nil {
		s.retryOrFail(ctx, log, job, fmt.Errorf("load subnet: %w", err))
		return
	}
	// Credentials are resolved and opened once, when the job starts: the scan
	// keeps them even if they change meanwhile (FR-013, FR-018).
	var cred effective
	if job.EnableSNMP {
		if cred, err = s.effectiveCreds(ctx, job.TenantID, job.SubnetID); err != nil {
			s.retryOrFail(ctx, log, job, fmt.Errorf("resolve snmp credentials: %w", err))
			return
		}
	}

	targets, err := enumerateTargets(subnet.CIDR, s.cfg.MaxHosts)
	if err != nil {
		s.retryOrFail(ctx, log, job, fmt.Errorf("enumerate targets: %w", err))
		return
	}
	if s.observeCancel(ctx, log, &job) {
		return
	}
	job.TotalAddresses = int64(len(targets))
	job.StatusMessage = "sweeping"
	job.Progress = 10
	_ = s.st.UpdateScanJob(ctx, job)

	ips := ipStrings(targets)
	alive, err := s.sweeper.Sweep(ctx, ips, s.cfg.Concurrency, s.timeout())
	if err != nil {
		s.retryOrFail(ctx, log, job, fmt.Errorf("sweep: %w", err))
		return
	}

	aliveList := make([]string, 0, len(alive))
	for _, ip := range ips {
		if alive[ip] {
			aliveList = append(aliveList, ip)
		}
	}

	if s.observeCancel(ctx, log, &job) {
		return
	}
	job.ScannedCount = int64(len(ips))
	job.AliveCount = int64(len(aliveList))
	job.Progress = 60
	_ = s.st.UpdateScanJob(ctx, job)

	var newC, updC int64
	for _, ip := range aliveList {
		hostname := ""
		if !job.SkipReverseDNS {
			hostname = s.revLookup(ctx, ip)
		}
		created, err := s.st.UpsertAddressByAddress(ctx, store.IPAddress{
			TenantID:      job.TenantID,
			Address:       ip,
			SubnetID:      job.SubnetID,
			Hostname:      hostname,
			Status:        store.IPActive,
			AddressType:   store.AddrHost,
			LastSeen:      ptrTime(s.now()),
			HasReverseDNS: hostname != "",
		})
		if err != nil {
			s.retryOrFail(ctx, log, job, fmt.Errorf("upsert address %s: %w", ip, err))
			return
		}
		if created {
			newC++
		} else {
			updC++
		}
		var addrID string
		if a, ferr := s.st.FindAddress(ctx, job.TenantID, ip); ferr == nil {
			addrID = a.ID
		}
		s.pub.Publish(ctx, job.TenantID, events.IPAddressScanned,
			events.IPAddressPayload(events.IPAddressScanned, addrID, ip, job.SubnetID, hostname, ""))
	}
	job.NewCount = newC
	job.UpdatedCount = updC

	s.snmpPhase(ctx, log, &job, cred, aliveList)

	if s.observeCancel(ctx, log, &job) {
		return
	}

	now := s.now()
	job.Status = store.ScanCompleted
	job.Progress = 100
	job.StatusMessage = "completed"
	job.CompletedAt = &now
	if err := s.st.UpdateScanJob(ctx, job); err != nil {
		log.Warn("scan mark completed", "job", job.ID, "err", err)
		return
	}
	s.pub.Publish(ctx, job.TenantID, events.ScanCompleted,
		events.ScanPayload(job.ID, job.SubnetID, job.AliveCount, job.NewCount))
	if s.linker != nil && job.EnableSNMP {
		if err := s.linker.Correlate(ctx, job.TenantID); err != nil {
			log.Warn("port correlation", "job", job.ID, "err", err)
		}
	}
}

// effective is the outcome of resolving a subnet's SNMP credentials at job
// start: a status other than "ran" means SNMP cannot run.
type effective struct {
	status string // SNMPRan (usable) | SNMPNoCredentials | SNMPUnreadable
	source string // subnet whose credentials apply
	creds  snmp.Creds
	secret snmpcred.Secret // for scrubbing error text only
}

// effectiveCreds resolves the nearest subnet with own credentials (own or an
// ancestor, same tenant) and opens its sealed blob.
func (s *Service) effectiveCreds(ctx context.Context, tenantID, subnetID string) (effective, error) {
	idx, err := subnets.SNMPIndex(ctx, s.st, tenantID)
	if err != nil {
		return effective{}, err
	}
	_, src, ok := idx.Effective(subnetID)
	if !ok {
		return effective{status: store.SNMPNoCredentials}, nil
	}
	row, err := s.st.GetSubnetSNMP(ctx, tenantID, src.SubnetID)
	if err != nil {
		return effective{status: store.SNMPUnreadable, source: src.SubnetID}, nil
	}
	sec, err := snmpcred.Open(s.env, tenantID, row.SubnetID, row.Sealed)
	if err != nil {
		return effective{status: store.SNMPUnreadable, source: src.SubnetID}, nil
	}
	return effective{status: store.SNMPRan, source: src.SubnetID, secret: sec,
		creds: snmpcred.ToCreds(snmpcred.MetaOf(row), sec, int(s.snmpTimeout()/time.Millisecond), snmpRetries)}, nil
}

// snmpPhase records why SNMP ran or not (FR-016, SC-005) and, when it can,
// probes every live host.
func (s *Service) snmpPhase(ctx context.Context, log *slog.Logger, job *store.IPScanJob, cred effective, alive []string) {
	job.SNMPSourceSubnetID = cred.source
	switch {
	case !job.EnableSNMP:
		job.SNMPStatus = store.SNMPNotRequested
	case cred.status != store.SNMPRan:
		job.SNMPStatus = cred.status
	case len(alive) == 0:
		job.SNMPStatus = store.SNMPNoLiveHosts
	default:
		job.SNMPStatus = store.SNMPRan
		s.discoverSNMP(ctx, log, job, cred, alive)
	}
}

// discoverSNMP probes the live hosts with the opened credentials, persisting
// each discovered device with its interfaces and L2 links and counting the
// hosts that did not answer or rejected the credentials. SNMP is
// best-effort: a silent host is counted, not fatal.
func (s *Service) discoverSNMP(ctx context.Context, log *slog.Logger, job *store.IPScanJob, cred effective, alive []string) {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		warned int
		other  int
		sem    = make(chan struct{}, snmpWorkers)
	)
	for _, ip := range alive {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(ip string) {
			defer func() { <-sem; wg.Done() }()
			dctx, cancel := context.WithTimeout(ctx, s.snmpDeadline()+s.snmpWalkBudget())
			dev, err := s.snmp.Discover(dctx, ip, cred.creds)
			cancel()
			mu.Lock()
			defer mu.Unlock()
			job.SNMPProbed++
			if err != nil {
				switch o := snmp.Classify(err); {
				case o == snmp.OutcomeNoResponse:
					job.SNMPNoAnswer++
				case o.Rejected():
					job.SNMPRejected++
					if warned < snmpLogCap {
						warned++
						log.Warn("scan snmp credentials rejected", "job", job.ID, "ip", ip, "outcome", string(o))
					}
				default:
					other++
					if warned < snmpLogCap {
						warned++
						log.Warn("scan snmp host error", "job", job.ID, "ip", ip, "err", snmpcred.Scrub(err.Error(), cred.secret))
					}
				}
				return
			}
			if err := s.persistDevice(ctx, job.TenantID, ip, dev); err != nil {
				log.Warn("scan persist device", "job", job.ID, "ip", ip, "err", err)
				return
			}
			job.SNMPDiscoveredCount++
		}(ip)
	}
	wg.Wait()
	log.Info("scan snmp phase", "job", job.ID, "probed", job.SNMPProbed, "discovered", job.SNMPDiscoveredCount,
		"no_answer", job.SNMPNoAnswer, "rejected", job.SNMPRejected, "other_errors", other,
		"version", cred.creds.Version, "auth", cred.creds.AuthProtocol, "priv", cred.creds.PrivProtocol,
		"timeout_ms", s.snmpTimeout().Milliseconds())
}

// snmpWalkBudget bounds the table walks after a host answered (interfaces,
// FDB, LLDP) so one slow device cannot stall the phase.
func (s *Service) snmpWalkBudget() time.Duration { return 60 * time.Second }

// persistDevice upserts a discovered device, its interfaces and their links.
func (s *Service) persistDevice(ctx context.Context, tenantID, ip string, dev snmp.DiscoveredDevice) error {
	d, err := s.st.UpsertDeviceByName(ctx, store.Device{
		TenantID:     tenantID,
		Name:         deviceName(dev, ip),
		DeviceType:   dev.DeviceType,
		Manufacturer: dev.Manufacturer,
		Model:        dev.Model,
		OSVersion:    dev.OSVersion,
		ManagementIP: ip,
		Status:       store.DevStActive,
		LastSeen:     ptrTime(s.now()),
	})
	if err != nil {
		return err
	}
	for _, iface := range dev.Interfaces {
		si, err := s.st.UpsertInterfaceByName(ctx, store.DeviceInterface{
			TenantID:      tenantID,
			DeviceID:      d.ID,
			Name:          ifaceName(iface),
			MACAddress:    iface.MAC,
			InterfaceType: iface.Type,
			IfIndex:       iface.IfIndex,
			SpeedMbps:     iface.Speed,
			Enabled:       true,
		})
		if err != nil {
			return err
		}
		var links []store.DeviceInterfaceLink
		for _, l := range dev.Links {
			if l.IfIndex != iface.IfIndex {
				continue
			}
			links = append(links, store.DeviceInterfaceLink{
				TenantID:       tenantID,
				InterfaceID:    si.ID,
				RemotePortName: l.RemotePort,
				LinkSource:     l.Source,
				LinkVlan:       l.VLAN,
			})
		}
		if len(links) > 0 {
			if err := s.st.ReplaceInterfaceLinks(ctx, tenantID, si.ID, links); err != nil {
				return err
			}
		}
	}
	return nil
}

// retryOrFail records a job failure: it schedules a backed-off retry (status
// pending, next_retry_at set) until max_retries is exhausted, then fails.
func (s *Service) retryOrFail(ctx context.Context, log *slog.Logger, job store.IPScanJob, cause error) {
	// Do not resurrect a job that was cancelled underneath us.
	if latest, err := s.st.GetScanJob(ctx, job.TenantID, job.ID); err == nil && latest.Status == store.ScanCancelled {
		return
	}
	job.RetryCount++
	now := s.now()
	if job.RetryCount > s.cfg.MaxRetries {
		job.Status = store.ScanFailed
		job.StatusMessage = "failed: " + cause.Error()
		job.CompletedAt = &now
		if err := s.st.UpdateScanJob(ctx, job); err != nil {
			log.Warn("scan mark failed", "job", job.ID, "err", err)
		}
		log.Warn("scan job failed", "job", job.ID, "retries", job.RetryCount-1, "err", cause)
		return
	}
	backoff := time.Duration(int64(1)<<uint(job.RetryCount-1)) * baseBackoff
	next := now.Add(backoff)
	job.Status = store.ScanPending
	job.NextRetryAt = &next
	job.StatusMessage = "retry scheduled: " + cause.Error()
	if err := s.st.UpdateScanJob(ctx, job); err != nil {
		log.Warn("scan schedule retry", "job", job.ID, "err", err)
	}
}

// observeCancel reloads the job and, if it was cancelled, stops the pipeline. It
// returns true when the caller should abort. A cancelled job is left as-is.
func (s *Service) observeCancel(ctx context.Context, _ *slog.Logger, job *store.IPScanJob) bool {
	cur, err := s.st.GetScanJob(ctx, job.TenantID, job.ID)
	if err != nil {
		return false
	}
	return cur.Status == store.ScanCancelled
}

// timeout is the per-host probe timeout.
// snmpRetries is the per-request SNMP retry count; snmpWorkers bounds the
// hosts probed in parallel during the SNMP phase (v3 parity: 10).
const (
	snmpRetries = 1
	snmpWorkers = 10
	// snmpLogCap bounds the per-job warnings for unexpected SNMP errors.
	snmpLogCap = 5
)

// snmpTimeout is the per-request SNMP timeout (scan.snmp_timeout_ms).
func (s *Service) snmpTimeout() time.Duration {
	if s.cfg.SNMPTimeoutMs <= 0 {
		return 5 * time.Second
	}
	return time.Duration(s.cfg.SNMPTimeoutMs) * time.Millisecond
}

// snmpDeadline bounds one SNMP operation: every attempt plus a margin.
func (s *Service) snmpDeadline() time.Duration {
	return time.Duration(snmpRetries+1)*s.snmpTimeout() + testMargin
}

func (s *Service) timeout() time.Duration {
	if s.cfg.TimeoutMs <= 0 {
		return time.Second
	}
	return time.Duration(s.cfg.TimeoutMs) * time.Millisecond
}

// enumerateTargets returns the subnet's usable host addresses, bounded to
// maxHosts. ipnet.Hosts already excludes the network and broadcast addresses and
// caps huge ranges; the extra bound confines a job to at most maxHosts probes.
func enumerateTargets(cidr string, maxHosts int) ([]net.IP, error) {
	hosts, err := ipnet.Hosts(cidr)
	if err != nil {
		return nil, err
	}
	if maxHosts > 0 && len(hosts) > maxHosts {
		hosts = hosts[:maxHosts]
	}
	return hosts, nil
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}

func deviceName(dev snmp.DiscoveredDevice, ip string) string {
	if dev.SysName != "" {
		return dev.SysName
	}
	return "device-" + ip
}

func ifaceName(iface snmp.Interface) string {
	if iface.Name != "" {
		return iface.Name
	}
	return fmt.Sprintf("if-%d", iface.IfIndex)
}

func ptrTime(t time.Time) *time.Time { return &t }
