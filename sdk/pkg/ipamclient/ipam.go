package ipamclient

import (
	"time"

	ipamv1 "github.com/go-tangra/go-tangra-ipam/sdk/v4/api/proto/ipam/v1"
)

// ---- plain Go value types (no internal/* imports) ----

// Subnet is a CIDR network in a tenant. Statuses are lowercase strings
// ("active", "reserved", "deprecated", "deleted"). Credentials are never
// present; snmp_secret_ref is an opaque warden id.
type Subnet struct {
	ID                 string
	TenantID           string
	Name               string
	CIDR               string
	Description        string
	Gateway            string
	DNSServers         string
	VlanID             string
	ParentID           string
	LocationID         string
	Status             string
	IPVersion          int
	NetworkAddress     string
	BroadcastAddress   string
	Mask               string
	PrefixLength       int
	SNMPSecretRef      string
	SNMPVersion        int
	Tags               map[string]string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	TotalAddresses     int64
	UsedAddresses      int64
	AvailableAddresses int64
	Utilization        float64
}

// SubnetFilter constrains ListSubnets. Empty fields match all.
type SubnetFilter struct {
	VlanID     string
	ParentID   string
	LocationID string
	Status     string
	IPVersion  int
	Query      string
	Limit      int
	CursorID   string
}

// IPAddress is an address within a subnet. The sealed owner field is never
// present. Status is one of "active","reserved","dhcp","deprecated","offline";
// AddressType is one of "host","gateway","broadcast","network","virtual","anycast".
type IPAddress struct {
	ID            string
	TenantID      string
	Address       string
	SubnetID      string
	Hostname      string
	MACAddress    string
	Description   string
	DeviceID      string
	InterfaceName string
	Status        string
	AddressType   string
	IsPrimary     bool
	PTRRecord     string
	DNSName       string
	LastSeen      time.Time
	LeaseExpiry   time.Time
	HasReverseDNS bool
	Note          string
	Tags          map[string]string
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AllocateRequest asks for the next free host in a subnet, optionally stamping
// a hostname/device/description onto the allocated address.
type AllocateRequest struct {
	SubnetID    string
	Hostname    string
	DeviceID    string
	Description string
}

// Device is a managed network device/host. Sealed contact fields are never
// present; ipmi_secret_ref is an opaque warden id, never the secret.
type Device struct {
	ID                  string
	TenantID            string
	Name                string
	DeviceType          string
	Description         string
	Manufacturer        string
	Model               string
	SerialNumber        string
	AssetTag            string
	LocationID          string
	RackID              string
	RackPosition        int
	DeviceHeightU       int
	Status              string
	PrimaryIP           string
	PrimaryIPv6         string
	ManagementIP        string
	OSType              string
	OSVersion           string
	FirmwareVersion     string
	LastSeen            time.Time
	IPMISecretRef       string
	RebootRequired      bool
	UnattendedUpgrades  bool
	Tags                map[string]string
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	InterfaceCount      int64
	AddressCount        int64
	PackageUpdateCount  int64
	SecurityUpdateCount int64
}

// IPGroup is a named collection of address/range/subnet members.
type IPGroup struct {
	ID          string
	TenantID    string
	Name        string
	Description string
	Status      string
	Tags        map[string]string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MemberCount int64
}

// ScanOptions are the per-scan toggles chosen at StartScan time.
type ScanOptions struct {
	SubnetID        string
	EnableSNMP      bool
	EnableDNSUpdate bool
	SkipReverseDNS  bool
	TCPProbePorts   string
	Concurrency     int
	TimeoutMs       int
}

// ScanJob is an async discovery job.
type ScanJob struct {
	ID             string
	TenantID       string
	SubnetID       string
	Status         string
	Progress       int
	StatusMessage  string
	TotalAddresses int64
	ScannedCount   int64
	AliveCount     int64
	NewCount       int64
	UpdatedCount   int64
	TriggeredBy    string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Statistics is a tenant's IPAM rollup.
type Statistics struct {
	SubnetsTotal       int64
	AddressesTotal     int64
	AddressesUsed      int64
	AddressesAvailable int64
	Utilization        float64
	DevicesTotal       int64
	VlansTotal         int64
	LocationsTotal     int64
	IPGroupsTotal      int64
	HostGroupsTotal    int64
	ScansRunning       int64
	DevicesByStatus    map[string]int64
	DevicesByType      map[string]int64
	AddressesByStatus  map[string]int64
	SubnetsByStatus    map[string]int64
}

// ---- decode helpers ----

func unixTime(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

func toSubnet(p *ipamv1.Subnet) Subnet {
	if p == nil {
		return Subnet{}
	}
	return Subnet{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), CIDR: p.GetCidr(),
		Description: p.GetDescription(), Gateway: p.GetGateway(), DNSServers: p.GetDnsServers(),
		VlanID: p.GetVlanId(), ParentID: p.GetParentId(), LocationID: p.GetLocationId(),
		Status: subnetStatusString(p.GetStatus()), IPVersion: int(p.GetIpVersion()),
		NetworkAddress: p.GetNetworkAddress(), BroadcastAddress: p.GetBroadcastAddress(), Mask: p.GetMask(),
		PrefixLength: int(p.GetPrefixLength()), SNMPSecretRef: p.GetSnmpSecretRef(), SNMPVersion: int(p.GetSnmpVersion()),
		Tags: p.GetTags(), CreatedBy: p.GetCreatedBy(), CreatedAt: unixTime(p.GetCreatedAt()),
		UpdatedAt: unixTime(p.GetUpdatedAt()), TotalAddresses: p.GetTotalAddresses(),
		UsedAddresses: p.GetUsedAddresses(), AvailableAddresses: p.GetAvailableAddresses(), Utilization: p.GetUtilization(),
	}
}

func toAddress(p *ipamv1.IPAddress) IPAddress {
	if p == nil {
		return IPAddress{}
	}
	return IPAddress{
		ID: p.GetId(), TenantID: p.GetTenantId(), Address: p.GetAddress(), SubnetID: p.GetSubnetId(),
		Hostname: p.GetHostname(), MACAddress: p.GetMacAddress(), Description: p.GetDescription(),
		DeviceID: p.GetDeviceId(), InterfaceName: p.GetInterfaceName(), Status: ipStatusString(p.GetStatus()),
		AddressType: addressTypeString(p.GetAddressType()), IsPrimary: p.GetIsPrimary(), PTRRecord: p.GetPtrRecord(),
		DNSName: p.GetDnsName(), LastSeen: unixTime(p.GetLastSeen()), LeaseExpiry: unixTime(p.GetLeaseExpiry()),
		HasReverseDNS: p.GetHasReverseDns(), Note: p.GetNote(), Tags: p.GetTags(), CreatedBy: p.GetCreatedBy(),
		CreatedAt: unixTime(p.GetCreatedAt()), UpdatedAt: unixTime(p.GetUpdatedAt()),
	}
}

func toDevice(p *ipamv1.Device) Device {
	if p == nil {
		return Device{}
	}
	return Device{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), DeviceType: deviceTypeString(p.GetDeviceType()),
		Description: p.GetDescription(), Manufacturer: p.GetManufacturer(), Model: p.GetModel(),
		SerialNumber: p.GetSerialNumber(), AssetTag: p.GetAssetTag(), LocationID: p.GetLocationId(),
		RackID: p.GetRackId(), RackPosition: int(p.GetRackPosition()), DeviceHeightU: int(p.GetDeviceHeightU()),
		Status: deviceStatusString(p.GetStatus()), PrimaryIP: p.GetPrimaryIp(), PrimaryIPv6: p.GetPrimaryIpv6(),
		ManagementIP: p.GetManagementIp(), OSType: p.GetOsType(), OSVersion: p.GetOsVersion(),
		FirmwareVersion: p.GetFirmwareVersion(), LastSeen: unixTime(p.GetLastSeen()), IPMISecretRef: p.GetIpmiSecretRef(),
		RebootRequired: p.GetRebootRequired(), UnattendedUpgrades: p.GetUnattendedUpgrades(), Tags: p.GetTags(),
		CreatedBy: p.GetCreatedBy(), CreatedAt: unixTime(p.GetCreatedAt()), UpdatedAt: unixTime(p.GetUpdatedAt()),
		InterfaceCount: p.GetInterfaceCount(), AddressCount: p.GetAddressCount(),
		PackageUpdateCount: p.GetPackageUpdateCount(), SecurityUpdateCount: p.GetSecurityUpdateCount(),
	}
}

func toIPGroup(p *ipamv1.IPGroup) IPGroup {
	if p == nil {
		return IPGroup{}
	}
	return IPGroup{
		ID: p.GetId(), TenantID: p.GetTenantId(), Name: p.GetName(), Description: p.GetDescription(),
		Status: groupStatusString(p.GetStatus()), Tags: p.GetTags(), CreatedBy: p.GetCreatedBy(),
		CreatedAt: unixTime(p.GetCreatedAt()), UpdatedAt: unixTime(p.GetUpdatedAt()), MemberCount: p.GetMemberCount(),
	}
}

func toScanJob(p *ipamv1.IPScanJob) ScanJob {
	if p == nil {
		return ScanJob{}
	}
	return ScanJob{
		ID: p.GetId(), TenantID: p.GetTenantId(), SubnetID: p.GetSubnetId(), Status: scanStatusString(p.GetStatus()),
		Progress: int(p.GetProgress()), StatusMessage: p.GetStatusMessage(), TotalAddresses: p.GetTotalAddresses(),
		ScannedCount: p.GetScannedCount(), AliveCount: p.GetAliveCount(), NewCount: p.GetNewCount(),
		UpdatedCount: p.GetUpdatedCount(), TriggeredBy: scanTriggerString(p.GetTriggeredBy()), CreatedBy: p.GetCreatedBy(),
		CreatedAt: unixTime(p.GetCreatedAt()), UpdatedAt: unixTime(p.GetUpdatedAt()),
	}
}

// ---- enum <-> string ----

func subnetStatusEnum(s string) ipamv1.SubnetStatus {
	switch s {
	case "active":
		return ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE
	case "reserved":
		return ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED
	case "deprecated":
		return ipamv1.SubnetStatus_SUBNET_STATUS_DEPRECATED
	case "deleted":
		return ipamv1.SubnetStatus_SUBNET_STATUS_DELETED
	}
	return ipamv1.SubnetStatus_SUBNET_STATUS_UNSPECIFIED
}

func subnetStatusString(s ipamv1.SubnetStatus) string {
	switch s {
	case ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE:
		return "active"
	case ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED:
		return "reserved"
	case ipamv1.SubnetStatus_SUBNET_STATUS_DEPRECATED:
		return "deprecated"
	case ipamv1.SubnetStatus_SUBNET_STATUS_DELETED:
		return "deleted"
	}
	return ""
}

func ipStatusEnum(s string) ipamv1.IpStatus {
	switch s {
	case "active":
		return ipamv1.IpStatus_IP_STATUS_ACTIVE
	case "reserved":
		return ipamv1.IpStatus_IP_STATUS_RESERVED
	case "dhcp":
		return ipamv1.IpStatus_IP_STATUS_DHCP
	case "deprecated":
		return ipamv1.IpStatus_IP_STATUS_DEPRECATED
	case "offline":
		return ipamv1.IpStatus_IP_STATUS_OFFLINE
	}
	return ipamv1.IpStatus_IP_STATUS_UNSPECIFIED
}

func ipStatusString(s ipamv1.IpStatus) string {
	switch s {
	case ipamv1.IpStatus_IP_STATUS_ACTIVE:
		return "active"
	case ipamv1.IpStatus_IP_STATUS_RESERVED:
		return "reserved"
	case ipamv1.IpStatus_IP_STATUS_DHCP:
		return "dhcp"
	case ipamv1.IpStatus_IP_STATUS_DEPRECATED:
		return "deprecated"
	case ipamv1.IpStatus_IP_STATUS_OFFLINE:
		return "offline"
	}
	return ""
}

func addressTypeString(s ipamv1.AddressType) string {
	switch s {
	case ipamv1.AddressType_ADDRESS_TYPE_HOST:
		return "host"
	case ipamv1.AddressType_ADDRESS_TYPE_GATEWAY:
		return "gateway"
	case ipamv1.AddressType_ADDRESS_TYPE_BROADCAST:
		return "broadcast"
	case ipamv1.AddressType_ADDRESS_TYPE_NETWORK:
		return "network"
	case ipamv1.AddressType_ADDRESS_TYPE_VIRTUAL:
		return "virtual"
	case ipamv1.AddressType_ADDRESS_TYPE_ANYCAST:
		return "anycast"
	}
	return ""
}

func deviceTypeString(s ipamv1.DeviceType) string {
	switch s {
	case ipamv1.DeviceType_DEVICE_TYPE_SERVER:
		return "server"
	case ipamv1.DeviceType_DEVICE_TYPE_VM:
		return "vm"
	case ipamv1.DeviceType_DEVICE_TYPE_ROUTER:
		return "router"
	case ipamv1.DeviceType_DEVICE_TYPE_SWITCH:
		return "switch"
	case ipamv1.DeviceType_DEVICE_TYPE_FIREWALL:
		return "firewall"
	case ipamv1.DeviceType_DEVICE_TYPE_LOAD_BALANCER:
		return "load_balancer"
	case ipamv1.DeviceType_DEVICE_TYPE_ACCESS_POINT:
		return "access_point"
	case ipamv1.DeviceType_DEVICE_TYPE_STORAGE:
		return "storage"
	case ipamv1.DeviceType_DEVICE_TYPE_PRINTER:
		return "printer"
	case ipamv1.DeviceType_DEVICE_TYPE_PHONE:
		return "phone"
	case ipamv1.DeviceType_DEVICE_TYPE_WORKSTATION:
		return "workstation"
	case ipamv1.DeviceType_DEVICE_TYPE_CONTAINER:
		return "container"
	case ipamv1.DeviceType_DEVICE_TYPE_OTHER:
		return "other"
	}
	return ""
}

func deviceTypeEnum(s string) ipamv1.DeviceType {
	switch s {
	case "server":
		return ipamv1.DeviceType_DEVICE_TYPE_SERVER
	case "vm":
		return ipamv1.DeviceType_DEVICE_TYPE_VM
	case "router":
		return ipamv1.DeviceType_DEVICE_TYPE_ROUTER
	case "switch":
		return ipamv1.DeviceType_DEVICE_TYPE_SWITCH
	case "firewall":
		return ipamv1.DeviceType_DEVICE_TYPE_FIREWALL
	case "load_balancer":
		return ipamv1.DeviceType_DEVICE_TYPE_LOAD_BALANCER
	case "access_point":
		return ipamv1.DeviceType_DEVICE_TYPE_ACCESS_POINT
	case "storage":
		return ipamv1.DeviceType_DEVICE_TYPE_STORAGE
	case "printer":
		return ipamv1.DeviceType_DEVICE_TYPE_PRINTER
	case "phone":
		return ipamv1.DeviceType_DEVICE_TYPE_PHONE
	case "workstation":
		return ipamv1.DeviceType_DEVICE_TYPE_WORKSTATION
	case "container":
		return ipamv1.DeviceType_DEVICE_TYPE_CONTAINER
	case "other":
		return ipamv1.DeviceType_DEVICE_TYPE_OTHER
	}
	return ipamv1.DeviceType_DEVICE_TYPE_UNSPECIFIED
}

func deviceStatusString(s ipamv1.DeviceStatus) string {
	switch s {
	case ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE:
		return "active"
	case ipamv1.DeviceStatus_DEVICE_STATUS_PLANNED:
		return "planned"
	case ipamv1.DeviceStatus_DEVICE_STATUS_STAGED:
		return "staged"
	case ipamv1.DeviceStatus_DEVICE_STATUS_DECOMMISSIONED:
		return "decommissioned"
	case ipamv1.DeviceStatus_DEVICE_STATUS_OFFLINE:
		return "offline"
	case ipamv1.DeviceStatus_DEVICE_STATUS_FAILED:
		return "failed"
	case ipamv1.DeviceStatus_DEVICE_STATUS_AVAILABLE:
		return "available"
	}
	return ""
}

func deviceStatusEnum(s string) ipamv1.DeviceStatus {
	switch s {
	case "active":
		return ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE
	case "planned":
		return ipamv1.DeviceStatus_DEVICE_STATUS_PLANNED
	case "staged":
		return ipamv1.DeviceStatus_DEVICE_STATUS_STAGED
	case "decommissioned":
		return ipamv1.DeviceStatus_DEVICE_STATUS_DECOMMISSIONED
	case "offline":
		return ipamv1.DeviceStatus_DEVICE_STATUS_OFFLINE
	case "failed":
		return ipamv1.DeviceStatus_DEVICE_STATUS_FAILED
	case "available":
		return ipamv1.DeviceStatus_DEVICE_STATUS_AVAILABLE
	}
	return ipamv1.DeviceStatus_DEVICE_STATUS_UNSPECIFIED
}

func groupStatusString(s ipamv1.GroupStatus) string {
	switch s {
	case ipamv1.GroupStatus_GROUP_STATUS_ACTIVE:
		return "active"
	case ipamv1.GroupStatus_GROUP_STATUS_INACTIVE:
		return "inactive"
	}
	return ""
}

func scanStatusString(s ipamv1.ScanStatus) string {
	switch s {
	case ipamv1.ScanStatus_SCAN_STATUS_PENDING:
		return "pending"
	case ipamv1.ScanStatus_SCAN_STATUS_SCANNING:
		return "scanning"
	case ipamv1.ScanStatus_SCAN_STATUS_COMPLETED:
		return "completed"
	case ipamv1.ScanStatus_SCAN_STATUS_FAILED:
		return "failed"
	case ipamv1.ScanStatus_SCAN_STATUS_CANCELLED:
		return "cancelled"
	}
	return ""
}

func scanTriggerString(s ipamv1.ScanTrigger) string {
	switch s {
	case ipamv1.ScanTrigger_SCAN_TRIGGER_AUTO:
		return "auto"
	case ipamv1.ScanTrigger_SCAN_TRIGGER_MANUAL:
		return "manual"
	}
	return ""
}

// powerActionEnum maps a lowercase verb ("on","off","cycle","reset","soft",
// "diag") to the proto PowerAction.
func powerActionEnum(action string) ipamv1.PowerAction {
	switch action {
	case "on":
		return ipamv1.PowerAction_POWER_ACTION_ON
	case "off":
		return ipamv1.PowerAction_POWER_ACTION_OFF
	case "cycle":
		return ipamv1.PowerAction_POWER_ACTION_CYCLE
	case "reset":
		return ipamv1.PowerAction_POWER_ACTION_RESET
	case "soft":
		return ipamv1.PowerAction_POWER_ACTION_SOFT
	case "diag":
		return ipamv1.PowerAction_POWER_ACTION_DIAG
	}
	return ipamv1.PowerAction_POWER_ACTION_UNSPECIFIED
}
