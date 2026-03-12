package providers

import (
	"github.com/google/wire"

	"github.com/go-tangra/go-tangra-ipam/internal/metrics"
	"github.com/go-tangra/go-tangra-ipam/internal/service"
)

var ProviderSet = wire.NewSet(
	metrics.NewCollector,
	service.NewSystemService,
	service.NewSubnetService,
	service.NewVlanService,
	service.NewDeviceService,
	service.NewLocationService,
	service.NewIpAddressService,
	service.NewIpScanService,
	service.NewScanExecutor,
	service.NewIpGroupService,
	service.NewHostGroupService,
	service.NewBackupService,
	service.NewDevicePackageService,
)
