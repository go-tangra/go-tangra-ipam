package scan

import (
	"context"
	"log/slog"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/arpplan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// arpRead collects the ARP tables the SNMP phase read (feature 022).
type arpRead struct {
	obs     []arpplan.Observation
	partial int
}

// add records one discovered device's ARP table (caller holds the phase lock).
func (r *arpRead) add(deviceID string, dev snmp.DiscoveredDevice) {
	if dev.ARPPartial {
		r.partial++
	}
	if len(dev.ARP) > 0 {
		r.obs = append(r.obs, arpplan.Observation{DeviceID: deviceID, Entries: dev.ARP})
	}
}

// arpPhase plans and applies the MACs the devices' ARP tables reported
// (research D5): after SNMP discovery (so this scan's devices count as network
// devices) and before the switch-port correlation. It never fails the scan;
// a failure is visible as arp_status "failed".
func (s *Service) arpPhase(ctx context.Context, log *slog.Logger, job *store.IPScanJob, cfg store.ARPSettings, cfgErr error, read arpRead) {
	switch {
	case cfgErr != nil:
		job.ARPStatus = store.ARPFailed
		log.Warn("scan arp settings", "job", job.ID, "err", cfgErr)
		return
	case !cfg.Enabled:
		job.ARPStatus = store.ARPDisabled
		return
	}
	job.ARPDevices, job.ARPPartial = len(read.obs), read.partial
	plan, err := s.planARP(ctx, job, cfg, read)
	if err == nil {
		job.ARPEntries, job.ARPApplied, job.ARPCreated = int64(plan.Entries), int64(plan.Applied), int64(plan.Created)
		job.ARPConflicts, job.ARPIgnored = int64(plan.Conflicts), plan.Ignored
		err = s.st.ApplyARP(ctx, job.TenantID, plan.Ops, []store.AuditRow{s.arpRunRow(job)})
	}
	if err != nil {
		job.ARPStatus = store.ARPFailed
		log.Warn("scan arp phase", "job", job.ID, "err", err)
		return
	}
	job.ARPStatus = store.ARPRan
	log.Info("scan arp phase", "job", job.ID, "devices", job.ARPDevices, "partial", job.ARPPartial,
		"entries", job.ARPEntries, "applied", job.ARPApplied, "created", job.ARPCreated,
		"conflicts", job.ARPConflicts, "ignored", job.ARPIgnored)
}

// planARP loads the tenant state the planner needs and builds the plan.
func (s *Service) planARP(ctx context.Context, job *store.IPScanJob, cfg store.ARPSettings, read arpRead) (arpplan.Plan, error) {
	subnets, err := s.st.AllSubnetCIDRs(ctx, job.TenantID)
	if err != nil {
		return arpplan.Plan{}, err
	}
	addrs, err := s.st.ListAddresses(ctx, job.TenantID, store.AddressFilter{})
	if err != nil {
		return arpplan.Plan{}, err
	}
	network, err := s.st.NetworkMACs(ctx, job.TenantID)
	if err != nil {
		return arpplan.Plan{}, err
	}
	return arpplan.Build(arpplan.Input{TenantID: job.TenantID, JobID: job.ID, Now: s.now(), Observations: read.obs,
		Subnets: subnets, Addresses: addrs, NetworkMACs: network, ProxyThreshold: cfg.ProxyThreshold,
		ExcludedDevices: cfg.ExcludedDevices}), nil
}

// arpRunRow is the per-scan summary audit row (arp_run).
func (s *Service) arpRunRow(job *store.IPScanJob) store.AuditRow {
	ignored := map[string]any{}
	for k, v := range job.ARPIgnored {
		ignored[k] = int64(v)
	}
	row, _ := audit.Row(audit.Event{TenantID: job.TenantID, EventType: audit.ARPRun, ActorKind: audit.ActorSystem,
		ActorID: audit.ScanActor, SubjectKind: audit.SubjectScan, SubjectID: job.ID, Outcome: audit.OutcomeOK,
		Details: map[string]any{"devices": int64(job.ARPDevices), "partial": int64(job.ARPPartial), "entries": job.ARPEntries,
			"applied": job.ARPApplied, "created": job.ARPCreated, "conflicts": job.ARPConflicts, "ignored": ignored}}, s.now())
	return row
}
