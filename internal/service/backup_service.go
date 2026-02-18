package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-common/grpcx"

	ipamV1 "github.com/go-tangra/go-tangra-ipam/gen/go/ipam/service/v1"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/device"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/deviceinterface"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/dnsconfig"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/hostgroup"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/hostgroupmember"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipaddress"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipgroup"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipgroupmember"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/ipscanjob"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/location"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/subnet"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/vlan"
)

const (
	backupModule  = "ipam"
	backupVersion = "1.0"
)

type BackupService struct {
	ipamV1.UnimplementedBackupServiceServer

	log       *log.Helper
	entClient *entCrud.EntClient[*ent.Client]
}

func NewBackupService(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *BackupService {
	return &BackupService{
		log:       ctx.NewLoggerHelper("ipam/service/backup"),
		entClient: entClient,
	}
}

type backupData struct {
	Module     string          `json:"module"`
	Version    string          `json:"version"`
	ExportedAt time.Time      `json:"exportedAt"`
	TenantID   uint32         `json:"tenantId"`
	FullBackup bool           `json:"fullBackup"`
	Data       backupEntities `json:"data"`
}

type backupEntities struct {
	Locations        []json.RawMessage `json:"locations,omitempty"`
	Vlans            []json.RawMessage `json:"vlans,omitempty"`
	DnsConfigs       []json.RawMessage `json:"dnsConfigs,omitempty"`
	Subnets          []json.RawMessage `json:"subnets,omitempty"`
	Devices          []json.RawMessage `json:"devices,omitempty"`
	DeviceInterfaces []json.RawMessage `json:"deviceInterfaces,omitempty"`
	IpAddresses      []json.RawMessage `json:"ipAddresses,omitempty"`
	IpGroups         []json.RawMessage `json:"ipGroups,omitempty"`
	IpGroupMembers   []json.RawMessage `json:"ipGroupMembers,omitempty"`
	HostGroups       []json.RawMessage `json:"hostGroups,omitempty"`
	HostGroupMembers []json.RawMessage `json:"hostGroupMembers,omitempty"`
	IpScanJobs       []json.RawMessage `json:"ipScanJobs,omitempty"`
}

func (s *BackupService) ExportBackup(ctx context.Context, req *ipamV1.ExportBackupRequest) (*ipamV1.ExportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	full := false

	if grpcx.IsPlatformAdmin(ctx) && req.TenantId != nil && *req.TenantId == 0 {
		full = true
		tenantID = 0
	} else if req.TenantId != nil && *req.TenantId != 0 {
		if grpcx.IsPlatformAdmin(ctx) {
			tenantID = *req.TenantId
		}
	}

	client := s.entClient.Client()
	now := time.Now()

	locations, err := s.exportLocations(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export locations: %w", err)
	}
	vlans, err := s.exportVlans(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export vlans: %w", err)
	}
	dnsConfigs, err := s.exportDnsConfigs(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export dns configs: %w", err)
	}
	subnets, err := s.exportSubnets(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export subnets: %w", err)
	}
	devices, err := s.exportDevices(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export devices: %w", err)
	}
	deviceInterfaces, err := s.exportDeviceInterfaces(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export device interfaces: %w", err)
	}
	ipAddresses, err := s.exportIpAddresses(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export ip addresses: %w", err)
	}
	ipGroups, err := s.exportIpGroups(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export ip groups: %w", err)
	}
	ipGroupMembers, err := s.exportIpGroupMembers(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export ip group members: %w", err)
	}
	hostGroups, err := s.exportHostGroups(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export host groups: %w", err)
	}
	hostGroupMembers, err := s.exportHostGroupMembers(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export host group members: %w", err)
	}
	ipScanJobs, err := s.exportIpScanJobs(ctx, client, tenantID, full)
	if err != nil {
		return nil, fmt.Errorf("export ip scan jobs: %w", err)
	}

	backup := backupData{
		Module:     backupModule,
		Version:    backupVersion,
		ExportedAt: now,
		TenantID:   tenantID,
		FullBackup: full,
		Data: backupEntities{
			Locations:        locations,
			Vlans:            vlans,
			DnsConfigs:       dnsConfigs,
			Subnets:          subnets,
			Devices:          devices,
			DeviceInterfaces: deviceInterfaces,
			IpAddresses:      ipAddresses,
			IpGroups:         ipGroups,
			IpGroupMembers:   ipGroupMembers,
			HostGroups:       hostGroups,
			HostGroupMembers: hostGroupMembers,
			IpScanJobs:       ipScanJobs,
		},
	}

	data, err := json.Marshal(backup)
	if err != nil {
		return nil, fmt.Errorf("marshal backup: %w", err)
	}

	entityCounts := map[string]int64{
		"locations":        int64(len(locations)),
		"vlans":            int64(len(vlans)),
		"dnsConfigs":       int64(len(dnsConfigs)),
		"subnets":          int64(len(subnets)),
		"devices":          int64(len(devices)),
		"deviceInterfaces": int64(len(deviceInterfaces)),
		"ipAddresses":      int64(len(ipAddresses)),
		"ipGroups":         int64(len(ipGroups)),
		"ipGroupMembers":   int64(len(ipGroupMembers)),
		"hostGroups":       int64(len(hostGroups)),
		"hostGroupMembers": int64(len(hostGroupMembers)),
		"ipScanJobs":       int64(len(ipScanJobs)),
	}

	s.log.Infof("exported backup: module=%s tenant=%d full=%v entities=%v", backupModule, tenantID, full, entityCounts)

	return &ipamV1.ExportBackupResponse{
		Data:         data,
		Module:       backupModule,
		Version:      backupVersion,
		ExportedAt:   timestamppb.New(now),
		TenantId:     tenantID,
		EntityCounts: entityCounts,
	}, nil
}

func (s *BackupService) ImportBackup(ctx context.Context, req *ipamV1.ImportBackupRequest) (*ipamV1.ImportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	isPlatformAdmin := grpcx.IsPlatformAdmin(ctx)
	mode := req.GetMode()

	var backup backupData
	if err := json.Unmarshal(req.GetData(), &backup); err != nil {
		return nil, fmt.Errorf("invalid backup data: %w", err)
	}

	if backup.Module != backupModule {
		return nil, fmt.Errorf("backup module mismatch: expected %s, got %s", backupModule, backup.Module)
	}
	if backup.Version != backupVersion {
		return nil, fmt.Errorf("backup version mismatch: expected %s, got %s", backupVersion, backup.Version)
	}

	// For full backups, only platform admins can restore
	if backup.FullBackup && !isPlatformAdmin {
		return nil, fmt.Errorf("only platform admins can restore full backups")
	}

	// Non-platform admins always restore to their own tenant
	if !isPlatformAdmin || !backup.FullBackup {
		tenantID = grpcx.GetTenantIDFromContext(ctx)
	} else {
		tenantID = 0 // Signal for full backup restore — each entity carries its own tenant_id
	}

	client := s.entClient.Client()
	var results []*ipamV1.EntityImportResult
	var warnings []string

	// Import in FK dependency order
	importFuncs := []struct {
		name string
		fn   func(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string)
	}{
		{"locations", s.importLocations},
		{"vlans", s.importVlans},
		{"dnsConfigs", s.importDnsConfigs},
		{"subnets", s.importSubnets},
		{"devices", s.importDevices},
		{"deviceInterfaces", s.importDeviceInterfaces},
		{"ipAddresses", s.importIpAddresses},
		{"ipGroups", s.importIpGroups},
		{"ipGroupMembers", s.importIpGroupMembers},
		{"hostGroups", s.importHostGroups},
		{"hostGroupMembers", s.importHostGroupMembers},
		{"ipScanJobs", s.importIpScanJobs},
	}

	dataMap := map[string][]json.RawMessage{
		"locations":        backup.Data.Locations,
		"vlans":            backup.Data.Vlans,
		"dnsConfigs":       backup.Data.DnsConfigs,
		"subnets":          backup.Data.Subnets,
		"devices":          backup.Data.Devices,
		"deviceInterfaces": backup.Data.DeviceInterfaces,
		"ipAddresses":      backup.Data.IpAddresses,
		"ipGroups":         backup.Data.IpGroups,
		"ipGroupMembers":   backup.Data.IpGroupMembers,
		"hostGroups":       backup.Data.HostGroups,
		"hostGroupMembers": backup.Data.HostGroupMembers,
		"ipScanJobs":       backup.Data.IpScanJobs,
	}

	for _, imp := range importFuncs {
		items := dataMap[imp.name]
		if len(items) == 0 {
			continue
		}
		result, w := imp.fn(ctx, client, items, tenantID, backup.FullBackup, mode)
		if result != nil {
			results = append(results, result)
		}
		warnings = append(warnings, w...)
	}

	s.log.Infof("imported backup: module=%s tenant=%d mode=%v results=%d warnings=%d", backupModule, tenantID, mode, len(results), len(warnings))

	return &ipamV1.ImportBackupResponse{
		Success:  true,
		Results:  results,
		Warnings: warnings,
	}, nil
}

// topologicalSortByParentID sorts items so parents come before children.
func topologicalSortByParentID[T any](items []T, getID func(T) string, getParentID func(T) string) []T {
	idSet := make(map[string]bool, len(items))
	for _, item := range items {
		idSet[getID(item)] = true
	}

	childMap := make(map[string][]T)
	var roots []T
	for _, item := range items {
		pid := getParentID(item)
		if pid == "" || !idSet[pid] {
			roots = append(roots, item)
		} else {
			childMap[pid] = append(childMap[pid], item)
		}
	}

	result := make([]T, 0, len(items))
	var walk func([]T)
	walk = func(nodes []T) {
		for _, n := range nodes {
			result = append(result, n)
			if children, ok := childMap[getID(n)]; ok {
				walk(children)
			}
		}
	}
	walk(roots)
	return result
}

func marshalEntities[T any](entities []*T) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0, len(entities))
	for _, e := range entities {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}

// --- Export helpers ---

func (s *BackupService) exportLocations(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.Location.Query()
	if !full {
		query = query.Where(location.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportVlans(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.Vlan.Query()
	if !full {
		query = query.Where(vlan.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportDnsConfigs(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.DnsConfig.Query()
	if !full {
		query = query.Where(dnsconfig.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportSubnets(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.Subnet.Query()
	if !full {
		query = query.Where(subnet.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportDevices(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.Device.Query()
	if !full {
		query = query.Where(device.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportDeviceInterfaces(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.DeviceInterface.Query()
	if !full {
		// DeviceInterface doesn't have tenant_id — filter via parent device
		query = query.Where(deviceinterface.HasDeviceWith(device.TenantID(tenantID)))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportIpAddresses(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.IpAddress.Query()
	if !full {
		query = query.Where(ipaddress.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportIpGroups(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.IpGroup.Query()
	if !full {
		query = query.Where(ipgroup.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportIpGroupMembers(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.IpGroupMember.Query()
	if !full {
		query = query.Where(ipgroupmember.HasGroupWith(ipgroup.TenantID(tenantID)))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportHostGroups(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.HostGroup.Query()
	if !full {
		query = query.Where(hostgroup.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportHostGroupMembers(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.HostGroupMember.Query()
	if !full {
		query = query.Where(hostgroupmember.HasGroupWith(hostgroup.TenantID(tenantID)))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

func (s *BackupService) exportIpScanJobs(ctx context.Context, client *ent.Client, tenantID uint32, full bool) ([]json.RawMessage, error) {
	query := client.IpScanJob.Query()
	if !full {
		query = query.Where(ipscanjob.TenantID(tenantID))
	}
	entities, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	return marshalEntities(entities)
}

// --- Import helpers ---

func (s *BackupService) importLocations(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "locations", Total: int64(len(items))}
	var warnings []string

	var entities []*ent.Location
	for _, raw := range items {
		var e ent.Location
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("locations: unmarshal error: %v", err))
			result.Failed++
			continue
		}
		entities = append(entities, &e)
	}

	// Topological sort for self-referential parent_id
	sorted := topologicalSortByParentID(entities,
		func(e *ent.Location) string { return e.ID },
		func(e *ent.Location) string { return e.ParentID },
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.Location.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			// Overwrite
			_, err := client.Location.UpdateOneID(e.ID).
				SetName(e.Name).
				SetCode(e.Code).
				SetLocationType(e.LocationType).
				SetDescription(e.Description).
				SetParentID(e.ParentID).
				SetPath(e.Path).
				SetAddress(e.Address).
				SetCity(e.City).
				SetState(e.State).
				SetCountry(e.Country).
				SetPostalCode(e.PostalCode).
				SetNillableLatitude(e.Latitude).
				SetNillableLongitude(e.Longitude).
				SetContact(e.Contact).
				SetPhone(e.Phone).
				SetEmail(e.Email).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableRackSizeU(e.RackSizeU).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("locations: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			// Create
			_, err := client.Location.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetName(e.Name).
				SetCode(e.Code).
				SetLocationType(e.LocationType).
				SetDescription(e.Description).
				SetParentID(e.ParentID).
				SetPath(e.Path).
				SetAddress(e.Address).
				SetCity(e.City).
				SetState(e.State).
				SetCountry(e.Country).
				SetPostalCode(e.PostalCode).
				SetNillableLatitude(e.Latitude).
				SetNillableLongitude(e.Longitude).
				SetContact(e.Contact).
				SetPhone(e.Phone).
				SetEmail(e.Email).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableRackSizeU(e.RackSizeU).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("locations: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importVlans(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "vlans", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.Vlan
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("vlans: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.Vlan.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.Vlan.UpdateOneID(e.ID).
				SetVlanID(e.VlanID).
				SetName(e.Name).
				SetDescription(e.Description).
				SetDomain(e.Domain).
				SetLocationID(e.LocationID).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("vlans: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.Vlan.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetVlanID(e.VlanID).
				SetName(e.Name).
				SetDescription(e.Description).
				SetDomain(e.Domain).
				SetLocationID(e.LocationID).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("vlans: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importDnsConfigs(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "dnsConfigs", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.DnsConfig
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("dnsConfigs: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.DnsConfig.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.DnsConfig.UpdateOneID(e.ID).
				SetDNSServers(e.DNSServers).
				SetTimeoutMs(e.TimeoutMs).
				SetUseSystemDNSFallback(e.UseSystemDNSFallback).
				SetReverseDNSEnabled(e.ReverseDNSEnabled).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("dnsConfigs: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.DnsConfig.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetDNSServers(e.DNSServers).
				SetTimeoutMs(e.TimeoutMs).
				SetUseSystemDNSFallback(e.UseSystemDNSFallback).
				SetReverseDNSEnabled(e.ReverseDNSEnabled).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("dnsConfigs: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importSubnets(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "subnets", Total: int64(len(items))}
	var warnings []string

	var entities []*ent.Subnet
	for _, raw := range items {
		var e ent.Subnet
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("subnets: unmarshal error: %v", err))
			result.Failed++
			continue
		}
		entities = append(entities, &e)
	}

	// Topological sort for self-referential parent_id
	sorted := topologicalSortByParentID(entities,
		func(e *ent.Subnet) string { return e.ID },
		func(e *ent.Subnet) string { return e.ParentID },
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.Subnet.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.Subnet.UpdateOneID(e.ID).
				SetName(e.Name).
				SetCidr(e.Cidr).
				SetDescription(e.Description).
				SetGateway(e.Gateway).
				SetDNSServers(e.DNSServers).
				SetVlanID(e.VlanID).
				SetParentID(e.ParentID).
				SetLocationID(e.LocationID).
				SetStatus(e.Status).
				SetIPVersion(e.IPVersion).
				SetNetworkAddress(e.NetworkAddress).
				SetBroadcastAddress(e.BroadcastAddress).
				SetMask(e.Mask).
				SetPrefixLength(e.PrefixLength).
				SetTotalAddresses(e.TotalAddresses).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("subnets: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.Subnet.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetName(e.Name).
				SetCidr(e.Cidr).
				SetDescription(e.Description).
				SetGateway(e.Gateway).
				SetDNSServers(e.DNSServers).
				SetVlanID(e.VlanID).
				SetParentID(e.ParentID).
				SetLocationID(e.LocationID).
				SetStatus(e.Status).
				SetIPVersion(e.IPVersion).
				SetNetworkAddress(e.NetworkAddress).
				SetBroadcastAddress(e.BroadcastAddress).
				SetMask(e.Mask).
				SetPrefixLength(e.PrefixLength).
				SetTotalAddresses(e.TotalAddresses).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("subnets: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importDevices(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "devices", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.Device
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("devices: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.Device.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.Device.UpdateOneID(e.ID).
				SetName(e.Name).
				SetDeviceType(e.DeviceType).
				SetDescription(e.Description).
				SetManufacturer(e.Manufacturer).
				SetModel(e.Model).
				SetSerialNumber(e.SerialNumber).
				SetAssetTag(e.AssetTag).
				SetLocationID(e.LocationID).
				SetRackID(e.RackID).
				SetNillableRackPosition(e.RackPosition).
				SetNillableDeviceHeightU(e.DeviceHeightU).
				SetStatus(e.Status).
				SetPrimaryIP(e.PrimaryIP).
				SetPrimaryIpv6(e.PrimaryIpv6).
				SetManagementIP(e.ManagementIP).
				SetOsType(e.OsType).
				SetOsVersion(e.OsVersion).
				SetFirmwareVersion(e.FirmwareVersion).
				SetContact(e.Contact).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNotes(e.Notes).
				SetNillableLastSeen(e.LastSeen).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("devices: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.Device.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetName(e.Name).
				SetDeviceType(e.DeviceType).
				SetDescription(e.Description).
				SetManufacturer(e.Manufacturer).
				SetModel(e.Model).
				SetSerialNumber(e.SerialNumber).
				SetAssetTag(e.AssetTag).
				SetLocationID(e.LocationID).
				SetRackID(e.RackID).
				SetNillableRackPosition(e.RackPosition).
				SetNillableDeviceHeightU(e.DeviceHeightU).
				SetStatus(e.Status).
				SetPrimaryIP(e.PrimaryIP).
				SetPrimaryIpv6(e.PrimaryIpv6).
				SetManagementIP(e.ManagementIP).
				SetOsType(e.OsType).
				SetOsVersion(e.OsVersion).
				SetFirmwareVersion(e.FirmwareVersion).
				SetContact(e.Contact).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNotes(e.Notes).
				SetNillableLastSeen(e.LastSeen).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("devices: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importDeviceInterfaces(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "deviceInterfaces", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.DeviceInterface
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("deviceInterfaces: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		existing, _ := client.DeviceInterface.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.DeviceInterface.UpdateOneID(e.ID).
				SetDeviceID(e.DeviceID).
				SetName(e.Name).
				SetMACAddress(e.MACAddress).
				SetInterfaceType(e.InterfaceType).
				SetEnabled(e.Enabled).
				SetNillableSpeedMbps(e.SpeedMbps).
				SetDescription(e.Description).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("deviceInterfaces: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.DeviceInterface.Create().
				SetID(e.ID).
				SetDeviceID(e.DeviceID).
				SetName(e.Name).
				SetMACAddress(e.MACAddress).
				SetInterfaceType(e.InterfaceType).
				SetEnabled(e.Enabled).
				SetNillableSpeedMbps(e.SpeedMbps).
				SetDescription(e.Description).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("deviceInterfaces: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importIpAddresses(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "ipAddresses", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.IpAddress
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("ipAddresses: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.IpAddress.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.IpAddress.UpdateOneID(e.ID).
				SetAddress(e.Address).
				SetSubnetID(e.SubnetID).
				SetHostname(e.Hostname).
				SetMACAddress(e.MACAddress).
				SetDescription(e.Description).
				SetDeviceID(e.DeviceID).
				SetInterfaceName(e.InterfaceName).
				SetStatus(e.Status).
				SetAddressType(e.AddressType).
				SetIsPrimary(e.IsPrimary).
				SetPtrRecord(e.PtrRecord).
				SetHasReverseDNS(e.HasReverseDNS).
				SetDNSName(e.DNSName).
				SetOwner(e.Owner).
				SetLastSeen(e.LastSeen).
				SetNillableLeaseExpiry(e.LeaseExpiry).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNote(e.Note).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipAddresses: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.IpAddress.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetAddress(e.Address).
				SetSubnetID(e.SubnetID).
				SetHostname(e.Hostname).
				SetMACAddress(e.MACAddress).
				SetDescription(e.Description).
				SetDeviceID(e.DeviceID).
				SetInterfaceName(e.InterfaceName).
				SetStatus(e.Status).
				SetAddressType(e.AddressType).
				SetIsPrimary(e.IsPrimary).
				SetPtrRecord(e.PtrRecord).
				SetHasReverseDNS(e.HasReverseDNS).
				SetDNSName(e.DNSName).
				SetOwner(e.Owner).
				SetLastSeen(e.LastSeen).
				SetNillableLeaseExpiry(e.LeaseExpiry).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNote(e.Note).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipAddresses: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importIpGroups(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "ipGroups", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.IpGroup
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("ipGroups: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.IpGroup.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.IpGroup.UpdateOneID(e.ID).
				SetName(e.Name).
				SetDescription(e.Description).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipGroups: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.IpGroup.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetName(e.Name).
				SetDescription(e.Description).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipGroups: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importIpGroupMembers(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "ipGroupMembers", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.IpGroupMember
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("ipGroupMembers: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		existing, _ := client.IpGroupMember.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.IpGroupMember.UpdateOneID(e.ID).
				SetIPGroupID(e.IPGroupID).
				SetMemberType(e.MemberType).
				SetValue(e.Value).
				SetDescription(e.Description).
				SetSequence(e.Sequence).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipGroupMembers: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.IpGroupMember.Create().
				SetID(e.ID).
				SetIPGroupID(e.IPGroupID).
				SetMemberType(e.MemberType).
				SetValue(e.Value).
				SetDescription(e.Description).
				SetSequence(e.Sequence).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipGroupMembers: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importHostGroups(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "hostGroups", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.HostGroup
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("hostGroups: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.HostGroup.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.HostGroup.UpdateOneID(e.ID).
				SetName(e.Name).
				SetDescription(e.Description).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("hostGroups: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.HostGroup.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetName(e.Name).
				SetDescription(e.Description).
				SetStatus(e.Status).
				SetTags(e.Tags).
				SetMetadata(e.Metadata).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("hostGroups: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importHostGroupMembers(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "hostGroupMembers", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.HostGroupMember
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("hostGroupMembers: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		existing, _ := client.HostGroupMember.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.HostGroupMember.UpdateOneID(e.ID).
				SetHostGroupID(e.HostGroupID).
				SetDeviceID(e.DeviceID).
				SetSequence(e.Sequence).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("hostGroupMembers: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.HostGroupMember.Create().
				SetID(e.ID).
				SetHostGroupID(e.HostGroupID).
				SetDeviceID(e.DeviceID).
				SetSequence(e.Sequence).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("hostGroupMembers: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}

func (s *BackupService) importIpScanJobs(ctx context.Context, client *ent.Client, items []json.RawMessage, tenantID uint32, full bool, mode ipamV1.RestoreMode) (*ipamV1.EntityImportResult, []string) {
	result := &ipamV1.EntityImportResult{EntityType: "ipScanJobs", Total: int64(len(items))}
	var warnings []string

	for _, raw := range items {
		var e ent.IpScanJob
		if err := json.Unmarshal(raw, &e); err != nil {
			warnings = append(warnings, fmt.Sprintf("ipScanJobs: unmarshal error: %v", err))
			result.Failed++
			continue
		}

		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, _ := client.IpScanJob.Get(ctx, e.ID)
		if existing != nil {
			if mode == ipamV1.RestoreMode_RESTORE_MODE_SKIP {
				result.Skipped++
				continue
			}
			_, err := client.IpScanJob.UpdateOneID(e.ID).
				SetSubnetID(e.SubnetID).
				SetStatus(e.Status).
				SetProgress(e.Progress).
				SetStatusMessage(e.StatusMessage).
				SetTotalAddresses(e.TotalAddresses).
				SetScannedCount(e.ScannedCount).
				SetAliveCount(e.AliveCount).
				SetNewCount(e.NewCount).
				SetUpdatedCount(e.UpdatedCount).
				SetTriggeredBy(e.TriggeredBy).
				SetRetryCount(e.RetryCount).
				SetMaxRetries(e.MaxRetries).
				SetNillableNextRetryAt(e.NextRetryAt).
				SetTimeoutMs(e.TimeoutMs).
				SetConcurrency(e.Concurrency).
				SetSkipReverseDNS(e.SkipReverseDNS).
				SetTCPProbePorts(e.TCPProbePorts).
				SetNillableStartedAt(e.StartedAt).
				SetNillableCompletedAt(e.CompletedAt).
				SetNillableCreateBy(e.CreateBy).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipScanJobs: update %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Updated++
		} else {
			_, err := client.IpScanJob.Create().
				SetID(e.ID).
				SetNillableTenantID(&tid).
				SetSubnetID(e.SubnetID).
				SetStatus(e.Status).
				SetProgress(e.Progress).
				SetStatusMessage(e.StatusMessage).
				SetTotalAddresses(e.TotalAddresses).
				SetScannedCount(e.ScannedCount).
				SetAliveCount(e.AliveCount).
				SetNewCount(e.NewCount).
				SetUpdatedCount(e.UpdatedCount).
				SetTriggeredBy(e.TriggeredBy).
				SetRetryCount(e.RetryCount).
				SetMaxRetries(e.MaxRetries).
				SetNillableNextRetryAt(e.NextRetryAt).
				SetTimeoutMs(e.TimeoutMs).
				SetConcurrency(e.Concurrency).
				SetSkipReverseDNS(e.SkipReverseDNS).
				SetTCPProbePorts(e.TCPProbePorts).
				SetNillableStartedAt(e.StartedAt).
				SetNillableCompletedAt(e.CompletedAt).
				SetNillableCreateBy(e.CreateBy).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ipScanJobs: create %s: %v", e.ID, err))
				result.Failed++
				continue
			}
			result.Created++
		}
	}

	return result, warnings
}
