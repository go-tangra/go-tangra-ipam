-- +goose Up
-- Per-tenant row-level security on every ipam_* table. ipam_app is NOBYPASSRLS;
-- every statement runs with app.tenant_id set to the caller's tenant. Trusted
-- worker/maintenance paths (scan executor, audit writer, tenant enumeration) set
-- app.system='on' (with app.tenant_id pinned to the nil uuid so the uuid cast
-- stays valid) so the policy admits their cross-tenant access.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'ipam_locations','ipam_vlans','ipam_subnets','ipam_devices','ipam_ip_addresses',
    'ipam_device_interfaces','ipam_device_interface_links','ipam_device_packages',
    'ipam_ip_groups','ipam_ip_group_members','ipam_host_groups','ipam_host_group_members',
    'ipam_dns_configs','ipam_ip_scan_jobs','ipam_audit_events'
  ]
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on') WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')$p$, t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO ipam_app', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'ipam_locations','ipam_vlans','ipam_subnets','ipam_devices','ipam_ip_addresses',
    'ipam_device_interfaces','ipam_device_interface_links','ipam_device_packages',
    'ipam_ip_groups','ipam_ip_group_members','ipam_host_groups','ipam_host_group_members',
    'ipam_dns_configs','ipam_ip_scan_jobs','ipam_audit_events'
  ]
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
  END LOOP;
END $$;
-- +goose StatementEnd
