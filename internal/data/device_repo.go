package data

import (
	"context"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	entCrud "github.com/tx7do/go-crud/entgo"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/device"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
)

type DeviceRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewDeviceRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *DeviceRepo {
	return &DeviceRepo{
		log:       ctx.NewLoggerHelper("ipam/device/repo"),
		entClient: entClient,
	}
}

func (r *DeviceRepo) Create(ctx context.Context, tenantID uint32, name string, opts ...func(*ent.DeviceCreate)) (*ent.Device, error) {
	id := uuid.New().String()

	create := r.entClient.Client().Device.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetName(name).
		SetStatus(1).
		SetCreateTime(time.Now())

	for _, opt := range opts {
		opt(create)
	}

	entity, err := create.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ipamV1.ErrorDeviceAlreadyExists("device '%s' already exists", name)
		}
		r.log.Errorf("create device failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("create device failed")
	}
	return entity, nil
}

// GetByPrimaryIP retrieves a device by tenant ID and primary IP
func (r *DeviceRepo) GetByPrimaryIP(ctx context.Context, tenantID uint32, ip string) (*ent.Device, error) {
	entity, err := r.entClient.Client().Device.Query().
		Where(
			device.TenantID(tenantID),
			device.PrimaryIP(ip),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get device by primary IP failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("get device by primary IP failed")
	}
	return entity, nil
}

// GetByTenantAndName retrieves a device by tenant ID and name
func (r *DeviceRepo) GetByTenantAndName(ctx context.Context, tenantID uint32, name string) (*ent.Device, error) {
	entity, err := r.entClient.Client().Device.Query().
		Where(
			device.TenantID(tenantID),
			device.Name(name),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get device by name failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("get device by name failed")
	}
	return entity, nil
}

func (r *DeviceRepo) GetByID(ctx context.Context, id string) (*ent.Device, error) {
	entity, err := r.entClient.Client().Device.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get device failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("get device failed")
	}
	return entity, nil
}

var deviceSortFields = map[string]func(opts ...sql.OrderTermOption) device.OrderOption{
	"name":          device.ByName,
	"deviceType":    device.ByDeviceType,
	"status":        device.ByStatus,
	"primaryIp":     device.ByPrimaryIP,
	"managementIp":  device.ByManagementIP,
	"osVersion":     device.ByOsVersion,
	"manufacturer":  device.ByManufacturer,
	"model":         device.ByModel,
	"notes":         device.ByNotes,
	"create_time":   device.ByCreateTime,
	"update_time":   device.ByUpdateTime,
}

func deviceOrderBy(orderBy []string) []device.OrderOption {
	var orders []device.OrderOption
	for _, o := range orderBy {
		desc := false
		field := o
		if strings.HasPrefix(field, "-") {
			desc = true
			field = field[1:]
		}
		byFn, ok := deviceSortFields[field]
		if !ok {
			continue
		}
		if desc {
			orders = append(orders, byFn(sql.OrderDesc()))
		} else {
			orders = append(orders, byFn())
		}
	}
	if len(orders) == 0 {
		orders = append(orders, device.ByName())
	}
	return orders
}

func (r *DeviceRepo) List(ctx context.Context, tenantID uint32, page, pageSize int, filters map[string]interface{}, orderBy []string) ([]*ent.Device, int, error) {
	query := r.entClient.Client().Device.Query().Where(device.TenantID(tenantID))

	if deviceType, ok := filters["device_type"].(int32); ok && deviceType > 0 {
		query = query.Where(device.DeviceType(deviceType))
	}
	if status, ok := filters["status"].(int32); ok && status > 0 {
		query = query.Where(device.Status(status))
	}
	if locationID, ok := filters["location_id"].(string); ok && locationID != "" {
		query = query.Where(device.LocationID(locationID))
	}
	if queryStr, ok := filters["query"].(string); ok && queryStr != "" {
		query = query.Where(device.Or(
			device.NameContainsFold(queryStr),
			device.PrimaryIPContainsFold(queryStr),
			device.ManagementIPContainsFold(queryStr),
		))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count devices failed: %s", err.Error())
		return nil, 0, ipamV1.ErrorInternalServerError("list devices failed")
	}

	if page > 0 && pageSize > 0 {
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}

	orders := deviceOrderBy(orderBy)
	entities, err := query.Order(orders...).All(ctx)
	if err != nil {
		r.log.Errorf("list devices failed: %s", err.Error())
		return nil, 0, ipamV1.ErrorInternalServerError("list devices failed")
	}

	return entities, total, nil
}

// ListByTypeWithMetadataCursor returns up to limit devices of the given type
// that have non-empty metadata, ordered by id with id > afterID (cursor
// pagination), across all tenants. Intended for background backfills that run
// under a system viewer context (tenant privacy bypassed).
func (r *DeviceRepo) ListByTypeWithMetadataCursor(ctx context.Context, deviceType int32, afterID string, limit int) ([]*ent.Device, error) {
	query := r.entClient.Client().Device.Query().
		Where(device.DeviceType(deviceType), device.MetadataNEQ(""))
	if afterID != "" {
		query = query.Where(device.IDGT(afterID))
	}
	entities, err := query.Order(ent.Asc(device.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		r.log.Errorf("list devices by type with metadata failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("list devices failed")
	}
	return entities, nil
}

func (r *DeviceRepo) Update(ctx context.Context, id string, updates map[string]interface{}) (*ent.Device, error) {
	update := r.entClient.Client().Device.UpdateOneID(id)

	if name, ok := updates["name"].(string); ok {
		update = update.SetName(name)
	}
	if description, ok := updates["description"].(string); ok {
		update = update.SetDescription(description)
	}
	if deviceType, ok := updates["device_type"].(int32); ok {
		update = update.SetDeviceType(deviceType)
	}
	if status, ok := updates["status"].(int32); ok {
		update = update.SetStatus(status)
	}
	if manufacturer, ok := updates["manufacturer"].(string); ok {
		update = update.SetManufacturer(manufacturer)
	}
	if model, ok := updates["model"].(string); ok {
		update = update.SetModel(model)
	}
	if serialNumber, ok := updates["serial_number"].(string); ok {
		update = update.SetSerialNumber(serialNumber)
	}
	if primaryIP, ok := updates["primary_ip"].(string); ok {
		update = update.SetPrimaryIP(primaryIP)
	}
	if primaryIpv6, ok := updates["primary_ipv6"].(string); ok {
		update = update.SetPrimaryIpv6(primaryIpv6)
	}
	if managementIP, ok := updates["management_ip"].(string); ok {
		update = update.SetManagementIP(managementIP)
	}
	if osType, ok := updates["os_type"].(string); ok {
		update = update.SetOsType(osType)
	}
	if osVersion, ok := updates["os_version"].(string); ok {
		update = update.SetOsVersion(osVersion)
	}
	if firmwareVersion, ok := updates["firmware_version"].(string); ok {
		update = update.SetFirmwareVersion(firmwareVersion)
	}
	if contact, ok := updates["contact"].(string); ok {
		update = update.SetContact(contact)
	}
	if tags, ok := updates["tags"].(string); ok {
		update = update.SetTags(tags)
	}
	if metadata, ok := updates["metadata"].(string); ok {
		update = update.SetMetadata(metadata)
	}
	if notes, ok := updates["notes"].(string); ok {
		update = update.SetNotes(notes)
	}
	if locationID, ok := updates["location_id"].(string); ok {
		update = update.SetLocationID(locationID)
	}
	if rackID, ok := updates["rack_id"].(string); ok {
		update = update.SetRackID(rackID)
	}
	if rackPosition, ok := updates["rack_position"].(int32); ok {
		update = update.SetRackPosition(rackPosition)
	}
	if deviceHeightU, ok := updates["device_height_u"].(int32); ok {
		update = update.SetDeviceHeightU(deviceHeightU)
	}
	if lastSeen, ok := updates["last_seen"].(time.Time); ok {
		update = update.SetLastSeen(lastSeen)
	}
	if assetTag, ok := updates["asset_tag"].(string); ok {
		update = update.SetAssetTag(assetTag)
	}

	update = update.SetUpdateTime(time.Now())

	entity, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ipamV1.ErrorDeviceNotFound("device not found")
		}
		r.log.Errorf("update device failed: %s", err.Error())
		return nil, ipamV1.ErrorInternalServerError("update device failed")
	}
	return entity, nil
}

func (r *DeviceRepo) Delete(ctx context.Context, id string, force bool) error {
	if !force {
		count, err := r.entClient.Client().Device.Query().
			Where(device.ID(id)).
			QueryAddresses().
			Count(ctx)
		if err != nil {
			r.log.Errorf("check device addresses failed: %s", err.Error())
			return ipamV1.ErrorInternalServerError("delete device failed")
		}
		if count > 0 {
			return ipamV1.ErrorDeviceHasAddresses("device has %d addresses", count)
		}
	}

	err := r.entClient.Client().Device.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ipamV1.ErrorDeviceNotFound("device not found")
		}
		r.log.Errorf("delete device failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("delete device failed")
	}
	return nil
}
