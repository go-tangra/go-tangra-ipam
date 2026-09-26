-- +goose Up
-- SNMP credentials on subnets (feature 021). Non-secret metadata in clear so
-- status and inheritance never decrypt; the secret values (community, v3 user,
-- passwords) are one envelope blob sealed with the module KEK and bound to
-- tenant+subnet by associated data. Deleting the subnet deletes its
-- credentials. The legacy ipam_subnets.snmp_secret_ref/snmp_version columns
-- stay (FR-023) but are no longer written.
CREATE TABLE ipam_subnet_snmp (
  tenant_id      uuid NOT NULL,
  subnet_id      uuid NOT NULL REFERENCES ipam_subnets(id) ON DELETE CASCADE,
  version        int  NOT NULL CHECK (version IN (2, 3)),
  security_level text NOT NULL DEFAULT '',
  auth_protocol  text NOT NULL DEFAULT '',
  priv_protocol  text NOT NULL DEFAULT '',
  sealed         bytea NOT NULL CHECK (octet_length(sealed) BETWEEN 1 AND 16384),
  updated_by     text NOT NULL DEFAULT '',
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, subnet_id),
  CONSTRAINT subnet_snmp_v2c CHECK (version <> 2 OR (security_level = '' AND auth_protocol = '' AND priv_protocol = '')),
  CONSTRAINT subnet_snmp_v3 CHECK (version <> 3 OR (
    security_level IN ('authNoPriv', 'authPriv')
    AND auth_protocol IN ('MD5', 'SHA', 'SHA224', 'SHA256', 'SHA384', 'SHA512'))),
  CONSTRAINT subnet_snmp_priv CHECK (
    (security_level = 'authPriv' AND priv_protocol IN ('DES', 'AES', 'AES192', 'AES256'))
    OR (security_level <> 'authPriv' AND priv_protocol = ''))
);
CREATE UNIQUE INDEX subnet_snmp_subnet ON ipam_subnet_snmp (subnet_id);

-- SNMP phase result of a scan (reason whenever SNMP discovered nothing).
ALTER TABLE ipam_ip_scan_jobs
  ADD COLUMN snmp_status text NOT NULL DEFAULT ''
      CHECK (snmp_status IN ('', 'not_requested', 'no_live_hosts', 'no_credentials', 'credentials_unreadable', 'ran')),
  ADD COLUMN snmp_source_subnet_id text NOT NULL DEFAULT '',
  ADD COLUMN snmp_probed    bigint NOT NULL DEFAULT 0,
  ADD COLUMN snmp_no_answer bigint NOT NULL DEFAULT 0,
  ADD COLUMN snmp_rejected  bigint NOT NULL DEFAULT 0;

-- RLS + grants (same policy as 0003).
ALTER TABLE ipam_subnet_snmp ENABLE ROW LEVEL SECURITY;
ALTER TABLE ipam_subnet_snmp FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ipam_subnet_snmp
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on');
GRANT SELECT, INSERT, UPDATE, DELETE ON ipam_subnet_snmp TO ipam_app;

-- +goose Down
DROP TABLE IF EXISTS ipam_subnet_snmp;
ALTER TABLE ipam_ip_scan_jobs DROP COLUMN IF EXISTS snmp_rejected, DROP COLUMN IF EXISTS snmp_no_answer,
  DROP COLUMN IF EXISTS snmp_probed, DROP COLUMN IF EXISTS snmp_source_subnet_id, DROP COLUMN IF EXISTS snmp_status;
