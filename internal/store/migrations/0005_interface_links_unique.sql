-- +goose Up
-- 0001's UNIQUE (interface_id, remote_device_id, link_source) rejects a second
-- FDB MAC on the same switch port (all FDB rows have remote_device_id = '').
-- The generated constraint name exceeds 63 bytes and is truncated by
-- PostgreSQL, so it is looked up instead of spelled out.
-- +goose StatementBegin
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'ipam_device_interface_links'::regclass AND contype = 'u';
  IF c IS NOT NULL THEN
    EXECUTE format('ALTER TABLE ipam_device_interface_links DROP CONSTRAINT %I', c);
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ipam_device_interface_links
  ADD CONSTRAINT interface_links_uniq UNIQUE (interface_id, link_source, remote_device_id, remote_port_name);
CREATE INDEX interface_links_source ON ipam_device_interface_links (tenant_id, link_source);
-- +goose Down
DROP INDEX IF EXISTS interface_links_source;
ALTER TABLE ipam_device_interface_links DROP CONSTRAINT interface_links_uniq,
  ADD CONSTRAINT interface_links_iface_remote_source_key UNIQUE (interface_id, remote_device_id, link_source);
