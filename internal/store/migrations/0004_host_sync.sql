-- +goose Up
-- Devices: provenance, inventory link, virtualization, hypervisor, report state.
ALTER TABLE ipam_devices
  ADD COLUMN source               text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','scan','host_report')),
  ADD COLUMN inventory_host_id    uuid,
  ADD COLUMN virtualization_kind  text NOT NULL DEFAULT '' CHECK (char_length(virtualization_kind) <= 32),
  ADD COLUMN hypervisor_device_id uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  ADD COLUMN update_status        text NOT NULL DEFAULT 'unknown'
      CHECK (update_status IN ('unknown','up_to_date','updates_available','unsupported','error')),
  ADD COLUMN report_state         text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported')),
  ADD COLUMN last_report_at       timestamptz,
  ADD COLUMN report_digest        text NOT NULL DEFAULT '',
  ADD CONSTRAINT devices_hypervisor_not_self CHECK (hypervisor_device_id IS DISTINCT FROM id);
CREATE UNIQUE INDEX devices_inventory_host ON ipam_devices (tenant_id, inventory_host_id) WHERE inventory_host_id IS NOT NULL;
CREATE INDEX devices_hypervisor ON ipam_devices (tenant_id, hypervisor_device_id);
CREATE INDEX devices_source     ON ipam_devices (tenant_id, source);

-- Interfaces: reported / no longer reported.
ALTER TABLE ipam_device_interfaces
  ADD COLUMN report_state text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported'));

-- Addresses: report state, last move, conflict flag.
ALTER TABLE ipam_ip_addresses
  ADD COLUMN report_state       text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported')),
  ADD COLUMN previous_device_id uuid,          -- no FK: history survives device deletion
  ADD COLUMN moved_at           timestamptz,
  ADD COLUMN move_count         int  NOT NULL DEFAULT 0 CHECK (move_count >= 0),
  ADD COLUMN move_window_start  timestamptz,
  ADD COLUMN conflict           boolean NOT NULL DEFAULT false;
CREATE INDEX addresses_conflict ON ipam_ip_addresses (tenant_id) WHERE conflict;

-- Subnets: created by the host sync.
ALTER TABLE ipam_subnets
  ADD COLUMN origin text NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','host_sync'));

-- Guests reported by a hypervisor host (matched or not).
CREATE TABLE ipam_hypervisor_guests (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  host_device_id   uuid NOT NULL REFERENCES ipam_devices(id) ON DELETE CASCADE,
  guest_ref        text NOT NULL CHECK (char_length(guest_ref) BETWEEN 1 AND 64),
  name             text NOT NULL DEFAULT '' CHECK (char_length(name) <= 128),
  kind             text NOT NULL CHECK (kind IN ('vm','container')),
  platform         text NOT NULL DEFAULT 'proxmox' CHECK (char_length(platform) <= 32),
  macs             text[] NOT NULL DEFAULT '{}' CHECK (cardinality(macs) <= 32),
  guest_device_id  uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  last_reported_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (host_device_id, guest_ref)
);
CREATE INDEX hypervisor_guests_host ON ipam_hypervisor_guests (tenant_id, host_device_id);
CREATE INDEX hypervisor_guests_macs ON ipam_hypervisor_guests USING gin (macs);

-- Per-tenant sync settings and state (one row per tenant).
CREATE TABLE ipam_hostsync_settings (
  tenant_id             uuid PRIMARY KEY,
  enabled               boolean NOT NULL DEFAULT true,
  full_interval_minutes int  NOT NULL DEFAULT 60 CHECK (full_interval_minutes BETWEEN 15 AND 1440),
  excluded_interfaces   text[] NOT NULL DEFAULT ARRAY['docker*','br-*','veth*','virbr*','cni*','flannel*','cali*',
                          'weave*','vxlan*','kube-*','cilium*','podman*','fwbr*','fwpr*','fwln*','tap*','vnet*']
                          CHECK (cardinality(excluded_interfaces) <= 64),
  changed_since         timestamptz,           -- watermark (inventory clock)
  last_poll_at          timestamptz,
  last_reconcile_at     timestamptz,
  reconcile_requested   boolean NOT NULL DEFAULT false, -- set by resync-all / re-enable
  status                text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','degraded','disabled')),
  last_error            text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 64), -- code only
  hosts_reported        int  NOT NULL DEFAULT 0,
  hosts_failed          int  NOT NULL DEFAULT 0,
  updated_by            text NOT NULL DEFAULT '',
  updated_at            timestamptz NOT NULL DEFAULT now()
);

-- Per-device last report outcome (issues shown to administrators).
CREATE TABLE ipam_hostsync_device_state (
  device_id         uuid PRIMARY KEY REFERENCES ipam_devices(id) ON DELETE CASCADE,
  tenant_id         uuid NOT NULL,
  inventory_host_id uuid NOT NULL,
  snapshot_id       text NOT NULL DEFAULT '' CHECK (char_length(snapshot_id) <= 64),
  collected_at      timestamptz,
  applied_at        timestamptz,
  trigger           text NOT NULL DEFAULT '' CHECK (char_length(trigger) <= 80),
  changes           int  NOT NULL DEFAULT 0,
  issues            jsonb NOT NULL DEFAULT '[]'::jsonb  -- ≤ 50 {field, reason, count}
);
CREATE INDEX hostsync_device_state_tenant ON ipam_hostsync_device_state (tenant_id);

-- RLS + grants for the new tables (same policy as 0003).
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['ipam_hypervisor_guests','ipam_hostsync_settings','ipam_hostsync_device_state']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on') WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')$p$, t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO ipam_app', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS ipam_hostsync_device_state;
DROP TABLE IF EXISTS ipam_hostsync_settings;
DROP TABLE IF EXISTS ipam_hypervisor_guests;
ALTER TABLE ipam_subnets DROP COLUMN IF EXISTS origin;
ALTER TABLE ipam_ip_addresses DROP COLUMN IF EXISTS conflict, DROP COLUMN IF EXISTS move_window_start,
  DROP COLUMN IF EXISTS move_count, DROP COLUMN IF EXISTS moved_at, DROP COLUMN IF EXISTS previous_device_id,
  DROP COLUMN IF EXISTS report_state;
ALTER TABLE ipam_device_interfaces DROP COLUMN IF EXISTS report_state;
ALTER TABLE ipam_devices DROP CONSTRAINT IF EXISTS devices_hypervisor_not_self,
  DROP COLUMN IF EXISTS report_digest, DROP COLUMN IF EXISTS last_report_at, DROP COLUMN IF EXISTS report_state,
  DROP COLUMN IF EXISTS update_status, DROP COLUMN IF EXISTS hypervisor_device_id,
  DROP COLUMN IF EXISTS virtualization_kind, DROP COLUMN IF EXISTS inventory_host_id, DROP COLUMN IF EXISTS source;
