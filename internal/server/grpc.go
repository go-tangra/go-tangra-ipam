package server

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/selector"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/grpc"

	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-ipam/internal/cert"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/metrics"
	customLogging "github.com/go-tangra/go-tangra-ipam/internal/middleware/logging"
	"github.com/go-tangra/go-tangra-ipam/internal/service"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"

	commonV1 "github.com/go-tangra/go-tangra-common/gen/go/common/service/v1"
	appViewer "github.com/go-tangra/go-tangra-common/viewer"
	"github.com/go-tangra/go-tangra-common/middleware/audit"
	"github.com/go-tangra/go-tangra-common/middleware/claims"
	"github.com/go-tangra/go-tangra-common/middleware/mtls"
)

// NewGrpcWhiteListMatcher defines public endpoints that don't require authentication
func NewGrpcWhiteListMatcher() selector.MatchFunc {
	whiteList := make(map[string]bool)
	whiteList["/ipam.service.v1.SystemService/HealthCheck"] = true

	return func(ctx context.Context, operation string) bool {
		if _, ok := whiteList[operation]; ok {
			return false
		}
		return true
	}
}

// systemViewerMiddleware injects system viewer context for all requests
// This allows the IPAM service to bypass tenant privacy checks at the ent level
func systemViewerMiddleware() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (interface{}, error) {
			ctx = appViewer.NewSystemViewerContext(ctx)
			return handler(ctx, req)
		}
	}
}

// NewGRPCServer creates a gRPC server with mTLS and audit logging
func NewGRPCServer(
	ctx *bootstrap.Context,
	certManager *cert.CertManager,
	collector *metrics.Collector,
	auditLogRepo *data.AuditLogRepo,
	systemSvc *service.SystemService,
	subnetSvc *service.SubnetService,
	vlanSvc *service.VlanService,
	deviceSvc *service.DeviceService,
	locationSvc *service.LocationService,
	ipAddressSvc *service.IpAddressService,
	ipScanSvc *service.IpScanService,
	ipGroupSvc *service.IpGroupService,
	hostGroupSvc *service.HostGroupService,
	backupSvc *service.BackupService,
	devicePackageSvc *service.DevicePackageService,
	taskExecutor *service.TaskExecutor,
	sqlBackupSvc *service.SqlBackupService,
) *grpc.Server {
	cfg := ctx.GetConfig()
	logger := ctx.GetLogger()
	l := ctx.NewLoggerHelper("ipam/grpc")

	var opts []grpc.ServerOption

	// Get gRPC server configuration
	if cfg.Server != nil && cfg.Server.Grpc != nil {
		opts = append(opts, grpc.Address(cfg.Server.Grpc.Addr))
		opts = append(opts, grpc.Timeout(cfg.Server.Grpc.Timeout.AsDuration()))
	}

	// Configure TLS if certificates are available
	tlsEnabled := false
	if certManager != nil && certManager.IsTLSEnabled() {
		tlsConfig, err := certManager.GetServerTLSConfig()
		if err != nil {
			l.Warnf("Failed to get TLS config, running without TLS: %v", err)
		} else {
			opts = append(opts, grpc.TLSConfig(tlsConfig))
			l.Info("gRPC server configured with mTLS")
			tlsEnabled = true
		}
	} else {
		l.Warn("TLS not enabled, running without mTLS")
	}

	// Build middleware stack
	var ms []middleware.Middleware
	ms = append(ms, recovery.Recovery())
	ms = append(ms, collector.Middleware())
	ms = append(ms, systemViewerMiddleware()) // Inject system viewer for ENT privacy
	ms = append(ms, customLogging.RedactedServer(logger))

	// Add mTLS middleware only when TLS is enabled
	// When TLS is disabled (e.g., for dynamic routing from admin gateway), skip cert validation
	if tlsEnabled {
		ms = append(ms, mtls.MTLSMiddleware(
			logger,
			mtls.WithPublicEndpoints(
				"/grpc.health.v1.Health/Check",
				"/grpc.health.v1.Health/Watch",
				"/ipam.service.v1.SystemService/HealthCheck",
			),
			// mTLS caller allow-list (client cert CN "lcm-<module>"); gateway=lcm-admin, backup=lcm-backup.
			mtls.WithAllowedIdentities("lcm-admin", "lcm-backup"),
		))
	} else {
		l.Warn("mTLS middleware disabled (TLS not configured)")
	}

	// Bind x-md-global-* user claims to the gateway's HMAC assertion so a direct
	// mTLS caller cannot forge platform:admin (CRIT-3.2). No-op for calls that
	// carry no user claims; strips unverified claims in enforce mode.
	ms = append(ms, claims.Server(logger))

	// Add audit logging middleware
	ms = append(ms, audit.Server(
		logger,
		audit.WithServiceName("ipam-service"),
		audit.WithWriteAuditLogFunc(func(ctx context.Context, log *audit.AuditLog) error {
			return auditLogRepo.CreateFromEntry(ctx, log.ToEntry())
		}),
		audit.WithSkipOperations(
			"/grpc.health.v1.Health/Check",
			"/grpc.health.v1.Health/Watch",
			"/ipam.service.v1.SystemService/HealthCheck",
			"/ipam.service.v1.BackupService/ExportBackup",
			"/ipam.service.v1.BackupService/ImportBackup",
		),
	))

	ms = append(ms, validate.Validator())

	opts = append(opts, grpc.Middleware(ms...))

	srv := grpc.NewServer(opts...)

	// Register services with redacted wrappers to prevent sensitive data from leaking in logs
	ipamV1.RegisterRedactedSystemServiceServer(srv, systemSvc, nil)
	ipamV1.RegisterRedactedSubnetServiceServer(srv, subnetSvc, nil)
	ipamV1.RegisterRedactedVlanServiceServer(srv, vlanSvc, nil)
	ipamV1.RegisterRedactedDeviceServiceServer(srv, deviceSvc, nil)
	ipamV1.RegisterRedactedLocationServiceServer(srv, locationSvc, nil)
	ipamV1.RegisterRedactedIpAddressServiceServer(srv, ipAddressSvc, nil)
	ipamV1.RegisterRedactedIpScanServiceServer(srv, ipScanSvc, nil)
	ipamV1.RegisterRedactedIpGroupServiceServer(srv, ipGroupSvc, nil)
	ipamV1.RegisterRedactedHostGroupServiceServer(srv, hostGroupSvc, nil)
	ipamV1.RegisterRedactedBackupServiceServer(srv, backupSvc, nil)
	ipamV1.RegisterRedactedDevicePackageServiceServer(srv, devicePackageSvc, nil)

	// TaskExecutorService lets the scheduler trigger IPAM tasks (e.g. network
	// scans) on a cron or one-off basis.
	commonV1.RegisterTaskExecutorServiceServer(srv, taskExecutor)

	// Streaming SQL-dump backup (schema-agnostic; replaces the legacy per-field
	// ipam.service.v1.BackupService, which stays registered during transition).
	commonV1.RegisterBackupServiceServer(srv, sqlBackupSvc)

	return srv
}
