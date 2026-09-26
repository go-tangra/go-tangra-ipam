package hostsync

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostplan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// MaxBackoff caps the back-off after inventory failures.
const MaxBackoff = 10 * time.Minute

// Triggers recorded on applies and audit rows.
const (
	TriggerPoll      = "poll"
	TriggerReconcile = "reconcile"
)

// Config tunes the runner (mirrors config.HostSync).
type Config struct {
	PollInterval   time.Duration
	Workers        int
	Pace           time.Duration
	ConflictMoves  int
	ConflictWindow time.Duration
}

// PortLinker correlates host MACs with switch ports after a run (US5).
type PortLinker interface {
	Correlate(ctx context.Context, tenantID string) error
}

// Runner is the host sync: the changed-since poller, the per-tenant
// reconcile and the single-host apply used by both and by re-sync.
type Runner struct {
	st      repo.Store
	inv     invclient.Client
	pub     events.Publisher
	cfg     Config
	log     *slog.Logger
	metrics *Metrics
	linker  PortLinker

	now   func() time.Time
	newID func() string
	sleep func(ctx context.Context, d time.Duration)

	mu          sync.Mutex
	globalSince time.Time
	failures    int
	nextAttempt time.Time
	retry       map[string]bool // tenants whose last run left hosts behind
	lastState   map[string]string
}

// New builds a runner. Nil publisher/logger/metrics are replaced by no-ops.
func New(st repo.Store, inv invclient.Client, pub events.Publisher, cfg Config, log *slog.Logger, m *Metrics) *Runner {
	if pub == nil {
		pub = events.HubPublisher{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if m == nil {
		m = NewMetrics(nil)
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Minute
	}
	return &Runner{st: st, inv: inv, pub: pub, cfg: cfg, log: log, metrics: m,
		now: func() time.Time { return time.Now().UTC() }, newID: store.NewID, sleep: sleepCtx,
		retry: map[string]bool{}, lastState: map[string]string{}}
}

// SetClock injects the clock (tests).
func (r *Runner) SetClock(now func() time.Time) { r.now = now }

// SetLinker installs the switch-port correlation hook (US5).
func (r *Runner) SetLinker(l PortLinker) { r.linker = l }

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Run polls every PollInterval until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.log.Info("host sync started", "poll_interval", r.cfg.PollInterval.String(), "workers", r.cfg.Workers)
	t := time.NewTicker(r.cfg.PollInterval)
	defer t.Stop()
	for {
		if err := r.Cycle(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("host sync cycle", "code", invclient.Code(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Cycle runs one poll: tenants with changed reports, tenants flagged for a
// reconcile or retry, each in parallel up to Workers. Inventory failures put
// every known tenant in degraded state and back off (max 10 min).
func (r *Runner) Cycle(ctx context.Context) error {
	r.mu.Lock()
	if r.now().Before(r.nextAttempt) {
		r.mu.Unlock()
		return nil
	}
	since := r.globalSince
	r.mu.Unlock()

	ids, maxAt, err := r.inv.ListReportTenants(ctx, since)
	if err != nil {
		r.backoff(ctx, err)
		return err
	}
	settings, err := r.st.ListHostSyncSettings(ctx)
	if err != nil {
		return err
	}
	due := map[string]bool{}
	for _, id := range ids {
		if hostreport.IsUUID(id) {
			due[strings.ToLower(id)] = true
		}
	}
	now := r.now()
	r.mu.Lock()
	for id := range r.retry {
		due[id] = true
	}
	r.mu.Unlock()
	for _, s := range settings {
		if s.Enabled && reconcileDue(s, now) {
			due[s.TenantID] = true
		}
	}
	tenants := make([]string, 0, len(due))
	for id := range due {
		tenants = append(tenants, id)
	}
	sort.Strings(tenants)

	var wg sync.WaitGroup
	sem := make(chan struct{}, r.cfg.Workers)
	var mu sync.Mutex
	var firstErr error
	for _, tid := range tenants {
		wg.Add(1)
		sem <- struct{}{}
		go func(tid string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := r.RunTenant(ctx, tid); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(tid)
	}
	wg.Wait()
	if firstErr != nil && errors.Is(firstErr, invclient.ErrUnavailable) {
		r.backoff(ctx, firstErr)
		return firstErr
	}
	r.mu.Lock()
	r.failures, r.nextAttempt = 0, time.Time{}
	if maxAt.After(r.globalSince) {
		r.globalSince = maxAt
	}
	r.mu.Unlock()
	return firstErr
}

func reconcileDue(s store.HostSyncSettings, now time.Time) bool {
	if s.ReconcileRequested || s.LastReconcileAt == nil {
		return true
	}
	return now.Sub(*s.LastReconcileAt) >= time.Duration(s.FullIntervalMinutes)*time.Minute
}

// backoff records an inventory failure: every known enabled tenant is
// degraded with the sanitised code and the next attempt is delayed.
func (r *Runner) backoff(ctx context.Context, cause error) {
	r.mu.Lock()
	r.failures++
	d := r.cfg.PollInterval << min(r.failures, 16)
	if d > MaxBackoff || d <= 0 {
		d = MaxBackoff
	}
	r.nextAttempt = r.now().Add(d)
	r.mu.Unlock()
	settings, err := r.st.ListHostSyncSettings(ctx)
	if err != nil {
		return
	}
	for _, s := range settings {
		if s.Enabled {
			r.saveState(ctx, s.TenantID, store.HostSyncStatus{Status: store.HostSyncDegraded, LastError: invclient.Code(cause),
				HostsReported: s.HostsReported, HostsFailed: s.HostsFailed})
		}
	}
}

// Backoff reports the current inventory back-off (tests, status).
func (r *Runner) Backoff() (failures int, next time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures, r.nextAttempt
}

func (r *Runner) saveState(ctx context.Context, tid string, st store.HostSyncStatus) {
	if err := r.st.SaveHostSyncState(ctx, tid, st); err != nil {
		r.log.Warn("host sync state", "tenant", tid, "err", err)
		return
	}
	r.mu.Lock()
	prev := r.lastState[tid]
	r.lastState[tid] = st.Status
	r.mu.Unlock()
	if prev != st.Status {
		if st.Status == store.HostSyncDegraded {
			r.metrics.degrade(ctx, 1)
		} else if prev == store.HostSyncDegraded {
			r.metrics.degrade(ctx, -1)
		}
		r.pub.Publish(ctx, tid, events.HostSyncStatus, events.HostSyncStatusPayload(st.Status))
	}
}

// run is one tenant run's bookkeeping.
type run struct {
	id                         string
	tenant                     string
	fetched, applied, failed   int
	skipped, changes, rejected int
}

// RunTenant polls one tenant (and reconciles it when due).
func (r *Runner) RunTenant(ctx context.Context, tid string) error {
	s, err := r.st.EnsureHostSyncSettings(ctx, tid)
	if err != nil {
		return err
	}
	if !s.Enabled {
		r.saveState(ctx, tid, store.HostSyncStatus{Status: store.HostSyncDisabled, HostsReported: s.HostsReported, HostsFailed: s.HostsFailed})
		r.clearRetry(tid)
		return nil
	}
	ru := &run{id: r.newID(), tenant: tid}
	ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", ru.id)
	start := r.now()
	var since time.Time
	if s.ChangedSince != nil {
		since = *s.ChangedSince
	}
	watermark, pollErr := r.poll(ctx, s, since, ru)
	var reconciled *time.Time
	if pollErr == nil && reconcileDue(s, start) {
		if pollErr = r.reconcile(ctx, s, ru); pollErr == nil {
			reconciled = &start
		}
	}
	st := store.HostSyncStatus{Status: store.HostSyncOK, LastPollAt: &start, HostsReported: ru.applied + ru.skipped, HostsFailed: ru.failed}
	if !watermark.IsZero() {
		st.ChangedSince = &watermark
	}
	if reconciled != nil {
		st.LastReconcileAt, st.ClearReconcile = reconciled, true
	}
	switch {
	case errors.Is(pollErr, repo.ErrSyncDisabled):
		st.Status = store.HostSyncDisabled
	case errors.Is(pollErr, invclient.ErrUnavailable):
		st.Status, st.LastError = store.HostSyncDegraded, invclient.Code(pollErr)
	case pollErr != nil:
		st.Status, st.LastError = store.HostSyncDegraded, "internal"
	}
	r.saveState(ctx, tid, st)
	r.mu.Lock()
	if ru.failed > 0 {
		r.retry[tid] = true
	} else {
		delete(r.retry, tid)
	}
	r.mu.Unlock()
	if ru.fetched > 0 || reconciled != nil {
		r.runAudit(ctx, ru)
		r.log.Info("host sync run", "run_id", ru.id, "tenant", tid, "fetched", ru.fetched, "applied", ru.applied,
			"skipped", ru.skipped, "failed", ru.failed, "rejected", ru.rejected, "changes", ru.changes,
			"duration_ms", r.now().Sub(start).Milliseconds())
	}
	if r.linker != nil && ru.changes > 0 {
		if err := r.linker.Correlate(ctx, tid); err != nil {
			r.log.Warn("port correlation", "tenant", tid, "err", err)
		}
	}
	if errors.Is(pollErr, repo.ErrSyncDisabled) {
		return nil
	}
	return pollErr
}

func (r *Runner) clearRetry(tid string) {
	r.mu.Lock()
	delete(r.retry, tid)
	r.mu.Unlock()
}

func (r *Runner) runAudit(ctx context.Context, ru *run) {
	row, _ := audit.Row(audit.Event{TenantID: ru.tenant, EventType: audit.HostSyncRun, ActorKind: audit.ActorSystem,
		ActorID: audit.HostSyncActor, SubjectKind: audit.SubjectHostSync, SubjectID: ru.id, Outcome: audit.OutcomeOK,
		Details: map[string]any{"hosts_fetched": ru.fetched, "hosts_applied": ru.applied, "hosts_failed": ru.failed,
			"hosts_skipped": ru.skipped, "changes": ru.changes, "run_id": ru.id}}, r.now())
	if err := r.st.AppendAudit(ctx, row); err != nil {
		r.log.Warn("host sync run audit", "tenant", ru.tenant, "err", err)
	}
}

// poll applies every report changed after since. The returned watermark only
// moves past hosts that were applied (or skipped as unchanged), never past a
// failed one.
func (r *Runner) poll(ctx context.Context, s store.HostSyncSettings, since time.Time, ru *run) (time.Time, error) {
	digests, err := r.deviceDigests(ctx, s.TenantID)
	if err != nil {
		return time.Time{}, err
	}
	var watermark time.Time
	blocked := false
	err = r.inv.ListHostReports(ctx, s.TenantID, invclient.Filter{ChangedSince: since}, func(page []invclient.Report) error {
		for _, rep := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			ru.fetched++
			outcome, err := r.handle(ctx, s, rep, TriggerPoll, ru, digests, false)
			if errors.Is(err, repo.ErrSyncDisabled) {
				return err
			}
			if outcome == outcomeFailed {
				blocked = true
			} else if !blocked && rep.ChangedAt.After(watermark) {
				watermark = rep.ChangedAt
			}
			r.sleep(ctx, r.cfg.Pace)
		}
		return nil
	})
	return watermark, err
}

type outcome string

const (
	outcomeApplied  outcome = "applied"
	outcomeSkipped  outcome = "skipped"
	outcomeRejected outcome = "rejected"
	outcomeFailed   outcome = "failed"
)

type deviceRef struct{ digest, state, id string }

func (r *Runner) deviceDigests(ctx context.Context, tid string) (map[string]deviceRef, error) {
	devs, err := r.st.HostDevices(ctx, tid)
	if err != nil {
		return nil, err
	}
	out := make(map[string]deviceRef, len(devs))
	for _, d := range devs {
		out[strings.ToLower(d.InventoryHostID)] = deviceRef{digest: d.ReportDigest, state: d.ReportState, id: d.ID}
	}
	return out, nil
}

// handle applies one report unless its digest is unchanged (force skips the
// shortcut) and books the outcome.
func (r *Runner) handle(ctx context.Context, s store.HostSyncSettings, rep invclient.Report, trigger string, ru *run, digests map[string]deviceRef, force bool) (outcome, error) {
	if d, ok := digests[strings.ToLower(rep.Host.ID)]; ok && !force && rep.Digest != "" && d.digest == rep.Digest && d.state == store.RepReported {
		ru.skipped++
		r.metrics.host(ctx, string(outcomeSkipped))
		return outcomeSkipped, nil
	}
	res, err := r.Apply(ctx, s, rep, trigger, ru.id)
	switch {
	case errors.Is(err, repo.ErrSyncDisabled):
		return outcomeFailed, err
	case errors.Is(err, hostreport.ErrTenant), errors.Is(err, hostreport.ErrHostID):
		ru.rejected++
		r.metrics.host(ctx, string(outcomeRejected))
		r.log.Warn("host report rejected", "tenant", s.TenantID, "reason", err.Error())
		return outcomeRejected, nil
	case err != nil:
		ru.failed++
		r.metrics.host(ctx, string(outcomeFailed))
		r.log.Warn("host report apply failed", "tenant", s.TenantID, "inventory_host_id", rep.Host.ID, "err", err)
		return outcomeFailed, nil
	}
	ru.applied++
	ru.changes += res.Changes
	r.metrics.host(ctx, string(outcomeApplied))
	return outcomeApplied, nil
}

// Result is the outcome of one applied report.
type Result struct {
	DeviceID string
	Applied  bool
	Changes  int
	Issues   []store.HostSyncIssue
}

// Apply validates one report and applies it in one tenant transaction
// (retried once on a unique-constraint race), then publishes its events.
// A retired host is only marked as no longer reported.
func (r *Runner) Apply(ctx context.Context, s store.HostSyncSettings, rep invclient.Report, trigger, runID string) (Result, error) {
	nr, err := hostreport.Normalize(rep, s.TenantID)
	if err != nil {
		return Result{}, err
	}
	for _, is := range nr.Issues {
		r.metrics.skip(ctx, is.Reason, is.Count)
	}
	if nr.HostStatus == "retired" {
		return r.markGone(ctx, s.TenantID, nr.HostID, "retired", trigger, runID)
	}
	start := time.Now()
	var plan hostplan.Plan
	attempt := func() error {
		return r.st.ApplyHostReport(ctx, s.TenantID, func(tx repo.HostTx) error {
			state, err := load(tx, nr)
			if err != nil {
				return err
			}
			plan = hostplan.Build(state, nr, hostplan.Params{
				TenantID: s.TenantID, RunID: runID, Trigger: trigger, Now: r.now(), Exclusions: s.ExcludedInterfaces,
				ConflictMoves: r.cfg.ConflictMoves, ConflictWindow: r.cfg.ConflictWindow, NewID: r.newID,
			})
			if err := execute(tx, plan); err != nil {
				return err
			}
			return tx.SaveDeviceState(plan.State)
		})
	}
	err = attempt()
	if errors.Is(err, repo.ErrConflict) {
		err = attempt() // two replicas created the same subnet/device: re-plan once
	}
	r.metrics.applied(ctx, time.Since(start).Seconds())
	if err != nil {
		return Result{}, err
	}
	for _, op := range plan.Ops {
		r.metrics.change(ctx, string(op.Kind), 1)
	}
	for _, e := range plan.Events {
		r.pub.Publish(ctx, s.TenantID, e.Type, e.Payload)
	}
	return Result{DeviceID: plan.DeviceID, Applied: true, Changes: len(plan.Ops), Issues: plan.Issues}, nil
}

// markGone flags the device of a retired or deleted inventory host (and its
// reported interfaces and addresses) as no longer reported; nothing is
// deleted.
func (r *Runner) markGone(ctx context.Context, tid, hostID, why, trigger, runID string) (Result, error) {
	var res Result
	err := r.st.ApplyHostReport(ctx, tid, func(tx repo.HostTx) error {
		d, ok, err := tx.DeviceByInventoryHost(hostID)
		if err != nil || !ok || d.ReportState == store.RepNotReported {
			return err
		}
		if err := tx.MarkDeviceNotReported(d.ID); err != nil {
			return err
		}
		row, _ := audit.Row(audit.Event{TenantID: tid, EventType: audit.DeviceNotReported, ActorKind: audit.ActorSystem,
			ActorID: audit.HostSyncActor, SubjectKind: audit.SubjectDevice, SubjectID: d.ID, Outcome: audit.OutcomeOK,
			Details: map[string]any{"inventory_status": why, "inventory_host_id": hostID, "run_id": runID, "trigger": trigger}}, r.now())
		res = Result{DeviceID: d.ID, Applied: true, Changes: 1}
		return tx.AppendAudit(row)
	})
	if err == nil && res.Applied {
		r.pub.Publish(ctx, tid, events.HostSyncApplied, events.HostSyncAppliedPayload(res.DeviceID, 1))
	}
	return res, err
}
