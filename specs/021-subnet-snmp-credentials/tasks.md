---

description: "Task list for 021 SNMP Credentials on Subnets for Network Device Discovery"
---

# Tasks: SNMP Credentials on Subnets for Network Device Discovery

**Input**: Design documents from `specs/021-subnet-snmp-credentials/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: MANDATORY (Constitution IV). In every phase the tests are listed
first and must be written and seen failing before the implementation tasks
of that phase. Negative security tests and fuzz tests are listed explicitly.

**Paths**: all paths are in this repository (go-tangra-ipam-v4).

**Release tasks** (tags, PR merges, stack pins, production deploy) require
explicit user confirmation before they are executed.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US6 from spec.md

---

## Phase 1: Setup

- [x] T001 Add `internal/snmpcred` to the 100 % coverage list in `scripts/coverage-gate.sh` (next to authz/sealed/ipnet/hostreport/hostplan)
- [x] T002 [P] Add `ADSNMP(tenantID, subnetID string) []byte` returning `snmp:<tenant>:<subnet>` to `internal/sealed/sealed.go` with a test in `internal/sealed/sealed_test.go` (keeps sealed at 100 %)

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: storage, the pure credential package and the completed SNMP client that every story needs.

### Tests first

- [x] T003 [P] Table tests for `snmpcred.Input.Validate` in `internal/snmpcred/validate_test.go`: v2c ok/empty/too long/v3 fields present; v3 authNoPriv ok, authPriv ok, missing user, password < 8, > 256, unknown protocol, priv fields with authNoPriv, version 1/4 rejected; errors name the field (FR-003); `Weak()` true for MD5/SHA/DES
- [x] T004 [P] Tests for `snmpcred.Seal`/`Open` in `internal/snmpcred/seal_test.go`: round trip per kind; blob opened with another subnet or tenant AD fails; tampered blob fails; `Open` of v2c yields only community; secret JSON never contains metadata fields
- [x] T005 [P] Tests for `snmpcred.Resolve` in `internal/snmpcred/resolve_test.go`: own wins; nearest ancestor; grandchild under child with own; none; parent missing from map; cycle guard; depth limit 64; returns source id (FR-011)
- [x] T006 [P] Tests for `snmpcred.ToCreds` and `snmpcred.Scrub` in `internal/snmpcred/creds_test.go`: mapping per kind incl. security level and protocols; Scrub removes community/user/passwords from error text (SR-004)
- [x] T007 [P] Fuzz target `FuzzDecodeValidate` in `internal/snmpcred/fuzz_test.go` (JSON decode with DisallowUnknownFields + Validate never panics, accepted inputs round-trip through Seal/Open)
- [x] T008 [P] Tests for the SNMP client builder in `internal/scan/snmp/snmp_test.go`: every auth protocol (MD5, SHA, SHA224, SHA256, SHA384, SHA512) and priv protocol (DES, AES, AES192, AES256) maps to the gosnmp constant; authNoPriv/authPriv flags follow `SecurityLevel`; empty v2c community is an error (no `public`); IPv6 target accepted
- [x] T009 [P] Tests for `snmp.Classify` in `internal/scan/snmp/classify_test.go`: timeout → no_response; unknown user → unknown_user; wrong digest/not authentic → auth_failed; decryption → privacy_failed; nil → ok; other → error
- [x] T010 [P] Repo tests for `SubnetSNMP` CRUD in `internal/memstore/snmp_test.go` (get/put/delete/list metadata per tenant; tenant isolation; delete of subnet removes row)

### Implementation

- [x] T011 Write migration `internal/store/migrations/0006_subnet_snmp.sql`: table `ipam_subnet_snmp` (data-model §1) with CHECK constraints, FK ON DELETE CASCADE, RLS enable + tenant policy like 0003, grants to the app role; `ipam_scan_jobs` columns `snmp_status`, `snmp_source_subnet_id`, `snmp_probed`, `snmp_no_answer`, `snmp_rejected`; Down section
- [x] T012 Add `store.SubnetSNMP`, `store.SNMPSummary` (`Subnet.SNMP *SNMPSummary json:"snmp,omitempty"`) and the five `IPScanJob` fields to `internal/store/models.go`
- [x] T013 Add `GetSubnetSNMP`, `PutSubnetSNMP`, `DeleteSubnetSNMP`, `ListSubnetSNMP(tenant)` (metadata only, no blob) and `LegacySNMPRefCount` to the store interface in `internal/repo/repo.go`
- [x] T014 [P] Implement them in `internal/repo/repodb/snmp.go` (tenant tx + RLS) and add the scan job columns to the scan job SQL in `internal/repo/repodb/db.go`
- [x] T015 [P] Implement them in `internal/memstore/snmp.go` and cascade on `DeleteSubnet` in `internal/memstore/memstore.go`
- [x] T016 Implement `internal/snmpcred/{snmpcred.go,validate.go,seal.go,resolve.go,creds.go}` to pass T003–T007
- [x] T017 Extend `internal/scan/snmp/snmp.go`: `Creds.SecurityLevel`, protocol maps, no default community, `client.Connect()`, `Classify`, `Probe(ctx, ip, creds) (sysName, sysDescr string, err error)`; extend `Fake` with per-IP outcomes (T008, T009)
- [x] T018 Add event types `snmp_credentials_set|replaced|cleared|tested` to `internal/audit/audit.go` with a test in `internal/audit/audit_test.go` that details with neutral keys survive and `community`/`password` keys are dropped

**Checkpoint**: storage, crypto binding, inheritance and SNMP client ready.

---

## Phase 3: User Story 1 — Attach v2c credentials and discover devices (Priority: P1) 🎯 MVP

**Goal**: an admin sets v2c credentials on a subnet; a scan with SNMP discovery finds its devices.

**Independent test**: set v2c on a subnet, scan with the SNMP Fake answering one IP → device persisted; no response contains the community.

### Tests first

- [x] T019 [P] [US1] Service tests in `internal/subnets/snmp_test.go`: `SetSNMP` stores sealed blob + metadata and returns status without values; `GetSNMP` status; `Update` of the subnet with `snmp_version`/`snmp_secret_ref`/`snmp` in the body leaves credentials untouched (FR-005) and never writes legacy columns
- [x] T020 [P] [US1] HTTP tests in `internal/httpapi/snmp_test.go`: GET/PUT routes, 400 with `detail.field`, 404, body limit; **negative**: response bodies of GET/PUT/list/get/tree never contain the community string (SC-003); read-only subject gets 403 on PUT
- [x] T021 [P] [US1] Executor tests in `internal/scan/executor_test.go`: job with EnableSNMP on a subnet with own v2c creds uses them (Fake records creds), persists the device, sets `snmp_status=ran`, `snmp_source_subnet_id`, probed/discovered counts
- [x] T022 [P] [US1] OpenAPI contract test update (existing contract test in `internal/httpapi`): new routes present with permissions `ipam:read`/`subnets:manage`, CSRF on PUT, body limit 4096

### Implementation

- [x] T023 [US1] Implement `internal/subnets/snmp.go`: `GetSNMP`, `SetSNMP` (validate → seal with `ADSNMP` → put → audit set/replaced), summary fill for Get/List/Tree via `ListSubnetSNMP` + `snmpcred.Resolve`; zero legacy fields in `Create`/`Update` in `internal/subnets/subnets.go`; the service gets the envelope via a setter
- [x] T024 [US1] Replace `snmpCreds` in `internal/scan/executor.go`: resolve effective creds at job start (subnet + ancestors + metadata), open the source blob with the envelope, map via `snmpcred.ToCreds`; record phase result fields; drop the warden dependency for SNMP
- [x] T025 [US1] Add routes in `internal/httpapi/snmp.go` (GET/PUT `/api/ipam/v1/subnets/{id}/snmp`) and register them in `internal/httpapi/handlers.go`
- [x] T026 [US1] Add schemas `SubnetSNMPInput`, `SubnetSNMPStatus` and the routes to `api/openapi/ipam.yaml`
- [x] T027 [US1] Wire the envelope into subnets and scan services in `internal/app/app.go`
- [x] T028 [P] [US1] UI: types in `ui/src/api/types.ts`, zod schema `ui/src/schemas/snmp.ts`, store actions in `ui/src/stores/subnets.ts` (`snmpStatus`, `setSnmp`)
- [x] T029 [US1] UI: `ui/src/components/SubnetSnmpCard.vue` (status line, Set/Replace form for v2c, never pre-filled, cleared after submit) mounted in edit mode of `ui/src/views/subnets/drawer.vue`
- [x] T030 [P] [US1] UI tests `ui/tests/unit/snmp.spec.ts`: card shows status, submits v2c, clears the field after save, hides the form without `subnets:manage`

**Checkpoint**: v3 parity for v2c — MVP.

---

## Phase 4: User Story 2 — SNMPv3 authNoPriv / authPriv (Priority: P1)

**Goal**: v3 credentials with the full protocol set work end to end.

**Independent test**: set v3 authPriv SHA256/AES256; executor passes level and protocols to the client; wrong-privacy outcome counted as rejected.

### Tests first

- [ ] T031 [P] [US2] Service/HTTP tests in `internal/subnets/snmp_test.go` and `internal/httpapi/snmp_test.go`: v3 authNoPriv and authPriv accepted; priv fields with authNoPriv rejected; responses carry level/protocols/weak but never user/passwords
- [ ] T032 [P] [US2] Executor test in `internal/scan/executor_test.go`: v3 creds reach the Fake with SecurityLevel/protocols; Fake returning privacy_failed/auth_failed increments `snmp_rejected`, no_response increments `snmp_no_answer`

### Implementation

- [ ] T033 [US2] Count outcomes per host with `snmp.Classify` in `discoverSNMP` in `internal/scan/executor.go`
- [ ] T034 [US2] UI: v3 fields in `ui/src/components/SubnetSnmpCard.vue` (user, level, auth protocol + password, priv protocol + password, weak labels) and schema rules in `ui/src/schemas/snmp.ts`; tests in `ui/tests/unit/snmp.spec.ts`

---

## Phase 5: User Story 3 — Inheritance from parent subnets (Priority: P2)

**Goal**: children without own credentials use the nearest ancestor's.

**Independent test**: creds on parent only → child status inherited + scan of child uses parent creds; own creds on child win.

### Tests first

- [ ] T035 [P] [US3] Service tests in `internal/subnets/snmp_test.go`: child status `inherited` with source name/CIDR; list/tree summaries for a 3-level tree; parent cleared → child none; parent deleted → cascade + child falls back; host-sync auto subnet (origin host_sync) inherits
- [ ] T036 [P] [US3] Executor test in `internal/scan/executor_test.go`: scan of grandchild uses nearest ancestor with creds and records its id as source; tenant B subnet never inherits tenant A creds (negative)

### Implementation

- [ ] T037 [US3] Shared resolution helper used by `internal/subnets/snmp.go` and `internal/scan/executor.go` (both call `snmpcred.Resolve` over the tenant's subnets + `ListSubnetSNMP`)
- [ ] T038 [US3] UI: inherited status line with source in `ui/src/components/SubnetSnmpCard.vue`; SNMP column (badge own/inherited/none) in `ui/src/views/subnets/index.vue`; tests in `ui/tests/unit/snmp.spec.ts`

---

## Phase 6: User Story 4 — Test credentials (Priority: P2)

**Goal**: probe one address in the subnet with the effective credentials.

**Independent test**: Fake answers → ok + sysName; outside address → 400; 11th call in a minute → 429; audit row written.

### Tests first

- [ ] T039 [P] [US4] Scan service tests in `internal/scan/snmptest_test.go`: `TestCredentials` outcomes ok/no_response/auth_failed/unknown_user/privacy_failed/no_credentials/credentials_unreadable; address outside subnet, network and broadcast refused (SR-003); deadline = timeout + 2 s
- [ ] T040 [P] [US4] HTTP tests in `internal/httpapi/snmp_test.go`: POST route with `scan:run`, 400 `detail.field=address`, **rate limit** 10/min/user → 429; response never contains creds; audit row `snmp_credentials_tested` with target/outcome

### Implementation

- [ ] T041 [US4] Implement `TestCredentials(ctx, subj, subnetID, address)` in `internal/scan/snmptest.go` (resolve, open, `snmp.Probe`, classify, audit)
- [ ] T042 [US4] Add the POST route and a per-user token-bucket limiter (10/min) in `internal/httpapi/snmp.go`; OpenAPI route + `SNMPTestResult` schema in `api/openapi/ipam.yaml`
- [ ] T043 [US4] UI: "Test SNMP" (address input, result line) in `ui/src/components/SubnetSnmpCard.vue`, store action in `ui/src/stores/subnets.ts`; test in `ui/tests/unit/snmp.spec.ts`

---

## Phase 7: User Story 5 — Why SNMP did nothing (Priority: P2)

**Goal**: every scan with SNMP requested states a reason when 0 devices were discovered.

**Independent test**: scans with no creds / unreadable creds / no live hosts / not requested set the matching `snmp_status`.

### Tests first

- [ ] T044 [P] [US5] Executor tests in `internal/scan/executor_test.go` for `snmp_status` = not_requested, no_live_hosts, no_credentials, credentials_unreadable (blob opened with another KEK), ran; the non-SNMP part of the scan completes in every case
- [ ] T045 [P] [US5] Repo test in `internal/memstore/snmp_test.go` (and integration T054) that the new scan job fields persist and list

### Implementation

- [ ] T046 [US5] Set `snmp_status` in every branch of `processJob`/`discoverSNMP` in `internal/scan/executor.go`
- [ ] T047 [US5] UI: SNMP phase line in `ui/src/views/scans/index.vue` (status → human text, source, probed/discovered/no answer/rejected), types in `ui/src/api/types.ts`; test in `ui/tests/unit/snmp.spec.ts`

---

## Phase 8: User Story 6 — Replace and clear, audited (Priority: P3)

**Goal**: rotate and remove credentials safely.

**Independent test**: replace → audit replaced with previous_version; clear → row gone, audit cleared; clear without own → 204 no audit.

### Tests first

- [ ] T048 [P] [US6] Service/HTTP tests in `internal/subnets/snmp_test.go` and `internal/httpapi/snmp_test.go`: DELETE route (`subnets:manage`, CSRF); replace requires all fields; audit rows for set/replaced/cleared contain version/level only (**negative**: no community/user/password anywhere in the audit detail)

### Implementation

- [ ] T049 [US6] Implement `ClearSNMP` in `internal/subnets/snmp.go` and the DELETE route in `internal/httpapi/snmp.go`; OpenAPI in `api/openapi/ipam.yaml`
- [ ] T050 [US6] UI: Clear button with the kit confirm dialog (not a browser dialog) in `ui/src/components/SubnetSnmpCard.vue`; test in `ui/tests/unit/snmp.spec.ts`

---

## Phase 9: Polish, Release & Cross-Cutting Concerns

- [ ] T051 [P] Backup: exported subnets carry the summary only; `backup.Result.SNMPCredentialsRequired` lists CIDRs with own creds; test that no blob/secret is exported in `internal/backup/backup_test.go` and changes in `internal/backup/backup.go` (FR-022)
- [ ] T052 [P] gRPC mapper: `snmp_version` = effective version, `snmp_secret_ref` empty in `internal/grpcapi/mapper.go` with test in `internal/grpcapi/mapper_test.go`
- [ ] T053 [P] Startup log of the legacy `snmp_secret_ref` count in `internal/app/app.go` (FR-023)
- [ ] T054 Integration tests (testcontainers) in `tests/integration/snmp_test.go`: migration upgrade to 0006; RLS isolation of `ipam_subnet_snmp`; cascade on subnet delete; seal/open round trip through the real store; CHECK constraints reject invalid rows
- [ ] T055 [P] SC-003 sweep test in `internal/httpapi/snmp_leak_test.go`: set creds, exercise list/get/tree/status/test/scan/backup, assert the community, user and passwords appear in no response body, event payload or captured log line
- [ ] T056 [P] README "SNMP credentials" section (storage, inheritance, test endpoint, permissions, audit) in `README.md`
- [ ] T057 Run `go vet ./...`, `go test -race ./...`, `make cover` (snmpcred/sealed 100 %, total ≥ 80 %), `make vuln`, `(cd ui && npm run lint && npm run test:unit && npm run build)`
- [ ] T058 Update quickstart results and tick tasks in `specs/021-subnet-snmp-credentials/tasks.md`
- [ ] T059 Release (user-confirmed): PR → merge → tag `v4.4.0`; bump `IPAM_IMAGE` in go-tangra-docker `.env.example`; deploy to production on explicit request

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 → US1 (MVP) → US2 → {US3, US4, US5} → US6 → Phase 9.
- US2 depends on US1 (same card/endpoints). US3, US4, US5 depend on Phase 2
  and US1 and are independent of each other. US6 depends on US1.
- Within a phase: tests (seen failing) → store/pure code → services →
  HTTP/OpenAPI → UI.

## Parallel Example: Phase 2

```text
T003 T004 T005 T006 T007   (snmpcred tests, different files)
T008 T009                  (snmp client tests)
T010                       (memstore tests)
then T014 ∥ T015 after T011–T013
```

## Parallel Example: after US1

```text
US3 (T035–T038) ∥ US4 (T039–T043) ∥ US5 (T044–T047)
```

## Implementation Strategy

1. MVP = Phase 1 + 2 + US1: v2c credentials on a subnet and working SNMP
   discovery (the capability that is broken today).
2. Add US2 (v3) in the same release — required by v3-only networks.
3. US3–US5 complete the operator experience; US6 lifecycle; Phase 9 hardening.
4. One release (ipam v4.4.0) at the end.

## Notes

- Never log or return credential values; tests assert it (SC-003).
- Audit detail keys must not contain `snmp`, `credential`, `secret`,
  `password` (the guard drops them).
- Commit after each phase; no Claude attribution trailer in commits.
