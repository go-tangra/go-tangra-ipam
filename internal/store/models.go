// Package store holds the IPAM domain types and the SQL-backed store.
package store

import "time"

// --- enums (stored as text) ---

const (
	SubnetActive, SubnetReserved, SubnetDeprecated, SubnetDeleted = "active", "reserved", "deprecated", "deleted"

	IPActive, IPReserved, IPDHCP, IPDeprecated, IPOffline = "active", "reserved", "dhcp", "deprecated", "offline"

	AddrHost, AddrGateway, AddrBroadcast, AddrNetwork, AddrVirtual, AddrAnycast = "host", "gateway", "broadcast", "network", "virtual", "anycast"

	DevServer, DevVM, DevRouter, DevSwitch, DevFirewall, DevLoadBalancer, DevAccessPoint, DevStorage, DevPrinter, DevPhone, DevWorkstation, DevContainer, DevOther = "server", "vm", "router", "switch", "firewall", "load_balancer", "access_point", "storage", "printer", "phone", "workstation", "container", "other"

	DevStActive, DevStPlanned, DevStStaged, DevStDecommissioned, DevStOffline, DevStFailed, DevStAvailable = "active", "planned", "staged", "decommissioned", "offline", "failed", "available"

	VlanActive, VlanReserved, VlanDeprecated = "active", "reserved", "deprecated"

	LocRegion, LocCountry, LocCity, LocDatacenter, LocBuilding, LocFloor, LocRoom, LocRack, LocSite, LocBranch = "region", "country", "city", "datacenter", "building", "floor", "room", "rack", "site", "branch"
	LocStActive, LocStPlanned, LocStDecommissioned                                                             = "active", "planned", "decommissioned"

	MemberAddress, MemberRange, MemberSubnet = "address", "range", "subnet"
	GroupActive, GroupInactive               = "active", "inactive"

	ScanPending, ScanScanning, ScanCompleted, ScanFailed, ScanCancelled = "pending", "scanning", "completed", "failed", "cancelled"
	TriggerAuto, TriggerManual                                          = "auto", "manual"

	LinkSNMPFDB, LinkLLDP, LinkManual = "snmp_fdb", "lldp", "manual"

	PowerOn, PowerOff, PowerCycle, PowerReset, PowerSoft, PowerDiag = "on", "off", "cycle", "reset", "soft", "diag"
)

// Subnet is a CIDR network in a tenant (hierarchical).
type Subnet struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	Name           string            `json:"name"`
	CIDR           string            `json:"cidr"`
	Description    string            `json:"description,omitempty"`
	Gateway        string            `json:"gateway,omitempty"`
	DNSServers     string            `json:"dns_servers,omitempty"`
	VlanID         string            `json:"vlan_id,omitempty"`
	ParentID       string            `json:"parent_id,omitempty"`
	LocationID     string            `json:"location_id,omitempty"`
	Status         string            `json:"status"`
	IPVersion      int               `json:"ip_version"`
	NetworkAddress string            `json:"network_address,omitempty"`
	BroadcastAddr  string            `json:"broadcast_address,omitempty"`
	Mask           string            `json:"mask,omitempty"`
	PrefixLength   int               `json:"prefix_length,omitempty"`
	SNMPSecretRef  string            `json:"snmp_secret_ref,omitempty"` // warden id; never the secret
	SNMPVersion    int               `json:"snmp_version,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	CreatedBy      string            `json:"created_by,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	// Computed (not stored):
	TotalAddresses     int64   `json:"total_addresses"`
	UsedAddresses      int64   `json:"used_addresses"`
	AvailableAddresses int64   `json:"available_addresses"`
	Utilization        float64 `json:"utilization"`
}

// IPAddress is an address within a subnet. Unique (tenant_id,address).
type IPAddress struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	Address       string            `json:"address"`
	SubnetID      string            `json:"subnet_id"`
	Hostname      string            `json:"hostname,omitempty"`
	MACAddress    string            `json:"mac_address,omitempty"`
	Description   string            `json:"description,omitempty"`
	DeviceID      string            `json:"device_id,omitempty"`
	InterfaceName string            `json:"interface_name,omitempty"`
	Status        string            `json:"status"`
	AddressType   string            `json:"address_type"`
	IsPrimary     bool              `json:"is_primary,omitempty"`
	PTRRecord     string            `json:"ptr_record,omitempty"`
	DNSName       string            `json:"dns_name,omitempty"`
	Owner         string            `json:"owner,omitempty"` // sealed/redacted
	LastSeen      *time.Time        `json:"last_seen,omitempty"`
	LeaseExpiry   *time.Time        `json:"lease_expiry,omitempty"`
	HasReverseDNS bool              `json:"has_reverse_dns,omitempty"`
	Note          string            `json:"note,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
	CreatedBy     string            `json:"created_by,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// Device is a managed network device/host. Unique (tenant_id,name).
type Device struct {
	ID                 string            `json:"id"`
	TenantID           string            `json:"tenant_id"`
	Name               string            `json:"name"`
	DeviceType         string            `json:"device_type"`
	Description        string            `json:"description,omitempty"`
	Manufacturer       string            `json:"manufacturer,omitempty"`
	Model              string            `json:"model,omitempty"`
	SerialNumber       string            `json:"serial_number,omitempty"`
	AssetTag           string            `json:"asset_tag,omitempty"`
	LocationID         string            `json:"location_id,omitempty"`
	RackID             string            `json:"rack_id,omitempty"`
	RackPosition       int               `json:"rack_position,omitempty"`
	DeviceHeightU      int               `json:"device_height_u,omitempty"`
	Status             string            `json:"status"`
	PrimaryIP          string            `json:"primary_ip,omitempty"`
	PrimaryIPv6        string            `json:"primary_ipv6,omitempty"`
	ManagementIP       string            `json:"management_ip,omitempty"`
	OSType             string            `json:"os_type,omitempty"`
	OSVersion          string            `json:"os_version,omitempty"`
	FirmwareVersion    string            `json:"firmware_version,omitempty"`
	Contact            string            `json:"contact,omitempty"` // sealed/redacted
	LastSeen           *time.Time        `json:"last_seen,omitempty"`
	IPMISecretRef      string            `json:"ipmi_secret_ref,omitempty"` // warden id; never the secret
	RebootRequired     bool              `json:"reboot_required,omitempty"`
	UnattendedUpgrades bool              `json:"unattended_upgrades,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	CreatedBy          string            `json:"created_by,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	// Computed:
	InterfaceCount      int64 `json:"interface_count"`
	AddressCount        int64 `json:"address_count"`
	PackageUpdateCount  int64 `json:"package_update_count"`
	SecurityUpdateCount int64 `json:"security_update_count"`
}

// DeviceInterface is a NIC on a device. Unique (device_id,name).
type DeviceInterface struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenant_id"`
	DeviceID          string     `json:"device_id"`
	Name              string     `json:"name"`
	MACAddress        string     `json:"mac_address,omitempty"`
	InterfaceType     string     `json:"interface_type,omitempty"`
	Enabled           bool       `json:"enabled"`
	SpeedMbps         int        `json:"speed_mbps,omitempty"`
	Description       string     `json:"description,omitempty"`
	IfIndex           int        `json:"if_index,omitempty"`
	RemoteDeviceID    string     `json:"remote_device_id,omitempty"`
	RemoteInterfaceID string     `json:"remote_interface_id,omitempty"`
	RemotePortName    string     `json:"remote_port_name,omitempty"`
	LinkSource        string     `json:"link_source,omitempty"`
	LinkVlan          int        `json:"link_vlan,omitempty"`
	LinkLastSeen      *time.Time `json:"link_last_seen,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// DeviceInterfaceLink is an L2 neighbor link. Unique (interface_id,remote_device_id,link_source).
type DeviceInterfaceLink struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenant_id"`
	InterfaceID       string     `json:"interface_id"`
	RemoteDeviceID    string     `json:"remote_device_id,omitempty"`
	RemoteInterfaceID string     `json:"remote_interface_id,omitempty"`
	RemotePortName    string     `json:"remote_port_name,omitempty"`
	LinkSource        string     `json:"link_source"`
	LinkVlan          int        `json:"link_vlan,omitempty"`
	LinkLastSeen      *time.Time `json:"link_last_seen,omitempty"`
}

// DevicePackage is an OS package on a device. Unique (tenant_id,device_id,name).
type DevicePackage struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	DeviceID         string    `json:"device_id"`
	Name             string    `json:"name"`
	CurrentVersion   string    `json:"current_version,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	NeedsUpdate      bool      `json:"needs_update,omitempty"`
	IsSecurityUpdate bool      `json:"is_security_update,omitempty"`
	PackageManager   string    `json:"package_manager,omitempty"`
	Description      string    `json:"description,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Vlan. Unique (tenant_id,vlan_id) and (tenant_id,name).
type Vlan struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	VlanID      int               `json:"vlan_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Domain      string            `json:"domain,omitempty"`
	LocationID  string            `json:"location_id,omitempty"`
	Status      string            `json:"status"`
	Tags        map[string]string `json:"tags,omitempty"`
	CreatedBy   string            `json:"created_by,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	SubnetCount int64             `json:"subnet_count"`
}

// Location is a node in the physical tree. Unique (tenant_id,name) and (tenant_id,code).
type Location struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	Name         string            `json:"name"`
	Code         string            `json:"code,omitempty"`
	LocationType string            `json:"location_type"`
	Description  string            `json:"description,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	Path         string            `json:"path,omitempty"`
	Address      string            `json:"address,omitempty"`
	City         string            `json:"city,omitempty"`
	State        string            `json:"state,omitempty"`
	Country      string            `json:"country,omitempty"`
	PostalCode   string            `json:"postal_code,omitempty"`
	Latitude     float64           `json:"latitude,omitempty"`
	Longitude    float64           `json:"longitude,omitempty"`
	Contact      string            `json:"contact,omitempty"` // sealed/redacted
	Phone        string            `json:"phone,omitempty"`   // sealed/redacted
	Email        string            `json:"email,omitempty"`   // sealed/redacted
	Status       string            `json:"status"`
	RackSizeU    int               `json:"rack_size_u,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	CreatedBy    string            `json:"created_by,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	ChildCount   int64             `json:"child_count"`
	DeviceCount  int64             `json:"device_count"`
	SubnetCount  int64             `json:"subnet_count"`
	VlanCount    int64             `json:"vlan_count"`
}

// IPGroup + members. HostGroup + members.
type IPGroup struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status"`
	Tags        map[string]string `json:"tags,omitempty"`
	CreatedBy   string            `json:"created_by,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	MemberCount int64             `json:"member_count"`
}

type IPGroupMember struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	IPGroupID   string `json:"ip_group_id"`
	MemberType  string `json:"member_type"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Sequence    int    `json:"sequence,omitempty"`
}

type HostGroup struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status"`
	Tags        map[string]string `json:"tags,omitempty"`
	CreatedBy   string            `json:"created_by,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	MemberCount int64             `json:"member_count"`
}

type HostGroupMember struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	HostGroupID string `json:"host_group_id"`
	DeviceID    string `json:"device_id"`
	Sequence    int    `json:"sequence,omitempty"`
	// enriched:
	DeviceName      string `json:"device_name,omitempty"`
	DeviceType      string `json:"device_type,omitempty"`
	DeviceStatus    string `json:"device_status,omitempty"`
	DevicePrimaryIP string `json:"device_primary_ip,omitempty"`
}

// IPScanJob is an async discovery job (work queue).
type IPScanJob struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenant_id"`
	SubnetID            string     `json:"subnet_id"`
	Status              string     `json:"status"`
	Progress            int        `json:"progress"`
	StatusMessage       string     `json:"status_message,omitempty"`
	TotalAddresses      int64      `json:"total_addresses"`
	ScannedCount        int64      `json:"scanned_count"`
	AliveCount          int64      `json:"alive_count"`
	NewCount            int64      `json:"new_count"`
	UpdatedCount        int64      `json:"updated_count"`
	SNMPDiscoveredCount int64      `json:"snmp_discovered_count"`
	TriggeredBy         string     `json:"triggered_by"`
	RetryCount          int        `json:"retry_count"`
	MaxRetries          int        `json:"max_retries"`
	NextRetryAt         *time.Time `json:"next_retry_at,omitempty"`
	TimeoutMs           int        `json:"timeout_ms"`
	Concurrency         int        `json:"concurrency"`
	SkipReverseDNS      bool       `json:"skip_reverse_dns,omitempty"`
	TCPProbePorts       string     `json:"tcp_probe_ports,omitempty"`
	EnableSNMP          bool       `json:"enable_snmp,omitempty"`
	EnableDNSUpdate     bool       `json:"enable_dns_update,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	CreatedBy           string     `json:"created_by,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// DNSConfig is one per tenant.
type DNSConfig struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	DNSServers           []string  `json:"dns_servers,omitempty"`
	TimeoutMs            int       `json:"timeout_ms"`
	UseSystemDNSFallback bool      `json:"use_system_dns_fallback"`
	ReverseDNSEnabled    bool      `json:"reverse_dns_enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// AuditRow is an append-only audit event.
type AuditRow struct {
	ID          string
	TenantID    string
	At          time.Time
	ActorKind   string
	ActorID     string
	Action      string
	SubjectKind string
	SubjectID   string
	Target      string
	Outcome     string
	Reason      string
	Detail      map[string]any
}

// --- filters ---

type SubnetFilter struct {
	VlanID, ParentID, LocationID, Status string
	IPVersion                            int
	Query                                string
	Limit                                int
	CursorID                             string
}

type AddressFilter struct {
	SubnetID, DeviceID, Status, AddressType, AddressPrefix, HostnamePattern string
	Limit                                                                   int
	CursorID                                                                string
}

type DeviceFilter struct {
	DeviceType, Status, LocationID, Manufacturer, RackID string
	Query                                                string
	Limit                                                int
	CursorID                                             string
}

type VlanFilter struct {
	LocationID, Domain, Status string
	VlanIDMin, VlanIDMax       int
	Limit                      int
	CursorID                   string
}

type LocationFilter struct {
	ParentID, LocationType, Country, Status string
	Limit                                   int
	CursorID                                string
}

type ScanFilter struct {
	SubnetID, Status string
	Limit            int
	CursorID         string
}
