// Package repo defines the storage contract for the IPAM service.
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/store"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict") // unique-constraint violation (duplicate)
	ErrNotEmpty = errors.New("not empty")
)

// Stats is a per-tenant statistics rollup.
type Stats struct {
	TotalSubnets       int64            `json:"total_subnets"`
	TotalAddresses     int64            `json:"total_addresses"`
	UsedAddresses      int64            `json:"used_addresses"`
	AvailableAddresses int64            `json:"available_addresses"`
	TotalVlans         int64            `json:"total_vlans"`
	TotalDevices       int64            `json:"total_devices"`
	TotalLocations     int64            `json:"total_locations"`
	OverallUtilization float64          `json:"overall_utilization"`
	DevicesByType      map[string]int64 `json:"devices_by_type"`
}

// Store is the IPAM persistence contract. Methods are tenant-scoped; the system
// scope (nil-uuid pin) is used only by the scan executor and maintenance paths.
type Store interface {
	// Subnets
	CreateSubnet(ctx context.Context, s store.Subnet) error
	GetSubnet(ctx context.Context, tenantID, id string) (store.Subnet, error)
	ListSubnets(ctx context.Context, tenantID string, f store.SubnetFilter) ([]store.Subnet, error)
	UpdateSubnet(ctx context.Context, s store.Subnet) error
	DeleteSubnet(ctx context.Context, tenantID, id string, force bool) error
	CountAddressesInSubnet(ctx context.Context, tenantID, subnetID string) (int64, error)
	ListAllocatedAddresses(ctx context.Context, tenantID, subnetID string) ([]string, error) // just the address strings
	SubnetsForVlan(ctx context.Context, tenantID, vlanID string) ([]store.Subnet, error)
	AllSubnetCIDRs(ctx context.Context, tenantID string) ([]store.Subnet, error) // for overlap checks

	// IP addresses
	CreateAddress(ctx context.Context, a store.IPAddress) error // ErrConflict on duplicate (allocation guard)
	GetAddress(ctx context.Context, tenantID, id string) (store.IPAddress, error)
	FindAddress(ctx context.Context, tenantID, address string) (store.IPAddress, error)
	ListAddresses(ctx context.Context, tenantID string, f store.AddressFilter) ([]store.IPAddress, error)
	UpdateAddress(ctx context.Context, a store.IPAddress) error
	DeleteAddress(ctx context.Context, tenantID, id string) error
	UpsertAddressByAddress(ctx context.Context, a store.IPAddress) (created bool, err error) // scan path
	AddressesForDevice(ctx context.Context, tenantID, deviceID string) ([]store.IPAddress, error)

	// Devices
	CreateDevice(ctx context.Context, d store.Device) error
	GetDevice(ctx context.Context, tenantID, id string) (store.Device, error)
	ListDevices(ctx context.Context, tenantID string, f store.DeviceFilter) ([]store.Device, error)
	UpdateDevice(ctx context.Context, d store.Device) error
	DeleteDevice(ctx context.Context, tenantID, id string, force bool) error
	UpsertDeviceByName(ctx context.Context, d store.Device) (store.Device, error) // scan path

	// Device interfaces + links
	CreateInterface(ctx context.Context, i store.DeviceInterface) error
	GetInterface(ctx context.Context, tenantID, id string) (store.DeviceInterface, error)
	ListInterfaces(ctx context.Context, tenantID, deviceID string) ([]store.DeviceInterface, error)
	UpsertInterfaceByName(ctx context.Context, i store.DeviceInterface) (store.DeviceInterface, error) // scan path
	DeleteInterface(ctx context.Context, tenantID, id string) error
	ReplaceInterfaceLinks(ctx context.Context, tenantID, interfaceID string, links []store.DeviceInterfaceLink) error
	ListInterfaceLinks(ctx context.Context, tenantID, interfaceID string) ([]store.DeviceInterfaceLink, error)

	// Device packages
	ReplaceDevicePackages(ctx context.Context, tenantID, deviceID string, pkgs []store.DevicePackage) error
	ListDevicePackages(ctx context.Context, tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string) ([]store.DevicePackage, error)
	DeleteDevicePackages(ctx context.Context, tenantID, deviceID string) error

	// VLANs
	CreateVlan(ctx context.Context, v store.Vlan) error
	GetVlan(ctx context.Context, tenantID, id string) (store.Vlan, error)
	ListVlans(ctx context.Context, tenantID string, f store.VlanFilter) ([]store.Vlan, error)
	UpdateVlan(ctx context.Context, v store.Vlan) error
	DeleteVlan(ctx context.Context, tenantID, id string, force bool) error

	// Locations
	CreateLocation(ctx context.Context, l store.Location) error
	GetLocation(ctx context.Context, tenantID, id string) (store.Location, error)
	ListLocations(ctx context.Context, tenantID string, f store.LocationFilter) ([]store.Location, error)
	UpdateLocation(ctx context.Context, l store.Location) error
	DeleteLocation(ctx context.Context, tenantID, id string, force bool) error

	// IP groups
	CreateIPGroup(ctx context.Context, g store.IPGroup) error
	GetIPGroup(ctx context.Context, tenantID, id string) (store.IPGroup, error)
	ListIPGroups(ctx context.Context, tenantID string, limit int, cursorID string) ([]store.IPGroup, error)
	UpdateIPGroup(ctx context.Context, g store.IPGroup) error
	DeleteIPGroup(ctx context.Context, tenantID, id string) error
	AddIPGroupMember(ctx context.Context, m store.IPGroupMember) error
	RemoveIPGroupMember(ctx context.Context, tenantID, memberID string) error
	UpdateIPGroupMember(ctx context.Context, m store.IPGroupMember) error
	ListIPGroupMembers(ctx context.Context, tenantID, groupID string) ([]store.IPGroupMember, error)
	AllIPGroupsWithMembers(ctx context.Context, tenantID string, groupIDs []string) ([]store.IPGroup, map[string][]store.IPGroupMember, error) // for CheckIp

	// Host groups
	CreateHostGroup(ctx context.Context, g store.HostGroup) error
	GetHostGroup(ctx context.Context, tenantID, id string) (store.HostGroup, error)
	ListHostGroups(ctx context.Context, tenantID string, limit int, cursorID string) ([]store.HostGroup, error)
	UpdateHostGroup(ctx context.Context, g store.HostGroup) error
	DeleteHostGroup(ctx context.Context, tenantID, id string) error
	AddHostGroupMember(ctx context.Context, m store.HostGroupMember) error
	RemoveHostGroupMember(ctx context.Context, tenantID, memberID string) error
	UpdateHostGroupMember(ctx context.Context, m store.HostGroupMember) error
	ListHostGroupMembers(ctx context.Context, tenantID, groupID string) ([]store.HostGroupMember, error)
	ListDeviceHostGroups(ctx context.Context, tenantID, deviceID string) ([]store.HostGroup, error)

	// Scan jobs (work queue)
	CreateScanJob(ctx context.Context, j store.IPScanJob) error
	GetScanJob(ctx context.Context, tenantID, id string) (store.IPScanJob, error)
	ListScanJobs(ctx context.Context, tenantID string, f store.ScanFilter) ([]store.IPScanJob, error)
	UpdateScanJob(ctx context.Context, j store.IPScanJob) error
	ClaimDueScanJobs(ctx context.Context, now time.Time, limit int) ([]store.IPScanJob, error) // system scope, FOR UPDATE SKIP LOCKED
	ActiveScanForSubnet(ctx context.Context, tenantID, subnetID string) (bool, error)

	// DNS config
	GetDNSConfig(ctx context.Context, tenantID string) (store.DNSConfig, error)
	UpsertDNSConfig(ctx context.Context, c store.DNSConfig) error

	// Statistics
	TenantStats(ctx context.Context, tenantID string) (Stats, error)
	TenantIDs(ctx context.Context) ([]string, error) // system scope

	// Audit
	AppendAudit(ctx context.Context, row store.AuditRow) error
}
