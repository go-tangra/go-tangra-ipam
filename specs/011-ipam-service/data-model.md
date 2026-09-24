# Phase 1 Data Model: IPAM Service

All tables carry `tenant_id uuid NOT NULL` and per-tenant RLS (the ent
SystemViewer bypass is replaced by RLS + a scoped system subject for the scan
executor). IDs are application-generated strings. Timestamps are `timestamptz`.
`tags`/`metadata` are JSONB. Enums are stored as small text or int. Credentials
are NEVER columns — only warden references. Every unique constraint below is a
conflict-detection guard and MUST be preserved.

## Entities

### ipam_subnets  (RLS)
- `id` (PK), `tenant_id`, `name`, `cidr`, `description`, `gateway`,
  `dns_servers` (text CSV or jsonb), `vlan_id` (FK, null), `parent_id` (self FK, null),
  `location_id` (FK, null), `status` (active|reserved|deprecated|deleted),
  `ip_version` (4|6, default 4), `network_address`, `broadcast_address`, `mask`,
  `prefix_length`, `snmp_secret_ref` (warden id, null; NO snmp passwords stored),
  `snmp_version` (0|2|3), tags/metadata, audit (created_by/updated_by/created_at/updated_at).
- used/available/utilization are COMPUTED (not stored) from allocated count vs total.
- **Unique**: `(tenant_id,name)`. Indexes: `(tenant_id,cidr)`, tenant_id, vlan_id, parent_id, location_id, status.

### ipam_ip_addresses  (RLS)
- `id` (PK), `tenant_id`, `address` (inet-ish text), `subnet_id` (FK, required),
  `hostname`, `mac_address`, `description`, `device_id` (FK, null), `interface_name`,
  `status` (active|reserved|dhcp|deprecated|offline), `address_type`
  (host|gateway|broadcast|network|virtual|anycast), `is_primary` (bool),
  `ptr_record`, `dns_name`, `owner` (sealed/redacted), `last_seen`, `lease_expiry`,
  `has_reverse_dns` (bool), `note`, tags/metadata, audit.
- **Unique**: `(tenant_id,address)` — the duplicate-IP guard (also the race-safe
  allocation guard: allocate = insert, on conflict retry next free).
- Indexes: `(tenant_id,subnet_id)`, tenant_id, subnet_id, device_id, status, hostname, mac_address.

### ipam_devices  (RLS)
- `id` (PK), `tenant_id`, `name`, `device_type` (server|vm|router|switch|firewall|
  load_balancer|access_point|storage|printer|phone|workstation|container|other),
  `description`, `manufacturer`, `model`, `serial_number`, `asset_tag`,
  `location_id` (FK), `rack_id`, `rack_position` (int, null), `device_height_u` (int, default 1),
  `status` (active|planned|staged|decommissioned|offline|failed|available),
  `primary_ip`, `primary_ipv6`, `management_ip`, `os_type`, `os_version`,
  `firmware_version`, `contact` (sealed/redacted), `last_seen`,
  `ipmi_secret_ref` (warden id, null; NO BMC creds stored), `reboot_required` (bool),
  `unattended_upgrades` (bool), tags/metadata, audit.
- interface_count/address_count/package_update_count/security_update_count are COMPUTED.
- **Unique**: `(tenant_id,name)`. Indexes: tenant_id, location_id, status, device_type, serial_number.

### ipam_device_interfaces  (RLS)
- `id` (PK), `tenant_id`, `device_id` (FK, required), `name` (eth0), `mac_address`,
  `interface_type` (ethernet|wifi|virtual), `enabled` (bool, default true),
  `speed_mbps` (int, null), `description`, `if_index` (int, null),
  + best-link flat fields: `remote_device_id`, `remote_interface_id`,
  `remote_port_name`, `link_source` (snmp_fdb|lldp|manual), `link_vlan` (int, null),
  `link_last_seen`.
- **Unique**: `(device_id,name)`. Indexes: device_id, mac_address, `(device_id,if_index)`, remote_device_id.

### ipam_device_interface_links  (RLS, cascade from interface)
- `id` (PK), `tenant_id`, `interface_id` (FK), `remote_device_id`,
  `remote_interface_id`, `remote_port_name`, `link_source`, `link_vlan` (null), `link_last_seen`.
- **Unique**: `(interface_id,remote_device_id,link_source)`.

### ipam_device_packages  (RLS)
- `id` (PK), `tenant_id`, `device_id` (FK), `name`, `current_version`,
  `available_version`, `needs_update` (bool), `is_security_update` (bool),
  `package_manager`, `description`, create/update time.
- **Unique**: `(tenant_id,device_id,name)`. Indexes: device_id, needs_update, is_security_update.

### ipam_vlans  (RLS)
- `id` (PK), `tenant_id`, `vlan_id` (int 1-4094), `name`, `description`, `domain`,
  `location_id` (FK), `status` (active|reserved|deprecated), tags/metadata, audit.
- subnet_count COMPUTED.
- **Unique**: `(tenant_id,vlan_id)` AND `(tenant_id,name)`. Indexes: tenant_id, location_id, status.

### ipam_locations  (RLS, self-referential tree)
- `id` (PK), `tenant_id`, `name`, `code`, `location_type` (region|country|city|
  datacenter|building|floor|room|rack|site|branch), `description`, `parent_id` (self FK),
  `path`, `address`, `city`, `state`, `country`, `postal_code`, `latitude` (null),
  `longitude` (null), `contact`/`phone`/`email` (sealed/redacted),
  `status` (active|planned|decommissioned), `rack_size_u` (int, null; rack only),
  tags/metadata, audit.
- child/device/subnet/vlan_count COMPUTED.
- **Unique**: `(tenant_id,name)` AND `(tenant_id,code)`. Indexes: tenant_id, parent_id, status, location_type, country.

### ipam_ip_groups + ipam_ip_group_members  (RLS)
- Group: `id, tenant_id, name, description, status (active|inactive), tags/metadata, audit`. **Unique** `(tenant_id,name)`.
- Member: `id, tenant_id, ip_group_id (FK), member_type (address|range|subnet), value, description, sequence (int)`. **Unique** `(ip_group_id,value)`.

### ipam_host_groups + ipam_host_group_members  (RLS)
- Group: `id, tenant_id, name, description, status (active|inactive), tags/metadata, audit`. **Unique** `(tenant_id,name)`.
- Member: `id, tenant_id, host_group_id (FK), device_id (FK), sequence`. **Unique** `(host_group_id,device_id)`. Responses enrich with device name/type/status/primary_ip.

### ipam_ip_scan_jobs  (RLS, work queue via FOR UPDATE SKIP LOCKED)
- `id, tenant_id, subnet_id (FK), status (pending|scanning|completed|failed|cancelled),
  progress (int 0-100), status_message, total_addresses, scanned_count, alive_count,
  new_count, updated_count, snmp_discovered_count, triggered_by (auto|manual),
  retry_count, max_retries (default 3), next_retry_at, timeout_ms (default 1000),
  concurrency (default 50), skip_reverse_dns (bool), tcp_probe_ports,
  enable_snmp (bool), enable_dns_update (bool), started_at, completed_at`, audit.
- Indexes: `(tenant_id,subnet_id)`, `(tenant_id,status)`, `(status,next_retry_at)`, tenant_id, subnet_id, status.

### ipam_dns_configs  (RLS, one per tenant)
- `id, tenant_id, dns_servers (jsonb []string), timeout_ms (default 5000),
  use_system_dns_fallback (bool), reverse_dns_enabled (bool)`, audit.
- **Unique**: `tenant_id`.

### ipam_audit_events  (append-only, hypertable)
- `id, tenant_id, at, actor_kind (user|service|system), actor_id, action,
  subject_kind (subnet|address|device|interface|vlan|location|group|scan|power|kvm|dns|backup|system),
  subject_id, target (host/ip being acted on, for active ops), outcome (ok|refused|error),
  reason, detail (jsonb, redacted)`.

## Enums / vocabularies
SubnetStatus, IpAddressStatus, IpAddressType, DeviceType, DeviceStatus, VlanStatus,
LocationType, LocationStatus, IpGroupMemberType(address|range|subnet), Group status
(active|inactive), IpScanJobStatus(pending|scanning|completed|failed|cancelled),
IpScanJobTrigger(auto|manual), link_source(snmp_fdb|lldp|manual), PowerAction
(on|off|cycle|reset|soft|diag), snmp_version(0|2|3).

**Permissions (API)**: ipam:read, subnets:manage, addresses:manage, addresses:allocate,
devices:manage, vlans:manage, locations:manage, groups:manage, scan:run, dns:manage,
backup:manage, power:control (platform-admin), kvm:access (platform-admin).

**AuditAction**: subnet_/address_/device_/vlan_/location_/group_ created/updated/deleted,
address_allocated, scan_started/cancelled/completed, power_action, kvm_session_started,
dns_config_updated, backup_exported/imported, access_refused.

## Relationships
Subnet→addresses (1-N); subnet self-tree (parent); subnet→vlan, subnet→location.
Device→interfaces→links; device→addresses; device→packages; device→location; device
holds ipmi_secret_ref (→warden). VLAN→subnets; Location tree→subnets/devices/vlans.
Groups→members. ScanJob→subnet. Secret references point at warden, never inline.

## Redaction / sealing
snmp_secret_ref/ipmi_secret_ref are warden ids (never the secret). owner/contact/
phone/email are sealed/redacted. No credential ever appears in a response, log,
audit detail or backup; backups exclude secret references' values (refs may be
exported as opaque ids only).
