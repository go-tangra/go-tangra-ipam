package hostsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Administrator-facing errors.
var (
	// ErrNotHostReported: the device is not linked to an inventory host.
	ErrNotHostReported = errors.New("hostsync: device is not maintained by host reports")
	// ErrValidation: invalid settings.
	ErrValidation = errors.New("hostsync: invalid settings")
)

// Settings bounds (contracts/ipam-http.md).
const (
	MinFullInterval = 15
	MaxFullInterval = 1440
)

// Admin is the administrator surface of the host sync (US6): settings,
// status, re-sync and conflict clearing, scoped to the caller's tenant.
type Admin struct {
	st            repo.Store
	inv           invclient.Client
	runner        *Runner
	globalEnabled bool
	now           func() time.Time
}

// NewAdmin builds the admin service. globalEnabled mirrors host_sync.enabled.
func NewAdmin(st repo.Store, inv invclient.Client, runner *Runner, globalEnabled bool) *Admin {
	return &Admin{st: st, inv: inv, runner: runner, globalEnabled: globalEnabled, now: func() time.Time { return time.Now().UTC() }}
}

// SettingsInput is the writable part of the settings.
type SettingsInput struct {
	Enabled             bool     `json:"enabled"`
	FullIntervalMinutes int      `json:"full_interval_minutes"`
	ExcludedInterfaces  []string `json:"excluded_interfaces"`
}

// Status is the tenant's sync status.
type Status struct {
	Enabled             bool       `json:"enabled"`
	State               string     `json:"state"`
	LastError           string     `json:"last_error,omitempty"`
	LastPollAt          *time.Time `json:"last_poll_at,omitempty"`
	LastReconcileAt     *time.Time `json:"last_reconcile_at,omitempty"`
	NextReconcileAt     *time.Time `json:"next_reconcile_at,omitempty"`
	HostsReported       int        `json:"hosts_reported"`
	HostsFailed         int        `json:"hosts_failed"`
	DevicesNotReported  int64      `json:"devices_not_reported"`
	AddressesInConflict int64      `json:"addresses_in_conflict"`
}

// DeviceHostSync is a device's host-sync provenance and last outcome.
type DeviceHostSync struct {
	Source          string                `json:"source"`
	InventoryHostID string                `json:"inventory_host_id,omitempty"`
	ReportState     string                `json:"report_state"`
	SnapshotID      string                `json:"snapshot_id,omitempty"`
	CollectedAt     *time.Time            `json:"collected_at,omitempty"`
	AppliedAt       *time.Time            `json:"applied_at,omitempty"`
	Trigger         string                `json:"trigger,omitempty"`
	Changes         int                   `json:"changes"`
	Issues          []store.HostSyncIssue `json:"issues"`
}

// ResyncResult is the outcome of a device re-sync.
type ResyncResult struct {
	Applied bool                  `json:"applied"`
	Changes int                   `json:"changes"`
	Issues  []store.HostSyncIssue `json:"issues"`
}

func userRow(subj authz.Subjects, t audit.EventType, kind, id string, detail map[string]any, at time.Time) store.AuditRow {
	row, _ := audit.Row(audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorUser, ActorID: subj.ActorID(),
		SubjectKind: kind, SubjectID: id, Outcome: audit.OutcomeOK, Details: detail}, at)
	return row
}

// Settings returns the tenant's settings (defaults on first read).
func (a *Admin) Settings(ctx context.Context, subj authz.Subjects) (store.HostSyncSettings, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostSyncSettings{}, err
	}
	return a.st.EnsureHostSyncSettings(ctx, subj.TenantID)
}

// UpdateSettings validates and stores the settings with an audit row carrying
// before/after (the permission hostsync:manage is enforced by the gateway).
func (a *Admin) UpdateSettings(ctx context.Context, subj authz.Subjects, in SettingsInput) (store.HostSyncSettings, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.HostSyncSettings{}, err
	}
	if in.FullIntervalMinutes < MinFullInterval || in.FullIntervalMinutes > MaxFullInterval {
		return store.HostSyncSettings{}, fmt.Errorf("%w: full_interval_minutes", ErrValidation)
	}
	if in.ExcludedInterfaces == nil {
		in.ExcludedInterfaces = []string{}
	}
	if err := hostreport.ValidatePatterns(in.ExcludedInterfaces); err != nil {
		return store.HostSyncSettings{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	cur, err := a.st.EnsureHostSyncSettings(ctx, subj.TenantID)
	if err != nil {
		return store.HostSyncSettings{}, err
	}
	ch := map[string]any{}
	if cur.Enabled != in.Enabled {
		ch["enabled"] = map[string]any{"before": cur.Enabled, "after": in.Enabled}
	}
	if cur.FullIntervalMinutes != in.FullIntervalMinutes {
		ch["full_interval_minutes"] = map[string]any{"before": cur.FullIntervalMinutes, "after": in.FullIntervalMinutes}
	}
	if !slices.Equal(cur.ExcludedInterfaces, in.ExcludedInterfaces) {
		ch["excluded_interfaces"] = map[string]any{"before": toAny(cur.ExcludedInterfaces), "after": toAny(in.ExcludedInterfaces)}
	}
	next := cur
	next.Enabled, next.FullIntervalMinutes, next.ExcludedInterfaces, next.UpdatedBy = in.Enabled, in.FullIntervalMinutes, in.ExcludedInterfaces, subj.ActorID()
	row := userRow(subj, audit.HostSyncSettingsUpdated, audit.SubjectHostSync, subj.TenantID, map[string]any{"changes": ch}, a.now())
	if err := a.st.UpdateHostSyncSettings(ctx, next, row); err != nil {
		return store.HostSyncSettings{}, err
	}
	return a.st.GetHostSyncSettings(ctx, subj.TenantID)
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Status returns the tenant's sync status and aggregates.
func (a *Admin) Status(ctx context.Context, subj authz.Subjects) (Status, error) {
	s, err := a.Settings(ctx, subj)
	if err != nil {
		return Status{}, err
	}
	nr, cf, err := a.st.HostSyncCounts(ctx, subj.TenantID)
	if err != nil {
		return Status{}, err
	}
	out := Status{Enabled: s.Enabled && a.globalEnabled, State: s.Status, LastError: s.LastError, LastPollAt: s.LastPollAt,
		LastReconcileAt: s.LastReconcileAt, HostsReported: s.HostsReported, HostsFailed: s.HostsFailed,
		DevicesNotReported: nr, AddressesInConflict: cf}
	if !out.Enabled {
		out.State = store.HostSyncDisabled
	}
	if out.State == "" {
		out.State = store.HostSyncOK
	}
	if out.Enabled && s.LastReconcileAt != nil && !s.ReconcileRequested {
		next := s.LastReconcileAt.Add(time.Duration(s.FullIntervalMinutes) * time.Minute)
		out.NextReconcileAt = &next
	}
	return out, nil
}

// ResyncAll schedules a full reconcile (with full apply) of the tenant.
func (a *Admin) ResyncAll(ctx context.Context, subj authz.Subjects) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	s, err := a.st.EnsureHostSyncSettings(ctx, subj.TenantID)
	if err != nil {
		return err
	}
	if !s.Enabled || !a.globalEnabled {
		return repo.ErrSyncDisabled
	}
	return a.st.RequestReconcile(ctx, subj.TenantID, userRow(subj, audit.HostSyncResyncRequested, audit.SubjectHostSync, "all", map[string]any{"trigger": "resync_all:" + subj.ActorID()}, a.now()))
}

func (a *Admin) hostDevice(ctx context.Context, subj authz.Subjects, deviceID string) (store.Device, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Device{}, err
	}
	return a.st.GetDevice(ctx, subj.TenantID, deviceID)
}

// ResyncDevice fetches the device's latest report and applies it now,
// ignoring the digest shortcut (US6 scenario 3).
func (a *Admin) ResyncDevice(ctx context.Context, subj authz.Subjects, deviceID string) (ResyncResult, error) {
	d, err := a.hostDevice(ctx, subj, deviceID)
	if err != nil {
		return ResyncResult{}, err
	}
	if d.InventoryHostID == "" {
		return ResyncResult{}, ErrNotHostReported
	}
	s, err := a.st.EnsureHostSyncSettings(ctx, subj.TenantID)
	if err != nil {
		return ResyncResult{}, err
	}
	if !s.Enabled || !a.globalEnabled {
		return ResyncResult{}, repo.ErrSyncDisabled
	}
	rep, err := a.inv.GetHostReport(ctx, subj.TenantID, d.InventoryHostID)
	if err != nil {
		return ResyncResult{}, err
	}
	trigger := "resync:" + subj.ActorID()
	if len(trigger) > 80 { // ipam_hostsync_device_state.trigger bound
		trigger = trigger[:80]
	}
	if err := a.st.AppendAudit(ctx, userRow(subj, audit.HostSyncResyncRequested, audit.SubjectHostSync, deviceID,
		map[string]any{"trigger": trigger, "inventory_host_id": d.InventoryHostID}, a.now())); err != nil {
		return ResyncResult{}, err
	}
	res, err := a.runner.Apply(ctx, s, rep, trigger, store.NewID())
	if err != nil {
		return ResyncResult{}, err
	}
	return ResyncResult{Applied: res.Applied, Changes: res.Changes, Issues: res.Issues}, nil
}

// DeviceHostSync returns the device's provenance and last report outcome.
func (a *Admin) DeviceHostSync(ctx context.Context, subj authz.Subjects, deviceID string) (DeviceHostSync, error) {
	d, err := a.hostDevice(ctx, subj, deviceID)
	if err != nil {
		return DeviceHostSync{}, err
	}
	out := DeviceHostSync{Source: d.Source, InventoryHostID: d.InventoryHostID, ReportState: d.ReportState, Issues: []store.HostSyncIssue{}}
	st, err := a.st.GetHostSyncDeviceState(ctx, subj.TenantID, deviceID)
	switch {
	case errors.Is(err, repo.ErrNotFound):
	case err != nil:
		return DeviceHostSync{}, err
	default:
		out.SnapshotID, out.CollectedAt, out.AppliedAt, out.Trigger, out.Changes = st.SnapshotID, st.CollectedAt, st.AppliedAt, st.Trigger, st.Changes
		if st.Issues != nil {
			out.Issues = st.Issues
		}
	}
	return out, nil
}

// Guests lists the guests a hypervisor device reports.
func (a *Admin) Guests(ctx context.Context, subj authz.Subjects, deviceID string) ([]store.HypervisorGuest, error) {
	if _, err := a.hostDevice(ctx, subj, deviceID); err != nil {
		return nil, err
	}
	out, err := a.st.ListGuests(ctx, subj.TenantID, deviceID)
	if out == nil && err == nil {
		out = []store.HypervisorGuest{}
	}
	return out, err
}

// ClearConflict resets an address's conflict flag (audited with the user).
func (a *Admin) ClearConflict(ctx context.Context, subj authz.Subjects, addressID string) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	return a.st.ClearAddressConflict(ctx, subj.TenantID, addressID,
		userRow(subj, audit.AddressConflictCleared, audit.SubjectAddress, addressID, map[string]any{"by": "user"}, a.now()))
}
