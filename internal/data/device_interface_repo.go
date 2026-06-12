package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	entCrud "github.com/tx7do/go-crud/entgo"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/device"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/deviceinterface"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
)

// DeviceTypeServer is the device_type value for a physical server. Layer-2 link
// discovery only attaches links to physical servers, not VMs (device_type=2).
const DeviceTypeServer int32 = 1

type DeviceInterfaceRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewDeviceInterfaceRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *DeviceInterfaceRepo {
	return &DeviceInterfaceRepo{
		log:       ctx.NewLoggerHelper("ipam/device_interface/repo"),
		entClient: entClient,
	}
}

// Create creates a new device interface
func (r *DeviceInterfaceRepo) Create(ctx context.Context, deviceID, name string, opts ...func(*ent.DeviceInterfaceCreate)) (*ent.DeviceInterface, error) {
	id := uuid.New().String()

	create := r.entClient.Client().DeviceInterface.Create().
		SetID(id).
		SetDeviceID(deviceID).
		SetName(name).
		SetCreateTime(time.Now())

	for _, opt := range opts {
		opt(create)
	}

	entity, err := create.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ipamV1.ErrorInternalServerError("interface '%s' already exists on device", name)
		}
		r.log.Errorf("create device interface failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("create device interface failed")
	}
	return entity, nil
}

// ListByDeviceID lists all interfaces for a device
func (r *DeviceInterfaceRepo) ListByDeviceID(ctx context.Context, deviceID string) ([]*ent.DeviceInterface, error) {
	entities, err := r.entClient.Client().DeviceInterface.Query().
		Where(deviceinterface.DeviceID(deviceID)).
		Order(ent.Asc(deviceinterface.FieldName)).
		All(ctx)
	if err != nil {
		r.log.Errorf("list device interfaces failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("list device interfaces failed")
	}
	return entities, nil
}

// GetByDeviceAndName gets an interface by device ID and name
func (r *DeviceInterfaceRepo) GetByDeviceAndName(ctx context.Context, deviceID, name string) (*ent.DeviceInterface, error) {
	entity, err := r.entClient.Client().DeviceInterface.Query().
		Where(
			deviceinterface.DeviceID(deviceID),
			deviceinterface.Name(name),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get device interface failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("get device interface failed")
	}
	return entity, nil
}

// FindServerInterfaceByMAC returns the interface with the given MAC that belongs
// to a physical server (device_type=Server, excluding VMs) within the tenant,
// with the owning device eager-loaded. Matching is case-insensitive. Returns
// (nil, nil) when no physical-server interface owns that MAC.
func (r *DeviceInterfaceRepo) FindServerInterfaceByMAC(ctx context.Context, tenantID uint32, mac string) (*ent.DeviceInterface, error) {
	if mac == "" {
		return nil, nil
	}
	entity, err := r.entClient.Client().DeviceInterface.Query().
		Where(
			deviceinterface.MACAddressEqualFold(mac),
			deviceinterface.HasDeviceWith(
				device.TenantID(tenantID),
				device.DeviceType(DeviceTypeServer),
			),
		).
		WithDevice().
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("find server interface by MAC failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("find interface by MAC failed")
	}
	return entity, nil
}

// Update updates a device interface
func (r *DeviceInterfaceRepo) Update(ctx context.Context, id string, updates map[string]any) (*ent.DeviceInterface, error) {
	update := r.entClient.Client().DeviceInterface.UpdateOneID(id)

	if mac, ok := updates["mac_address"].(string); ok {
		update = update.SetMACAddress(mac)
	}
	if ifType, ok := updates["interface_type"].(string); ok {
		update = update.SetInterfaceType(ifType)
	}
	if enabled, ok := updates["enabled"].(bool); ok {
		update = update.SetEnabled(enabled)
	}
	if speed, ok := updates["speed_mbps"].(int32); ok {
		update = update.SetSpeedMbps(speed)
	}
	if description, ok := updates["description"].(string); ok {
		update = update.SetDescription(description)
	}
	if ifIndex, ok := updates["if_index"].(int32); ok {
		update = update.SetIfIndex(ifIndex)
	}
	if remoteDeviceID, ok := updates["remote_device_id"].(string); ok {
		update = update.SetRemoteDeviceID(remoteDeviceID)
	}
	if remoteInterfaceID, ok := updates["remote_interface_id"].(string); ok {
		update = update.SetRemoteInterfaceID(remoteInterfaceID)
	}
	if remotePortName, ok := updates["remote_port_name"].(string); ok {
		update = update.SetRemotePortName(remotePortName)
	}
	if linkSource, ok := updates["link_source"].(string); ok {
		update = update.SetLinkSource(linkSource)
	}
	if linkVLAN, ok := updates["link_vlan"].(int32); ok {
		update = update.SetLinkVlan(linkVLAN)
	}
	if linkLastSeen, ok := updates["link_last_seen"].(time.Time); ok {
		update = update.SetLinkLastSeen(linkLastSeen)
	}

	update = update.SetUpdateTime(time.Now())

	entity, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ipamV1.ErrorInternalServerError("device interface not found")
		}
		r.log.Errorf("update device interface failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("update device interface failed")
	}
	return entity, nil
}

// Delete deletes a single device interface
func (r *DeviceInterfaceRepo) Delete(ctx context.Context, id string) error {
	err := r.entClient.Client().DeviceInterface.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ipamV1.ErrorInternalServerError("device interface not found")
		}
		r.log.Errorf("delete device interface failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("delete device interface failed")
	}
	return nil
}

// DeleteByDeviceID deletes all interfaces for a device
func (r *DeviceInterfaceRepo) DeleteByDeviceID(ctx context.Context, deviceID string) error {
	_, err := r.entClient.Client().DeviceInterface.Delete().
		Where(deviceinterface.DeviceID(deviceID)).
		Exec(ctx)
	if err != nil {
		r.log.Errorf("delete device interfaces failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("delete device interfaces failed")
	}
	return nil
}
