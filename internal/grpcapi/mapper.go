package grpcapi

import (
	"time"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
)

// ---- time helpers ----

// unix returns t as unix seconds, or 0 for the zero time.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// unixPtr returns *t as unix seconds, or 0 for a nil/zero time.
func unixPtr(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.Unix()
}

// unixTime maps unix seconds back to a time (0 -> zero time).
func unixTime(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

// ---- enums: store string <-> proto ----

func subnetStatusToPB(s string) ipamv1.SubnetStatus {
	switch s {
	case store.SubnetActive:
		return ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE
	case store.SubnetReserved:
		return ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED
	case store.SubnetDeprecated:
		return ipamv1.SubnetStatus_SUBNET_STATUS_DEPRECATED
	case store.SubnetDeleted:
		return ipamv1.SubnetStatus_SUBNET_STATUS_DELETED
	}
	return ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED
}

func subnetStatusFromPB(s ipamv1.SubnetStatus) string {
	switch s {
	case ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE:
		return store.SubnetActive
	case ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED:
		return store.SubnetReserved
	case ipamv1.SubnetStatus_SUBNET_STATUS_DEPRECATED:
		return store.SubnetDeprecated
	case ipamv1.SubnetStatus_SUBNET_STATUS_DELETED:
		return store.SubnetDeleted
	}
	return ""
}

func ipStatusToPB(s string) ipamv1.IpStatus {
	switch s {
	case store.IPActive:
		return ipamv1.IpStatus_IP_STATUS_ACTIVE
	case store.IPReserved:
		return ipamv1.IpStatus_IP_STATUS_RESERVED
	case store.IPDHCP:
		return ipamv1.IpStatus_IP_STATUS_DHCP
	case store.IPDeprecated:
		return ipamv1.IpStatus_IP_STATUS_DEPRECATED
	case store.IPOffline:
		return ipamv1.IpStatus_IP_STATUS_OFFLINE
	}
	return ipamv1.IpStatus_IP_STATUS_UNSPECIFIED
}

func ipStatusFromPB(s ipamv1.IpStatus) string {
	switch s {
	case ipamv1.IpStatus_IP_STATUS_ACTIVE:
		return store.IPActive
	case ipamv1.IpStatus_IP_STATUS_RESERVED:
		return store.IPReserved
	case ipamv1.IpStatus_IP_STATUS_DHCP:
		return store.IPDHCP
	case ipamv1.IpStatus_IP_STATUS_DEPRECATED:
		return store.IPDeprecated
	case ipamv1.IpStatus_IP_STATUS_OFFLINE:
		return store.IPOffline
	}
	return ""
}

func addressTypeToPB(s string) ipamv1.AddressType {
	switch s {
	case store.AddrHost:
		return ipamv1.AddressType_ADDRESS_TYPE_HOST
	case store.AddrGateway:
		return ipamv1.AddressType_ADDRESS_TYPE_GATEWAY
	case store.AddrBroadcast:
		return ipamv1.AddressType_ADDRESS_TYPE_BROADCAST
	case store.AddrNetwork:
		return ipamv1.AddressType_ADDRESS_TYPE_NETWORK
	case store.AddrVirtual:
		return ipamv1.AddressType_ADDRESS_TYPE_VIRTUAL
	case store.AddrAnycast:
		return ipamv1.AddressType_ADDRESS_TYPE_ANYCAST
	}
	return ipamv1.AddressType_ADDRESS_TYPE_UNSPECIFIED
}

func addressTypeFromPB(s ipamv1.AddressType) string {
	switch s {
	case ipamv1.AddressType_ADDRESS_TYPE_HOST:
		return store.AddrHost
	case ipamv1.AddressType_ADDRESS_TYPE_GATEWAY:
		return store.AddrGateway
	case ipamv1.AddressType_ADDRESS_TYPE_BROADCAST:
		return store.AddrBroadcast
	case ipamv1.AddressType_ADDRESS_TYPE_NETWORK:
		return store.AddrNetwork
	case ipamv1.AddressType_ADDRESS_TYPE_VIRTUAL:
		return store.AddrVirtual
	case ipamv1.AddressType_ADDRESS_TYPE_ANYCAST:
		return store.AddrAnycast
	}
	return ""
}

func deviceTypeToPB(s string) ipamv1.DeviceType {
	switch s {
	case store.DevServer:
		return ipamv1.DeviceType_DEVICE_TYPE_SERVER
	case store.DevVM:
		return ipamv1.DeviceType_DEVICE_TYPE_VM
	case store.DevRouter:
		return ipamv1.DeviceType_DEVICE_TYPE_ROUTER
	case store.DevSwitch:
		return ipamv1.DeviceType_DEVICE_TYPE_SWITCH
	case store.DevFirewall:
		return ipamv1.DeviceType_DEVICE_TYPE_FIREWALL
	case store.DevLoadBalancer:
		return ipamv1.DeviceType_DEVICE_TYPE_LOAD_BALANCER
	case store.DevAccessPoint:
		return ipamv1.DeviceType_DEVICE_TYPE_ACCESS_POINT
	case store.DevStorage:
		return ipamv1.DeviceType_DEVICE_TYPE_STORAGE
	case store.DevPrinter:
		return ipamv1.DeviceType_DEVICE_TYPE_PRINTER
	case store.DevPhone:
		return ipamv1.DeviceType_DEVICE_TYPE_PHONE
	case store.DevWorkstation:
		return ipamv1.DeviceType_DEVICE_TYPE_WORKSTATION
	case store.DevContainer:
		return ipamv1.DeviceType_DEVICE_TYPE_CONTAINER
	case store.DevOther:
		return ipamv1.DeviceType_DEVICE_TYPE_OTHER
	}
	return ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED
}

func deviceTypeFromPB(s ipamv1.DeviceType) string {
	switch s {
	case ipamv1.DeviceType_DEVICE_TYPE_SERVER:
		return store.DevServer
	case ipamv1.DeviceType_DEVICE_TYPE_VM:
		return store.DevVM
	case ipamv1.DeviceType_DEVICE_TYPE_ROUTER:
		return store.DevRouter
	case ipamv1.DeviceType_DEVICE_TYPE_SWITCH:
		return store.DevSwitch
	case ipamv1.DeviceType_DEVICE_TYPE_FIREWALL:
		return store.DevFirewall
	case ipamv1.DeviceType_DEVICE_TYPE_LOAD_BALANCER:
		return store.DevLoadBalancer
	case ipamv1.DeviceType_DEVICE_TYPE_ACCESS_POINT:
		return store.DevAccessPoint
	case ipamv1.DeviceType_DEVICE_TYPE_STORAGE:
		return store.DevStorage
	case ipamv1.DeviceType_DEVICE_TYPE_PRINTER:
		return store.DevPrinter
	case ipamv1.DeviceType_DEVICE_TYPE_PHONE:
		return store.DevPhone
	case ipamv1.DeviceType_DEVICE_TYPE_WORKSTATION:
		return store.DevWorkstation
	case ipamv1.DeviceType_DEVICE_TYPE_CONTAINER:
		return store.DevContainer
	case ipamv1.DeviceType_DEVICE_TYPE_OTHER:
		return store.DevOther
	}
	return ""
}

func deviceStatusToPB(s string) ipamv1.DeviceStatus {
	switch s {
	case store.DevStActive:
		return ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE
	case store.DevStPlanned:
		return ipamv1.DeviceStatus_DEVICE_STATUS_PLANNED
	case store.DevStStaged:
		return ipamv1.DeviceStatus_DEVICE_STATUS_STAGED
	case store.DevStDecommissioned:
		return ipamv1.DeviceStatus_DEVICE_STATUS_DECOMMISSIONED
	case store.DevStOffline:
		return ipamv1.DeviceStatus_DEVICE_STATUS_OFFLINE
	case store.DevStFailed:
		return ipamv1.DeviceStatus_DEVICE_STATUS_FAILED
	case store.DevStAvailable:
		return ipamv1.DeviceStatus_DEVICE_STATUS_AVAILABLE
	}
	return ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED
}

func deviceStatusFromPB(s ipamv1.DeviceStatus) string {
	switch s {
	case ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE:
		return store.DevStActive
	case ipamv1.DeviceStatus_DEVICE_STATUS_PLANNED:
		return store.DevStPlanned
	case ipamv1.DeviceStatus_DEVICE_STATUS_STAGED:
		return store.DevStStaged
	case ipamv1.DeviceStatus_DEVICE_STATUS_DECOMMISSIONED:
		return store.DevStDecommissioned
	case ipamv1.DeviceStatus_DEVICE_STATUS_OFFLINE:
		return store.DevStOffline
	case ipamv1.DeviceStatus_DEVICE_STATUS_FAILED:
		return store.DevStFailed
	case ipamv1.DeviceStatus_DEVICE_STATUS_AVAILABLE:
		return store.DevStAvailable
	}
	return ""
}

func vlanStatusToPB(s string) ipamv1.VlanStatus {
	switch s {
	case store.VlanActive:
		return ipamv1.VlanStatus_VLAN_STATUS_ACTIVE
	case store.VlanReserved:
		return ipamv1.VlanStatus_VLAN_STATUS_RESERVED
	case store.VlanDeprecated:
		return ipamv1.VlanStatus_VLAN_STATUS_DEPRECATED
	}
	return ipamv1.VlanStatus_VLAN_STATUS_UNSPECIFIED
}

func vlanStatusFromPB(s ipamv1.VlanStatus) string {
	switch s {
	case ipamv1.VlanStatus_VLAN_STATUS_ACTIVE:
		return store.VlanActive
	case ipamv1.VlanStatus_VLAN_STATUS_RESERVED:
		return store.VlanReserved
	case ipamv1.VlanStatus_VLAN_STATUS_DEPRECATED:
		return store.VlanDeprecated
	}
	return ""
}

func locationTypeToPB(s string) ipamv1.LocationType {
	switch s {
	case store.LocRegion:
		return ipamv1.LocationType_LOCATION_TYPE_REGION
	case store.LocCountry:
		return ipamv1.LocationType_LOCATION_TYPE_COUNTRY
	case store.LocCity:
		return ipamv1.LocationType_LOCATION_TYPE_CITY
	case store.LocDatacenter:
		return ipamv1.LocationType_LOCATION_TYPE_DATACENTER
	case store.LocBuilding:
		return ipamv1.LocationType_LOCATION_TYPE_BUILDING
	case store.LocFloor:
		return ipamv1.LocationType_LOCATION_TYPE_FLOOR
	case store.LocRoom:
		return ipamv1.LocationType_LOCATION_TYPE_ROOM
	case store.LocRack:
		return ipamv1.LocationType_LOCATION_TYPE_RACK
	case store.LocSite:
		return ipamv1.LocationType_LOCATION_TYPE_SITE
	case store.LocBranch:
		return ipamv1.LocationType_LOCATION_TYPE_BRANCH
	}
	return ipamv1.LocationType_LOCATION_TYPE_UNSPECIFIED
}

func locationTypeFromPB(s ipamv1.LocationType) string {
	switch s {
	case ipamv1.LocationType_LOCATION_TYPE_REGION:
		return store.LocRegion
	case ipamv1.LocationType_LOCATION_TYPE_COUNTRY:
		return store.LocCountry
	case ipamv1.LocationType_LOCATION_TYPE_CITY:
		return store.LocCity
	case ipamv1.LocationType_LOCATION_TYPE_DATACENTER:
		return store.LocDatacenter
	case ipamv1.LocationType_LOCATION_TYPE_BUILDING:
		return store.LocBuilding
	case ipamv1.LocationType_LOCATION_TYPE_FLOOR:
		return store.LocFloor
	case ipamv1.LocationType_LOCATION_TYPE_ROOM:
		return store.LocRoom
	case ipamv1.LocationType_LOCATION_TYPE_RACK:
		return store.LocRack
	case ipamv1.LocationType_LOCATION_TYPE_SITE:
		return store.LocSite
	case ipamv1.LocationType_LOCATION_TYPE_BRANCH:
		return store.LocBranch
	}
	return ""
}

func locationStatusToPB(s string) ipamv1.LocationStatus {
	switch s {
	case store.LocStActive:
		return ipamv1.LocationStatus_LOCATION_STATUS_ACTIVE
	case store.LocStPlanned:
		return ipamv1.LocationStatus_LOCATION_STATUS_PLANNED
	case store.LocStDecommissioned:
		return ipamv1.LocationStatus_LOCATION_STATUS_DECOMMISSIONED
	}
	return ipamv1.LocationStatus_LOCATION_STATUS_UNSPECIFIED
}

func locationStatusFromPB(s ipamv1.LocationStatus) string {
	switch s {
	case ipamv1.LocationStatus_LOCATION_STATUS_ACTIVE:
		return store.LocStActive
	case ipamv1.LocationStatus_LOCATION_STATUS_PLANNED:
		return store.LocStPlanned
	case ipamv1.LocationStatus_LOCATION_STATUS_DECOMMISSIONED:
		return store.LocStDecommissioned
	}
	return ""
}

func memberTypeToPB(s string) ipamv1.MemberType {
	switch s {
	case store.MemberAddress:
		return ipamv1.MemberType_MEMBER_TYPE_ADDRESS
	case store.MemberRange:
		return ipamv1.MemberType_MEMBER_TYPE_RANGE
	case store.MemberSubnet:
		return ipamv1.MemberType_MEMBER_TYPE_SUBNET
	}
	return ipamv1.MemberType_MEMBER_TYPE_UNSPECIFIED
}

func memberTypeFromPB(s ipamv1.MemberType) string {
	switch s {
	case ipamv1.MemberType_MEMBER_TYPE_ADDRESS:
		return store.MemberAddress
	case ipamv1.MemberType_MEMBER_TYPE_RANGE:
		return store.MemberRange
	case ipamv1.MemberType_MEMBER_TYPE_SUBNET:
		return store.MemberSubnet
	}
	return ""
}

func groupStatusToPB(s string) ipamv1.GroupStatus {
	switch s {
	case store.GroupActive:
		return ipamv1.GroupStatus_GROUP_STATUS_ACTIVE
	case store.GroupInactive:
		return ipamv1.GroupStatus_GROUP_STATUS_INACTIVE
	}
	return ipamv1.GroupStatus_GROUP_STATUS_UNSPECIFIED
}

func groupStatusFromPB(s ipamv1.GroupStatus) string {
	switch s {
	case ipamv1.GroupStatus_GROUP_STATUS_ACTIVE:
		return store.GroupActive
	case ipamv1.GroupStatus_GROUP_STATUS_INACTIVE:
		return store.GroupInactive
	}
	return ""
}

func scanStatusToPB(s string) ipamv1.ScanStatus {
	switch s {
	case store.ScanPending:
		return ipamv1.ScanStatus_SCAN_STATUS_PENDING
	case store.ScanScanning:
		return ipamv1.ScanStatus_SCAN_STATUS_SCANNING
	case store.ScanCompleted:
		return ipamv1.ScanStatus_SCAN_STATUS_COMPLETED
	case store.ScanFailed:
		return ipamv1.ScanStatus_SCAN_STATUS_FAILED
	case store.ScanCancelled:
		return ipamv1.ScanStatus_SCAN_STATUS_CANCELLED
	}
	return ipamv1.ScanStatus_SCAN_STATUS_UNSPECIFIED
}

func scanStatusFromPB(s ipamv1.ScanStatus) string {
	switch s {
	case ipamv1.ScanStatus_SCAN_STATUS_PENDING:
		return store.ScanPending
	case ipamv1.ScanStatus_SCAN_STATUS_SCANNING:
		return store.ScanScanning
	case ipamv1.ScanStatus_SCAN_STATUS_COMPLETED:
		return store.ScanCompleted
	case ipamv1.ScanStatus_SCAN_STATUS_FAILED:
		return store.ScanFailed
	case ipamv1.ScanStatus_SCAN_STATUS_CANCELLED:
		return store.ScanCancelled
	}
	return ""
}

func scanTriggerToPB(s string) ipamv1.ScanTrigger {
	switch s {
	case store.TriggerAuto:
		return ipamv1.ScanTrigger_SCAN_TRIGGER_AUTO
	case store.TriggerManual:
		return ipamv1.ScanTrigger_SCAN_TRIGGER_MANUAL
	}
	return ipamv1.ScanTrigger_SCAN_TRIGGER_UNSPECIFIED
}

// powerActionToStore maps a proto PowerAction to the ipmi action verb.
func powerActionToStore(a ipamv1.PowerAction) string {
	switch a {
	case ipamv1.PowerAction_POWER_ACTION_ON:
		return store.PowerOn
	case ipamv1.PowerAction_POWER_ACTION_OFF:
		return store.PowerOff
	case ipamv1.PowerAction_POWER_ACTION_CYCLE:
		return store.PowerCycle
	case ipamv1.PowerAction_POWER_ACTION_RESET:
		return store.PowerReset
	case ipamv1.PowerAction_POWER_ACTION_SOFT:
		return store.PowerSoft
	case ipamv1.PowerAction_POWER_ACTION_DIAG:
		return store.PowerDiag
	}
	return ""
}

// ---- subnet ----

func subnetToPB(v store.Subnet) *ipamv1.Subnet {
	return &ipamv1.Subnet{
		Id: v.ID, TenantId: v.TenantID, Name: v.Name, Cidr: v.CIDR, Description: v.Description,
		Gateway: v.Gateway, DnsServers: v.DNSServers, VlanId: v.VlanID, ParentId: v.ParentID,
		LocationId: v.LocationID, Status: subnetStatusToPB(v.Status), IpVersion: int32(v.IPVersion),
		NetworkAddress: v.NetworkAddress, BroadcastAddress: v.BroadcastAddr, Mask: v.Mask,
		PrefixLength: int32(v.PrefixLength), SnmpSecretRef: v.SNMPSecretRef, SnmpVersion: int32(v.SNMPVersion),
		Tags: v.Tags, CreatedBy: v.CreatedBy, CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
		TotalAddresses: v.TotalAddresses, UsedAddresses: v.UsedAddresses,
		AvailableAddresses: v.AvailableAddresses, Utilization: v.Utilization,
	}
}

func subnetFromPB(p *ipamv1.Subnet) store.Subnet {
	if p == nil {
		return store.Subnet{}
	}
	return store.Subnet{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), CIDR: p.GetCidr(),
		Description: p.GetDescription(), Gateway: p.GetGateway(), DNSServers: p.GetDnsServers(),
		VlanID: p.GetVlanId(), ParentID: p.GetParentId(), LocationID: p.GetLocationId(),
		Status: subnetStatusFromPB(p.GetStatus()), SNMPSecretRef: p.GetSnmpSecretRef(),
		SNMPVersion: int(p.GetSnmpVersion()), Tags: p.GetTags(),
	}
}

func subnetTreeToPB(nodes []*subnets.TreeNode) []*ipamv1.SubnetTreeNode {
	out := make([]*ipamv1.SubnetTreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &ipamv1.SubnetTreeNode{
			Subnet:   subnetToPB(n.Subnet),
			Children: subnetTreeToPB(n.Children),
		})
	}
	return out
}

// ---- address ----

func addressToPB(v store.IPAddress) *ipamv1.IPAddress {
	return &ipamv1.IPAddress{
		Id: v.ID, TenantId: v.TenantID, Address: v.Address, SubnetId: v.SubnetID, Hostname: v.Hostname,
		MacAddress: v.MACAddress, Description: v.Description, DeviceId: v.DeviceID,
		InterfaceName: v.InterfaceName, Status: ipStatusToPB(v.Status), AddressType: addressTypeToPB(v.AddressType),
		IsPrimary: v.IsPrimary, PtrRecord: v.PTRRecord, DnsName: v.DNSName, LastSeen: unixPtr(v.LastSeen),
		LeaseExpiry: unixPtr(v.LeaseExpiry), HasReverseDns: v.HasReverseDNS, Note: v.Note, Tags: v.Tags,
		CreatedBy: v.CreatedBy, CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
	}
}

func addressFromPB(p *ipamv1.IPAddress) store.IPAddress {
	if p == nil {
		return store.IPAddress{}
	}
	a := store.IPAddress{
		ID: p.GetId(), TenantID: p.GetTenantId(), Address: p.GetAddress(), SubnetID: p.GetSubnetId(),
		Hostname: p.GetHostname(), MACAddress: p.GetMacAddress(), Description: p.GetDescription(),
		DeviceID: p.GetDeviceId(), InterfaceName: p.GetInterfaceName(), Status: ipStatusFromPB(p.GetStatus()),
		AddressType: addressTypeFromPB(p.GetAddressType()), IsPrimary: p.GetIsPrimary(),
		PTRRecord: p.GetPtrRecord(), DNSName: p.GetDnsName(), HasReverseDNS: p.GetHasReverseDns(),
		Note: p.GetNote(), Tags: p.GetTags(),
	}
	if ts := p.GetLastSeen(); ts != 0 {
		t := unixTime(ts)
		a.LastSeen = &t
	}
	if ts := p.GetLeaseExpiry(); ts != 0 {
		t := unixTime(ts)
		a.LeaseExpiry = &t
	}
	return a
}

// ---- device ----

func deviceToPB(v store.Device) *ipamv1.Device {
	return &ipamv1.Device{
		Id: v.ID, TenantId: v.TenantID, Name: v.Name, DeviceType: deviceTypeToPB(v.DeviceType),
		Description: v.Description, Manufacturer: v.Manufacturer, Model: v.Model, SerialNumber: v.SerialNumber,
		AssetTag: v.AssetTag, LocationId: v.LocationID, RackId: v.RackID, RackPosition: int32(v.RackPosition),
		DeviceHeightU: int32(v.DeviceHeightU), Status: deviceStatusToPB(v.Status), PrimaryIp: v.PrimaryIP,
		PrimaryIpv6: v.PrimaryIPv6, ManagementIp: v.ManagementIP, OsType: v.OSType, OsVersion: v.OSVersion,
		FirmwareVersion: v.FirmwareVersion, LastSeen: unixPtr(v.LastSeen), IpmiSecretRef: v.IPMISecretRef,
		RebootRequired: v.RebootRequired, UnattendedUpgrades: v.UnattendedUpgrades, Tags: v.Tags,
		CreatedBy: v.CreatedBy, CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
		InterfaceCount: v.InterfaceCount, AddressCount: v.AddressCount,
		PackageUpdateCount: v.PackageUpdateCount, SecurityUpdateCount: v.SecurityUpdateCount,
	}
}

func deviceFromPB(p *ipamv1.Device) store.Device {
	if p == nil {
		return store.Device{}
	}
	return store.Device{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), DeviceType: deviceTypeFromPB(p.GetDeviceType()),
		Description: p.GetDescription(), Manufacturer: p.GetManufacturer(), Model: p.GetModel(),
		SerialNumber: p.GetSerialNumber(), AssetTag: p.GetAssetTag(), LocationID: p.GetLocationId(),
		RackID: p.GetRackId(), RackPosition: int(p.GetRackPosition()), DeviceHeightU: int(p.GetDeviceHeightU()),
		Status: deviceStatusFromPB(p.GetStatus()), PrimaryIP: p.GetPrimaryIp(), PrimaryIPv6: p.GetPrimaryIpv6(),
		ManagementIP: p.GetManagementIp(), OSType: p.GetOsType(), OSVersion: p.GetOsVersion(),
		FirmwareVersion: p.GetFirmwareVersion(), IPMISecretRef: p.GetIpmiSecretRef(),
		RebootRequired: p.GetRebootRequired(), UnattendedUpgrades: p.GetUnattendedUpgrades(), Tags: p.GetTags(),
	}
}

func deviceInterfaceToPB(v store.DeviceInterface) *ipamv1.DeviceInterface {
	return &ipamv1.DeviceInterface{
		Id: v.ID, TenantId: v.TenantID, DeviceId: v.DeviceID, Name: v.Name, MacAddress: v.MACAddress,
		InterfaceType: v.InterfaceType, Enabled: v.Enabled, SpeedMbps: int32(v.SpeedMbps),
		Description: v.Description, IfIndex: int32(v.IfIndex), RemoteDeviceId: v.RemoteDeviceID,
		RemoteInterfaceId: v.RemoteInterfaceID, RemotePortName: v.RemotePortName, LinkSource: v.LinkSource,
		LinkVlan: int32(v.LinkVlan), LinkLastSeen: unixPtr(v.LinkLastSeen),
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
	}
}

func deviceInterfaceFromPB(p *ipamv1.DeviceInterface) store.DeviceInterface {
	if p == nil {
		return store.DeviceInterface{}
	}
	return store.DeviceInterface{
		ID: p.GetId(), TenantID: p.GetTenantId(), DeviceID: p.GetDeviceId(), Name: p.GetName(),
		MACAddress: p.GetMacAddress(), InterfaceType: p.GetInterfaceType(), Enabled: p.GetEnabled(),
		SpeedMbps: int(p.GetSpeedMbps()), Description: p.GetDescription(), IfIndex: int(p.GetIfIndex()),
		RemoteDeviceID: p.GetRemoteDeviceId(), RemoteInterfaceID: p.GetRemoteInterfaceId(),
		RemotePortName: p.GetRemotePortName(), LinkSource: p.GetLinkSource(), LinkVlan: int(p.GetLinkVlan()),
	}
}

func devicePackageToPB(v store.DevicePackage) *ipamv1.DevicePackage {
	return &ipamv1.DevicePackage{
		Id: v.ID, TenantId: v.TenantID, DeviceId: v.DeviceID, Name: v.Name, CurrentVersion: v.CurrentVersion,
		AvailableVersion: v.AvailableVersion, NeedsUpdate: v.NeedsUpdate, IsSecurityUpdate: v.IsSecurityUpdate,
		PackageManager: v.PackageManager, Description: v.Description, CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
	}
}

// ---- vlan ----

func vlanToPB(v store.Vlan) *ipamv1.Vlan {
	return &ipamv1.Vlan{
		Id: v.ID, TenantId: v.TenantID, VlanId: int32(v.VlanID), Name: v.Name, Description: v.Description,
		Domain: v.Domain, LocationId: v.LocationID, Status: vlanStatusToPB(v.Status), Tags: v.Tags,
		CreatedBy: v.CreatedBy, CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt), SubnetCount: v.SubnetCount,
	}
}

func vlanFromPB(p *ipamv1.Vlan) store.Vlan {
	if p == nil {
		return store.Vlan{}
	}
	return store.Vlan{
		ID: p.GetId(), TenantID: p.GetTenantId(), VlanID: int(p.GetVlanId()), Name: p.GetName(),
		Description: p.GetDescription(), Domain: p.GetDomain(), LocationID: p.GetLocationId(),
		Status: vlanStatusFromPB(p.GetStatus()), Tags: p.GetTags(),
	}
}

// ---- location ----

func locationToPB(v store.Location) *ipamv1.Location {
	return &ipamv1.Location{
		Id: v.ID, TenantId: v.TenantID, Name: v.Name, Code: v.Code, LocationType: locationTypeToPB(v.LocationType),
		Description: v.Description, ParentId: v.ParentID, Path: v.Path, Address: v.Address, City: v.City,
		State: v.State, Country: v.Country, PostalCode: v.PostalCode, Latitude: v.Latitude, Longitude: v.Longitude,
		Status: locationStatusToPB(v.Status), RackSizeU: int32(v.RackSizeU), Tags: v.Tags, CreatedBy: v.CreatedBy,
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt), ChildCount: v.ChildCount,
		DeviceCount: v.DeviceCount, SubnetCount: v.SubnetCount, VlanCount: v.VlanCount,
	}
}

func locationFromPB(p *ipamv1.Location) store.Location {
	if p == nil {
		return store.Location{}
	}
	return store.Location{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), Code: p.GetCode(),
		LocationType: locationTypeFromPB(p.GetLocationType()), Description: p.GetDescription(),
		ParentID: p.GetParentId(), Address: p.GetAddress(), City: p.GetCity(), State: p.GetState(),
		Country: p.GetCountry(), PostalCode: p.GetPostalCode(), Latitude: p.GetLatitude(),
		Longitude: p.GetLongitude(), Status: locationStatusFromPB(p.GetStatus()), RackSizeU: int(p.GetRackSizeU()),
		Tags: p.GetTags(),
	}
}

func locationTreeToPB(nodes []*locations.Node) []*ipamv1.LocationTreeNode {
	out := make([]*ipamv1.LocationTreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &ipamv1.LocationTreeNode{
			Location: locationToPB(n.Location),
			Children: locationTreeToPB(n.Children),
		})
	}
	return out
}

// ---- groups ----

func ipGroupToPB(v store.IPGroup) *ipamv1.IPGroup {
	return &ipamv1.IPGroup{
		Id: v.ID, TenantId: v.TenantID, Name: v.Name, Description: v.Description,
		Status: groupStatusToPB(v.Status), Tags: v.Tags, CreatedBy: v.CreatedBy,
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt), MemberCount: v.MemberCount,
	}
}

func ipGroupFromPB(p *ipamv1.IPGroup) store.IPGroup {
	if p == nil {
		return store.IPGroup{}
	}
	return store.IPGroup{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), Description: p.GetDescription(),
		Status: groupStatusFromPB(p.GetStatus()), Tags: p.GetTags(),
	}
}

func ipGroupMemberToPB(v store.IPGroupMember) *ipamv1.IPGroupMember {
	return &ipamv1.IPGroupMember{
		Id: v.ID, TenantId: v.TenantID, IpGroupId: v.IPGroupID, MemberType: memberTypeToPB(v.MemberType),
		Value: v.Value, Description: v.Description, Sequence: int32(v.Sequence),
	}
}

func ipGroupMemberFromPB(p *ipamv1.IPGroupMember) store.IPGroupMember {
	if p == nil {
		return store.IPGroupMember{}
	}
	return store.IPGroupMember{
		ID: p.GetId(), TenantID: p.GetTenantId(), IPGroupID: p.GetIpGroupId(),
		MemberType: memberTypeFromPB(p.GetMemberType()), Value: p.GetValue(),
		Description: p.GetDescription(), Sequence: int(p.GetSequence()),
	}
}

func hostGroupToPB(v store.HostGroup) *ipamv1.HostGroup {
	return &ipamv1.HostGroup{
		Id: v.ID, TenantId: v.TenantID, Name: v.Name, Description: v.Description,
		Status: groupStatusToPB(v.Status), Tags: v.Tags, CreatedBy: v.CreatedBy,
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt), MemberCount: v.MemberCount,
	}
}

func hostGroupFromPB(p *ipamv1.HostGroup) store.HostGroup {
	if p == nil {
		return store.HostGroup{}
	}
	return store.HostGroup{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), Description: p.GetDescription(),
		Status: groupStatusFromPB(p.GetStatus()), Tags: p.GetTags(),
	}
}

func hostGroupMemberToPB(v store.HostGroupMember) *ipamv1.HostGroupMember {
	return &ipamv1.HostGroupMember{
		Id: v.ID, TenantId: v.TenantID, HostGroupId: v.HostGroupID, DeviceId: v.DeviceID,
		Sequence: int32(v.Sequence), DeviceName: v.DeviceName, DeviceType: deviceTypeToPB(v.DeviceType),
		DeviceStatus: deviceStatusToPB(v.DeviceStatus), DevicePrimaryIp: v.DevicePrimaryIP,
	}
}

// ---- scan ----

func scanJobToPB(v store.IPScanJob) *ipamv1.IPScanJob {
	return &ipamv1.IPScanJob{
		Id: v.ID, TenantId: v.TenantID, SubnetId: v.SubnetID, Status: scanStatusToPB(v.Status),
		Progress: int32(v.Progress), StatusMessage: v.StatusMessage, TotalAddresses: v.TotalAddresses,
		ScannedCount: v.ScannedCount, AliveCount: v.AliveCount, NewCount: v.NewCount, UpdatedCount: v.UpdatedCount,
		SnmpDiscoveredCount: v.SNMPDiscoveredCount, TriggeredBy: scanTriggerToPB(v.TriggeredBy),
		RetryCount: int32(v.RetryCount), MaxRetries: int32(v.MaxRetries), NextRetryAt: unixPtr(v.NextRetryAt),
		TimeoutMs: int32(v.TimeoutMs), Concurrency: int32(v.Concurrency), SkipReverseDns: v.SkipReverseDNS,
		TcpProbePorts: v.TCPProbePorts, EnableSnmp: v.EnableSNMP, EnableDnsUpdate: v.EnableDNSUpdate,
		StartedAt: unixPtr(v.StartedAt), CompletedAt: unixPtr(v.CompletedAt), CreatedBy: v.CreatedBy,
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
	}
}

// ---- dns ----

func dnsConfigToPB(v store.DNSConfig) *ipamv1.DNSConfig {
	return &ipamv1.DNSConfig{
		Id: v.ID, TenantId: v.TenantID, DnsServers: v.DNSServers, TimeoutMs: int32(v.TimeoutMs),
		UseSystemDnsFallback: v.UseSystemDNSFallback, ReverseDnsEnabled: v.ReverseDNSEnabled,
		CreatedAt: unix(v.CreatedAt), UpdatedAt: unix(v.UpdatedAt),
	}
}

func dnsConfigFromPB(p *ipamv1.DNSConfig) store.DNSConfig {
	if p == nil {
		return store.DNSConfig{}
	}
	return store.DNSConfig{
		ID: p.GetId(), TenantID: p.GetTenantId(), DNSServers: p.GetDnsServers(),
		TimeoutMs: int(p.GetTimeoutMs()), UseSystemDNSFallback: p.GetUseSystemDnsFallback(),
		ReverseDNSEnabled: p.GetReverseDnsEnabled(),
	}
}

// ---- stats ----

func statsToPB(s repo.Stats) *ipamv1.Stats {
	return &ipamv1.Stats{
		SubnetsTotal: s.TotalSubnets, AddressesTotal: s.TotalAddresses, AddressesUsed: s.UsedAddresses,
		AddressesAvailable: s.AvailableAddresses, Utilization: s.OverallUtilization, DevicesTotal: s.TotalDevices,
		VlansTotal: s.TotalVlans, LocationsTotal: s.TotalLocations, DevicesByType: s.DevicesByType,
	}
}
