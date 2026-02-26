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
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/devicepackage"
	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
)

type DevicePackageRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewDevicePackageRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *DevicePackageRepo {
	return &DevicePackageRepo{
		log:       ctx.NewLoggerHelper("ipam/device_package/repo"),
		entClient: entClient,
	}
}

// SyncPackages replaces all packages for a device in a single transaction
func (r *DevicePackageRepo) SyncPackages(ctx context.Context, tenantID uint32, deviceID string, packages []*ipamV1.DevicePackage, packageManager string) error {
	tx, err := r.entClient.Client().Tx(ctx)
	if err != nil {
		r.log.Errorf("start transaction failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("sync packages failed")
	}

	// Delete existing packages for this device
	_, err = tx.DevicePackage.Delete().
		Where(
			devicepackage.TenantID(tenantID),
			devicepackage.DeviceID(deviceID),
		).
		Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		r.log.Errorf("delete existing packages failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("sync packages failed")
	}

	// Bulk insert new packages
	now := time.Now()
	builders := make([]*ent.DevicePackageCreate, 0, len(packages))
	for _, pkg := range packages {
		b := tx.DevicePackage.Create().
			SetID(uuid.New().String()).
			SetTenantID(tenantID).
			SetDeviceID(deviceID).
			SetName(pkg.GetName()).
			SetNeedsUpdate(pkg.GetNeedsUpdate()).
			SetIsSecurityUpdate(pkg.GetIsSecurityUpdate()).
			SetCreateTime(now).
			SetUpdateTime(now)

		if pkg.CurrentVersion != nil {
			b.SetCurrentVersion(*pkg.CurrentVersion)
		}
		if pkg.AvailableVersion != nil {
			b.SetAvailableVersion(*pkg.AvailableVersion)
		}
		if pkg.Description != nil {
			b.SetDescription(*pkg.Description)
		}
		if packageManager != "" {
			b.SetPackageManager(packageManager)
		} else if pkg.PackageManager != nil {
			b.SetPackageManager(*pkg.PackageManager)
		}

		builders = append(builders, b)
	}

	if len(builders) > 0 {
		_, err = tx.DevicePackage.CreateBulk(builders...).Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			r.log.Errorf("bulk insert packages failed: %s", err.Error())
			return ipamV1.ErrorInternalServerError("sync packages failed")
		}
	}

	if err := tx.Commit(); err != nil {
		r.log.Errorf("commit transaction failed: %s", err.Error())
		return ipamV1.ErrorInternalServerError("sync packages failed")
	}

	return nil
}

var devicePackageSortFields = map[string]func(opts ...sql.OrderTermOption) devicepackage.OrderOption{
	"name":             devicepackage.ByName,
	"currentVersion":   devicepackage.ByCurrentVersion,
	"availableVersion": devicepackage.ByAvailableVersion,
	"needsUpdate":      devicepackage.ByNeedsUpdate,
	"isSecurityUpdate": devicepackage.ByIsSecurityUpdate,
	"packageManager":   devicepackage.ByPackageManager,
	"createdAt":        devicepackage.ByCreateTime,
	"updatedAt":        devicepackage.ByUpdateTime,
	"create_time":      devicepackage.ByCreateTime,
	"update_time":      devicepackage.ByUpdateTime,
}

func devicePackageOrderBy(orderBy []string) []devicepackage.OrderOption {
	var orders []devicepackage.OrderOption
	for _, o := range orderBy {
		desc := false
		field := o
		if strings.HasPrefix(field, "-") {
			desc = true
			field = field[1:]
		}
		byFn, ok := devicePackageSortFields[field]
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
		orders = append(orders, devicepackage.ByName())
	}
	return orders
}

// ListByDevice lists packages for a device with filters and pagination
func (r *DevicePackageRepo) ListByDevice(ctx context.Context, tenantID uint32, deviceID string, page, pageSize int, filters map[string]interface{}, orderBy []string) ([]*ent.DevicePackage, int, error) {
	query := r.entClient.Client().DevicePackage.Query().
		Where(
			devicepackage.TenantID(tenantID),
			devicepackage.DeviceID(deviceID),
		)

	if needsUpdate, ok := filters["needs_update"].(bool); ok {
		query = query.Where(devicepackage.NeedsUpdate(needsUpdate))
	}
	if isSecurityUpdate, ok := filters["is_security_update"].(bool); ok {
		query = query.Where(devicepackage.IsSecurityUpdate(isSecurityUpdate))
	}
	if pm, ok := filters["package_manager"].(string); ok && pm != "" {
		query = query.Where(devicepackage.PackageManager(pm))
	}
	if queryStr, ok := filters["query"].(string); ok && queryStr != "" {
		query = query.Where(devicepackage.NameContainsFold(queryStr))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count device packages failed: %s", err.Error())
		return nil, 0, ipamV1.ErrorInternalServerError("list device packages failed")
	}

	if page > 0 && pageSize > 0 {
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}

	orders := devicePackageOrderBy(orderBy)
	entities, err := query.Order(orders...).All(ctx)
	if err != nil {
		r.log.Errorf("list device packages failed: %s", err.Error())
		return nil, 0, ipamV1.ErrorInternalServerError("list device packages failed")
	}

	return entities, total, nil
}

// GetStatsByDevice returns aggregate counts for a device's packages
func (r *DevicePackageRepo) GetStatsByDevice(ctx context.Context, tenantID uint32, deviceID string) (total, updatesAvailable, securityUpdates int, lastSync *time.Time, err error) {
	query := r.entClient.Client().DevicePackage.Query().
		Where(
			devicepackage.TenantID(tenantID),
			devicepackage.DeviceID(deviceID),
		)

	total, err = query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count device packages failed: %s", err.Error())
		return 0, 0, 0, nil, ipamV1.ErrorInternalServerError("get device package stats failed")
	}

	updatesAvailable, err = query.Clone().Where(devicepackage.NeedsUpdate(true)).Count(ctx)
	if err != nil {
		r.log.Errorf("count updates available failed: %s", err.Error())
		return 0, 0, 0, nil, ipamV1.ErrorInternalServerError("get device package stats failed")
	}

	securityUpdates, err = query.Clone().Where(devicepackage.IsSecurityUpdate(true)).Count(ctx)
	if err != nil {
		r.log.Errorf("count security updates failed: %s", err.Error())
		return 0, 0, 0, nil, ipamV1.ErrorInternalServerError("get device package stats failed")
	}

	// Get last sync time from the most recent package create_time
	latest, queryErr := query.Clone().Order(devicepackage.ByCreateTime(sql.OrderDesc())).First(ctx)
	if queryErr == nil && latest != nil {
		lastSync = latest.CreateTime
	}

	return total, updatesAvailable, securityUpdates, lastSync, nil
}

// DeleteByDevice removes all packages for a device
func (r *DevicePackageRepo) DeleteByDevice(ctx context.Context, tenantID uint32, deviceID string) (int, error) {
	count, err := r.entClient.Client().DevicePackage.Delete().
		Where(
			devicepackage.TenantID(tenantID),
			devicepackage.DeviceID(deviceID),
		).
		Exec(ctx)
	if err != nil {
		r.log.Errorf("delete device packages failed: %s", err.Error())
		return 0, ipamV1.ErrorInternalServerError("delete device packages failed")
	}
	return count, nil
}
