-- +goose Up
-- Core IPAM tables. Every table carries tenant_id and is RLS-protected (policies
-- + grants applied in 0003_rls.sql). The scan work-queue and the append-only
-- audit hypertable live in 0002_scan_audit.sql. Every UNIQUE constraint below is
-- a conflict-detection guard and MUST be preserved. Credentials are never stored;
-- snmp_secret_ref / ipmi_secret_ref hold warden ids only.

-- Physical location tree (self-referential). Created first: subnets, vlans and
-- devices reference it.
CREATE TABLE ipam_locations (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  name          text NOT NULL DEFAULT '',
  code          text NOT NULL DEFAULT '',
  location_type text NOT NULL DEFAULT '',
  description   text NOT NULL DEFAULT '',
  parent_id     uuid REFERENCES ipam_locations(id) ON DELETE SET NULL,
  path          text NOT NULL DEFAULT '',
  address       text NOT NULL DEFAULT '',
  city          text NOT NULL DEFAULT '',
  state         text NOT NULL DEFAULT '',
  country       text NOT NULL DEFAULT '',
  postal_code   text NOT NULL DEFAULT '',
  latitude      double precision NOT NULL DEFAULT 0,
  longitude     double precision NOT NULL DEFAULT 0,
  contact       text NOT NULL DEFAULT '',
  phone         text NOT NULL DEFAULT '',
  email         text NOT NULL DEFAULT '',
  status        text NOT NULL DEFAULT 'active',
  rack_size_u   int  NOT NULL DEFAULT 0,
  tags          jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by    text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
-- code is unique per tenant when present (empty codes are not forced unique).
CREATE UNIQUE INDEX locations_uniq_code ON ipam_locations (tenant_id, code) WHERE code <> '';
CREATE INDEX locations_tenant   ON ipam_locations (tenant_id);
CREATE INDEX locations_parent   ON ipam_locations (tenant_id, parent_id);
CREATE INDEX locations_status   ON ipam_locations (tenant_id, status);
CREATE INDEX locations_type     ON ipam_locations (tenant_id, location_type);
CREATE INDEX locations_country  ON ipam_locations (tenant_id, country);

CREATE TABLE ipam_vlans (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  vlan_id     int  NOT NULL,
  name        text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  domain      text NOT NULL DEFAULT '',
  location_id uuid REFERENCES ipam_locations(id) ON DELETE SET NULL,
  status      text NOT NULL DEFAULT 'active',
  tags        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, vlan_id),
  UNIQUE (tenant_id, name)
);
CREATE INDEX vlans_tenant   ON ipam_vlans (tenant_id);
CREATE INDEX vlans_location ON ipam_vlans (tenant_id, location_id);
CREATE INDEX vlans_status   ON ipam_vlans (tenant_id, status);

CREATE TABLE ipam_subnets (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  name              text NOT NULL DEFAULT '',
  cidr              text NOT NULL DEFAULT '',
  description       text NOT NULL DEFAULT '',
  gateway           text NOT NULL DEFAULT '',
  dns_servers       text NOT NULL DEFAULT '',
  vlan_id           uuid REFERENCES ipam_vlans(id) ON DELETE SET NULL,
  parent_id         uuid REFERENCES ipam_subnets(id) ON DELETE SET NULL,
  location_id       uuid REFERENCES ipam_locations(id) ON DELETE SET NULL,
  status            text NOT NULL DEFAULT 'active',
  ip_version        int  NOT NULL DEFAULT 4,
  network_address   text NOT NULL DEFAULT '',
  broadcast_address text NOT NULL DEFAULT '',
  mask              text NOT NULL DEFAULT '',
  prefix_length     int  NOT NULL DEFAULT 0,
  snmp_secret_ref   text NOT NULL DEFAULT '',
  snmp_version      int  NOT NULL DEFAULT 0,
  tags              jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by        text NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE INDEX subnets_cidr     ON ipam_subnets (tenant_id, cidr);
CREATE INDEX subnets_tenant   ON ipam_subnets (tenant_id);
CREATE INDEX subnets_vlan     ON ipam_subnets (tenant_id, vlan_id);
CREATE INDEX subnets_parent   ON ipam_subnets (tenant_id, parent_id);
CREATE INDEX subnets_location ON ipam_subnets (tenant_id, location_id);
CREATE INDEX subnets_status   ON ipam_subnets (tenant_id, status);

CREATE TABLE ipam_devices (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL,
  name                text NOT NULL DEFAULT '',
  device_type         text NOT NULL DEFAULT 'other',
  description         text NOT NULL DEFAULT '',
  manufacturer        text NOT NULL DEFAULT '',
  model               text NOT NULL DEFAULT '',
  serial_number       text NOT NULL DEFAULT '',
  asset_tag           text NOT NULL DEFAULT '',
  location_id         uuid REFERENCES ipam_locations(id) ON DELETE SET NULL,
  rack_id             text NOT NULL DEFAULT '',
  rack_position       int  NOT NULL DEFAULT 0,
  device_height_u     int  NOT NULL DEFAULT 1,
  status              text NOT NULL DEFAULT 'active',
  primary_ip          text NOT NULL DEFAULT '',
  primary_ipv6        text NOT NULL DEFAULT '',
  management_ip       text NOT NULL DEFAULT '',
  os_type             text NOT NULL DEFAULT '',
  os_version          text NOT NULL DEFAULT '',
  firmware_version    text NOT NULL DEFAULT '',
  contact             text NOT NULL DEFAULT '',
  last_seen           timestamptz,
  ipmi_secret_ref     text NOT NULL DEFAULT '',
  reboot_required     boolean NOT NULL DEFAULT false,
  unattended_upgrades boolean NOT NULL DEFAULT false,
  tags                jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by          text NOT NULL DEFAULT '',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE INDEX devices_tenant   ON ipam_devices (tenant_id);
CREATE INDEX devices_location ON ipam_devices (tenant_id, location_id);
CREATE INDEX devices_status   ON ipam_devices (tenant_id, status);
CREATE INDEX devices_type     ON ipam_devices (tenant_id, device_type);
CREATE INDEX devices_serial   ON ipam_devices (tenant_id, serial_number);

CREATE TABLE ipam_ip_addresses (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  address         text NOT NULL,
  subnet_id       uuid NOT NULL REFERENCES ipam_subnets(id) ON DELETE CASCADE,
  hostname        text NOT NULL DEFAULT '',
  mac_address     text NOT NULL DEFAULT '',
  description     text NOT NULL DEFAULT '',
  device_id       uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  interface_name  text NOT NULL DEFAULT '',
  status          text NOT NULL DEFAULT 'active',
  address_type    text NOT NULL DEFAULT 'host',
  is_primary      boolean NOT NULL DEFAULT false,
  ptr_record      text NOT NULL DEFAULT '',
  dns_name        text NOT NULL DEFAULT '',
  owner           text NOT NULL DEFAULT '',
  last_seen       timestamptz,
  lease_expiry    timestamptz,
  has_reverse_dns boolean NOT NULL DEFAULT false,
  note            text NOT NULL DEFAULT '',
  tags            jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by      text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  -- the duplicate-IP / race-safe allocation guard:
  UNIQUE (tenant_id, address)
);
CREATE INDEX addresses_subnet   ON ipam_ip_addresses (tenant_id, subnet_id);
CREATE INDEX addresses_tenant   ON ipam_ip_addresses (tenant_id);
CREATE INDEX addresses_device   ON ipam_ip_addresses (tenant_id, device_id);
CREATE INDEX addresses_status   ON ipam_ip_addresses (tenant_id, status);
CREATE INDEX addresses_hostname ON ipam_ip_addresses (tenant_id, hostname);
CREATE INDEX addresses_mac      ON ipam_ip_addresses (tenant_id, mac_address);

CREATE TABLE ipam_device_interfaces (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL,
  device_id           uuid NOT NULL REFERENCES ipam_devices(id) ON DELETE CASCADE,
  name                text NOT NULL DEFAULT '',
  mac_address         text NOT NULL DEFAULT '',
  interface_type      text NOT NULL DEFAULT '',
  enabled             boolean NOT NULL DEFAULT true,
  speed_mbps          int  NOT NULL DEFAULT 0,
  description         text NOT NULL DEFAULT '',
  if_index            int  NOT NULL DEFAULT 0,
  remote_device_id    text NOT NULL DEFAULT '',
  remote_interface_id text NOT NULL DEFAULT '',
  remote_port_name    text NOT NULL DEFAULT '',
  link_source         text NOT NULL DEFAULT '',
  link_vlan           int  NOT NULL DEFAULT 0,
  link_last_seen      timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (device_id, name)
);
CREATE INDEX interfaces_device   ON ipam_device_interfaces (tenant_id, device_id);
CREATE INDEX interfaces_mac      ON ipam_device_interfaces (tenant_id, mac_address);
CREATE INDEX interfaces_ifindex  ON ipam_device_interfaces (device_id, if_index);
CREATE INDEX interfaces_remote   ON ipam_device_interfaces (tenant_id, remote_device_id);

CREATE TABLE ipam_device_interface_links (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL,
  interface_id        uuid NOT NULL REFERENCES ipam_device_interfaces(id) ON DELETE CASCADE,
  remote_device_id    text NOT NULL DEFAULT '',
  remote_interface_id text NOT NULL DEFAULT '',
  remote_port_name    text NOT NULL DEFAULT '',
  link_source         text NOT NULL DEFAULT '',
  link_vlan           int  NOT NULL DEFAULT 0,
  link_last_seen      timestamptz,
  UNIQUE (interface_id, remote_device_id, link_source)
);
CREATE INDEX interface_links_iface ON ipam_device_interface_links (tenant_id, interface_id);

CREATE TABLE ipam_device_packages (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  device_id         uuid NOT NULL REFERENCES ipam_devices(id) ON DELETE CASCADE,
  name              text NOT NULL DEFAULT '',
  current_version   text NOT NULL DEFAULT '',
  available_version text NOT NULL DEFAULT '',
  needs_update      boolean NOT NULL DEFAULT false,
  is_security_update boolean NOT NULL DEFAULT false,
  package_manager   text NOT NULL DEFAULT '',
  description       text NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, device_id, name)
);
CREATE INDEX packages_device   ON ipam_device_packages (tenant_id, device_id);
CREATE INDEX packages_needs    ON ipam_device_packages (tenant_id, needs_update);
CREATE INDEX packages_security ON ipam_device_packages (tenant_id, is_security_update);

CREATE TABLE ipam_ip_groups (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  name        text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  status      text NOT NULL DEFAULT 'active',
  tags        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE INDEX ip_groups_tenant ON ipam_ip_groups (tenant_id);

CREATE TABLE ipam_ip_group_members (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  ip_group_id uuid NOT NULL REFERENCES ipam_ip_groups(id) ON DELETE CASCADE,
  member_type text NOT NULL DEFAULT 'address',
  value       text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  sequence    int  NOT NULL DEFAULT 0,
  UNIQUE (ip_group_id, value)
);
CREATE INDEX ip_group_members_group ON ipam_ip_group_members (tenant_id, ip_group_id);

CREATE TABLE ipam_host_groups (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  name        text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  status      text NOT NULL DEFAULT 'active',
  tags        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE INDEX host_groups_tenant ON ipam_host_groups (tenant_id);

CREATE TABLE ipam_host_group_members (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  host_group_id uuid NOT NULL REFERENCES ipam_host_groups(id) ON DELETE CASCADE,
  device_id     uuid NOT NULL REFERENCES ipam_devices(id) ON DELETE CASCADE,
  sequence      int  NOT NULL DEFAULT 0,
  UNIQUE (host_group_id, device_id)
);
CREATE INDEX host_group_members_group  ON ipam_host_group_members (tenant_id, host_group_id);
CREATE INDEX host_group_members_device ON ipam_host_group_members (tenant_id, device_id);

CREATE TABLE ipam_dns_configs (
  id                      uuid PRIMARY KEY,
  tenant_id               uuid NOT NULL,
  dns_servers             jsonb NOT NULL DEFAULT '[]'::jsonb,
  timeout_ms              int  NOT NULL DEFAULT 5000,
  use_system_dns_fallback boolean NOT NULL DEFAULT false,
  reverse_dns_enabled     boolean NOT NULL DEFAULT false,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id)
);

-- +goose Down
DROP TABLE IF EXISTS ipam_dns_configs;
DROP TABLE IF EXISTS ipam_host_group_members;
DROP TABLE IF EXISTS ipam_host_groups;
DROP TABLE IF EXISTS ipam_ip_group_members;
DROP TABLE IF EXISTS ipam_ip_groups;
DROP TABLE IF EXISTS ipam_device_packages;
DROP TABLE IF EXISTS ipam_device_interface_links;
DROP TABLE IF EXISTS ipam_device_interfaces;
DROP TABLE IF EXISTS ipam_ip_addresses;
DROP TABLE IF EXISTS ipam_devices;
DROP TABLE IF EXISTS ipam_subnets;
DROP TABLE IF EXISTS ipam_vlans;
DROP TABLE IF EXISTS ipam_locations;
