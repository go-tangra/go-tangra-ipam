package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/go-kratos/kratos/v2/errors"

	"github.com/go-tangra/go-tangra-common/grpcx"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
	"github.com/go-tangra/go-tangra-ipam/internal/client"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/internal/metrics"
)

type DeviceService struct {
	ipamV1.UnimplementedDeviceServiceServer

	log                 *log.Helper
	deviceRepo          *data.DeviceRepo
	deviceInterfaceRepo *data.DeviceInterfaceRepo
	ipAddressRepo       *data.IpAddressRepo
	devicePackageRepo   *data.DevicePackageRepo
	wardenClient        *client.WardenClient
	kvmService          *kvm.Service
	metrics             *metrics.Collector
}

func NewDeviceService(ctx *bootstrap.Context, deviceRepo *data.DeviceRepo, deviceInterfaceRepo *data.DeviceInterfaceRepo, ipAddressRepo *data.IpAddressRepo, devicePackageRepo *data.DevicePackageRepo, wardenClient *client.WardenClient, kvmService *kvm.Service, metrics *metrics.Collector) *DeviceService {
	return &DeviceService{
		log:                 ctx.NewLoggerHelper("ipam/service/device"),
		deviceRepo:          deviceRepo,
		deviceInterfaceRepo: deviceInterfaceRepo,
		ipAddressRepo:       ipAddressRepo,
		devicePackageRepo:   devicePackageRepo,
		wardenClient:        wardenClient,
		kvmService:          kvmService,
		metrics:             metrics,
	}
}

// StartKvmSession resolves a device's BMC address + linked Warden credentials,
// logs into the BMC, and returns a short-lived token + console URL for the
// reverse-proxied HTML5 KVM. Platform-admin only; credentials never leave the
// server.
func (s *DeviceService) StartKvmSession(ctx context.Context, req *ipamV1.StartKvmSessionRequest) (*ipamV1.StartKvmSessionResponse, error) {
	if !grpcx.IsPlatformAdmin(ctx) {
		return nil, errors.Forbidden("FORBIDDEN", "IPMI console access is restricted to platform admins")
	}

	device, err := s.deviceRepo.GetByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ipamV1.ErrorDeviceNotFound("device not found")
	}
	if device.IpmiSecretRef == "" {
		return nil, ipamV1.ErrorBadRequest("device has no IPMI credentials linked")
	}

	host := bmcHostFromDevice(device)
	if host == "" {
		return nil, ipamV1.ErrorBadRequest("device has no IPMI/BMC address")
	}

	username, password, err := s.wardenClient.GetCredentials(ctx, device.IpmiSecretRef)
	if err != nil {
		s.log.Warnf("kvm: resolve credentials for device %s: %v", device.ID, err)
		return nil, ipamV1.ErrorInternalServerError("could not resolve IPMI credentials")
	}
	if username == "" || password == "" {
		return nil, ipamV1.ErrorBadRequest("linked secret has no username/password")
	}

	token, err := s.kvmService.StartSession(device.ID, host, username, password)
	if err != nil {
		s.log.Errorf("kvm: mint session for device %s: %v", device.ID, err)
		return nil, ipamV1.ErrorInternalServerError("could not start KVM session")
	}

	consoleURL := fmt.Sprintf(
		"/modules/ipam/bmc/%s/cgi/url_redirect.cgi?url_name=man_ikvm_html5_bootstrap&kvmtoken=%s",
		device.ID, token)

	return &ipamV1.StartKvmSessionResponse{
		Token:      token,
		ConsoleUrl: consoleURL,
		BmcHost:    host,
	}, nil
}

// bmcHostFromDevice resolves a device's BMC/IPMI address: the metadata ipmi.ip
// (authoritative, agent-collected) takes precedence, falling back to the
// device's management_ip.
func bmcHostFromDevice(device *ent.Device) string {
	if device.Metadata != "" {
		var env struct {
			IPMI struct {
				IP string `json:"ip"`
			} `json:"ipmi"`
		}
		if err := json.Unmarshal([]byte(device.Metadata), &env); err == nil {
			if ip := strings.TrimSpace(env.IPMI.IP); ip != "" {
				return ip
			}
		}
	}
	return strings.TrimSpace(device.ManagementIP)
}

// SearchWardenSecrets proxies a metadata-only secret search to the Warden module
// so a device can reference a secret as its IPMI credentials. Never returns
// secret values.
func (s *DeviceService) SearchWardenSecrets(ctx context.Context, req *ipamV1.SearchWardenSecretsRequest) (*ipamV1.SearchWardenSecretsResponse, error) {
	limit := req.GetLimit()
	if limit == 0 || limit > 50 {
		limit = 25
	}
	refs, err := s.wardenClient.SearchSecrets(ctx, req.GetQuery(), limit)
	if err != nil {
		s.log.Warnf("warden secret search failed: %v", err)
		return nil, ipamV1.ErrorInternalServerError("failed to search secrets")
	}
	out := make([]*ipamV1.WardenSecretRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, secretRefToProto(r))
	}
	return &ipamV1.SearchWardenSecretsResponse{Secrets: out}, nil
}

// GetWardenSecret proxies a metadata-only secret lookup to the Warden module.
func (s *DeviceService) GetWardenSecret(ctx context.Context, req *ipamV1.GetWardenSecretRequest) (*ipamV1.GetWardenSecretResponse, error) {
	ref, err := s.wardenClient.GetSecret(ctx, req.GetId())
	if err != nil {
		s.log.Warnf("warden secret get failed: %v", err)
		return nil, ipamV1.ErrorInternalServerError("failed to resolve secret")
	}
	resp := &ipamV1.GetWardenSecretResponse{}
	if ref != nil {
		resp.Secret = secretRefToProto(*ref)
	}
	return resp, nil
}

func secretRefToProto(r client.SecretRef) *ipamV1.WardenSecretRef {
	return &ipamV1.WardenSecretRef{
		Id:         r.ID,
		Name:       r.Name,
		FolderPath: r.FolderPath,
		Username:   r.Username,
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
	if req.RebootRequired != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetRebootRequired(*req.RebootRequired) })
	}
	if req.UnattendedUpgrades != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetUnattendedUpgrades(*req.UnattendedUpgrades) })
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
	if req.IpmiSecretRef != nil {
		opts = append(opts, func(c *ent.DeviceCreate) { c.SetIpmiSecretRef(*req.IpmiSecretRef) })
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

	s.metrics.DeviceCreated()

	// Promote agent-reported metadata interfaces to interface rows so their MACs
	// are visible to SNMP bridge-FDB link correlation.
	if req.Metadata != nil {
		materializeMetadataInterfaces(ctx, s.deviceInterfaceRepo, s.log, entity.ID, *req.Metadata)
		// Link any guests this host reports (Proxmox) to it, so each VM's
		// "Connected To" shows its hypervisor host.
		correlateHostedVMs(ctx, s.deviceInterfaceRepo, s.log, entity.ID, derefTenantID(entity.TenantID), *req.Metadata)
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
		if req.Data.RebootRequired != nil {
			updates["reboot_required"] = *req.Data.RebootRequired
		}
		if req.Data.UnattendedUpgrades != nil {
			updates["unattended_upgrades"] = *req.Data.UnattendedUpgrades
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
		if req.Data.IpmiSecretRef != nil {
			updates["ipmi_secret_ref"] = *req.Data.IpmiSecretRef
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

	// Keep interface rows in sync with agent-reported metadata interfaces.
	if req.Data != nil && req.Data.Metadata != nil {
		materializeMetadataInterfaces(ctx, s.deviceInterfaceRepo, s.log, entity.ID, *req.Data.Metadata)
		// Re-link guests reported by this host (Proxmox) so VMs that appeared
		// since the last sync get their hypervisor "Connected To".
		correlateHostedVMs(ctx, s.deviceInterfaceRepo, s.log, entity.ID, derefTenantID(entity.TenantID), *req.Data.Metadata)
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

	s.metrics.DeviceDeleted()

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
		IfIndex:       e.IfIndex,
		LinkVlan:      e.LinkVlan,
	}
	if e.RemoteDeviceID != "" {
		result.RemoteDeviceId = &e.RemoteDeviceID
	}
	if e.RemoteInterfaceID != "" {
		result.RemoteInterfaceId = &e.RemoteInterfaceID
	}
	if e.RemotePortName != "" {
		result.RemotePortName = &e.RemotePortName
	}
	if e.LinkSource != "" {
		result.LinkSource = &e.LinkSource
	}
	if e.CreateTime != nil {
		result.CreatedAt = timestamppb.New(*e.CreateTime)
	}
	if e.UpdateTime != nil {
		result.UpdatedAt = timestamppb.New(*e.UpdateTime)
	}
	if e.LinkLastSeen != nil {
		result.LinkLastSeen = timestamppb.New(*e.LinkLastSeen)
	}
	// Map the full set of discovered links (an interface may connect to several
	// switches via an LACP bond / MLAG pair). Only present when eager-loaded.
	for _, l := range e.Edges.Links {
		dl := &ipamV1.DeviceLink{LinkVlan: l.LinkVlan}
		if l.RemoteDeviceID != "" {
			dl.RemoteDeviceId = &l.RemoteDeviceID
		}
		if l.RemoteInterfaceID != "" {
			dl.RemoteInterfaceId = &l.RemoteInterfaceID
		}
		if l.RemotePortName != "" {
			dl.RemotePortName = &l.RemotePortName
		}
		if l.LinkSource != "" {
			dl.LinkSource = &l.LinkSource
		}
		if l.LinkLastSeen != nil {
			dl.LinkLastSeen = timestamppb.New(*l.LinkLastSeen)
		}
		result.Links = append(result.Links, dl)
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
		Id:                 &e.ID,
		TenantId:           e.TenantID,
		Name:               ptrString(e.Name),
		DeviceType:         &deviceType,
		Description:        ptrString(e.Description),
		Manufacturer:       ptrString(e.Manufacturer),
		Model:              ptrString(e.Model),
		SerialNumber:       ptrString(e.SerialNumber),
		AssetTag:           ptrString(e.AssetTag),
		LocationId:         ptrString(e.LocationID),
		RackId:             ptrString(e.RackID),
		RackPosition:       e.RackPosition,
		DeviceHeightU:      e.DeviceHeightU,
		Status:             &status,
		PrimaryIp:          ptrString(e.PrimaryIP),
		PrimaryIpv6:        ptrString(e.PrimaryIpv6),
		ManagementIp:       ptrString(e.ManagementIP),
		OsType:             ptrString(e.OsType),
		OsVersion:          ptrString(e.OsVersion),
		RebootRequired:     ptrBool(e.RebootRequired),
		UnattendedUpgrades: ptrBool(e.UnattendedUpgrades),
		FirmwareVersion:    ptrString(e.FirmwareVersion),
		Contact:            ptrString(e.Contact),
		Tags:               ptrString(e.Tags),
		Metadata:           ptrString(e.Metadata),
		Notes:              ptrString(e.Notes),
		IpmiSecretRef:      ptrString(e.IpmiSecretRef),
		CreatedBy:          e.CreateBy,
		UpdatedBy:          e.UpdateBy,
	}

	if e.CreateTime != nil {
		result.CreatedAt = timestamppb.New(*e.CreateTime)
	}
	if e.UpdateTime != nil {
		result.UpdatedAt = timestamppb.New(*e.UpdateTime)
	}

	return result
}
