-- +goose Up
-- Async IP scan work queue and the append-only audit hypertable. The scan queue
-- is drained with FOR UPDATE SKIP LOCKED by the (system-scoped) scan executor;
-- indexes cover the due-job scan and per-tenant/per-subnet listing. Audit events
-- are append-only and partitioned by their timestamp (a hypertable carries no
-- PRIMARY KEY excluding the partition column, so ids are app-generated uuid v7).

CREATE TABLE ipam_ip_scan_jobs (
  id                    uuid PRIMARY KEY,
  tenant_id             uuid NOT NULL,
  subnet_id             uuid NOT NULL REFERENCES ipam_subnets(id) ON DELETE CASCADE,
  status                text NOT NULL DEFAULT 'pending',
  progress              int  NOT NULL DEFAULT 0,
  status_message        text NOT NULL DEFAULT '',
  total_addresses       bigint NOT NULL DEFAULT 0,
  scanned_count         bigint NOT NULL DEFAULT 0,
  alive_count           bigint NOT NULL DEFAULT 0,
  new_count             bigint NOT NULL DEFAULT 0,
  updated_count         bigint NOT NULL DEFAULT 0,
  snmp_discovered_count bigint NOT NULL DEFAULT 0,
  triggered_by          text NOT NULL DEFAULT 'manual',
  retry_count           int  NOT NULL DEFAULT 0,
  max_retries           int  NOT NULL DEFAULT 3,
  next_retry_at         timestamptz,
  timeout_ms            int  NOT NULL DEFAULT 1000,
  concurrency           int  NOT NULL DEFAULT 50,
  skip_reverse_dns      boolean NOT NULL DEFAULT false,
  tcp_probe_ports       text NOT NULL DEFAULT '',
  enable_snmp           boolean NOT NULL DEFAULT false,
  enable_dns_update     boolean NOT NULL DEFAULT false,
  started_at            timestamptz,
  completed_at          timestamptz,
  created_by            text NOT NULL DEFAULT '',
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scan_jobs_tenant_status ON ipam_ip_scan_jobs (tenant_id, status);
CREATE INDEX scan_jobs_due           ON ipam_ip_scan_jobs (status, next_retry_at);
CREATE INDEX scan_jobs_subnet        ON ipam_ip_scan_jobs (tenant_id, subnet_id);
CREATE INDEX scan_jobs_subnet_id     ON ipam_ip_scan_jobs (subnet_id);
CREATE INDEX scan_jobs_tenant        ON ipam_ip_scan_jobs (tenant_id);
CREATE INDEX scan_jobs_status        ON ipam_ip_scan_jobs (status);

CREATE TABLE ipam_audit_events (
  id           uuid NOT NULL,
  tenant_id    uuid NOT NULL,
  at           timestamptz NOT NULL DEFAULT now(),
  actor_kind   text NOT NULL DEFAULT '',
  actor_id     text NOT NULL DEFAULT '',
  action       text NOT NULL DEFAULT '',
  subject_kind text NOT NULL DEFAULT '',
  subject_id   text NOT NULL DEFAULT '',
  target       text NOT NULL DEFAULT '',
  outcome      text NOT NULL DEFAULT '',
  reason       text NOT NULL DEFAULT '',
  detail       jsonb
);
SELECT create_hypertable('ipam_audit_events', 'at', if_not_exists => TRUE, migrate_data => TRUE);
CREATE INDEX ipam_audit_tenant ON ipam_audit_events (tenant_id, at DESC);

-- +goose Down
DROP TABLE IF EXISTS ipam_audit_events;
DROP TABLE IF EXISTS ipam_ip_scan_jobs;
