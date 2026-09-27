-- +goose Up
-- ARP-based MAC linking (feature 022). Addresses record where their MAC came
-- from (manual entry, host-sync agent, or a router's ARP/neighbour table read
-- during a scan) so untrusted ARP data never overwrites an agent or manual MAC,
-- plus the switch port the address is connected to (the address-level
-- counterpart of the interface link columns).
ALTER TABLE ipam_ip_addresses
  ADD COLUMN mac_source text NOT NULL DEFAULT ''
      CONSTRAINT addresses_mac_source CHECK (mac_source IN ('', 'manual', 'agent', 'arp')),
  ADD COLUMN mac_source_device_id uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  ADD COLUMN mac_seen_at timestamptz,
  ADD COLUMN mac_conflict text NOT NULL DEFAULT ''
      CONSTRAINT addresses_mac_conflict CHECK (char_length(mac_conflict) <= 17),
  ADD COLUMN origin text NOT NULL DEFAULT ''
      CONSTRAINT addresses_origin CHECK (origin IN ('', 'arp')),
  ADD COLUMN link_switch_id uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  ADD COLUMN link_port_id uuid REFERENCES ipam_device_interfaces(id) ON DELETE SET NULL,
  ADD COLUMN link_port_name text NOT NULL DEFAULT '',
  ADD COLUMN link_vlan int NOT NULL DEFAULT 0,
  ADD COLUMN link_source text NOT NULL DEFAULT ''
      CONSTRAINT addresses_link_source CHECK (link_source IN ('', 'snmp_fdb', 'lldp')),
  ADD COLUMN link_last_seen timestamptz;

-- FR-009: MACs that exist before this feature are the host sync's when the
-- address is host-reported, otherwise a user's.
UPDATE ipam_ip_addresses SET mac_source = CASE WHEN report_state <> '' THEN 'agent' ELSE 'manual' END
  WHERE mac_address <> '';

-- MAC search (FR-015) on the hex-only form, and the "addresses behind a
-- switch port" lookup.
CREATE INDEX addresses_mac_hex ON ipam_ip_addresses
  (tenant_id, (regexp_replace(lower(mac_address), '[^0-9a-f]', '', 'g')) text_pattern_ops);
CREATE INDEX addresses_link_port ON ipam_ip_addresses (tenant_id, link_port_id) WHERE link_port_id IS NOT NULL;

-- Per-tenant ARP settings (absent row = defaults: enabled, threshold 8).
CREATE TABLE ipam_arp_settings (
  tenant_id        uuid PRIMARY KEY,
  enabled          boolean NOT NULL DEFAULT true,
  excluded_devices uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(excluded_devices) <= 256),
  proxy_threshold  int NOT NULL DEFAULT 8 CHECK (proxy_threshold BETWEEN 2 AND 256),
  updated_by       text NOT NULL DEFAULT '',
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- ARP phase result of a scan (FR-013).
ALTER TABLE ipam_ip_scan_jobs
  ADD COLUMN arp_status text NOT NULL DEFAULT ''
      CHECK (arp_status IN ('', 'ran', 'disabled', 'failed')),
  ADD COLUMN arp_devices   int    NOT NULL DEFAULT 0,
  ADD COLUMN arp_partial   int    NOT NULL DEFAULT 0,
  ADD COLUMN arp_entries   bigint NOT NULL DEFAULT 0,
  ADD COLUMN arp_applied   bigint NOT NULL DEFAULT 0,
  ADD COLUMN arp_created   bigint NOT NULL DEFAULT 0,
  ADD COLUMN arp_conflicts bigint NOT NULL DEFAULT 0,
  ADD COLUMN arp_ignored   jsonb  NOT NULL DEFAULT '{}'::jsonb;

-- RLS + grants (same policy as 0004/0006).
ALTER TABLE ipam_arp_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ipam_arp_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ipam_arp_settings
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on');
GRANT SELECT, INSERT, UPDATE, DELETE ON ipam_arp_settings TO ipam_app;

-- +goose Down
DROP TABLE IF EXISTS ipam_arp_settings;
ALTER TABLE ipam_ip_scan_jobs DROP COLUMN IF EXISTS arp_ignored, DROP COLUMN IF EXISTS arp_conflicts,
  DROP COLUMN IF EXISTS arp_created, DROP COLUMN IF EXISTS arp_applied, DROP COLUMN IF EXISTS arp_entries,
  DROP COLUMN IF EXISTS arp_partial, DROP COLUMN IF EXISTS arp_devices, DROP COLUMN IF EXISTS arp_status;
DROP INDEX IF EXISTS addresses_link_port;
DROP INDEX IF EXISTS addresses_mac_hex;
ALTER TABLE ipam_ip_addresses DROP COLUMN IF EXISTS link_last_seen, DROP COLUMN IF EXISTS link_source,
  DROP COLUMN IF EXISTS link_vlan, DROP COLUMN IF EXISTS link_port_name, DROP COLUMN IF EXISTS link_port_id,
  DROP COLUMN IF EXISTS link_switch_id, DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS mac_conflict,
  DROP COLUMN IF EXISTS mac_seen_at, DROP COLUMN IF EXISTS mac_source_device_id, DROP COLUMN IF EXISTS mac_source;
