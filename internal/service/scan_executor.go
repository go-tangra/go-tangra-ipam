package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	appViewer "github.com/go-tangra/go-tangra-common/viewer"
	"github.com/go-tangra/go-tangra-ipam/internal/biz"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipscanjob"
	"github.com/go-tangra/go-tangra-ipam/internal/event"
)

const (
	// Default configuration values
	defaultWorkerCount       = 3
	defaultMaxRetries        = 3
	defaultRetryDelaySeconds = 60
	defaultBackoffMultiplier = 2.0
	defaultCleanupDays       = 30
	defaultPollInterval      = 5 * time.Second

	// staleScanTimeout is how long a SCANNING job may go without a progress
	// update before the reaper treats it as stalled and fails it. Scans of a
	// valid (size-capped) subnet finish well within this, and a healthy scan
	// bumps update_time as it progresses, so only genuinely hung jobs are hit.
	staleScanTimeout = 30 * time.Minute
	// reaperInterval is how often the periodic stale-scan sweep runs.
	reaperInterval = 5 * time.Minute
)

// ScanExecutorConfig holds configuration for the scan executor
type ScanExecutorConfig struct {
	WorkerCount            int32
	MaxRetries             int32
	RetryDelaySeconds      int32
	RetryBackoffMultiplier float64
	CleanupDays            int32
}

// ScanExecutor is a background worker that processes scan jobs
type ScanExecutor struct {
	log                 *log.Helper
	scanJobRepo         *data.IpScanJobRepo
	subnetRepo          *data.SubnetRepo
	ipAddressRepo       *data.IpAddressRepo
	deviceRepo          *data.DeviceRepo
	deviceInterfaceRepo *data.DeviceInterfaceRepo
	publisher           *event.Publisher
	config              ScanExecutorConfig

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
	mu      sync.Mutex
}

// NewScanExecutor creates a new ScanExecutor
func NewScanExecutor(
	ctx *bootstrap.Context,
	scanJobRepo *data.IpScanJobRepo,
	subnetRepo *data.SubnetRepo,
	ipAddressRepo *data.IpAddressRepo,
	deviceRepo *data.DeviceRepo,
	deviceInterfaceRepo *data.DeviceInterfaceRepo,
	publisher *event.Publisher,
) *ScanExecutor {
	// Use default config
	config := ScanExecutorConfig{
		WorkerCount:            defaultWorkerCount,
		MaxRetries:             defaultMaxRetries,
		RetryDelaySeconds:      defaultRetryDelaySeconds,
		RetryBackoffMultiplier: defaultBackoffMultiplier,
		CleanupDays:            defaultCleanupDays,
	}

	return &ScanExecutor{
		log:                 ctx.NewLoggerHelper("ipam/scan-executor"),
		scanJobRepo:         scanJobRepo,
		subnetRepo:          subnetRepo,
		ipAddressRepo:       ipAddressRepo,
		deviceRepo:          deviceRepo,
		deviceInterfaceRepo: deviceInterfaceRepo,
		publisher:           publisher,
		config:              config,
	}
}

// Start starts the scan executor
func (e *ScanExecutor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return nil
	}

	// Use system viewer context (bypasses tenant privacy checks)
	baseCtx := appViewer.NewSystemViewerContext(context.Background())
	e.ctx, e.cancel = context.WithCancel(baseCtx)
	e.running = true

	workerCount := e.config.WorkerCount
	if workerCount <= 0 {
		workerCount = defaultWorkerCount
	}

	e.log.Infof("Starting scan executor with %d workers", workerCount)

	// Reap scans orphaned by a previous run. No worker is live yet, so any job
	// still marked SCANNING was interrupted (typically a service restart
	// mid-scan) and would otherwise block every new scan of its subnet via the
	// HasActiveScan check ("scan already in progress"). Done synchronously
	// before workers start so there is no race with a worker claiming a job.
	if n, err := e.scanJobRepo.ReapStaleScanning(e.ctx, time.Now()); err != nil {
		e.log.Errorf("startup reap of orphaned scans failed: %v", err)
	} else if n > 0 {
		e.log.Infof("Reaped %d orphaned scan job(s) left SCANNING by a previous run", n)
	}

	// Start worker goroutines
	for i := int32(0); i < workerCount; i++ {
		e.wg.Add(1)
		go e.worker(i)
	}

	// Start cleanup goroutine
	e.wg.Add(1)
	go e.cleanupWorker()

	// Start the stale-scan reaper (handles hung workers while running)
	e.wg.Add(1)
	go e.reaperWorker()

	// One-time backfill: promote agent-reported metadata interfaces to rows so
	// previously-collected server data participates in link discovery.
	e.wg.Add(1)
	go e.backfillMetadataInterfaces()

	return nil
}

// backfillMetadataInterfaces promotes agent-reported metadata interfaces into
// interface rows for existing devices. tangra-client reports NICs inside device
// metadata JSON rather than as interface records; without this, their MACs are
// invisible to link correlation until the agent next re-syncs (which only
// happens on a hardware change). Servers and VMs are both processed: server NICs
// feed SNMP bridge-FDB correlation, VM NICs feed hypervisor hosted-VM
// correlation. After materializing, hosted-VM links are (re)built so guests get
// their "Connected To" host without every guest having to re-sync. Idempotent.
func (e *ScanExecutor) backfillMetadataInterfaces() {
	defer e.wg.Done()

	e.backfillTypeInterfaces(data.DeviceTypeServer, "server")
	e.backfillTypeInterfaces(data.DeviceTypeVM, "vm")
	e.backfillHostedVMLinks()
}

// backfillTypeInterfaces materializes metadata NICs for all devices of one type.
func (e *ScanExecutor) backfillTypeInterfaces(deviceType int32, label string) {
	const pageSize = 100
	afterID := ""
	devicesTouched, ifacesApplied := 0, 0
	for {
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		devices, err := e.deviceRepo.ListByTypeWithMetadataCursor(e.ctx, deviceType, afterID, pageSize)
		if err != nil {
			e.log.Warnf("interface backfill (%s): list failed: %v", label, err)
			return
		}
		if len(devices) == 0 {
			break
		}
		for _, d := range devices {
			if n := materializeMetadataInterfaces(e.ctx, e.deviceInterfaceRepo, e.log, d.ID, d.Metadata); n > 0 {
				devicesTouched++
				ifacesApplied += n
			}
			afterID = d.ID
		}
		if len(devices) < pageSize {
			break
		}
	}
	if ifacesApplied > 0 {
		e.log.Infof("interface backfill (%s): materialized %d interfaces across %d devices", label, ifacesApplied, devicesTouched)
	}
}

// backfillHostedVMLinks re-runs hosted-VM correlation for every hypervisor host
// (servers carrying a hosted_vms metadata list), linking their guests now that
// guest interfaces are materialized. correlateHostedVMs is a no-op for servers
// without hosted_vms.
func (e *ScanExecutor) backfillHostedVMLinks() {
	const pageSize = 100
	afterID := ""
	linked := 0
	for {
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		devices, err := e.deviceRepo.ListByTypeWithMetadataCursor(e.ctx, data.DeviceTypeServer, afterID, pageSize)
		if err != nil {
			e.log.Warnf("hosted-vm backfill: list failed: %v", err)
			return
		}
		if len(devices) == 0 {
			break
		}
		for _, d := range devices {
			linked += correlateHostedVMs(e.ctx, e.deviceInterfaceRepo, e.log, d.ID, derefTenantID(d.TenantID), d.Metadata)
			afterID = d.ID
		}
		if len(devices) < pageSize {
			break
		}
	}
	if linked > 0 {
		e.log.Infof("hosted-vm backfill: linked %d guest interfaces to their hosts", linked)
	}
}

// Stop stops the scan executor
func (e *ScanExecutor) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	e.log.Info("Stopping scan executor")
	e.cancel()
	e.wg.Wait()
	e.running = false

	return nil
}

// worker is a long-running worker goroutine
func (e *ScanExecutor) worker(id int32) {
	defer e.wg.Done()

	e.log.Infof("Scan worker %d started", id)

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			e.log.Infof("Scan worker %d stopped", id)
			return
		case <-ticker.C:
			e.processJobs()
		}
	}
}

// processJobs processes pending and retryable jobs
func (e *ScanExecutor) processJobs() {
	// Process pending jobs
	pendingJobs, err := e.scanJobRepo.ListPending(e.ctx, 10)
	if err != nil {
		e.log.Errorf("Failed to list pending scan jobs: %v", err)
		return
	}

	for _, job := range pendingJobs {
		// Check if cancelled
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		// Atomically claim the job
		claimed, err := e.scanJobRepo.ClaimJob(e.ctx, job.ID, ipscanjob.StatusPENDING)
		if err != nil {
			e.log.Errorf("Failed to claim scan job %s: %v", job.ID, err)
			continue
		}
		if !claimed {
			// Job already claimed by another worker
			continue
		}

		if err := e.processJob(job); err != nil {
			e.log.Errorf("Failed to process scan job %s: %v", job.ID, err)
		}
	}

	// Process retryable jobs
	retryJobs, err := e.scanJobRepo.ListRetryable(e.ctx, 10)
	if err != nil {
		e.log.Errorf("Failed to list retryable scan jobs: %v", err)
		return
	}

	for _, job := range retryJobs {
		// Check if cancelled
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		// Atomically claim the job
		claimed, err := e.scanJobRepo.ClaimJob(e.ctx, job.ID, ipscanjob.StatusFAILED)
		if err != nil {
			e.log.Errorf("Failed to claim retry scan job %s: %v", job.ID, err)
			continue
		}
		if !claimed {
			continue
		}

		if err := e.processJob(job); err != nil {
			e.log.Errorf("Failed to process retry scan job %s: %v", job.ID, err)
		}
	}
}

// processJob processes a single scan job
func (e *ScanExecutor) processJob(job *ent.IpScanJob) error {
	e.log.Infof("Processing scan job %s for subnet %s", job.ID, job.SubnetID)

	// Get the subnet
	subnet, err := e.subnetRepo.GetByID(e.ctx, job.SubnetID)
	if err != nil {
		return e.failJob(job, "Failed to get subnet: "+err.Error())
	}
	if subnet == nil {
		return e.failJob(job, "Subnet not found")
	}

	// Get DNS servers from subnet if configured
	var dnsServers []string
	var dnsTimeoutMs int32 = 5000
	var useSystemDNSFallback = true

	jobTenantID := uint32(0)
	if job.TenantID != nil {
		jobTenantID = *job.TenantID
	}

	if !job.SkipReverseDNS && subnet.DNSServers != "" {
		// Parse DNS servers from subnet (comma-separated)
		for _, server := range strings.Split(subnet.DNSServers, ",") {
			server = strings.TrimSpace(server)
			if server != "" {
				dnsServers = append(dnsServers, server)
			}
		}
	}

	// Create scanner with job configuration
	scanConfig := biz.ScanConfig{
		TimeoutMs:            job.TimeoutMs,
		Concurrency:          job.Concurrency,
		SkipReverseDNS:       job.SkipReverseDNS,
		TCPProbePorts:        job.TCPProbePorts,
		DNSServers:           dnsServers,
		DNSTimeoutMs:         dnsTimeoutMs,
		UseSystemDNSFallback: useSystemDNSFallback,
	}
	scanner := biz.NewScanner(scanConfig)

	// Progress callback
	progressCb := func(progress biz.ScanProgress) {
		if err := e.scanJobRepo.UpdateProgress(
			e.ctx,
			job.ID,
			progress.ScannedCount,
			progress.AliveCount,
			progress.NewCount,
			progress.UpdatedCount,
			progress.Progress,
			"Scanning...",
		); err != nil {
			e.log.Warnf("Failed to update scan progress for job %s: %v", job.ID, err)
		}
	}

	// Execute the scan
	e.log.Infof("Starting scan for subnet %s (CIDR: %s)", subnet.ID, subnet.Cidr)
	results, err := scanner.ScanSubnet(e.ctx, subnet.Cidr, progressCb)
	if err != nil {
		if e.ctx.Err() != nil {
			// Cancelled
			if _, statusErr := e.scanJobRepo.UpdateStatus(e.ctx, job.ID, ipscanjob.StatusCANCELLED, "Cancelled", job.Progress); statusErr != nil {
				e.log.Warnf("Failed to update cancelled status for job %s: %v", job.ID, statusErr)
			}
			return nil
		}

		// Check for retry
		if job.RetryCount < job.MaxRetries {
			return e.scheduleRetry(job, err.Error())
		}
		return e.failJob(job, err.Error())
	}

	// Process results - create/update IP addresses
	var newCount, updatedCount int64

	for _, result := range results {
		if !result.Alive {
			continue
		}

		// Check if IP already exists
		existing, err := e.ipAddressRepo.GetByAddress(e.ctx, jobTenantID, result.Address)
		if err != nil {
			e.log.Warnf("Failed to check existing IP %s: %v", result.Address, err)
			continue
		}

		if existing != nil {
			// Update existing record
			updates := map[string]interface{}{
				"last_seen": time.Now(),
			}
			if result.Hostname != "" {
				if existing.Hostname != result.Hostname {
					updates["hostname"] = result.Hostname
				}
				updates["has_reverse_dns"] = true
			}
			_, err := e.ipAddressRepo.Update(e.ctx, existing.ID, updates)
			if err != nil {
				e.log.Warnf("Failed to update IP %s: %v", result.Address, err)
			} else {
				updatedCount++
				// DNS sync: ensure records exist for every discovered host that
				// has a hostname — not only newly-created ones. If the hostname
				// changed, move the record (drop the previous name); otherwise
				// idempotently upsert the current name's A + PTR.
				if job.EnableDNSUpdate && e.publisher != nil && result.Hostname != "" {
					if existing.Hostname != "" && existing.Hostname != result.Hostname {
						e.publisher.PublishIPAddressUpdated(e.ctx, &event.IPAddressEvent{
							IPAddressID: existing.ID, TenantID: jobTenantID, Address: result.Address,
							Hostname: result.Hostname, OldHostname: existing.Hostname, SubnetID: job.SubnetID,
						})
					} else {
						e.publisher.PublishIPAddressScanned(e.ctx, &event.IPAddressEvent{
							IPAddressID: existing.ID, TenantID: jobTenantID, Address: result.Address,
							Hostname: result.Hostname, SubnetID: job.SubnetID,
						})
					}
				}
			}
		} else {
			// Create new record
			opts := []func(*ent.IpAddressCreate){}
			if result.Hostname != "" {
				opts = append(opts, func(c *ent.IpAddressCreate) {
					c.SetHostname(result.Hostname)
					c.SetHasReverseDNS(true)
				})
			}
			opts = append(opts, func(c *ent.IpAddressCreate) {
				c.SetDescription("Auto-discovered by network scan")
			})
			opts = append(opts, func(c *ent.IpAddressCreate) {
				c.SetLastSeen(time.Now())
			})

			created, err := e.ipAddressRepo.Create(e.ctx, jobTenantID, result.Address, job.SubnetID, opts...)
			if err != nil {
				e.log.Warnf("Failed to create IP %s: %v", result.Address, err)
			} else {
				newCount++
				// DNS sync: newly discovered host with a hostname.
				if job.EnableDNSUpdate && e.publisher != nil && result.Hostname != "" {
					id := result.Address
					if created != nil {
						id = created.ID
					}
					e.publisher.PublishIPAddressScanned(e.ctx, &event.IPAddressEvent{
						IPAddressID: id, TenantID: jobTenantID, Address: result.Address,
						Hostname: result.Hostname, SubnetID: job.SubnetID,
					})
				}
			}
		}
	}

	// Calculate alive count
	var aliveCount int64
	var aliveIPs []string
	for _, r := range results {
		if r.Alive {
			aliveCount++
			aliveIPs = append(aliveIPs, r.Address)
		}
	}

	// SNMP discovery phase
	var snmpDiscovered int64
	e.log.Infof("SNMP check for job %s: enableSnmp=%v, aliveIPs=%d, subnet.SnmpVersion=%d",
		job.ID, job.EnableSnmp, len(aliveIPs), subnet.SnmpVersion)

	if !job.EnableSnmp {
		e.log.Infof("SNMP discovery skipped for job %s: enable_snmp is false", job.ID)
	} else if len(aliveIPs) == 0 {
		e.log.Infof("SNMP discovery skipped for job %s: no alive hosts found in ICMP scan", job.ID)
	} else if subnet.SnmpVersion <= 0 {
		e.log.Warnf("SNMP discovery skipped for job %s: subnet %s has no SNMP configuration (snmp_version=%d). "+
			"Configure SNMP credentials on the subnet first.", job.ID, subnet.ID, subnet.SnmpVersion)
	}

	if job.EnableSnmp && len(aliveIPs) > 0 && subnet.SnmpVersion > 0 {
		snmpConfig := biz.SNMPConfig{
			Version:      int(subnet.SnmpVersion),
			Community:    subnet.SnmpCommunity,
			User:         subnet.SnmpUser,
			AuthPassword: subnet.SnmpAuthPassword,
			PrivPassword: subnet.SnmpPrivPassword,
			AuthProtocol: subnet.SnmpAuthProtocol,
			PrivProtocol: subnet.SnmpPrivProtocol,
		}

		e.log.Infof("Starting SNMP discovery for job %s: version=%d, community=%q, user=%q, authProto=%q, privProto=%q, targets=%d",
			job.ID, snmpConfig.Version,
			maskString(snmpConfig.Community),
			snmpConfig.User,
			snmpConfig.AuthProtocol,
			snmpConfig.PrivProtocol,
			len(aliveIPs))

		if err := e.scanJobRepo.UpdateProgress(e.ctx, job.ID, int64(len(results)), aliveCount, newCount, updatedCount, 80, "SNMP discovery..."); err != nil {
			e.log.Warnf("Failed to update SNMP progress for job %s: %v", job.ID, err)
		}

		snmpResults, snmpErr := biz.ScanSubnetSNMP(e.ctx, e.log, aliveIPs, snmpConfig, 10, func(scanned, discovered int) {
			// Progress callback for SNMP phase
		})
		if snmpErr != nil {
			e.log.Errorf("SNMP scan failed for job %s: %v", job.ID, snmpErr)
		} else {
			snmpDiscovered = int64(len(snmpResults))
			e.log.Infof("SNMP discovery completed for job %s: %d devices found out of %d alive hosts",
				job.ID, snmpDiscovered, len(aliveIPs))
			for i, r := range snmpResults {
				e.log.Infof("  SNMP device [%d]: address=%s, sysName=%q, manufacturer=%q, model=%q, interfaces=%d",
					i+1, r.Address, r.SysName, r.Manufacturer, r.Model, len(r.Interfaces))
			}
			e.processSNMPResults(jobTenantID, job.SubnetID, snmpResults)
		}

		if err := e.scanJobRepo.UpdateSNMPCount(e.ctx, job.ID, snmpDiscovered); err != nil {
			e.log.Warnf("Failed to update SNMP count for job %s: %v", job.ID, err)
		}
	}

	// Update final progress
	if err := e.scanJobRepo.UpdateProgress(
		e.ctx,
		job.ID,
		int64(len(results)),
		aliveCount,
		newCount,
		updatedCount,
		100,
		"Completed",
	); err != nil {
		e.log.Warnf("Failed to update final progress for job %s: %v", job.ID, err)
	}

	// Mark as completed
	message := fmt.Sprintf("Scan completed: %d alive hosts", aliveCount)
	if snmpDiscovered > 0 {
		message = fmt.Sprintf("Scan completed: %d alive hosts, %d SNMP devices", aliveCount, snmpDiscovered)
	}
	_, err = e.scanJobRepo.UpdateStatus(e.ctx, job.ID, ipscanjob.StatusCOMPLETED, message, 100)
	if err != nil {
		return err
	}

	e.log.Infof("Scan job %s completed: %d scanned, %d alive, %d new, %d updated, %d SNMP devices",
		job.ID, len(results), aliveCount, newCount, updatedCount, snmpDiscovered)

	return nil
}

// failJob marks a job as failed
func (e *ScanExecutor) failJob(job *ent.IpScanJob, message string) error {
	e.log.Errorf("Scan job %s failed: %s", job.ID, message)
	_, err := e.scanJobRepo.UpdateStatus(e.ctx, job.ID, ipscanjob.StatusFAILED, message, job.Progress)
	return err
}

// scheduleRetry schedules a job for retry with exponential backoff
func (e *ScanExecutor) scheduleRetry(job *ent.IpScanJob, message string) error {
	delay := float64(e.config.RetryDelaySeconds) * float64(time.Second)
	multiplier := e.config.RetryBackoffMultiplier
	if multiplier <= 0 {
		multiplier = defaultBackoffMultiplier
	}

	// Exponential backoff
	for i := int32(0); i < job.RetryCount; i++ {
		delay *= multiplier
	}

	nextRetry := time.Now().Add(time.Duration(delay))

	e.log.Infof("Scheduling scan job %s for retry at %v (attempt %d/%d): %s",
		job.ID, nextRetry, job.RetryCount+1, job.MaxRetries, message)

	_, err := e.scanJobRepo.MarkForRetry(e.ctx, job.ID, nextRetry)
	return err
}

// cleanupWorker periodically cleans up old jobs
func (e *ScanExecutor) cleanupWorker() {
	defer e.wg.Done()

	// Run cleanup once per day
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Initial cleanup
	e.runCleanup()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.runCleanup()
		}
	}
}

// reaperWorker periodically fails SCANNING jobs that have stopped making
// progress (a hung or wedged worker). It complements the one-shot startup reap,
// which handles jobs orphaned by a restart; together they ensure a stuck job
// never blocks new scans of its subnet indefinitely.
func (e *ScanExecutor) reaperWorker() {
	defer e.wg.Done()

	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-staleScanTimeout)
			n, err := e.scanJobRepo.ReapStaleScanning(e.ctx, cutoff)
			if err != nil {
				e.log.Errorf("periodic reap of stale scans failed: %v", err)
			} else if n > 0 {
				e.log.Warnf("Reaped %d stalled scan job(s) with no progress for over %s", n, staleScanTimeout)
			}

			// Clear hypervisor "Connected To" links whose host has stopped
			// reporting entirely (e.g. a decommissioned hypervisor). Hosts that
			// are still syncing prune their own dropped guests authoritatively
			// in correlateHostedVMs and refresh link_last_seen each pass, so this
			// only catches links no live host owns anymore.
			hvCutoff := time.Now().Add(-hypervisorLinkStaleAfter)
			if hv, err := e.deviceInterfaceRepo.PruneStaleFlatLinks(e.ctx, linkSourceHypervisor, hvCutoff); err != nil {
				e.log.Errorf("periodic prune of stale hypervisor links failed: %v", err)
			} else if hv > 0 {
				e.log.Warnf("Cleared %d stale hypervisor link(s) not refreshed in over %s", hv, hypervisorLinkStaleAfter)
			}
		}
	}
}

// runCleanup removes old completed/failed/cancelled jobs
func (e *ScanExecutor) runCleanup() {
	days := e.config.CleanupDays
	if days <= 0 {
		days = defaultCleanupDays
	}

	deleted, err := e.scanJobRepo.CleanupOld(e.ctx, int(days))
	if err != nil {
		e.log.Errorf("Failed to cleanup old scan jobs: %v", err)
		return
	}

	if deleted > 0 {
		e.log.Infof("Cleaned up %d old scan jobs", deleted)
	}
}

// processSNMPResults creates or updates devices and their interfaces from SNMP scan results
func (e *ScanExecutor) processSNMPResults(tenantID uint32, _ string, results []biz.SNMPDeviceInfo) {
	e.log.Infof("Processing %d SNMP device results for tenant %d", len(results), tenantID)

	var switchScans []switchFDB

	for _, info := range results {
		// Try to find existing device by primary IP
		e.log.Debugf("Looking up device by primary IP: %s", info.Address)
		existing, err := e.deviceRepo.GetByPrimaryIP(e.ctx, tenantID, info.Address)
		if err != nil {
			e.log.Warnf("Failed to lookup device by IP %s: %v", info.Address, err)
			continue
		}

		// If not found by IP, try by name
		if existing == nil && info.SysName != "" {
			e.log.Debugf("Device not found by IP %s, trying by name %q", info.Address, info.SysName)
			existing, err = e.deviceRepo.GetByTenantAndName(e.ctx, tenantID, info.SysName)
			if err != nil {
				e.log.Warnf("Failed to lookup device by name %s: %v", info.SysName, err)
			}
		}

		var deviceID string

		if existing != nil {
			// Update existing device
			deviceID = existing.ID
			e.log.Infof("Updating existing device %s (%s) from SNMP data at %s", existing.Name, deviceID, info.Address)
			updates := map[string]interface{}{
				"last_seen": time.Now(),
			}
			if info.SysDescr != "" {
				updates["description"] = info.SysDescr
			}
			if info.Manufacturer != "" {
				updates["manufacturer"] = info.Manufacturer
			}
			if info.Model != "" {
				updates["model"] = info.Model
			}
			if info.OsVersion != "" {
				updates["os_version"] = info.OsVersion
			}
			if info.DeviceType > 0 && info.DeviceType != 99 {
				updates["device_type"] = info.DeviceType
			}
			if info.SysContact != "" {
				updates["contact"] = info.SysContact
			}
			if info.SysLocation != "" && existing.LocationID == "" {
				updates["notes"] = "SNMP Location: " + info.SysLocation
			}
			_, err = e.deviceRepo.Update(e.ctx, deviceID, updates)
			if err != nil {
				e.log.Warnf("Failed to update device %s: %v", deviceID, err)
			} else {
				e.log.Infof("Device %s updated successfully with %d fields", deviceID, len(updates))
			}
		} else {
			// Create new device
			name := info.SysName
			if name == "" {
				name = info.Address
			}
			opts := []func(*ent.DeviceCreate){
				func(c *ent.DeviceCreate) { c.SetPrimaryIP(info.Address) },
				func(c *ent.DeviceCreate) { c.SetDeviceType(info.DeviceType) },
				func(c *ent.DeviceCreate) { c.SetLastSeen(time.Now()) },
			}
			if info.SysDescr != "" {
				opts = append(opts, func(c *ent.DeviceCreate) { c.SetDescription(info.SysDescr) })
			}
			if info.Manufacturer != "" {
				opts = append(opts, func(c *ent.DeviceCreate) { c.SetManufacturer(info.Manufacturer) })
			}
			if info.Model != "" {
				opts = append(opts, func(c *ent.DeviceCreate) { c.SetModel(info.Model) })
			}
			if info.OsVersion != "" {
				opts = append(opts, func(c *ent.DeviceCreate) { c.SetOsVersion(info.OsVersion) })
			}
			if info.SysContact != "" {
				opts = append(opts, func(c *ent.DeviceCreate) { c.SetContact(info.SysContact) })
			}

			e.log.Infof("Creating new device from SNMP: name=%q, ip=%s, type=%d, manufacturer=%q",
				name, info.Address, info.DeviceType, info.Manufacturer)
			newDevice, err := e.deviceRepo.Create(e.ctx, tenantID, name, opts...)
			if err != nil {
				e.log.Errorf("Failed to create device for %s: %v", info.Address, err)
				continue
			}
			deviceID = newDevice.ID
			e.log.Infof("Device created successfully: id=%s, name=%s", deviceID, name)
		}

		// Link IP address to device
		if deviceID != "" {
			ipAddr, err := e.ipAddressRepo.GetByAddress(e.ctx, tenantID, info.Address)
			if err == nil && ipAddr != nil {
				_, _ = e.ipAddressRepo.Update(e.ctx, ipAddr.ID, map[string]interface{}{
					"device_id": deviceID,
				})
			}
		}

		// Sync interfaces
		e.syncDeviceInterfaces(deviceID, info.Interfaces)

		// Collect switch forwarding databases for a single global correlation
		// pass once all devices/interfaces in this scan are persisted.
		if info.DeviceType == deviceTypeSwitch && len(info.ForwardingDB) > 0 {
			switchScans = append(switchScans, switchFDB{deviceID: deviceID, fdb: info.ForwardingDB})
		}
	}

	e.correlateLinks(tenantID, switchScans)
}

const (
	// deviceTypeSwitch mirrors ipam_devices.device_type for a switch.
	deviceTypeSwitch = 4

	// linkSourceSNMPFDB labels links discovered from the bridge forwarding DB.
	linkSourceSNMPFDB = "snmp_fdb"

	// fdbEdgePortMaxMACs is the most distinct MACs a switch port may have learned
	// for it to still be treated as an edge (access) port directly attached to a
	// host. Ports above this are uplinks/trunks carrying many MACs, where a host
	// MAC is seen transitively rather than directly — those are not linked.
	fdbEdgePortMaxMACs = 16

	// linkStaleAfter is how long a discovered link survives without being seen in
	// any FDB before it is pruned. Generous so partial (single-subnet) scans that
	// omit a switch don't evict that switch's still-valid link prematurely.
	linkStaleAfter = 14 * 24 * time.Hour

	// hypervisorLinkStaleAfter is how long a guest's "Connected To" hypervisor
	// link survives without its host re-reporting it (link_last_seen heartbeat)
	// before the reaper clears it. A live host refreshes its guests every sync
	// (~hourly by default), so this is generous enough to tolerate a host being
	// briefly down while still clearing links owned by hosts that are gone for
	// good. A guest that merely migrated is re-pointed immediately by its new
	// host; this only matters when no host claims the guest at all.
	hypervisorLinkStaleAfter = 3 * 24 * time.Hour
)

// switchFDB pairs a discovered switch with its bridge forwarding database for
// the global link-correlation pass.
type switchFDB struct {
	deviceID string
	fdb      []biz.FDBEntry
}

// correlateLinks discovers server -> switch-port links across ALL switches in a
// scan at once.
//
// A host MAC appears on its access switch's edge port (few MACs) AND on a core
// switch's uplink port (many MACs). For each switch we keep that switch's most
// direct (fewest-MAC) edge port for the MAC, and persist ONE link per switch.
// This is what lets an LACP bond across an MLAG pair show up as connected to
// BOTH switches: the same server MAC is learned on an edge port of each, so we
// record a link to each rather than letting one switch overwrite the other.
//
// The flat remote_* columns on the interface are also set to the single globally
// best link, for backward compatibility and clients that don't read the link
// list.
func (e *ScanExecutor) correlateLinks(tenantID uint32, switches []switchFDB) {
	type candidate struct {
		switchID string
		port     *ent.DeviceInterface
		macCount int
		vlan     int
	}
	// mac -> switchID -> best edge port on that switch.
	perMAC := make(map[string]map[string]candidate)

	for _, sw := range switches {
		ports, err := e.deviceInterfaceRepo.ListByDeviceID(e.ctx, sw.deviceID)
		if err != nil {
			e.log.Warnf("Link correlation: failed to list ports for switch %s: %v", sw.deviceID, err)
			continue
		}
		portByIf := make(map[int32]*ent.DeviceInterface)
		for _, p := range ports {
			if p.IfIndex != nil {
				portByIf[*p.IfIndex] = p
			}
		}
		if len(portByIf) == 0 {
			continue
		}

		for mac, ap := range biz.RankAccessPorts(sw.fdb) {
			port, ok := portByIf[int32(ap.IfIndex)]
			if !ok {
				continue // switch port for this ifIndex not modeled
			}
			bySwitch := perMAC[mac]
			if bySwitch == nil {
				bySwitch = make(map[string]candidate)
				perMAC[mac] = bySwitch
			}
			if cur, exists := bySwitch[sw.deviceID]; !exists || ap.MACCount < cur.macCount {
				bySwitch[sw.deviceID] = candidate{switchID: sw.deviceID, port: port, macCount: ap.MACCount, vlan: ap.VLAN}
			}
		}
	}

	now := time.Now()
	linked := 0
	for mac, bySwitch := range perMAC {
		serverIface, err := e.deviceInterfaceRepo.FindServerInterfaceByMAC(e.ctx, tenantID, mac)
		if err != nil {
			e.log.Warnf("Link correlation: lookup for MAC %s failed: %v", mac, err)
			continue
		}
		if serverIface == nil {
			continue // not a known physical server
		}

		var best *candidate
		for _, c := range bySwitch {
			if c.macCount > fdbEdgePortMaxMACs {
				continue // only seen on an uplink/trunk here — host is behind another switch
			}
			if c.switchID == serverIface.DeviceID {
				continue // the switch's own MAC
			}
			if err := e.deviceInterfaceRepo.UpsertLink(e.ctx, serverIface.ID, c.switchID, c.port.ID, c.port.Name, linkSourceSNMPFDB, c.vlan, now); err != nil {
				e.log.Warnf("Link correlation: failed to upsert link on interface %s: %v", serverIface.ID, err)
				continue
			}
			linked++
			e.log.Infof("Link discovered: server interface %s (MAC %s) -> switch %s port %s (macs %d, vlan %d)",
				serverIface.Name, mac, c.switchID, c.port.Name, c.macCount, c.vlan)
			cc := c
			if best == nil || cc.macCount < best.macCount {
				best = &cc
			}
		}

		// Mirror the globally best link into the flat columns for backward compat.
		if best != nil {
			updates := map[string]any{
				"remote_device_id":    best.switchID,
				"remote_interface_id": best.port.ID,
				"remote_port_name":    best.port.Name,
				"link_source":         linkSourceSNMPFDB,
				"link_last_seen":      now,
			}
			if best.vlan > 0 {
				updates["link_vlan"] = int32(best.vlan)
			}
			if _, err := e.deviceInterfaceRepo.Update(e.ctx, serverIface.ID, updates); err != nil {
				e.log.Warnf("Link correlation: failed to set primary link on interface %s: %v", serverIface.ID, err)
			}
		}
	}

	// Drop links no longer observed in any FDB. The window is generous so a
	// partial (single-subnet) scan that didn't include a switch doesn't evict
	// that switch's still-valid link before its own scan refreshes it.
	cutoff := now.Add(-linkStaleAfter)
	if pruned, err := e.deviceInterfaceRepo.PruneStaleLinks(e.ctx, linkSourceSNMPFDB, cutoff); err != nil {
		e.log.Warnf("Link correlation: prune stale links failed: %v", err)
	} else if pruned > 0 {
		e.log.Infof("Link correlation: pruned %d stale links", pruned)
	}

	e.log.Infof("Link correlation: %d server links upserted across %d switches", linked, len(switches))
}

// maskString masks a string for logging, showing only first 2 chars
func maskString(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 2 {
		return "***"
	}
	return s[:2] + "***"
}

// syncDeviceInterfaces creates or updates device interfaces from SNMP data
func (e *ScanExecutor) syncDeviceInterfaces(deviceID string, interfaces []biz.SNMPInterface) {
	for _, iface := range interfaces {
		if iface.Name == "" {
			continue
		}

		existing, err := e.deviceInterfaceRepo.GetByDeviceAndName(e.ctx, deviceID, iface.Name)
		if err != nil {
			e.log.Warnf("Failed to lookup interface %s on device %s: %v", iface.Name, deviceID, err)
			continue
		}

		if existing != nil {
			// Update existing interface
			updates := map[string]any{}
			if iface.MAC != "" {
				updates["mac_address"] = iface.MAC
			}
			if iface.SpeedMbps > 0 {
				updates["speed_mbps"] = iface.SpeedMbps
			}
			updates["enabled"] = iface.Enabled
			if iface.Type != "" {
				updates["interface_type"] = iface.Type
			}
			if iface.Description != "" {
				updates["description"] = iface.Description
			}
			if iface.Index > 0 {
				updates["if_index"] = int32(iface.Index)
			}
			_, err = e.deviceInterfaceRepo.Update(e.ctx, existing.ID, updates)
			if err != nil {
				e.log.Warnf("Failed to update interface %s: %v", iface.Name, err)
			}
		} else {
			// Create new interface
			opts := []func(*ent.DeviceInterfaceCreate){}
			if iface.MAC != "" {
				opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetMACAddress(iface.MAC) })
			}
			if iface.SpeedMbps > 0 {
				opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetSpeedMbps(iface.SpeedMbps) })
			}
			opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetEnabled(iface.Enabled) })
			if iface.Type != "" {
				opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetInterfaceType(iface.Type) })
			}
			if iface.Description != "" {
				opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetDescription(iface.Description) })
			}
			if iface.Index > 0 {
				opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetIfIndex(int32(iface.Index)) })
			}
			_, err = e.deviceInterfaceRepo.Create(e.ctx, deviceID, iface.Name, opts...)
			if err != nil {
				e.log.Warnf("Failed to create interface %s on device %s: %v", iface.Name, deviceID, err)
			}
		}
	}
}
