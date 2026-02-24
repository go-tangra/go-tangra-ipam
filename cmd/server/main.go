package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	kratosHttp "github.com/go-kratos/kratos/v2/transport/http"

	conf "github.com/tx7do/kratos-bootstrap/api/gen/go/conf/v1"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-common/registration"
	pkgService "github.com/go-tangra/go-tangra-common/service"
	"github.com/go-tangra/go-tangra-ipam/cmd/server/assets"
	"github.com/go-tangra/go-tangra-ipam/internal/service"
)

var (
	// Module info
	moduleID    = "ipam"
	moduleName  = "IPAM"
	version     = "1.0.0"
	description = "IP Address Management service for managing subnets, IP addresses, VLANs, devices, and locations"
)

// Global references for cleanup
var globalScanExecutor *service.ScanExecutor
var globalRegHelper *registration.RegistrationHelper

func newApp(
	ctx *bootstrap.Context,
	gs *grpc.Server,
	hs *kratosHttp.Server,
	scanExecutor *service.ScanExecutor,
) *kratos.App {
	// Start the scan executor and store reference for cleanup
	globalScanExecutor = scanExecutor
	if scanExecutor != nil {
		if err := scanExecutor.Start(); err != nil {
			log.Warnf("Failed to start scan executor: %v", err)
		}
	}

	globalRegHelper = registration.StartRegistration(ctx, ctx.GetLogger(), &registration.Config{
		ModuleID:          moduleID,
		ModuleName:        moduleName,
		Version:           version,
		Description:       description,
		GRPCEndpoint:      registration.GetGRPCAdvertiseAddr(ctx, "0.0.0.0:9400"),
		AdminEndpoint:     registration.GetEnvOrDefault("ADMIN_GRPC_ENDPOINT", ""),
		FrontendEntryUrl:  registration.GetEnvOrDefault("FRONTEND_ENTRY_URL", ""),
		HttpEndpoint:      registration.GetEnvOrDefault("HTTP_ADVERTISE_ADDR", ""),
		OpenapiSpec:       assets.OpenApiData,
		ProtoDescriptor:   assets.DescriptorData,
		MenusYaml:         assets.MenusData,
		HeartbeatInterval: 30 * time.Second,
		RetryInterval:     5 * time.Second,
		MaxRetries:        60,
	})

	return bootstrap.NewApp(ctx, gs, hs)
}

// stopServices stops background services (called from wire cleanup)
func stopServices() {
	if globalScanExecutor != nil {
		if err := globalScanExecutor.Stop(); err != nil {
			log.Warnf("Failed to stop scan executor: %v", err)
		}
	}
}

func runApp() error {
	ctx := bootstrap.NewContext(
		context.Background(),
		&conf.AppInfo{
			Project: pkgService.Project,
			AppId:   "ipam.service",
			Version: version,
		},
	)

	// Ensure services are stopped on exit
	defer stopServices()
	defer globalRegHelper.Stop()

	return bootstrap.RunApp(ctx, initApp)
}

func main() {
	if err := runApp(); err != nil {
		panic(err)
	}
}

