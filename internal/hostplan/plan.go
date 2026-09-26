package hostplan

import (
	"sort"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Kind is the kind of a planned write.
type Kind string

// Op kinds. Each is executed by exactly one repo.HostTx write.
const (
	OpCreateDevice    Kind = "create_device"
	OpUpdateDevice    Kind = "update_device"
	OpCreateInterface Kind = "create_interface"
	OpUpdateInterface Kind = "update_interface"
	OpCreateSubnet    Kind = "create_subnet"
	OpCreateAddress   Kind = "create_address"
	OpUpdateAddress   Kind = "update_address"
	OpReplacePackages Kind = "replace_packages"
	OpReplaceGuests   Kind = "replace_guests"
	OpSetHypervisor   Kind = "set_hypervisor"
	OpSetGuestDevice  Kind = "set_guest_device"
)

// Op is one planned write and the audit rows that record it. Exactly the
// field matching Kind is set.
type Op struct {
	Kind      Kind
	Device    *store.Device
	Interface *store.DeviceInterface
	Subnet    *store.Subnet
	Address   *store.IPAddress
	Packages  []store.DevicePackage
	Guests    []store.HypervisorGuest
	// DeviceID is the device the op targets (packages, guests, hypervisor).
	DeviceID string
	// RefID is the hypervisor (set_hypervisor, "" = unlink) or the guest
	// device (set_guest_device); GuestRowID the guest row.
	RefID      string
	GuestRowID string
	Audit      []store.AuditRow
}

// Event is a realtime event to publish after the transaction commits.
type Event struct {
	Type    string
	Payload map[string]any
}

// Plan is everything one report changes.
type Plan struct {
	DeviceID string
	Match    string
	Ops      []Op
	Events   []Event
	State    store.HostSyncDeviceState
	Issues   []store.HostSyncIssue
}

// Params are the per-run inputs of the planner.
type Params struct {
	TenantID       string
	RunID          string
	Trigger        string
	Now            time.Time
	Exclusions     []string
	ConflictMoves  int
	ConflictWindow time.Duration
	NewID          func() string
}

// State is what the store loaded for one report (see Load in the host sync).
type State struct {
	Candidates Candidates
	// Everything below is empty for a device that does not exist yet.
	Interfaces []store.DeviceInterface
	// Addresses holds the rows for every reported address and every row the
	// matched device owns, keyed by address.
	Addresses map[string]store.IPAddress
	Subnets   []store.Subnet
	Packages  []store.DevicePackage
	// Guests are the guest rows the matched device reported last time.
	Guests []store.HypervisorGuest
	// MACOwners are the devices whose interfaces carry a reported guest MAC.
	MACOwners []repo.MACOwner
	// GuestDevices are the devices currently linked to the matched device as
	// their hypervisor.
	GuestDevices []store.Device
	// GuestRows are other hosts' guest rows listing one of this host's MACs.
	GuestRows []store.HypervisorGuest
}

type planner struct {
	st      State
	r       hostreport.Report
	p       Params
	plan    *Plan
	dev     store.Device
	created bool
	issues  map[[2]string]int

	ifaces      []reportedIface
	subnets     []ipnet.Candidate
	subnetNames map[string]bool
}

// Build plans the changes a validated report makes to the loaded state. It
// is pure: the same state, report and params always give the same plan (ids
// come from Params.NewID).
func Build(st State, r hostreport.Report, p Params) Plan {
	pl := &planner{st: st, r: r, p: p, plan: &Plan{}, issues: map[[2]string]int{}}
	pl.device()
	pl.interfaces()
	pl.addresses()
	pl.packages()
	pl.guests()
	pl.ownHypervisor()
	pl.finish()
	return *pl.plan
}

func (pl *planner) issue(field, reason string) { pl.issues[[2]string{field, reason}]++ }

func (pl *planner) add(op Op) { pl.plan.Ops = append(pl.plan.Ops, op) }

// row builds an audit row for a host-sync change with the common detail keys
// (detail is never nil: every caller passes its own map).
func (pl *planner) row(t audit.EventType, subjectKind, subjectID string, detail map[string]any) store.AuditRow {
	detail["inventory_host_id"] = pl.r.HostID
	detail["run_id"] = pl.p.RunID
	detail["trigger"] = pl.p.Trigger
	// The event type and subject kind come from the closed vocabulary and the
	// tenant is validated before planning, so Row cannot refuse the event.
	row, _ := audit.Row(audit.Event{
		TenantID: pl.p.TenantID, EventType: t, ActorKind: audit.ActorSystem, ActorID: audit.HostSyncActor,
		SubjectKind: subjectKind, SubjectID: subjectID, Outcome: audit.OutcomeOK, Details: detail,
	}, pl.p.Now)
	return row
}

func (pl *planner) event(eventType, action string, a store.IPAddress) {
	pl.plan.Events = append(pl.plan.Events, Event{Type: eventType,
		Payload: events.IPAddressPayload(action, a.ID, a.Address, a.SubnetID, a.Hostname, a.DeviceID)})
}

func (pl *planner) finish() {
	if n := len(pl.plan.Ops); n > 0 {
		pl.plan.Events = append(pl.plan.Events, Event{Type: events.HostSyncApplied, Payload: events.HostSyncAppliedPayload(pl.dev.ID, n)})
	}
	issues := append([]store.HostSyncIssue(nil), pl.r.Issues...)
	keys := make([][2]string, 0, len(pl.issues))
	for k := range pl.issues {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		issues = append(issues, store.HostSyncIssue{Field: k[0], Reason: k[1], Count: pl.issues[k]})
	}
	if len(issues) > hostreport.MaxIssues {
		issues = issues[:hostreport.MaxIssues]
	}
	pl.plan.Issues = issues
	now := pl.p.Now
	pl.plan.State = store.HostSyncDeviceState{
		DeviceID: pl.dev.ID, TenantID: pl.p.TenantID, InventoryHostID: pl.r.HostID, SnapshotID: pl.r.SnapshotID,
		CollectedAt: timePtr(pl.r.CollectedAt), AppliedAt: &now, Trigger: pl.p.Trigger,
		Changes: len(pl.plan.Ops), Issues: issues,
	}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
