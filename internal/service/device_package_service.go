package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
)

type DevicePackageService struct {
	ipamV1.UnimplementedDevicePackageServiceServer

	log              *log.Helper
	deviceRepo       *data.DeviceRepo
	devicePkgRepo    *data.DevicePackageRepo
}

func NewDevicePackageService(ctx *bootstrap.Context, deviceRepo *data.DeviceRepo, devicePkgRepo *data.DevicePackageRepo) *DevicePackageService {
	return &DevicePackageService{
		log:           ctx.NewLoggerHelper("ipam/service/device_package"),
		deviceRepo:    deviceRepo,
		devicePkgRepo: devicePkgRepo,
	}
}

func (s *DevicePackageService) SyncDevicePackages(ctx context.Context, req *ipamV1.SyncDevicePackagesRequest) (*ipamV1.SyncDevicePackagesResponse, error) {
	// Verify device exists
	device, err := s.deviceRepo.GetByID(ctx, req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}

	tenantID := tenantIDFromDevice(device)

	// Sync packages
	err = s.devicePkgRepo.SyncPackages(ctx, tenantID, req.GetDeviceId(), req.GetPackages(), req.GetPackageManager())
	if err != nil {
		return nil, err
	}

	// Count stats for response
	total, updatesAvailable, securityUpdates, _, err := s.devicePkgRepo.GetStatsByDevice(ctx, tenantID, req.GetDeviceId())
	if err != nil {
		return nil, err
	}

	return &ipamV1.SyncDevicePackagesResponse{
		Total:           ptrInt32(int32(total)),
		UpdatesAvailable: ptrInt32(int32(updatesAvailable)),
		SecurityUpdates: ptrInt32(int32(securityUpdates)),
	}, nil
}

func (s *DevicePackageService) ListDevicePackages(ctx context.Context, req *ipamV1.ListDevicePackagesRequest) (*ipamV1.ListDevicePackagesResponse, error) {
	// Verify device exists and get tenant_id
	device, err := s.deviceRepo.GetByID(ctx, req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}

	tenantID := tenantIDFromDevice(device)

	filters := make(map[string]interface{})
	if req.NeedsUpdate != nil {
		filters["needs_update"] = *req.NeedsUpdate
	}
	if req.IsSecurityUpdate != nil {
		filters["is_security_update"] = *req.IsSecurityUpdate
	}
	if req.PackageManager != nil {
		filters["package_manager"] = *req.PackageManager
	}
	if req.Query != nil {
		filters["query"] = *req.Query
	}

	page := int(req.GetPage())
	pageSize := int(req.GetPageSize())
	if req.GetNoPaging() {
		page = 0
		pageSize = 0
	}

	entities, total, err := s.devicePkgRepo.ListByDevice(ctx, tenantID, req.GetDeviceId(), page, pageSize, filters, req.GetOrderBy())
	if err != nil {
		return nil, err
	}

	items := make([]*ipamV1.DevicePackage, len(entities))
	for i, e := range entities {
		items[i] = devicePackageToProto(e)
	}

	return &ipamV1.ListDevicePackagesResponse{
		Items: items,
		Total: ptrInt32(int32(total)),
	}, nil
}

func (s *DevicePackageService) GetDevicePackageStats(ctx context.Context, req *ipamV1.GetDevicePackageStatsRequest) (*ipamV1.GetDevicePackageStatsResponse, error) {
	// Verify device exists and get tenant_id
	device, err := s.deviceRepo.GetByID(ctx, req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}

	tenantID := tenantIDFromDevice(device)

	total, updatesAvailable, securityUpdates, lastSync, err := s.devicePkgRepo.GetStatsByDevice(ctx, tenantID, req.GetDeviceId())
	if err != nil {
		return nil, err
	}

	resp := &ipamV1.GetDevicePackageStatsResponse{
		Total:           ptrInt32(int32(total)),
		UpdatesAvailable: ptrInt32(int32(updatesAvailable)),
		SecurityUpdates: ptrInt32(int32(securityUpdates)),
	}
	if lastSync != nil {
		resp.LastSync = timestamppb.New(*lastSync)
	}

	return resp, nil
}

func (s *DevicePackageService) DeleteDevicePackages(ctx context.Context, req *ipamV1.DeleteDevicePackagesRequest) (*emptypb.Empty, error) {
	// Verify device exists and get tenant_id
	device, err := s.deviceRepo.GetByID(ctx, req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}

	tenantID := tenantIDFromDevice(device)

	_, err = s.devicePkgRepo.DeleteByDevice(ctx, tenantID, req.GetDeviceId())
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func tenantIDFromDevice(d *ent.Device) uint32 {
	if d.TenantID != nil {
		return *d.TenantID
	}
	return 0
}

func devicePackageToProto(e *ent.DevicePackage) *ipamV1.DevicePackage {
	if e == nil {
		return nil
	}

	result := &ipamV1.DevicePackage{
		Id:               &e.ID,
		DeviceId:         &e.DeviceID,
		TenantId:         e.TenantID,
		Name:             ptrString(e.Name),
		CurrentVersion:   ptrString(e.CurrentVersion),
		AvailableVersion: ptrString(e.AvailableVersion),
		NeedsUpdate:      &e.NeedsUpdate,
		IsSecurityUpdate: &e.IsSecurityUpdate,
		PackageManager:   ptrString(e.PackageManager),
		Description:      ptrString(e.Description),
	}
	if e.CreateTime != nil {
		result.CreatedAt = timestamppb.New(*e.CreateTime)
	}
	if e.UpdateTime != nil {
		result.UpdatedAt = timestamppb.New(*e.UpdateTime)
	}

	return result
}
