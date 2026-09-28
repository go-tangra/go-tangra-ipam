// Package audit records every IPAM operation in a closed vocabulary with a
// detail guard that keeps SNMP/IPMI credentials, sealed owner/contact fields and
// passwords out of the log. Events are buffered and written to the audit
// hypertable by a background goroutine so recording never blocks the caller.
package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// EventType is the closed audit vocabulary (data-model.md, AuditAction).
type EventType string

// Event types.
const (
	SubnetCreated EventType = "subnet_created"
	SubnetUpdated EventType = "subnet_updated"
	SubnetDeleted EventType = "subnet_deleted"

	AddressCreated   EventType = "address_created"
	AddressUpdated   EventType = "address_updated"
	AddressDeleted   EventType = "address_deleted"
	AddressAllocated EventType = "address_allocated"

	DeviceCreated EventType = "device_created"
	DeviceUpdated EventType = "device_updated"
	DeviceDeleted EventType = "device_deleted"

	VlanCreated EventType = "vlan_created"
	VlanUpdated EventType = "vlan_updated"
	VlanDeleted EventType = "vlan_deleted"

	LocationCreated EventType = "location_created"
	LocationUpdated EventType = "location_updated"
	LocationDeleted EventType = "location_deleted"

	GroupCreated EventType = "group_created"
	GroupUpdated EventType = "group_updated"
	GroupDeleted EventType = "group_deleted"

	ScanStarted   EventType = "scan_started"
	ScanCancelled EventType = "scan_cancelled"
	ScanCompleted EventType = "scan_completed"

	PowerAction       EventType = "power_action"
	KVMSessionStarted EventType = "kvm_session_started"
	DNSConfigUpdated  EventType = "dns_config_updated"

	BackupExported EventType = "backup_exported"
	BackupImported EventType = "backup_imported"

	AccessRefused EventType = "access_refused"

	// Host sync (feature 020, contracts/audit-events.md). Written in the
	// apply transaction with actor system/hostsync.
	InterfaceCreated        EventType = "interface_created"
	InterfaceUpdated        EventType = "interface_updated"
	InterfaceNotReported    EventType = "interface_not_reported"
	AddressMoved            EventType = "address_moved"
	AddressReleased         EventType = "address_released"
	AddressConflict         EventType = "address_conflict"
	AddressConflictCleared  EventType = "address_conflict_cleared"
	PackagesUpdated         EventType = "packages_updated"
	HypervisorLinked        EventType = "hypervisor_linked"
	HypervisorUnlinked      EventType = "hypervisor_unlinked"
	PortLinked              EventType = "port_linked"
	PortUnlinked            EventType = "port_unlinked"
	DeviceNotReported       EventType = "device_not_reported"
	HostSyncSettingsUpdated EventType = "hostsync_settings_updated"
	HostSyncResyncRequested EventType = "hostsync_resync_requested"
	HostSyncRun             EventType = "hostsync_run"

	// Subnet SNMP credentials (feature 021, contracts/audit-events.md).
	// Details use neutral keys only (protocol_version, security_level,
	// previous_version, target, outcome, source_subnet_id).
	SNMPCredentialsSet      EventType = "snmp_credentials_set"
	SNMPCredentialsReplaced EventType = "snmp_credentials_replaced"
	SNMPCredentialsCleared  EventType = "snmp_credentials_cleared"
	SNMPCredentialsTested   EventType = "snmp_credentials_tested"

	// ARP-based MAC linking (feature 022, contracts/ipam-http.md). Written
	// with actor system/scan (settings: the user); details use neutral keys
	// (address, mac, previous_mac, observed_mac, source_device_id, job_id).
	// An address created from ARP is address_created with origin "arp".
	MACLearned         EventType = "mac_learned"
	MACChanged         EventType = "mac_changed"
	MACConflict        EventType = "mac_conflict"
	ARPRun             EventType = "arp_run"
	ARPSettingsUpdated EventType = "arp_settings_updated"

	// Device hardware (feature 023, contracts/audit-events.md). Written by
	// the host sync in the apply transaction; details use neutral keys
	// (summary, changes [{field, change, before, after}], changes_truncated).
	HardwareReported EventType = "hardware_reported"
	HardwareUpdated  EventType = "hardware_updated"

	// Device BMC reference (feature 024, contracts/audit-events.md). Written
	// with the column change in one transaction; details use neutral keys
	// (reference, previous_reference, reference_name).
	BMCReferenceSet     EventType = "bmc_reference_set"
	BMCReferenceChanged EventType = "bmc_reference_changed"
	BMCReferenceCleared EventType = "bmc_reference_cleared"
)

// HostSyncActor is the actor id of every change the host sync makes.
const HostSyncActor = "hostsync"

// ScanActor is the actor id of every change a scan's ARP phase makes.
const ScanActor = "scan"

// Subject kinds (closed set).
const (
	SubjectSubnet    = "subnet"
	SubjectAddress   = "address"
	SubjectDevice    = "device"
	SubjectInterface = "interface"
	SubjectVlan      = "vlan"
	SubjectLocation  = "location"
	SubjectGroup     = "group"
	SubjectScan      = "scan"
	SubjectPower     = "power"
	SubjectKVM       = "kvm"
	SubjectDNS       = "dns"
	SubjectBackup    = "backup"
	SubjectSystem    = "system"
	SubjectPackage   = "package"
	SubjectHostSync  = "hostsync"
	SubjectTenant    = "tenant"
)

// Outcomes (closed set).
const (
	OutcomeOK      = "ok"
	OutcomeRefused = "refused"
	OutcomeError   = "error"
)

// Actor kinds (closed set).
const (
	ActorUser    = "user"
	ActorService = "service"
	ActorSystem  = "system"
)

var known = map[EventType]struct{}{}

func init() {
	for _, t := range []EventType{
		SubnetCreated, SubnetUpdated, SubnetDeleted,
		AddressCreated, AddressUpdated, AddressDeleted, AddressAllocated,
		DeviceCreated, DeviceUpdated, DeviceDeleted,
		VlanCreated, VlanUpdated, VlanDeleted,
		LocationCreated, LocationUpdated, LocationDeleted,
		GroupCreated, GroupUpdated, GroupDeleted,
		ScanStarted, ScanCancelled, ScanCompleted,
		PowerAction, KVMSessionStarted, DNSConfigUpdated,
		BackupExported, BackupImported,
		AccessRefused,
		InterfaceCreated, InterfaceUpdated, InterfaceNotReported,
		AddressMoved, AddressReleased, AddressConflict, AddressConflictCleared,
		PackagesUpdated, HypervisorLinked, HypervisorUnlinked, PortLinked, PortUnlinked,
		DeviceNotReported, HostSyncSettingsUpdated, HostSyncResyncRequested, HostSyncRun,
		SNMPCredentialsSet, SNMPCredentialsReplaced, SNMPCredentialsCleared, SNMPCredentialsTested,
		MACLearned, MACChanged, MACConflict, ARPRun, ARPSettingsUpdated,
		HardwareReported, HardwareUpdated,
		BMCReferenceSet, BMCReferenceChanged, BMCReferenceCleared,
	} {
		known[t] = struct{}{}
	}
}

// Known reports whether t is in the vocabulary.
func Known(t string) bool { _, ok := known[EventType(t)]; return ok }

// Event is one record before persistence. At is filled by Record.
type Event struct {
	TenantID    string
	EventType   EventType
	ActorKind   string // user | service | system
	ActorID     string
	SubjectKind string // subnet | address | device | ... | system
	SubjectID   string
	Target      string // host/ip being acted on (active ops)
	Outcome     string // ok | refused | error
	Reason      string
	Details     map[string]any
}

// Store persists audit rows. repo.Store satisfies it, as does any test double.
type Store interface {
	AppendAudit(ctx context.Context, row store.AuditRow) error
}

// Validate checks the closed vocabulary and required fields. An unknown event
// type is a programming error and is surfaced to the caller.
func Validate(e Event) error {
	if _, ok := known[e.EventType]; !ok {
		return fmt.Errorf("audit: unknown event type %q", e.EventType)
	}
	if e.TenantID == "" {
		return errors.New("audit: tenant_id is required")
	}
	switch e.ActorKind {
	case ActorUser, ActorService, ActorSystem:
	default:
		return fmt.Errorf("audit: actor_kind %q", e.ActorKind)
	}
	switch e.SubjectKind {
	case SubjectSubnet, SubjectAddress, SubjectDevice, SubjectInterface,
		SubjectVlan, SubjectLocation, SubjectGroup, SubjectScan,
		SubjectPower, SubjectKVM, SubjectDNS, SubjectBackup, SubjectSystem,
		SubjectPackage, SubjectHostSync, SubjectTenant:
	default:
		return fmt.Errorf("audit: subject_kind %q", e.SubjectKind)
	}
	switch e.Outcome {
	case OutcomeOK, OutcomeRefused, OutcomeError:
	default:
		return fmt.Errorf("audit: outcome %q", e.Outcome)
	}
	return nil
}

// forbidden detail-key substrings (case-insensitive): SNMP/IPMI credentials
// (communities, passphrases), sealed owner/contact fields and passwords never
// belong in an audit detail (SR-005).
var forbidden = []string{"secret", "credential", "snmp", "ipmi", "password", "passphrase", "community", "owner", "contact"}

func forbiddenKey(k string) bool {
	lk := strings.ToLower(k)
	for _, f := range forbidden {
		if strings.Contains(lk, f) {
			return true
		}
	}
	return false
}

func guardValue(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > 256 {
			return x[:256]
		}
		return x
	case map[string]any:
		return guardMap(x)
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = guardValue(vv)
		}
		return out
	default:
		return v
	}
}

// guardMap returns a copy of m with any key whose lowercased name carries
// secret|credential|snmp|ipmi|password|passphrase|community|owner|contact (at
// any depth) dropped and
// string values truncated to 256 characters.
func guardMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if forbiddenKey(k) {
			continue
		}
		out[k] = guardValue(v)
	}
	return out
}

// Row validates e and turns it into a guarded audit row for a caller that
// writes it itself (the host sync writes its rows inside the apply
// transaction so that no change is ever committed without its audit row).
func Row(e Event, at time.Time) (store.AuditRow, error) {
	if err := Validate(e); err != nil {
		return store.AuditRow{}, err
	}
	return store.AuditRow{
		ID:          store.NewID(),
		TenantID:    e.TenantID,
		At:          at,
		ActorKind:   e.ActorKind,
		ActorID:     e.ActorID,
		Action:      string(e.EventType),
		SubjectKind: e.SubjectKind,
		SubjectID:   e.SubjectID,
		Target:      e.Target,
		Outcome:     e.Outcome,
		Reason:      e.Reason,
		Detail:      guardMap(e.Details),
	}, nil
}

// Writer buffers events and writes them one row at a time; Record never blocks.
type Writer struct {
	st      Store
	ch      chan store.AuditRow
	wg      sync.WaitGroup
	mu      sync.Mutex
	closed  bool
	dropped int64
	onError func(error)
	flushCh chan chan struct{}
	tick    time.Duration
}

// NewWriter starts the batch writer (queue 10k, flush every 500 ms).
func NewWriter(st Store, onError func(error)) *Writer {
	w := newWriter(st, onError, 10000)
	w.start()
	return w
}

func newWriter(st Store, onError func(error), queue int) *Writer {
	w := &Writer{st: st, ch: make(chan store.AuditRow, queue), onError: onError, flushCh: make(chan chan struct{}), tick: 500 * time.Millisecond}
	if w.onError == nil {
		w.onError = func(error) {}
	}
	return w
}

func (w *Writer) start() {
	w.wg.Add(1)
	go w.run()
}

// Record validates the event, fills At=now, guards the details and enqueues the
// row. An invalid event (unknown type, missing tenant, bad enum) returns an
// error and is not queued.
func (w *Writer) Record(_ context.Context, e Event) error {
	if err := Validate(e); err != nil {
		return err
	}
	row := store.AuditRow{
		ID:          store.NewID(),
		TenantID:    e.TenantID,
		At:          time.Now(),
		ActorKind:   e.ActorKind,
		ActorID:     e.ActorID,
		Action:      string(e.EventType),
		SubjectKind: e.SubjectKind,
		SubjectID:   e.SubjectID,
		Target:      e.Target,
		Outcome:     e.Outcome,
		Reason:      e.Reason,
		Detail:      guardMap(e.Details),
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		w.dropped++
		return errors.New("audit: writer closed")
	}
	select {
	case w.ch <- row:
	default:
		w.dropped++
		w.onError(errors.New("audit: queue full, event dropped"))
	}
	return nil
}

// Flush writes everything queued so far and returns when it is stored or ctx is done.
func (w *Writer) Flush(ctx context.Context) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	done := make(chan struct{})
	select {
	case w.flushCh <- done:
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Dropped returns the number of dropped events.
func (w *Writer) Dropped() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.dropped }

func (w *Writer) writeRow(r store.AuditRow) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.st.AppendAudit(ctx, r); err != nil {
		w.onError(err)
	}
}

func (w *Writer) run() {
	defer w.wg.Done()
	t := time.NewTicker(w.tick)
	defer t.Stop()
	for {
		select {
		case r, ok := <-w.ch:
			if !ok {
				return
			}
			w.writeRow(r)
		case <-t.C:
			// nothing buffered locally; rows are written as they arrive.
		case done := <-w.flushCh:
			for {
				select {
				case r := <-w.ch:
					w.writeRow(r)
					continue
				default:
				}
				break
			}
			close(done)
		}
	}
}

// Close drains and stops the writer.
func (w *Writer) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.ch)
	w.mu.Unlock()
	w.wg.Wait()
}
