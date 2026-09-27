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
  // Effective SNMP version (0/absent when none); the legacy warden ref is
  // never returned.
  snmp_version?: number
  // Effective SNMP credential state (feature 021); never a credential value.
  snmp?: SNMPSummary
  tags?: Record<string, string>
  created_by?: string
  created_at?: string
  updated_at?: string
  total_addresses?: number
  used_addresses?: number
  available_addresses?: number
  utilization?: number
  // "host_sync" when the host sync created it for a reported network.
  origin?: 'manual' | 'host_sync'
}

// --- subnet SNMP credentials (feature 021): write-only ---

export type SNMPVersion = 2 | 3
export type SNMPSecurityLevel = 'authNoPriv' | 'authPriv'
export type SNMPState = 'none' | 'own' | 'inherited'

// SNMPSummary is a subnet's effective SNMP state.
export interface SNMPSummary {
  state: SNMPState
  version?: SNMPVersion
  security_level?: SNMPSecurityLevel
  weak?: boolean
  source_subnet_id?: string
  source_name?: string
  source_cidr?: string
}

export interface SubnetSNMPOwn {
  version: SNMPVersion
  security_level?: SNMPSecurityLevel
  auth_protocol?: string
  priv_protocol?: string
  weak: boolean
  updated_by?: string
  updated_at?: string
}

// SubnetSNMPStatus is GET/PUT /subnets/{id}/snmp: status only, never values.
export interface SubnetSNMPStatus {
  own: SubnetSNMPOwn | null
  effective: SNMPSummary
}

// SubnetSNMPInput is the set/replace body; every field of the kind is sent.
export interface SubnetSNMPInput {
  version: SNMPVersion
  community?: string
  user?: string
  security_level?: SNMPSecurityLevel
  auth_protocol?: string
  auth_password?: string
  priv_protocol?: string
  priv_password?: string
}

export type SNMPTestOutcome = 'ok' | 'no_response' | 'auth_failed' | 'unknown_user' | 'privacy_failed' | 'no_credentials' | 'credentials_unreadable' | 'error'

// SNMPTestResult is POST /subnets/{id}/snmp/test.
export interface SNMPTestResult {
  outcome: SNMPTestOutcome
  sys_name?: string
  sys_descr?: string
  detail?: string // scrubbed reason when outcome is error
  source_subnet_id?: string
  duration_ms: number
}

// SubnetTreeNode is a subnet enriched with nested children for /subnets/tree.
export interface SubnetTreeNode extends Subnet {
  children?: SubnetTreeNode[]
}

// SplitResult is the /subnets/{id}/split response.
export interface SplitResult {
  parent: Subnet
  created: Subnet[]
  skipped: { cidr: string; reason: string }[] | null
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
  // Host sync (server-owned).
  report_state?: ReportState
  previous_device_id?: string
  moved_at?: string
  conflict?: boolean
  // MAC provenance (feature 022, server-owned): agent (host sync), manual
  // (entered by a user) or arp (a router's ARP table, reporting device and
  // last seen); mac_conflict is an ARP MAC disagreeing with an agent/manual
  // one; origin "arp" marks an address created from ARP data.
  mac_source?: MACSource
  mac_source_device_id?: string
  mac_seen_at?: string
  mac_conflict?: string
  origin?: '' | 'arp'
  // Switch port the address is connected to (inferred, server-owned).
  link?: AddressLink
  // Per-switch links, primary first (a host bonded across a switch pair has
  // one per switch; link is the primary).
  links?: HostSwitchLink[]
}

// HostSwitchLink is one per-switch link of a host interface or an address.
export interface HostSwitchLink extends AddressLink {
  primary: boolean
}

export interface AddressLink {
  switch_id: string
  switch_name?: string
  port_id: string
  port_name?: string
  vlan?: number
  source: 'snmp_fdb' | 'lldp'
  last_seen?: string
}

// BehindAddress is an address linked to a switch port.
export interface BehindAddress {
  address_id: string
  address: string
  hostname?: string
}

export type MACSource = '' | 'manual' | 'agent' | 'arp'

// PingResult is the /ip-addresses/{id}/ping response.
export interface PingResult {
  address?: string
  alive: boolean
  rtt_ms?: number
  // false when the server has no ICMP prober (alive is then meaningless).
  available?: boolean
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
  // Host sync (server-owned).
  source?: DeviceSource
  inventory_host_id?: string
  virtualization_kind?: string
  hypervisor_device_id?: string
  update_status?: UpdateStatus
  report_state?: ReportState
  last_report_at?: string
  guest_count?: number
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
  report_state?: ReportState
  // Computed: the linked switch's name and, on a switch port, the host behind it.
  remote_device_name?: string
  behind_device_id?: string
  behind_device_name?: string
  // Addresses linked to this switch port (feature 022).
  behind_addresses?: BehindAddress[]
  // Per-switch links of a host interface, primary first.
  links?: HostSwitchLink[]
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

// PowerStatus is the GET /devices/{id}/power response (BMC chassis status).
export interface PowerStatus {
  on: boolean
  power_restore_policy?: string
  power_fault?: boolean
  power_overload?: boolean
  intrusion?: boolean
  cooling_fault?: boolean
  drive_fault?: boolean
  identify_active?: boolean
}

// Sensor is one entry of the GET /devices/{id}/sensors items.
export interface Sensor {
  number?: number
  name: string
  type?: string
  reading?: string
  value?: number
  unit?: string
  status?: string
  valid?: boolean
}

// SelEntry is one entry of the GET /devices/{id}/sel items.
export interface SelEntry {
  record_id: number
  record_type?: string
  timestamp?: string
  sensor_type?: string
  sensor_name?: string
  description: string
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
  // SNMP phase (feature 021): why SNMP ran or not, and its counters.
  snmp_status?: '' | 'not_requested' | 'no_live_hosts' | 'no_credentials' | 'credentials_unreadable' | 'ran'
  snmp_source_subnet_id?: string
  snmp_probed?: number
  snmp_no_answer?: number
  snmp_rejected?: number
  // ARP phase (feature 022).
  arp_status?: '' | 'ran' | 'disabled' | 'failed'
  arp_devices?: number
  arp_partial?: number
  arp_entries?: number
  arp_applied?: number
  arp_created?: number
  arp_conflicts?: number
  arp_ignored?: Record<string, number>
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

// --- statistics ---

// Stats mirrors the GET /stats tenant rollup.
export interface Stats {
  total_subnets: number
  total_addresses: number
  used_addresses: number
  available_addresses: number
  total_vlans: number
  total_devices: number
  total_locations: number
  overall_utilization: number
  devices_by_type?: Record<string, number> | null
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

// --- host sync (feature 020) ---

export type DeviceSource = 'manual' | 'scan' | 'host_report'
export type ReportState = '' | 'reported' | 'not_reported'
export type UpdateStatus = 'unknown' | 'up_to_date' | 'updates_available' | 'unsupported' | 'error'
export type HostSyncState = 'ok' | 'degraded' | 'disabled'

export interface HostSyncSettings {
  enabled: boolean
  full_interval_minutes: number
  excluded_interfaces: string[]
  updated_by?: string
  updated_at?: string
}

export interface HostSyncStatus {
  enabled: boolean
  state: HostSyncState
  last_error?: string
  last_poll_at?: string
  last_reconcile_at?: string
  next_reconcile_at?: string
  hosts_reported: number
  hosts_failed: number
  devices_not_reported: number
  addresses_in_conflict: number
}

export interface HostSyncIssue {
  field: string
  reason: string
  count: number
}

export interface DeviceHostSync {
  source: DeviceSource
  inventory_host_id?: string
  report_state: ReportState
  snapshot_id?: string
  collected_at?: string
  applied_at?: string
  trigger?: string
  changes: number
  issues: HostSyncIssue[]
}

export interface ResyncResult {
  applied: boolean
  changes: number
  issues: HostSyncIssue[]
}

export interface HypervisorGuest {
  id?: string
  guest_ref: string
  name?: string
  kind: 'vm' | 'container'
  platform?: string
  macs: string[]
  guest_device_id?: string
  guest_device_name?: string
  last_reported_at?: string
}

// --- ARP settings (feature 022) ---

// ARPSettings is GET/PUT /arp/settings: whether scans with SNMP read the ARP
// tables, devices never used as ARP sources, and the proxy-ARP threshold.
export interface ARPSettings {
  enabled: boolean
  excluded_devices: string[]
  proxy_threshold: number
  updated_by?: string
  updated_at?: string
}
