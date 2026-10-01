-- +goose Up
-- Feature 032 performance follow-up (specs/032-server-side-tables/perf.md):
-- index-backed list sorts.

-- ipam_try_inet parses a text address / prefix as inet, NULL when it does not
-- parse (rows stored as text are not guaranteed to be valid). The address and
-- subnet cidr sorts order by it (store.inetOf).
--
-- Declared IMMUTABLE although pg_input_is_valid is only STABLE: the STABLE
-- marking covers input functions in general (some read GUCs such as DateStyle
-- or TimeZone); inet input reads no setting, so for the fixed type 'inet' the
-- result depends on the argument alone, which is what an index expression
-- requires. Names are schema-qualified so the body does not depend on the
-- caller's search_path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ipam_try_inet(v text) RETURNS inet
  LANGUAGE sql IMMUTABLE PARALLEL SAFE
  AS $$ SELECT CASE WHEN pg_catalog.pg_input_is_valid(v, 'inet') THEN v::pg_catalog.inet END $$;
-- +goose StatementEnd

-- Default address / subnet pages: inet ascending (ASC NULLS LAST, id ASC).
-- The inet expression is nullable (unparsable rows), so it keeps NULLS LAST in
-- both directions and a backward scan (DESC NULLS FIRST) cannot serve the
-- descending sort; the large address table gets a second index for it.
--
-- INCLUDE carries the expression's source column: the planner only plans an
-- index-only scan of an expression index when the referenced columns are in
-- the index, and the deferred page query (repodb.pageRowsDeferred) picks a deep
-- page's ids by walking the index without heap fetches.
CREATE INDEX IF NOT EXISTS addresses_tenant_inet ON ipam_ip_addresses (tenant_id, ipam_try_inet(address), id) INCLUDE (address);
CREATE INDEX IF NOT EXISTS addresses_tenant_inet_desc ON ipam_ip_addresses (tenant_id, ipam_try_inet(address) DESC NULLS LAST, id DESC) INCLUDE (address);
CREATE INDEX IF NOT EXISTS subnets_tenant_inet ON ipam_subnets (tenant_id, ipam_try_inet(cidr), id) INCLUDE (cidr);

-- Case-insensitive text sorts of the address list (Text fields order by
-- lower(expr); the columns are NOT NULL so both directions use the index).
CREATE INDEX IF NOT EXISTS addresses_tenant_lower_hostname ON ipam_ip_addresses (tenant_id, lower(hostname), id) INCLUDE (hostname);
CREATE INDEX IF NOT EXISTS addresses_tenant_lower_mac ON ipam_ip_addresses (tenant_id, lower(mac_address), id) INCLUDE (mac_address);

-- scan_jobs_tenant_created (0010: tenant_id, created_at DESC, id DESC) is kept
-- as is: created_at is NOT NULL and now sorts without a NULLS clause, so the
-- forward scan matches "created_at DESC, id DESC" (DESC defaults to NULLS
-- FIRST) and the backward scan matches "created_at ASC, id ASC". It only
-- failed before because the ORDER BY said DESC NULLS LAST.

-- +goose Down
DROP INDEX IF EXISTS addresses_tenant_lower_mac;
DROP INDEX IF EXISTS addresses_tenant_lower_hostname;
DROP INDEX IF EXISTS subnets_tenant_inet;
DROP INDEX IF EXISTS addresses_tenant_inet_desc;
DROP INDEX IF EXISTS addresses_tenant_inet;
DROP FUNCTION IF EXISTS ipam_try_inet(text);
