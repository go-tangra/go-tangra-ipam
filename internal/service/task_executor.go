package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	commonV1 "github.com/go-tangra/go-tangra-common/gen/go/common/service/v1"
	appViewer "github.com/go-tangra/go-tangra-common/viewer"

	"github.com/go-tangra/go-tangra-ipam/internal/data"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
)

// taskTypeScanNetwork is the scheduler task type that triggers an IPAM scan of
// a specific subnet or of every subnet for the tenant.
const taskTypeScanNetwork = "ipam:scan-network"

// scanNetworkPayloadSchema is the JSON Schema advertised to the scheduler for
// the ipam:scan-network task. All fields are optional; with none set (or
// all=true) every scannable subnet for the tenant is scanned.
const scanNetworkPayloadSchema = `{"type":"object","properties":{` +
	`"all":{"type":"boolean","description":"Scan every subnet for the tenant. Default when no subnetId/cidr is given."},` +
	`"subnetId":{"type":"string","description":"UUID of a single subnet to scan."},` +
	`"cidr":{"type":"string","description":"CIDR of a single subnet to scan (resolved to its subnet)."},` +
	`"enableSnmp":{"type":"boolean","description":"Enable SNMP device + switch-port discovery during the scan."},` +
	`"enableDnsUpdate":{"type":"boolean","description":"Fire DNS-sync events for discovered hosts."}` +
	`}}`

// scanNetworkConfig is the decoded ipam:scan-network task payload.
type scanNetworkConfig struct {
	All             bool   `json:"all"`
	SubnetID        string `json:"subnetId"`
	CIDR            string `json:"cidr"`
	EnableSnmp      bool   `json:"enableSnmp"`
	EnableDnsUpdate bool   `json:"enableDnsUpdate"`
}

// TaskExecutor implements common.service.v1.TaskExecutorService so the
// scheduler can trigger IPAM work on a cron or one-off basis. It enqueues scan
// jobs (which the background ScanExecutor then processes) rather than blocking
// on scan completion, so a "scan all" of many subnets returns promptly.
type TaskExecutor struct {
	commonV1.UnimplementedTaskExecutorServiceServer

	log        *log.Helper
	ipScan     *IpScanService
	subnetRepo *data.SubnetRepo
}

func NewTaskExecutor(ctx *bootstrap.Context, ipScan *IpScanService, subnetRepo *data.SubnetRepo) *TaskExecutor {
	return &TaskExecutor{
		log:        ctx.NewLoggerHelper("task-executor/ipam-service"),
		ipScan:     ipScan,
		subnetRepo: subnetRepo,
	}
}

// ExecuteTask is the entry point the scheduler calls via gRPC when a schedule
// fires.
func (e *TaskExecutor) ExecuteTask(ctx context.Context, req *commonV1.ExecuteTaskRequest) (*commonV1.ExecuteTaskResponse, error) {
	e.log.Infof("Executing task %s (execution=%s, attempt=%d/%d, tenant=%d)",
		req.GetTaskType(), req.GetExecutionId(), req.GetAttempt(), req.GetMaxAttempts(), req.GetTenantId())

	switch req.GetTaskType() {
	case taskTypeScanNetwork:
		return e.handleScanNetwork(ctx, req)
	default:
		return &commonV1.ExecuteTaskResponse{
			Success:          false,
			PermanentFailure: true,
			Message:          fmt.Sprintf("unknown task type: %s", req.GetTaskType()),
		}, nil
	}
}

// handleScanNetwork enqueues scans for the targeted subnet(s). Scans run
// asynchronously in the ScanExecutor; this returns once the jobs are queued.
func (e *TaskExecutor) handleScanNetwork(ctx context.Context, req *commonV1.ExecuteTaskRequest) (*commonV1.ExecuteTaskResponse, error) {
	var cfg scanNetworkConfig
	if raw := req.GetPayload(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return &commonV1.ExecuteTaskResponse{
				Success:          false,
				PermanentFailure: true,
				Message:          fmt.Sprintf("invalid payload: %v", err),
			}, nil
		}
	}

	tenantID := req.GetTenantId()
	// Use a system viewer context so subnet lookups/listing bypass tenant
	// privacy filters; the tenant is still scoped explicitly by tenantID.
	sysCtx := appViewer.NewSystemViewerContext(ctx)

	// Resolve the target subnet IDs.
	var subnetIDs []string
	switch {
	case cfg.SubnetID != "":
		subnetIDs = []string{cfg.SubnetID}
	case cfg.CIDR != "":
		id, err := e.resolveSubnetByCIDR(sysCtx, tenantID, cfg.CIDR)
		if err != nil {
			return &commonV1.ExecuteTaskResponse{Success: false, PermanentFailure: true, Message: err.Error()}, nil
		}
		subnetIDs = []string{id}
	default: // all (explicit or implied)
		ids, err := e.listSubnetIDs(sysCtx, tenantID)
		if err != nil {
			return &commonV1.ExecuteTaskResponse{Success: false, Message: fmt.Sprintf("list subnets: %v", err)}, nil
		}
		subnetIDs = ids
	}

	if len(subnetIDs) == 0 {
		return &commonV1.ExecuteTaskResponse{Success: true, Message: "no subnets to scan"}, nil
	}

	enableSnmp := cfg.EnableSnmp
	enableDNS := cfg.EnableDnsUpdate
	var queued, skipped, failed int
	var notes []string
	for _, id := range subnetIDs {
		_, err := e.ipScan.StartScan(sysCtx, &ipamV1.StartScanRequest{
			TenantId:        &tenantID,
			SubnetId:        id,
			EnableSnmp:      &enableSnmp,
			EnableDnsUpdate: &enableDNS,
		})
		switch {
		case err == nil:
			queued++
		case ipamV1.IsScanAlreadyInProgress(err), ipamV1.IsIpv6NotSupported(err), ipamV1.IsSubnetTooLarge(err):
			// Expected, non-fatal: already running, or not scannable.
			skipped++
		default:
			failed++
			notes = append(notes, fmt.Sprintf("%s: %v", id, err))
		}
	}

	msg := fmt.Sprintf("queued %d scan(s), skipped %d", queued, skipped)
	if failed > 0 {
		msg += fmt.Sprintf(", failed %d (%s)", failed, strings.Join(notes, "; "))
	}
	e.log.Infof("scan-network task done: %s", msg)

	// Succeed as long as nothing failed unexpectedly; transient failures are
	// retried by the scheduler, so do not mark permanent.
	return &commonV1.ExecuteTaskResponse{Success: failed == 0, Message: msg}, nil
}

// resolveSubnetByCIDR finds the tenant's subnet matching the given CIDR.
func (e *TaskExecutor) resolveSubnetByCIDR(ctx context.Context, tenantID uint32, cidr string) (string, error) {
	cidr = strings.TrimSpace(cidr)
	const pageSize = 200
	for page := 1; ; page++ {
		subnets, total, err := e.subnetRepo.List(ctx, tenantID, page, pageSize, nil)
		if err != nil {
			return "", fmt.Errorf("list subnets: %w", err)
		}
		for _, s := range subnets {
			if s.Cidr == cidr {
				return s.ID, nil
			}
		}
		if page*pageSize >= total || len(subnets) == 0 {
			break
		}
	}
	return "", fmt.Errorf("no subnet found for cidr %q", cidr)
}

// listSubnetIDs returns every subnet ID for the tenant (paginated).
func (e *TaskExecutor) listSubnetIDs(ctx context.Context, tenantID uint32) ([]string, error) {
	const pageSize = 200
	var ids []string
	for page := 1; ; page++ {
		subnets, total, err := e.subnetRepo.List(ctx, tenantID, page, pageSize, nil)
		if err != nil {
			return nil, err
		}
		for _, s := range subnets {
			ids = append(ids, s.ID)
		}
		if page*pageSize >= total || len(subnets) == 0 {
			break
		}
	}
	return ids, nil
}
