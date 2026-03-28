package service

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-common/backup"
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
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/location"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/subnet"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/vlan"
)

const (
	backupModule        = "ipam"
	backupSchemaVersion = 1
)

// Migrations registry — add entries here when schema changes.
var migrations = backup.NewMigrationRegistry(backupModule)

// Register migrations in init. Example for future use:
//
//	func init() {
//	    migrations.Register(1, func(entities map[string]json.RawMessage) error {
//	        return backup.MigrateAddField(entities, "locations", "newField", "")
//	    })
//	}

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

// ExportBackup exports all IPAM entities as a gzipped archive.
func (s *BackupService) ExportBackup(ctx context.Context, req *ipamV1.ExportBackupRequest) (*ipamV1.ExportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	full := false

	if grpcx.IsPlatformAdmin(ctx) && req.TenantId != nil && *req.TenantId == 0 {
		full = true
		tenantID = 0
	} else if req.TenantId != nil && *req.TenantId != 0 && grpcx.IsPlatformAdmin(ctx) {
		tenantID = *req.TenantId
	}

	client := s.entClient.Client()
	a := backup.NewArchive(backupModule, backupSchemaVersion, tenantID, full)

	// Export locations
	locQuery := client.Location.Query()
	if !full {
		locQuery = locQuery.Where(location.TenantID(tenantID))
	}
	locations, err := locQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export locations: %w", err)
	}
	if err := backup.SetEntities(a, "locations", locations); err != nil {
		return nil, fmt.Errorf("set locations: %w", err)
	}

	// Export vlans
	vlanQuery := client.Vlan.Query()
	if !full {
		vlanQuery = vlanQuery.Where(vlan.TenantID(tenantID))
	}
	vlans, err := vlanQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export vlans: %w", err)
	}
	if err := backup.SetEntities(a, "vlans", vlans); err != nil {
		return nil, fmt.Errorf("set vlans: %w", err)
	}

	// Export DNS configs
	dnsQuery := client.DnsConfig.Query()
	if !full {
		dnsQuery = dnsQuery.Where(dnsconfig.TenantID(tenantID))
	}
	dnsConfigs, err := dnsQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export dns configs: %w", err)
	}
	if err := backup.SetEntities(a, "dnsConfigs", dnsConfigs); err != nil {
		return nil, fmt.Errorf("set dns configs: %w", err)
	}

	// Export subnets
	subnetQuery := client.Subnet.Query()
	if !full {
		subnetQuery = subnetQuery.Where(subnet.TenantID(tenantID))
	}
	subnets, err := subnetQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export subnets: %w", err)
	}
	if err := backup.SetEntities(a, "subnets", subnets); err != nil {
		return nil, fmt.Errorf("set subnets: %w", err)
	}

	// Export devices
	devQuery := client.Device.Query()
	if !full {
		devQuery = devQuery.Where(device.TenantID(tenantID))
	}
	devices, err := devQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export devices: %w", err)
	}
	if err := backup.SetEntities(a, "devices", devices); err != nil {
		return nil, fmt.Errorf("set devices: %w", err)
	}

	// Export device interfaces (no tenant_id — filter via parent device)
	diQuery := client.DeviceInterface.Query()
	if !full {
		diQuery = diQuery.Where(deviceinterface.HasDeviceWith(device.TenantID(tenantID)))
	}
	deviceInterfaces, err := diQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export device interfaces: %w", err)
	}
	if err := backup.SetEntities(a, "deviceInterfaces", deviceInterfaces); err != nil {
		return nil, fmt.Errorf("set device interfaces: %w", err)
	}

	// Export IP addresses
	ipQuery := client.IpAddress.Query()
	if !full {
		ipQuery = ipQuery.Where(ipaddress.TenantID(tenantID))
	}
	ipAddresses, err := ipQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export ip addresses: %w", err)
	}
	if err := backup.SetEntities(a, "ipAddresses", ipAddresses); err != nil {
		return nil, fmt.Errorf("set ip addresses: %w", err)
	}

	// Export IP groups
	igQuery := client.IpGroup.Query()
	if !full {
		igQuery = igQuery.Where(ipgroup.TenantID(tenantID))
	}
	ipGroups, err := igQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export ip groups: %w", err)
	}
	if err := backup.SetEntities(a, "ipGroups", ipGroups); err != nil {
		return nil, fmt.Errorf("set ip groups: %w", err)
	}

	// Export IP group members (no tenant_id — filter via parent group)
	igmQuery := client.IpGroupMember.Query()
	if !full {
		igmQuery = igmQuery.Where(ipgroupmember.HasGroupWith(ipgroup.TenantID(tenantID)))
	}
	ipGroupMembers, err := igmQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export ip group members: %w", err)
	}
	if err := backup.SetEntities(a, "ipGroupMembers", ipGroupMembers); err != nil {
		return nil, fmt.Errorf("set ip group members: %w", err)
	}

	// Export host groups
	hgQuery := client.HostGroup.Query()
	if !full {
		hgQuery = hgQuery.Where(hostgroup.TenantID(tenantID))
	}
	hostGroups, err := hgQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export host groups: %w", err)
	}
	if err := backup.SetEntities(a, "hostGroups", hostGroups); err != nil {
		return nil, fmt.Errorf("set host groups: %w", err)
	}

	// Export host group members (no tenant_id — filter via parent group)
	hgmQuery := client.HostGroupMember.Query()
	if !full {
		hgmQuery = hgmQuery.Where(hostgroupmember.HasGroupWith(hostgroup.TenantID(tenantID)))
	}
	hostGroupMembers, err := hgmQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("export host group members: %w", err)
	}
	if err := backup.SetEntities(a, "hostGroupMembers", hostGroupMembers); err != nil {
		return nil, fmt.Errorf("set host group members: %w", err)
	}

	// NOTE: ipScanJobs and devicePackages are excluded from backup.
	// ipScanJobs are operational scan configs that can be recreated.
	// devicePackages (77K+ rows) are discovered data from scans, not configuration.

	// Pack (JSON + gzip)
	data, err := backup.Pack(a)
	if err != nil {
		return nil, fmt.Errorf("pack backup: %w", err)
	}

	s.log.Infof("exported backup: module=%s tenant=%d full=%v entities=%v", backupModule, tenantID, full, a.Manifest.EntityCounts)

	return &ipamV1.ExportBackupResponse{
		Data:          data,
		Module:        backupModule,
		Version:       fmt.Sprintf("%d", backupSchemaVersion),
		ExportedAt:    timestamppb.New(a.Manifest.ExportedAt),
		TenantId:      tenantID,
		EntityCounts:  a.Manifest.EntityCounts,
		SchemaVersion: int32(backupSchemaVersion),
	}, nil
}

// ImportBackup restores IPAM entities from a gzipped archive.
func (s *BackupService) ImportBackup(ctx context.Context, req *ipamV1.ImportBackupRequest) (*ipamV1.ImportBackupResponse, error) {
	tenantID := grpcx.GetTenantIDFromContext(ctx)
	isPlatformAdmin := grpcx.IsPlatformAdmin(ctx)
	mode := mapRestoreMode(req.GetMode())

	// Unpack
	a, err := backup.Unpack(req.GetData())
	if err != nil {
		return nil, fmt.Errorf("unpack backup: %w", err)
	}

	// Validate
	if err := backup.Validate(a, backupModule, backupSchemaVersion); err != nil {
		return nil, err
	}

	// Full backups require platform admin
	if a.Manifest.FullBackup && !isPlatformAdmin {
		return nil, fmt.Errorf("only platform admins can restore full backups")
	}

	// Run migrations if needed
	sourceVersion := a.Manifest.SchemaVersion
	applied, err := migrations.RunMigrations(a, backupSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	// Determine restore tenant
	if !isPlatformAdmin || !a.Manifest.FullBackup {
		tenantID = grpcx.GetTenantIDFromContext(ctx)
	} else {
		tenantID = 0
	}

	client := s.entClient.Client()
	result := backup.NewRestoreResult(sourceVersion, backupSchemaVersion, applied)

	// Import in FK dependency order
	s.importLocations(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importVlans(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importDnsConfigs(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importSubnets(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importDevices(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importDeviceInterfaces(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importIpAddresses(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importIpGroups(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importIpGroupMembers(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importHostGroups(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	s.importHostGroupMembers(ctx, client, a, tenantID, a.Manifest.FullBackup, mode, result)
	// ipScanJobs and devicePackages excluded — operational data, not configuration

	s.log.Infof("imported backup: module=%s tenant=%d mode=%v migrations=%d results=%d",
		backupModule, tenantID, mode, applied, len(result.Results))

	// Convert to proto response
	protoResults := make([]*ipamV1.EntityImportResult, len(result.Results))
	for i, r := range result.Results {
		protoResults[i] = &ipamV1.EntityImportResult{
			EntityType: r.EntityType,
			Total:      r.Total,
			Created:    r.Created,
			Updated:    r.Updated,
			Skipped:    r.Skipped,
			Failed:     r.Failed,
		}
	}

	return &ipamV1.ImportBackupResponse{
		Success:           result.Success,
		Results:           protoResults,
		Warnings:          result.Warnings,
		SourceVersion:     int32(result.SourceVersion),
		TargetVersion:     int32(result.TargetVersion),
		MigrationsApplied: int32(result.MigrationsApplied),
	}, nil
}

func mapRestoreMode(m ipamV1.RestoreMode) backup.RestoreMode {
	if m == ipamV1.RestoreMode_RESTORE_MODE_OVERWRITE {
		return backup.RestoreModeOverwrite
	}
	return backup.RestoreModeSkip
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

	sorted := make([]T, 0, len(items))
	var walk func([]T)
	walk = func(nodes []T) {
		for _, n := range nodes {
			sorted = append(sorted, n)
			if children, ok := childMap[getID(n)]; ok {
				walk(children)
			}
		}
	}
	walk(roots)
	return sorted
}

// --- Import helpers ---

func (s *BackupService) importLocations(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	locations, err := backup.GetEntities[ent.Location](a, "locations")
	if err != nil {
		result.AddWarning(fmt.Sprintf("locations: unmarshal error: %v", err))
		return
	}
	if len(locations) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "locations", Total: int64(len(locations))}

	// Topological sort for self-referential parent_id
	sorted := topologicalSortByParentID(locations,
		func(e ent.Location) string { return e.ID },
		func(e ent.Location) string { return e.ParentID },
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Location.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("locations: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
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
				result.AddWarning(fmt.Sprintf("locations: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
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
				result.AddWarning(fmt.Sprintf("locations: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importVlans(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	vlans, err := backup.GetEntities[ent.Vlan](a, "vlans")
	if err != nil {
		result.AddWarning(fmt.Sprintf("vlans: unmarshal error: %v", err))
		return
	}
	if len(vlans) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "vlans", Total: int64(len(vlans))}

	for _, e := range vlans {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Vlan.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("vlans: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("vlans: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("vlans: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importDnsConfigs(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	dnsConfigs, err := backup.GetEntities[ent.DnsConfig](a, "dnsConfigs")
	if err != nil {
		result.AddWarning(fmt.Sprintf("dnsConfigs: unmarshal error: %v", err))
		return
	}
	if len(dnsConfigs) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "dnsConfigs", Total: int64(len(dnsConfigs))}

	for _, e := range dnsConfigs {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.DnsConfig.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("dnsConfigs: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("dnsConfigs: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("dnsConfigs: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importSubnets(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	subnets, err := backup.GetEntities[ent.Subnet](a, "subnets")
	if err != nil {
		result.AddWarning(fmt.Sprintf("subnets: unmarshal error: %v", err))
		return
	}
	if len(subnets) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "subnets", Total: int64(len(subnets))}

	// Topological sort for self-referential parent_id
	sorted := topologicalSortByParentID(subnets,
		func(e ent.Subnet) string { return e.ID },
		func(e ent.Subnet) string { return e.ParentID },
	)

	for _, e := range sorted {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Subnet.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("subnets: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("subnets: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("subnets: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importDevices(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	devices, err := backup.GetEntities[ent.Device](a, "devices")
	if err != nil {
		result.AddWarning(fmt.Sprintf("devices: unmarshal error: %v", err))
		return
	}
	if len(devices) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "devices", Total: int64(len(devices))}

	for _, e := range devices {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.Device.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("devices: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("devices: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("devices: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importDeviceInterfaces(ctx context.Context, client *ent.Client, a *backup.Archive, _ uint32, _ bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	deviceInterfaces, err := backup.GetEntities[ent.DeviceInterface](a, "deviceInterfaces")
	if err != nil {
		result.AddWarning(fmt.Sprintf("deviceInterfaces: unmarshal error: %v", err))
		return
	}
	if len(deviceInterfaces) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "deviceInterfaces", Total: int64(len(deviceInterfaces))}

	for _, e := range deviceInterfaces {
		existing, getErr := client.DeviceInterface.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("deviceInterfaces: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("deviceInterfaces: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("deviceInterfaces: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importIpAddresses(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	ipAddresses, err := backup.GetEntities[ent.IpAddress](a, "ipAddresses")
	if err != nil {
		result.AddWarning(fmt.Sprintf("ipAddresses: unmarshal error: %v", err))
		return
	}
	if len(ipAddresses) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "ipAddresses", Total: int64(len(ipAddresses))}

	for _, e := range ipAddresses {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.IpAddress.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("ipAddresses: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("ipAddresses: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("ipAddresses: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importIpGroups(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	ipGroups, err := backup.GetEntities[ent.IpGroup](a, "ipGroups")
	if err != nil {
		result.AddWarning(fmt.Sprintf("ipGroups: unmarshal error: %v", err))
		return
	}
	if len(ipGroups) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "ipGroups", Total: int64(len(ipGroups))}

	for _, e := range ipGroups {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.IpGroup.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("ipGroups: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("ipGroups: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("ipGroups: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importIpGroupMembers(ctx context.Context, client *ent.Client, a *backup.Archive, _ uint32, _ bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	ipGroupMembers, err := backup.GetEntities[ent.IpGroupMember](a, "ipGroupMembers")
	if err != nil {
		result.AddWarning(fmt.Sprintf("ipGroupMembers: unmarshal error: %v", err))
		return
	}
	if len(ipGroupMembers) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "ipGroupMembers", Total: int64(len(ipGroupMembers))}

	for _, e := range ipGroupMembers {
		existing, getErr := client.IpGroupMember.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("ipGroupMembers: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("ipGroupMembers: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("ipGroupMembers: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importHostGroups(ctx context.Context, client *ent.Client, a *backup.Archive, tenantID uint32, full bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	hostGroups, err := backup.GetEntities[ent.HostGroup](a, "hostGroups")
	if err != nil {
		result.AddWarning(fmt.Sprintf("hostGroups: unmarshal error: %v", err))
		return
	}
	if len(hostGroups) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "hostGroups", Total: int64(len(hostGroups))}

	for _, e := range hostGroups {
		tid := tenantID
		if full && e.TenantID != nil {
			tid = *e.TenantID
		}

		existing, getErr := client.HostGroup.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("hostGroups: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
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
				result.AddWarning(fmt.Sprintf("hostGroups: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
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
				result.AddWarning(fmt.Sprintf("hostGroups: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

func (s *BackupService) importHostGroupMembers(ctx context.Context, client *ent.Client, a *backup.Archive, _ uint32, _ bool, mode backup.RestoreMode, result *backup.RestoreResult) {
	hostGroupMembers, err := backup.GetEntities[ent.HostGroupMember](a, "hostGroupMembers")
	if err != nil {
		result.AddWarning(fmt.Sprintf("hostGroupMembers: unmarshal error: %v", err))
		return
	}
	if len(hostGroupMembers) == 0 {
		return
	}

	er := backup.EntityResult{EntityType: "hostGroupMembers", Total: int64(len(hostGroupMembers))}

	for _, e := range hostGroupMembers {
		existing, getErr := client.HostGroupMember.Get(ctx, e.ID)
		if getErr != nil && !ent.IsNotFound(getErr) {
			result.AddWarning(fmt.Sprintf("hostGroupMembers: lookup %s: %v", e.ID, getErr))
			er.Failed++
			continue
		}
		if existing != nil {
			if mode == backup.RestoreModeSkip {
				er.Skipped++
				continue
			}
			_, err := client.HostGroupMember.UpdateOneID(e.ID).
				SetHostGroupID(e.HostGroupID).
				SetDeviceID(e.DeviceID).
				SetSequence(e.Sequence).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("hostGroupMembers: update %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Updated++
		} else {
			_, err := client.HostGroupMember.Create().
				SetID(e.ID).
				SetHostGroupID(e.HostGroupID).
				SetDeviceID(e.DeviceID).
				SetSequence(e.Sequence).
				SetNillableCreateTime(e.CreateTime).
				Save(ctx)
			if err != nil {
				result.AddWarning(fmt.Sprintf("hostGroupMembers: create %s: %v", e.ID, err))
				er.Failed++
				continue
			}
			er.Created++
		}
	}

	result.AddResult(er)
}

// importIpScanJobs and importDevicePackages intentionally removed —
// these contain operational/discovered data, not configuration.
