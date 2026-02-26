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

type DeviceService struct {
	ipamV1.UnimplementedDeviceServiceServer

	log                 *log.Helper
	deviceRepo          *data.DeviceRepo
	deviceInterfaceRepo *data.DeviceInterfaceRepo
	ipAddressRepo       *data.IpAddressRepo
	devicePackageRepo   *data.DevicePackageRepo
}

func NewDeviceService(ctx *bootstrap.Context, deviceRepo *data.DeviceRepo, deviceInterfaceRepo *data.DeviceInterfaceRepo, ipAddressRepo *data.IpAddressRepo, devicePackageRepo *data.DevicePackageRepo) *DeviceService {
	return &DeviceService{
		log:                 ctx.NewLoggerHelper("ipam/service/device"),
		deviceRepo:          deviceRepo,
		deviceInterfaceRepo: deviceInterfaceRepo,
		ipAddressRepo:       ipAddressRepo,
		devicePackageRepo:   devicePackageRepo,
	}
}

func (s *DeviceService) CreateDevice(ctx context.Context, req *ipamV1.CreateDeviceRequest) (*ipamV1.CreateDeviceResponse, error) {
	opts := []func(*ent.DeviceCreate){}

	if req.DeviceType != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetDeviceType(int32(*req.DeviceType)) })
	}
	if req.Description != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetDescription(*req.Description) })
	}
	if req.Manufacturer != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetManufacturer(*req.Manufacturer) })
	}
	if req.Model != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetModel(*req.Model) })
	}
	if req.SerialNumber != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetSerialNumber(*req.SerialNumber) })
	}
	if req.AssetTag != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetAssetTag(*req.AssetTag) })
	}
	if req.LocationId != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetLocationID(*req.LocationId) })
	}
	if req.RackId != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetRackID(*req.RackId) })
	}
	if req.RackPosition != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetRackPosition(*req.RackPosition) })
	}
	if req.DeviceHeightU != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetDeviceHeightU(*req.DeviceHeightU) })
	}
	if req.PrimaryIp != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetPrimaryIP(*req.PrimaryIp) })
	}
	if req.ManagementIp != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetManagementIP(*req.ManagementIp) })
	}
	if req.OsType != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetOsType(*req.OsType) })
	}
	if req.OsVersion != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetOsVersion(*req.OsVersion) })
	}
	if req.Contact != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetContact(*req.Contact) })
	}
	if req.Tags != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetTags(*req.Tags) })
	}
	if req.Metadata != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetMetadata(*req.Metadata) })
	}
	if req.Notes != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetNotes(*req.Notes) })
	}
	if req.Status != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetStatus(int32(*req.Status)) })
	}

	entity, err := s.deviceRepo.Create(ctx, req.GetTenantId(), req.GetName(), opts...)
	if err != nil {
		return nil, err
	}

	return &ipamV1.CreateDeviceResponse{
		Device: deviceToProto(entity),
	}, nil
}

func (s *DeviceService) GetDevice(ctx context.Context, req *ipamV1.GetDeviceRequest) (*ipamV1.GetDeviceResponse, error) {
	entity, err := s.deviceRepo.GetByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}

	return &ipamV1.GetDeviceResponse{
		Device: deviceToProto(entity),
	}, nil
}

func (s *DeviceService) ListDevices(ctx context.Context, req *ipamV1.ListDevicesRequest) (*ipamV1.ListDevicesResponse, error) {
	filters := make(map[string]interface{})
	if req.DeviceType != nil {
		filters["device_type"] = int32(*req.DeviceType)
	}
	if req.Status != nil {
		filters["status"] = int32(*req.Status)
	}
	if req.LocationId != nil {
		filters["location_id"] = *req.LocationId
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

	entities, total, err := s.deviceRepo.List(ctx, req.GetTenantId(), page, pageSize, filters, req.GetOrderBy())
	if err != nil {
		return nil, err
	}

	items := make([]*ipamV1.Device, len(entities))
	var packageDeviceIDs []string
	for i, e := range entities {
		items[i] = deviceToProto(e)
		// Collect IDs for SERVER and VM devices to enrich with package stats
		if e.DeviceType == int32(ipamV1.DeviceType_DEVICE_TYPE_SERVER) || e.DeviceType == int32(ipamV1.DeviceType_DEVICE_TYPE_VM) {
			packageDeviceIDs = append(packageDeviceIDs, e.ID)
		}
	}

	// Enrich with package update stats
	if len(packageDeviceIDs) > 0 {
		statsMap, err := s.devicePackageRepo.GetBulkStatsByDeviceIDs(ctx, req.GetTenantId(), packageDeviceIDs)
		if err == nil && statsMap != nil {
			for _, item := range items {
				if stats, ok := statsMap[item.GetId()]; ok {
					item.PackageUpdateCount = ptrInt32(int32(stats.UpdatesAvailable))
					item.SecurityUpdateCount = ptrInt32(int32(stats.SecurityUpdates))
				}
			}
		}
	}

	return &ipamV1.ListDevicesResponse{
		Items: items,
		Total: ptrInt32(int32(total)),
	}, nil
}

func (s *DeviceService) UpdateDevice(ctx context.Context, req *ipamV1.UpdateDeviceRequest) (*ipamV1.UpdateDeviceResponse, error) {
	updates := make(map[string]interface{})

	if req.Data != nil {
		if req.Data.Name != nil {
			updates["name"] = *req.Data.Name
		}
		if req.Data.Description != nil {
			updates["description"] = *req.Data.Description
		}
		if req.Data.DeviceType != nil {
			updates["device_type"] = int32(*req.Data.DeviceType)
		}
		if req.Data.Status != nil {
			updates["status"] = int32(*req.Data.Status)
		}
		if req.Data.Manufacturer != nil {
			updates["manufacturer"] = *req.Data.Manufacturer
		}
		if req.Data.Model != nil {
			updates["model"] = *req.Data.Model
		}
		if req.Data.SerialNumber != nil {
			updates["serial_number"] = *req.Data.SerialNumber
		}
		if req.Data.PrimaryIp != nil {
			updates["primary_ip"] = *req.Data.PrimaryIp
		}
		if req.Data.PrimaryIpv6 != nil {
			updates["primary_ipv6"] = *req.Data.PrimaryIpv6
		}
		if req.Data.ManagementIp != nil {
			updates["management_ip"] = *req.Data.ManagementIp
		}
		if req.Data.OsType != nil {
			updates["os_type"] = *req.Data.OsType
		}
		if req.Data.OsVersion != nil {
			updates["os_version"] = *req.Data.OsVersion
		}
		if req.Data.FirmwareVersion != nil {
			updates["firmware_version"] = *req.Data.FirmwareVersion
		}
		if req.Data.Contact != nil {
			updates["contact"] = *req.Data.Contact
		}
		if req.Data.Tags != nil {
			updates["tags"] = *req.Data.Tags
		}
		if req.Data.Metadata != nil {
			updates["metadata"] = *req.Data.Metadata
		}
		if req.Data.Notes != nil {
			updates["notes"] = *req.Data.Notes
		}
		if req.Data.LocationId != nil {
			updates["location_id"] = *req.Data.LocationId
		}
		if req.Data.RackId != nil {
			updates["rack_id"] = *req.Data.RackId
		}
		if req.Data.RackPosition != nil {
			updates["rack_position"] = *req.Data.RackPosition
		}
		if req.Data.DeviceHeightU != nil {
			updates["device_height_u"] = *req.Data.DeviceHeightU
		}
	}

	entity, err := s.deviceRepo.Update(ctx, req.GetId(), updates)
	if err != nil {
		return nil, err
	}

	return &ipamV1.UpdateDeviceResponse{
		Device: deviceToProto(entity),
	}, nil
}

func (s *DeviceService) DeleteDevice(ctx context.Context, req *ipamV1.DeleteDeviceRequest) (*emptypb.Empty, error) {
	err := s.deviceRepo.Delete(ctx, req.GetId(), req.GetForce())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *DeviceService) GetDeviceAddresses(ctx context.Context, req *ipamV1.GetDeviceAddressesRequest) (*ipamV1.GetDeviceAddressesResponse, error) {
	// Query IP addresses by device_id
	addresses, _, err := s.ipAddressRepo.List(ctx, 0, 0, 0, map[string]interface{}{
		"device_id": req.GetId(),
	}, nil)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(addresses))
	for i, addr := range addresses {
		ids[i] = addr.ID
	}

	return &ipamV1.GetDeviceAddressesResponse{
		AddressIds: ids,
	}, nil
}

func (s *DeviceService) GetDeviceInterfaces(ctx context.Context, req *ipamV1.GetDeviceInterfacesRequest) (*ipamV1.GetDeviceInterfacesResponse, error) {
	entities, err := s.deviceInterfaceRepo.ListByDeviceID(ctx, req.GetDeviceId())
	if err != nil {
		return nil, err
	}

	interfaces := make([]*ipamV1.DeviceInterface, len(entities))
	for i, e := range entities {
		interfaces[i] = deviceInterfaceToProto(e)
	}

	return &ipamV1.GetDeviceInterfacesResponse{
		Interfaces: interfaces,
	}, nil
}

func (s *DeviceService) CreateDeviceInterface(ctx context.Context, req *ipamV1.CreateDeviceInterfaceRequest) (*ipamV1.CreateDeviceInterfaceResponse, error) {
	opts := []func(*ent.DeviceInterfaceCreate){}
	if req.MacAddress != nil {
		opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetMACAddress(*req.MacAddress) })
	}
	if req.InterfaceType != nil {
		opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetInterfaceType(*req.InterfaceType) })
	}
	if req.Enabled != nil {
		opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetEnabled(*req.Enabled) })
	}
	if req.SpeedMbps != nil {
		opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetSpeedMbps(*req.SpeedMbps) })
	}
	if req.Description != nil {
		opts = append(opts, func(c *ent.DeviceInterfaceCreate) { c.SetDescription(*req.Description) })
	}

	entity, err := s.deviceInterfaceRepo.Create(ctx, req.GetDeviceId(), req.GetName(), opts...)
	if err != nil {
		return nil, err
	}

	return &ipamV1.CreateDeviceInterfaceResponse{
		Interface: deviceInterfaceToProto(entity),
	}, nil
}

func (s *DeviceService) DeleteDeviceInterface(ctx context.Context, req *ipamV1.DeleteDeviceInterfaceRequest) (*emptypb.Empty, error) {
	err := s.deviceInterfaceRepo.Delete(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func deviceInterfaceToProto(e *ent.DeviceInterface) *ipamV1.DeviceInterface {
	if e == nil {
		return nil
	}
	result := &ipamV1.DeviceInterface{
		Id:            &e.ID,
		DeviceId:      &e.DeviceID,
		Name:          &e.Name,
		MacAddress:    ptrString(e.MACAddress),
		InterfaceType: ptrString(e.InterfaceType),
		Enabled:       &e.Enabled,
		SpeedMbps:     e.SpeedMbps,
		Description:   ptrString(e.Description),
	}
	if e.CreateTime != nil {
		result.CreatedAt = timestamppb.New(*e.CreateTime)
	}
	if e.UpdateTime != nil {
		result.UpdatedAt = timestamppb.New(*e.UpdateTime)
	}
	return result
}

// Helper function
func deviceToProto(e *ent.Device) *ipamV1.Device {
	if e == nil {
		return nil
	}

	status := ipamV1.DeviceStatus(e.Status)
	deviceType := ipamV1.DeviceType(e.DeviceType)

	result := &ipamV1.Device{
		Id:              &e.ID,
		TenantId:        e.TenantID,
		Name:            ptrString(e.Name),
		DeviceType:      &deviceType,
		Description:     ptrString(e.Description),
		Manufacturer:    ptrString(e.Manufacturer),
		Model:           ptrString(e.Model),
		SerialNumber:    ptrString(e.SerialNumber),
		AssetTag:        ptrString(e.AssetTag),
		LocationId:      ptrString(e.LocationID),
		RackId:          ptrString(e.RackID),
		RackPosition:    e.RackPosition,
		DeviceHeightU:   e.DeviceHeightU,
		Status:          &status,
		PrimaryIp:       ptrString(e.PrimaryIP),
		PrimaryIpv6:     ptrString(e.PrimaryIpv6),
		ManagementIp:    ptrString(e.ManagementIP),
		OsType:          ptrString(e.OsType),
		OsVersion:       ptrString(e.OsVersion),
		FirmwareVersion: ptrString(e.FirmwareVersion),
		Contact:         ptrString(e.Contact),
		Tags:            ptrString(e.Tags),
		Metadata:        ptrString(e.Metadata),
		Notes:           ptrString(e.Notes),
		CreatedBy:       e.CreateBy,
		UpdatedBy:       e.UpdateBy,
	}

	if e.CreateTime != nil {
		result.CreatedAt = timestamppb.New(*e.CreateTime)
	}
	if e.UpdateTime != nil {
		result.UpdatedAt = timestamppb.New(*e.UpdateTime)
	}

	return result
}
