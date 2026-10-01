-- +goose Up
-- Feature 032 (server-side tables): the scan list pages newest first per
-- tenant, and the device list sorts by name case-insensitively by default.
CREATE INDEX IF NOT EXISTS scan_jobs_tenant_created ON ipam_ip_scan_jobs (tenant_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS devices_tenant_lower_name ON ipam_devices (tenant_id, lower(name), id);

-- +goose Down
DROP INDEX IF EXISTS devices_tenant_lower_name;
DROP INDEX IF EXISTS scan_jobs_tenant_created;
