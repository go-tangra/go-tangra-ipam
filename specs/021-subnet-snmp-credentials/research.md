# Research: SNMP Credentials on Subnets (021)

Findings from the code as of `main` @ ipam v4.3.2, and the decisions they led to.

## Findings

- **F1 — warden link is a stub.** `internal/warden.grpcClient.GetSecret`
  always returns `errNotWired`; `scan.snmpCreds` therefore always fails and
  `discoverSNMP` returns 0 silently. BMC power/KVM have the same problem
  (out of scope here).
- **F2 — the envelope exists but is unused.** `app.go` loads the KEK
  (`kek.source` file/env, required in production) and builds
  `sealed.Envelope`, but nothing calls `Seal`/`Open`; owner/contact are only
  cleared from responses. Production therefore already has the key.
- **F3 — subnet update is a full replace.** `subnets.Update` writes the
  incoming `store.Subnet` over the row; `snmp_secret_ref`/`snmp_version`
  are overwritten by whatever the client sends.
- **F4 — SNMP client gaps.** `newClient` knows only MD5/SHA and DES/AES,
  always sets `AuthPriv` first, falls back to community `public` when the
  community is empty, and connects with `ConnectIPv4` only. Errors are
  wrapped strings; nothing classifies them. gosnmp v1.43.2 supports
  SHA224/256/384/512 and AES192/AES256 (and the Cisco "C" variants).
- **F5 — scan result.** `IPScanJob` has only `snmp_discovered_count`; there
  is no reason when SNMP did nothing.
- **F6 — audit guard.** `audit.guardMap` drops any detail key containing
  `snmp`, `credential`, `secret`, `password` …, so audit details for this
  feature must use neutral keys (`protocol_version`, `security_level`).
- **F7 — API surface.** The OpenAPI file has typed schemas only for newer
  operations (host sync); subnets are loose objects. Per-route permission is
  a single `x-freya-permission`.
- **F8 — gRPC/SDK.** `ipam.v1.Subnet` carries `snmp_secret_ref` and
  `snmp_version`; consumers (dns) do not use them.

## Decisions

### D1 — Separate table for credentials, sealed as one blob

`ipam_subnet_snmp (tenant_id, subnet_id PK→ipam_subnets ON DELETE CASCADE,
version, security_level, auth_protocol, priv_protocol, sealed bytea,
updated_by, updated_at)` with RLS like every tenant table. Non-secret
metadata (version, level, protocols) is clear text so status and inheritance
never need to decrypt; the secret values (community, v3 user, passwords) are
one JSON document sealed with the envelope (per-row data key wrapped by the
KEK, AES-256-GCM) and associated data `snmp:<tenant>:<subnet>` so a blob
copied to another row or tenant fails to open.

- *Rationale*: FR-005 falls out naturally (the subnet PUT never touches the
  table); cascade delete handles the "parent deleted" edge case; the subnet
  row and its JSON (events, backup, gRPC) never carry secret material.
- *Alternatives*: columns on `ipam_subnets` (every subnet read/write path
  would need to strip them, easy to leak through events/backup); the warden
  module (rejected by the user: needs module access without a user and
  multi-field secrets).

### D2 — Dedicated endpoints, write-only

- `GET  /api/ipam/v1/subnets/{id}/snmp` (`ipam:read`) → status (own +
  effective), never values.
- `PUT  /api/ipam/v1/subnets/{id}/snmp` (`subnets:manage`) → set/replace; all
  fields required per kind (FR-006).
- `DELETE /api/ipam/v1/subnets/{id}/snmp` (`subnets:manage`) → clear (FR-007).
- `POST /api/ipam/v1/subnets/{id}/snmp/test` (`scan:run`) → test one address.

Subnet list/detail responses gain a read-only `snmp` summary (state
none/own/inherited, version, source subnet id/name/CIDR) computed in the
service. The subnet create/update paths ignore any `snmp_*` input and stop
writing the legacy columns.

- *FR-019 note*: a route has one permission; `scan:run` is the natural one
  for an active probe. Operators with only `subnets:manage` cannot test.
  Spec FR-019 is amended accordingly.

### D3 — Inheritance resolved in memory from one tenant listing

`snmpcred.Resolve(subnetID, byID, own)` walks `parent_id` upwards
(cycle-guarded, depth ≤ 64) to the nearest subnet with own credentials. The
list endpoint loads all tenant subnets (already done for the tree) plus all
credential metadata rows (one query) and resolves each subnet in O(depth).
Scans/tests resolve at start (FR-013) and then open the source row's blob
once; the decrypted `snmp.Creds` lives only in the job goroutine (FR-010,
FR-018).

### D4 — Pure package `internal/snmpcred` (100 % coverage gate)

Holds: input type + `Validate` (FR-003: per-kind required fields, v3
passwords ≥ 8, values ≤ 256, no empty, allowed protocols, weak flags),
`Seal`/`Open` helpers over `sealed.Envelope` with the AD, `Resolve`,
mapping to `snmp.Creds`, and `Scrub` for error text. Fuzz target for
`Validate` + JSON decode.

### D5 — SNMP client: full protocol set, explicit levels, no defaults

`snmp.Creds` gains `SecurityLevel` (`authNoPriv`|`authPriv`). Protocol maps:
MD5, SHA (SHA-1), SHA224, SHA256, SHA384, SHA512; DES, AES (AES-128), AES192,
AES256. The `public` fallback is removed (empty community → error). Connect
with `client.Connect()` (IPv4 and IPv6). New `Classify(err) Outcome` maps
gosnmp errors: timeout → `no_response`; unknown user report →
`unknown_user`; wrong digest / not authentic → `auth_failed`; decryption
errors → `privacy_failed`; else `error`. A new `Probe(ctx, ip, creds)
(sysName, sysDescr, error)` does one GET for the test.

### D6 — Scan SNMP phase result (migration 0006 columns on `ipam_scan_jobs`)

`snmp_status` (`not_requested` | `no_live_hosts` | `no_credentials` |
`credentials_unreadable` | `ran`), `snmp_source_subnet_id`, `snmp_probed`,
`snmp_no_answer`, `snmp_rejected` (auth/unknown-user/privacy). Existing
`snmp_discovered_count` kept. Executor sets them; UI shows a reason whenever
discovered = 0 (SC-005).

### D7 — Test endpoint safety

Target must be inside the subnet CIDR (SR-003) and not the network/broadcast
address; per-user token bucket 10/min in process (FR-020, returns 429
`rate_limited`); deadline = SNMP timeout + 2 s; result carries outcome,
sysName/sysDescr (truncated 256) and duration; audited (FR-021).

### D8 — Audit events (typed, neutral keys)

`snmp_credentials_set`, `snmp_credentials_replaced`, `snmp_credentials_cleared`,
`snmp_credentials_tested`; subject `subnet`; details `protocol_version`,
`security_level`, and for tests `target`, `outcome`. Written through the
existing audit row path in the same transaction as the change.

### D9 — Legacy fields

Migration 0006 keeps `snmp_secret_ref`/`snmp_version` columns (FR-023 "may
be retained") but the service zeroes them on create/update. On startup the
app logs once `legacy snmp_secret_ref present` with the count. The gRPC
`Subnet.snmp_version` is filled with the effective version;
`snmp_secret_ref` is always empty. No proto change → no SDK release.

### D10 — Backup

Exported subnets carry the effective summary (`snmp.state`, version) and no
secrets; `backup.Result` gains `snmp_credentials_required` (CIDRs of subnets
that had own credentials in the export) so restore tells operators what to
re-enter (FR-022).

### D11 — UI

Subnet drawer (edit mode) gets an "SNMP credentials" card: status line
(not configured / own vN / inherited from X), Set/Replace form (version,
community or v3 user + level + protocols + passwords, weak labels), Clear,
and "Test SNMP" (address input, result). Subnet list gains an SNMP column
(badge). Scan job view shows the SNMP phase line with the reason. Form values
are cleared from component state after submit.

### D12 — Performance

Credential metadata per tenant is small (≤ number of subnets); list
resolution is in memory. No caching of decrypted values anywhere.

## STRIDE threat model

| Threat | Vector | Mitigation |
|---|---|---|
| **S**poofing | Forged caller sets credentials | Gateway session + `subnets:manage`; CSRF header on PUT/DELETE/POST |
| **T**ampering | Blob swapped between rows/tenants | AEAD with AD `snmp:<tenant>:<subnet>`; RLS on the table |
| **R**epudiation | Who changed/tested credentials | Audit rows in the same transaction (D8) |
| **I**nformation disclosure | Values in responses/events/backup/logs/errors | Separate table; write-only API; `snmpcred.Scrub` on error text; audit guard; values never logged; test result has no creds; backup excludes table |
| **I**nformation disclosure | Credentials sent to attacker host via test | Target must be inside the subnet (SR-003); rate limit |
| **D**enial of service | Test endpoint as UDP probe cannon | 10/min/user, single target, bounded deadline, body limit 4 KiB |
| **E**levation | Read-only user reads/changes creds | `ipam:read` gets status only; changes need `subnets:manage`; tests need `scan:run` |
