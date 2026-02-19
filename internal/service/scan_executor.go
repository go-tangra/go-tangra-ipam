package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-ipam/internal/biz"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipscanjob"
	appViewer "github.com/go-tangra/go-tangra-common/viewer"
)

const (
	// Default configuration values
	defaultWorkerCount       = 3
	defaultMaxRetries        = 3
	defaultRetryDelaySeconds = 60
	defaultBackoffMultiplier = 2.0
	defaultCleanupDays       = 30
	defaultPollInterval      = 5 * time.Second
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

	// Start worker goroutines
	for i := int32(0); i < workerCount; i++ {
		e.wg.Add(1)
		go e.worker(i)
	}

	// Start cleanup goroutine
	e.wg.Add(1)
	go e.cleanupWorker()

	return nil
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

			_, err := e.ipAddressRepo.Create(e.ctx, jobTenantID, result.Address, job.SubnetID, opts...)
			if err != nil {
				e.log.Warnf("Failed to create IP %s: %v", result.Address, err)
			} else {
				newCount++
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

		if err := e.scanJobRepo.UpdateProgress(e.ctx, job.ID, int64(len(results)), aliveCount, newCount, updatedCount, 80, "SNMP discovery..."); err != nil {
			e.log.Warnf("Failed to update SNMP progress for job %s: %v", job.ID, err)
		}

		snmpResults, snmpErr := biz.ScanSubnetSNMP(e.ctx, aliveIPs, snmpConfig, 10, func(scanned, discovered int) {
			// Progress callback for SNMP phase
		})
		if snmpErr != nil {
			e.log.Warnf("SNMP scan failed for job %s: %v", job.ID, snmpErr)
		} else {
			snmpDiscovered = int64(len(snmpResults))
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
	for _, info := range results {
		// Try to find existing device by primary IP
		existing, err := e.deviceRepo.GetByPrimaryIP(e.ctx, tenantID, info.Address)
		if err != nil {
			e.log.Warnf("Failed to lookup device by IP %s: %v", info.Address, err)
			continue
		}

		// If not found by IP, try by name
		if existing == nil && info.SysName != "" {
			existing, err = e.deviceRepo.GetByTenantAndName(e.ctx, tenantID, info.SysName)
			if err != nil {
				e.log.Warnf("Failed to lookup device by name %s: %v", info.SysName, err)
			}
		}

		var deviceID string

		if existing != nil {
			// Update existing device
			deviceID = existing.ID
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

			newDevice, err := e.deviceRepo.Create(e.ctx, tenantID, name, opts...)
			if err != nil {
				e.log.Warnf("Failed to create device for %s: %v", info.Address, err)
				continue
			}
			deviceID = newDevice.ID
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
	}
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
			_, err = e.deviceInterfaceRepo.Create(e.ctx, deviceID, iface.Name, opts...)
			if err != nil {
				e.log.Warnf("Failed to create interface %s on device %s: %v", iface.Name, deviceID, err)
			}
		}
	}
}
