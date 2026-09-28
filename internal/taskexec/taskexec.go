// Package taskexec executes the ipam task types the scheduler runs (feature
// 026): "ipam:scan-network" queues subnet scans through the scan service for
// the request's tenant. The executor server itself (caller check, tenant and
// payload bounds) is the scheduler SDK's taskexec.Server; this package only
// supplies the handlers, the type descriptors registered with the scheduler
// and the mesh caller check.
//
// Security: the tenant comes from the request (the SDK server accepts only
// UUIDs and only from the scheduler); every lookup and scan is scoped to it,
// so a task can never reach another tenant's subnet. The payload is untrusted:
// it is decoded strictly and never echoed into messages, results or audit.
package taskexec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-tangra/go-tangra/v4/authn"

	"github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/schedulerclient"
	schedexec "github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/taskexec"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TypeScanNetwork queues scans of one subnet or of every subnet of the tenant.
const TypeScanNetwork = "ipam:scan-network"

// maxNotes bounds the failure notes carried in the result message.
const maxNotes = 5

const scanNetworkSchema = `{"type":"object","additionalProperties":false,"properties":{` +
	`"all":{"type":"boolean","description":"Scan every subnet for the tenant. Default when no subnetId/cidr is given."},` +
	`"subnetId":{"type":"string","format":"uuid","description":"UUID of a single subnet to scan."},` +
	`"cidr":{"type":"string","maxLength":64,"description":"CIDR of a single subnet to scan (resolved to its subnet)."},` +
	`"enableSnmp":{"type":"boolean","description":"Enable SNMP device + switch-port discovery during the scan. Unset: run it when the subnet has SNMP credentials."},` +
	`"enableDnsUpdate":{"type":"boolean","description":"Fire DNS-sync events for discovered hosts."}` +
	`}}`

// Descriptors are the task types ipam registers with the scheduler.
func Descriptors() []schedulerclient.Descriptor {
	return []schedulerclient.Descriptor{{
		Type:        TypeScanNetwork,
		DisplayName: "Scan network",
		Description: "Run an IPAM network scan. Target a single subnet (subnetId or cidr) or, with no target (or all=true), " +
			"every subnet for the tenant. Optionally enable SNMP device/switch-port discovery and DNS sync.",
		PayloadSchema:   scanNetworkSchema,
		DefaultCron:     "0 3 * * *",
		DefaultMaxRetry: 2,
	}}
}

// Scanner queues one subnet scan (scan.Service satisfies it).
type Scanner interface {
	StartScan(ctx context.Context, subj authz.Subjects, subnetID string, opts scan.Options) (store.IPScanJob, error)
}

// Store lists the tenant's subnets and appends audit rows (repo.Store satisfies it).
type Store interface {
	AllSubnetCIDRs(ctx context.Context, tenantID string) ([]store.Subnet, error)
	AppendAudit(ctx context.Context, row store.AuditRow) error
}

// Executor runs the ipam task types.
type Executor struct {
	st    Store
	scan  Scanner
	actor string
}

// New builds the executor; actor is the scheduler's SPIFFE id recorded as the
// creator of the queued scans and in their audit rows.
func New(st Store, sc Scanner, actor string) *Executor {
	return &Executor{st: st, scan: sc, actor: actor}
}

// Handlers maps the task types to their handlers for schedexec.NewServer.
func (e *Executor) Handlers() map[string]schedexec.Handler {
	return map[string]schedexec.Handler{TypeScanNetwork: e.ScanNetwork}
}

// Actor is the SPIFFE id of the scheduler service in trust domain td.
func Actor(td, service string) string { return "spiffe://" + td + "/svc/" + service }

// Caller returns the verified mesh peer's service name when the peer belongs
// to trust domain td (schedexec.Options.Caller).
func Caller(td string) func(ctx context.Context) (string, bool) {
	return func(ctx context.Context) (string, bool) {
		p, ok := authn.FromContext(ctx)
		if !ok || p.ID.TrustDomain() != td {
			return "", false
		}
		return p.ID.ServiceName(), true
	}
}

// scanNetworkPayload is the ipam:scan-network payload.
type scanNetworkPayload struct {
	All             bool   `json:"all"`
	SubnetID        string `json:"subnetId"`
	CIDR            string `json:"cidr"`
	EnableSnmp      *bool  `json:"enableSnmp"`
	EnableDNSUpdate bool   `json:"enableDnsUpdate"`
}

// Summary is the result data of ipam:scan-network.
type Summary struct {
	Queued  int      `json:"queued"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	JobIDs  []string `json:"job_ids"`
}

// ScanNetwork queues the scans of the payload's target in the request tenant.
// Already-active, IPv6 and too-large subnets are skipped; any other failure
// makes the attempt retryable. Invalid payloads and unknown targets are
// permanent failures.
func (e *Executor) ScanNetwork(ctx context.Context, req schedexec.Request) schedexec.Result {
	var p scanNetworkPayload
	if err := schedexec.DecodeStrict(req.Payload, &p); err != nil {
		return schedexec.Permanent("invalid payload: " + strings.TrimPrefix(err.Error(), schedexec.ErrPayload.Error()+": "))
	}
	p.CIDR = strings.TrimSpace(p.CIDR)
	switch {
	case p.SubnetID != "" && p.CIDR != "":
		return schedexec.Permanent("subnetId and cidr are mutually exclusive")
	case p.All && (p.SubnetID != "" || p.CIDR != ""):
		return schedexec.Permanent("all cannot be combined with subnetId or cidr")
	case p.SubnetID != "" && !schedexec.ValidTenant(p.SubnetID):
		return schedexec.Permanent("subnetId must be a UUID")
	}

	var targets []string
	single := p.SubnetID != "" || p.CIDR != ""
	switch {
	case p.SubnetID != "":
		targets = []string{p.SubnetID}
	case p.CIDR != "":
		want, err := canonical(p.CIDR)
		if err != nil {
			return schedexec.Permanent("cidr is not a valid CIDR")
		}
		subnets, err := e.st.AllSubnetCIDRs(ctx, req.TenantID)
		if err != nil {
			return schedexec.Retry("listing subnets failed")
		}
		for _, s := range subnets {
			if c, err := canonical(s.CIDR); err == nil && c == want {
				targets = []string{s.ID}
				break
			}
		}
		if targets == nil {
			return schedexec.Permanent("no subnet found for cidr")
		}
	default:
		subnets, err := e.st.AllSubnetCIDRs(ctx, req.TenantID)
		if err != nil {
			return schedexec.Retry("listing subnets failed")
		}
		for _, s := range subnets {
			targets = append(targets, s.ID)
		}
		if len(targets) == 0 {
			return schedexec.OK("no subnets to scan")
		}
	}

	subj := authz.Subjects{TenantID: req.TenantID, UserID: e.actor, ActorKind: authz.ActorService}
	opts := scan.Options{
		EnableSNMP:      p.EnableSnmp != nil && *p.EnableSnmp,
		SNMPAuto:        p.EnableSnmp == nil,
		EnableDNSUpdate: p.EnableDNSUpdate,
		Trigger:         store.TriggerAuto,
	}
	sum := Summary{JobIDs: []string{}}
	var notes []string
	for _, id := range targets {
		job, err := e.scan.StartScan(ctx, subj, id, opts)
		switch {
		case err == nil:
			sum.Queued++
			sum.JobIDs = append(sum.JobIDs, job.ID)
			e.audit(ctx, req, job)
		case errors.Is(err, scan.ErrActiveScan), errors.Is(err, scan.ErrIPv6), errors.Is(err, scan.ErrTooLarge):
			sum.Skipped++
		case single && errors.Is(err, repo.ErrNotFound):
			return schedexec.Permanent("subnet not found")
		default:
			sum.Failed++
			if len(notes) < maxNotes {
				notes = append(notes, note(err))
			} else if len(notes) == maxNotes {
				notes = append(notes, "…")
			}
		}
	}

	msg := fmt.Sprintf("queued %d scan(s), skipped %d", sum.Queued, sum.Skipped)
	if sum.Failed > 0 {
		msg += fmt.Sprintf(", failed %d (%s)", sum.Failed, strings.Join(notes, "; "))
		r := schedexec.Retry(msg)
		r.Data = sum
		return r
	}
	r := schedexec.OK(msg)
	r.Data = sum
	return r
}

// audit records the scheduler-queued scan; a failed write never fails the
// run because the scan is already queued.
func (e *Executor) audit(ctx context.Context, req schedexec.Request, job store.IPScanJob) {
	row, _ := audit.Row(audit.Event{
		TenantID: req.TenantID, EventType: audit.ScanStarted, ActorKind: audit.ActorService, ActorID: e.actor,
		SubjectKind: audit.SubjectScan, SubjectID: job.ID, Outcome: audit.OutcomeOK,
		Details: map[string]any{"trigger": "scheduler", "execution_id": req.ExecutionID, "subnet_id": job.SubnetID},
	}, job.CreatedAt)
	_ = e.st.AppendAudit(ctx, row)
}

// note names a scan failure without store detail or payload values.
func note(err error) string {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return "subnet not found"
	case errors.Is(err, authz.ErrForbidden):
		return "refused"
	default:
		return "internal error"
	}
}

// canonical returns the network/prefix form of a CIDR.
func canonical(cidr string) (string, error) {
	info, err := ipnet.Parse(cidr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%d", info.Network, info.PrefixLen), nil
}
