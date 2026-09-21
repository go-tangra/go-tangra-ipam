// Domain types mirror the ipam OpenAPI responses (api/openapi/ipam.yaml) and the
// store models (internal/store/models.go). Credentials and sealed fields
// (SNMP/BMC/IPMI secrets, owner/contact) are never returned in listings and are
// omitted here. Response projections use optional (`?:`) fields; inputs/filters
// use explicit `T | undefined` to satisfy exactOptionalPropertyTypes.

export type SubnetStatus = 'active' | 'reserved' | 'deprecated' | 'deleted'
export type IPStatus = 'active' | 'reserved' | 'dhcp' | 'deprecated' | 'offline'
export type AddressType = 'host' | 'gateway' | 'broadcast' | 'network' | 'virtual' | 'anycast'
export type DeviceType =
  | 'server' | 'vm' | 'router' | 'switch' | 'firewall' | 'load_balancer'
  | 'access_point' | 'storage' | 'printer' | 'phone' | 'workstation' | 'container' | 'other'
export type DeviceStatus = 'active' | 'planned' | 'staged' | 'decommissioned' | 'offline' | 'failed' | 'available'
export type VlanStatus = 'active' | 'reserved' | 'deprecated'
export type LocationType =
  | 'region' | 'country' | 'city' | 'datacenter' | 'building'
  | 'floor' | 'room' | 'rack' | 'site' | 'branch'
export type LocationStatus = 'active' | 'planned' | 'decommissioned'
export type MemberType = 'address' | 'range' | 'subnet'
export type GroupStatus = 'active' | 'inactive'
export type ScanStatus = 'pending' | 'scanning' | 'completed' | 'failed' | 'cancelled'
export type PowerAction = 'on' | 'off' | 'cycle' | 'reset' | 'soft' | 'diag'

// --- subnets ---

export interface Subnet {
  id: string
  tenant_id?: string
  name: string
  cidr: string
  description?: string
  gateway?: string
  dns_servers?: string
  vlan_id?: string
  parent_id?: string
  location_id?: string
  status: SubnetStatus
  ip_version: number
  network_address?: string
  broadcast_address?: string
  mask?: string
  prefix_length?: number
  snmp_secret_ref?: string
  snmp_version?: number
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  total_addresses?: number
  used_addresses?: number
  available_addresses?: number
  utilization?: number
}

// SubnetTreeNode is a subnet enriched with nested children for /subnets/tree.
export interface SubnetTreeNode extends Subnet {
  children?: SubnetTreeNode[]
}

// SubnetStats is the /subnets/{id}/stats response.
export interface SubnetStats {
  subnet_id?: string
  total_addresses?: number
  used_addresses?: number
  available_addresses?: number
  reserved_addresses?: number
  utilization?: number
  by_status?: Record<string, number>
  by_type?: Record<string, number>
}

// --- IP addresses ---

export interface IPAddress {
  id: string
  tenant_id?: string
  address: string
  subnet_id: string
  hostname?: string
  mac_address?: string
  description?: string
  device_id?: string
  interface_name?: string
  status: IPStatus
  address_type: AddressType
  is_primary?: boolean
  ptr_record?: string
  dns_name?: string
  last_seen?: string
  lease_expiry?: string
  has_reverse_dns?: boolean
  note?: string
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
}

// PingResult is the /ip-addresses/{id}/ping response.
export interface PingResult {
  address?: string
  alive: boolean
  rtt_ms?: number
}

// --- devices ---

export interface Device {
  id: string
  tenant_id?: string
  name: string
  device_type: DeviceType
  description?: string
  manufacturer?: string
  model?: string
  serial_number?: string
  asset_tag?: string
  location_id?: string
  rack_id?: string
  rack_position?: number
  device_height_u?: number
  status: DeviceStatus
  primary_ip?: string
  primary_ipv6?: string
  management_ip?: string
  os_type?: string
  os_version?: string
  firmware_version?: string
  last_seen?: string
  ipmi_secret_ref?: string
  reboot_required?: boolean
  unattended_upgrades?: boolean
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  interface_count?: number
  address_count?: number
  package_update_count?: number
  security_update_count?: number
}

export interface DeviceInterface {
  id: string
  tenant_id?: string
  device_id: string
  name: string
  mac_address?: string
  interface_type?: string
  enabled?: boolean
  speed_mbps?: number
  description?: string
  if_index?: number
  remote_device_id?: string
  remote_interface_id?: string
  remote_port_name?: string
  link_source?: string
  link_vlan?: number
  link_last_seen?: string
  created_at?: string
  updated_at?: string
}

export interface DevicePackage {
  id?: string
  tenant_id?: string
  device_id?: string
  name: string
  current_version?: string
  available_version?: string
  needs_update?: boolean
  is_security_update?: boolean
  package_manager?: string
  description?: string
  created_at?: string
  updated_at?: string
}

// --- out-of-band (IPMI / BMC / KVM) ---

// PowerStatus is the GET /devices/{id}/power response.
export interface PowerStatus {
  power_state?: string
  chassis_on?: boolean
  last_power_event?: string
}

export interface Sensor {
  name: string
  reading?: number
  unit?: string
  status?: string
  lower_critical?: number
  upper_critical?: number
}

// Sensors is the GET /devices/{id}/sensors response.
export interface Sensors {
  sensors?: Sensor[]
}

export interface SelEntry {
  id?: string
  timestamp?: string
  sensor?: string
  event?: string
  severity?: string
}

// KvmSession is the POST /devices/{id}/kvm-session response.
export interface KvmSession {
  token: string
  console_url: string
  expires_at?: string
}

// --- VLANs ---

export interface Vlan {
  id: string
  tenant_id?: string
  vlan_id: number
  name: string
  description?: string
  domain?: string
  location_id?: string
  status: VlanStatus
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  subnet_count?: number
}

// --- locations ---

export interface Location {
  id: string
  tenant_id?: string
  name: string
  code?: string
  location_type: LocationType
  description?: string
  parent_id?: string
  path?: string
  address?: string
  city?: string
  state?: string
  country?: string
  postal_code?: string
  latitude?: number
  longitude?: number
  phone?: string
  email?: string
  status: LocationStatus
  rack_size_u?: number
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  child_count?: number
  device_count?: number
  subnet_count?: number
  vlan_count?: number
}

// LocationTreeNode is a location enriched with nested children for /locations/tree.
export interface LocationTreeNode extends Location {
  children?: LocationTreeNode[]
}

// --- groups ---

export interface IPGroup {
  id: string
  tenant_id?: string
  name: string
  description?: string
  status: GroupStatus
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  member_count?: number
}

export interface IPGroupMember {
  id: string
  tenant_id?: string
  ip_group_id?: string
  member_type: MemberType
  value: string
  description?: string
  sequence?: number
}

export interface HostGroup {
  id: string
  tenant_id?: string
  name: string
  description?: string
  status: GroupStatus
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  member_count?: number
}

export interface HostGroupMember {
  id: string
  tenant_id?: string
  host_group_id?: string
  device_id: string
  sequence?: number
  device_name?: string
  device_type?: string
  device_status?: string
  device_primary_ip?: string
}

// GroupMatch is one entry of the /ip-groups/check response.
export interface GroupMatch {
  group_id: string
  name?: string
  member_type?: string
  value?: string
}

// --- scans ---

export interface IPScanJob {
  id: string
  tenant_id?: string
  subnet_id: string
  status: ScanStatus
  progress: number
  status_message?: string
  total_addresses?: number
  scanned_count?: number
  alive_count?: number
  new_count?: number
  updated_count?: number
  snmp_discovered_count?: number
  triggered_by?: string
  retry_count?: number
  max_retries?: number
  next_retry_at?: string
  timeout_ms?: number
  concurrency?: number
  skip_reverse_dns?: boolean
  tcp_probe_ports?: string
  enable_snmp?: boolean
  enable_dns_update?: boolean
  started_at?: string
  completed_at?: string
  created_by?: string
  created_at?: string
  updated_at?: string
}

// ScanResult is the synchronous POST /subnets/{id}/scan response.
export interface ScanResult {
  subnet_id?: string
  total_addresses?: number
  alive_count?: number
  new_count?: number
  updated_count?: number
}

// --- statistics ---

// Stats mirrors the GET /stats tenant rollup.
export interface Stats {
  subnets_total?: number
  addresses_total?: number
  addresses_used?: number
  addresses_available?: number
  utilization?: number
  devices_total?: number
  devices_by_type?: Record<string, number>
  devices_by_status?: Record<string, number>
  vlans_total?: number
  locations_total?: number
  ip_groups_total?: number
  host_groups_total?: number
  scans_active?: number
  scans_completed?: number
  security_updates?: number
}

// --- DNS config ---

export interface DNSConfig {
  id?: string
  tenant_id?: string
  dns_servers?: string[]
  timeout_ms?: number
  use_system_dns_fallback?: boolean
  reverse_dns_enabled?: boolean
  created_at?: string
  updated_at?: string
}
