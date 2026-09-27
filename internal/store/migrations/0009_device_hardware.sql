-- +goose Up
-- Feature 023: the hardware profile an inventory agent reports for a
-- host-reported device (one row per device, replaced by every report that
-- carries hardware; never written through the device API). The summary
-- columns are lifted from the profile for the device list and summary line.
-- The application bounds the compact profile to 256 KiB; the CHECK allows
-- for the wider jsonb text form.
CREATE TABLE ipam_device_hardware (
  device_id          uuid PRIMARY KEY REFERENCES ipam_devices(id) ON DELETE CASCADE,
  tenant_id          uuid NOT NULL,
  profile            jsonb NOT NULL CHECK (octet_length(profile::text) <= 524288),
  digest             text  NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  cpu_model          text    NOT NULL DEFAULT '' CHECK (length(cpu_model) <= 256),
  cpu_sockets        integer NOT NULL DEFAULT 0 CHECK (cpu_sockets >= 0),
  cpu_cores          integer NOT NULL DEFAULT 0 CHECK (cpu_cores >= 0),
  cpu_threads        integer NOT NULL DEFAULT 0 CHECK (cpu_threads >= 0),
  memory_total_bytes bigint  NOT NULL DEFAULT 0 CHECK (memory_total_bytes >= 0),
  memory_type        text    NOT NULL DEFAULT '' CHECK (length(memory_type) <= 256),
  memory_slots_total integer NOT NULL DEFAULT 0 CHECK (memory_slots_total >= 0),
  memory_slots_used  integer NOT NULL DEFAULT 0 CHECK (memory_slots_used >= 0),
  disk_count         integer NOT NULL DEFAULT 0 CHECK (disk_count >= 0),
  disk_total_bytes   bigint  NOT NULL DEFAULT 0 CHECK (disk_total_bytes >= 0),
  reported_at        timestamptz NOT NULL,
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_hardware_tenant ON ipam_device_hardware (tenant_id);
ALTER TABLE ipam_device_hardware ENABLE ROW LEVEL SECURITY;
ALTER TABLE ipam_device_hardware FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ipam_device_hardware
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on');
GRANT SELECT, INSERT, UPDATE, DELETE ON ipam_device_hardware TO ipam_app;

-- +goose Down
DROP TABLE IF EXISTS ipam_device_hardware;
