# Implementation Plan: SNMP Credentials on Subnets for Network Device Discovery

**Branch**: `021-subnet-snmp-credentials` | **Date**: 2026-09-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/021-subnet-snmp-credentials/spec.md`

## Summary

Subnets get their own SNMP credentials (v2c community, or v3 user with
authNoPriv/authPriv, SHA-1/SHA-2 and DES/AES-128/192/256) stored by IPAM in a
new table `ipam_subnet_snmp`: non-secret metadata in clear, the secret values
sealed as one envelope blob with IPAM's existing KEK (first real use of
`internal/sealed`) and bound to tenant+subnet by associated data. Four
dedicated write-only endpoints (status, set/replace, clear, test) keep the
subnet PUT untouched. A pure `internal/snmpcred` package validates input,
seals/opens, and resolves inheritance (nearest ancestor with own
credentials). The scan executor resolves effective credentials at job start,
opens them once, probes live hosts with a completed SNMP client (full
protocol set, explicit security level, no `public` fallback, IPv4+IPv6,
error classification) and records an SNMP phase result with a reason. Every
change and test is audited with neutral detail keys. The UI adds an SNMP
card to the subnet drawer, an SNMP column to the subnet list and the SNMP
phase line to scan jobs. The dead warden reference is retired (columns kept,
never written; startup count logged).

## Technical Context

**Language/Version**: Go 1.26 (IPAM), TypeScript/Vue 3 (IPAM UI remote)

**Primary Dependencies**: existing only — go-tangra/v4 framework, pgx/goose,
`github.com/gosnmp/gosnmp v1.43.2` (already direct), `internal/sealed`
(AES-256-GCM envelope), `@go-tangra/ui` 4.2.1

**Storage**: PostgreSQL/TimescaleDB with RLS — IPAM migration **0006**
(`ipam_subnet_snmp` + RLS policy; five `snmp_*` columns on `ipam_scan_jobs`)

**Testing**: `go test -race` with memstore and fakes (SNMP `Fake` extended with
outcomes, envelope with a test KEK); negative security tests (values never in
any response/event/backup/audit/log; blob moved to another subnet/tenant
fails to open; test target outside subnet refused; rate limit; read-only
user cannot change; subnet PUT keeps credentials); fuzz `snmpcred.Validate`
and input decoding; testcontainers integration (migration upgrade 0005→0006,
RLS isolation of `ipam_subnet_snmp`, cascade on subnet delete, seal/open
round trip against PostgreSQL); OpenAPI route/permission contract test;
vitest for the drawer card, list column and scan line

**Target Platform**: Linux container (freya-stack)

**Project Type**: single repo (go-tangra-ipam-v4): service + UI remote

**Performance Goals**: subnet list with SNMP summary adds one metadata query
per request (in-memory resolution); credential test ≤ SNMP timeout + 2 s
(SC-006); scan cost unchanged apart from one decrypt per job

**Constraints**: write-only secrets (SR-002); no secret in logs/events/backup
(FR-009, SC-003); forward-only migration; no proto change (no SDK release);
coverage gates — 100 % for `internal/authz`, `internal/sealed`,
`internal/ipnet`, `internal/hostreport`, `internal/hostplan` + new
`internal/snmpcred`; ≥ 80 % total

**Scale/Scope**: ≤ a few thousand subnets per tenant; 1 migration, 1 new
package, 4 new HTTP operations, 4 audit event types, no new permission

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1 design — all PASS.*

- [x] **I. Secure by Default**: no default community (removes the `public`
      fallback); credentials only via explicit admin action; weak protocols
      labelled; secrets write-only.
- [x] **II. Zero Trust**: no new service-to-service path (SNMP goes to lab
      hosts, not mesh peers); endpoints behind the gateway with module-scoped
      permissions and CSRF.
- [x] **III. Boundary Validation**: typed OpenAPI schemas with
      `additionalProperties: false`, lengths and enums; `snmpcred.Validate`
      server-side; DB CHECK constraints; body limits (4 KiB / 1 KiB); test
      target constrained to the subnet; rate limit.
- [x] **IV. Test-First**: each phase lists tests first; negative tests
      enumerated above; new pure package at 100 %; fuzz targets.
- [x] **V. Observability**: audit rows for set/replace/clear/test in the
      same transaction; scan SNMP phase counters and reason; no secret in
      logs (error text scrubbed).
- [x] **VI. Supply Chain**: no new dependency.
- [x] **VII. Simplicity**: one table, one pure package, dedicated endpoints;
      inheritance computed, not stored.
- [x] **Threat Model**: STRIDE in [research.md](research.md#stride-threat-model).

## Project Structure

### Documentation (this feature)

```text
specs/021-subnet-snmp-credentials/
├── spec.md  plan.md  research.md  data-model.md  quickstart.md
├── contracts/{ipam-http.md,audit-events.md}
├── checklists/requirements.md
└── tasks.md
```

### Source Code

```text
go-tangra-ipam-v4
  internal/store/migrations/0006_subnet_snmp.sql     # table + RLS + scan job columns
  internal/store/models.go                           # SubnetSNMP, SNMPSummary, IPScanJob fields
  internal/repo/repo.go                              # SubnetSNMP store methods
  internal/repo/repodb/{db.go,snmp.go}               # SQL + scan job columns
  internal/memstore/{memstore.go,snmp.go}            # parity
  internal/sealed/sealed.go                          # ADSNMP
  internal/snmpcred/                                 # NEW pure: Input/Validate, Seal/Open, Resolve, ToCreds, Scrub (100 %)
  internal/scan/snmp/snmp.go                         # protocols, levels, Connect, Classify, Probe
  internal/scan/{scan.go,executor.go}                # effective creds, phase result, Test
  internal/subnets/{subnets.go,snmp.go}              # status/set/clear service, summary fill, ignore legacy input
  internal/audit/audit.go                            # 4 event types
  internal/backup/backup.go                          # summary + snmp_credentials_required
  internal/httpapi/{snmp.go,handlers.go}             # 4 routes, rate limiter
  internal/grpcapi/mapper.go                         # effective snmp_version, empty ref
  internal/app/app.go                                # wire envelope into subnets/scan; legacy count log
  api/openapi/ipam.yaml, pkg/ipammanifest (generated from it)
  ui/src/components/SubnetSnmpCard.vue (NEW), ui/src/views/subnets/{drawer.vue,index.vue},
  ui/src/views/scans/index.vue, ui/src/stores/{subnets.ts,scans.ts}, ui/src/api/types.ts, ui/src/schemas/snmp.ts (NEW)
  scripts/coverage-gate.sh, README.md
```

**Structure Decision**: the pure `snmpcred` package owns every rule that
matters for security (validation, AD binding, inheritance, scrubbing) so it
is proven at 100 % without a database; the store only persists metadata and
the opaque blob; the scan and HTTP layers only orchestrate.

## Rollout

1. **ipam v4.4.0** (minor: new API): migration 0006 runs on start;
   credentials start empty (the warden refs never worked); startup log states
   how many subnets carried a legacy ref.
2. **go-tangra-docker**: bump `IPAM_IMAGE`; no config change (KEK already
   configured).
3. Operators set credentials on parent subnets and run scans with SNMP.

Merges, tags, pins and the production deploy are user-confirmed steps.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Separate table instead of subnet columns | Secret material must never ride along subnet JSON (events, backup, gRPC, list) | Columns would need stripping on every read path; one missed path leaks |
| Dedicated endpoints instead of subnet PUT fields | Full-replace PUT would erase or require resending secrets (FR-005/006) | Marker-merge on PUT couples every subnet edit to secret handling |
| In-process rate limiter | FR-020 bounds the test endpoint as a probe tool | Gateway has no per-route limits; a DB-backed limiter is overkill for 10/min |
