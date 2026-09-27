---

description: "Task list for 022 Link Agentless Hosts to Switch Ports via ARP Tables"
---

# Tasks: Link Agentless Hosts to Switch Ports via ARP Tables

**Input**: Design documents from `specs/022-arp-mac-linking/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: MANDATORY (Constitution IV). In every phase the tests are listed
first and must be written and seen failing before the implementation tasks
of that phase. Negative security tests and fuzz tests are listed explicitly.

**Paths**: all paths are in this repository (go-tangra-ipam-v4).

**Release tasks** (tags, PR merges, stack pins, production deploy) require
explicit user confirmation before they are executed.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US4 from spec.md

---

## Phase 1: Setup

- [x] T001 Add `internal/arpplan` to the 100 % coverage list in `scripts/coverage-gate.sh` and its fuzz target to the Makefile fuzz list

---

## Phase 2: Foundational (blocking prerequisites)

### Tests first

- [x] T002 [P] OID index decoder tests in `internal/scan/snmp/arp_test.go`: ipNetToPhysical IPv4 (`ifIndex.1.4.a.b.c.d`) and IPv6 (`ifIndex.2.16.<16 octets>`), legacy ipNetToMedia (`ifIndex.a.b.c.d`), malformed/short/oversized indexes dropped, MAC octet decoding (6 bytes only), cap at 65,536 sets partial
- [x] T003 [P] Fuzz target `FuzzARPIndex` in `internal/scan/snmp/arp_fuzz_test.go` (decoder never panics; accepted rows have canonical IP and valid MAC)
- [x] T004 [P] SNMP Fake ARP support test in `internal/scan/snmp/snmp_test.go`: `Fake.SetARP(ip, entries)`; `Discover` returns ARP only when `Creds.CollectARP`
- [x] T005 [P] Repo tests in `internal/memstore/arp_test.go`: ARP settings get/put defaults; `ApplyARP` executes fill/update/conflict/clear_conflict/create/touch with audit rows in one call; tenant isolation; address list `mac` filter (partial, notations)
- [x] T006 [P] Audit test in `internal/audit/audit_test.go`: new event types accepted; `mac`/`previous_mac`/`observed_mac` keys survive the guard

### Implementation

- [x] T007 Write migration `internal/store/migrations/0008_arp_mac_linking.sql` (data-model §1–3): address columns + CHECKs + backfill (`report_state <> ''` → agent, else manual), MAC search expression index, `ipam_arp_settings` with RLS/grants like 0004, scan job ARP columns; Down section
- [x] T008 Extend `internal/store/models.go`: `IPAddress` provenance + `Link *AddressLink`, `ARPSettings`, scan job ARP counters and `ARPIgnored map[string]int`, constants for sources/origins/reasons
- [x] T009 Extend `internal/repo/repo.go`: `GetARPSettings`, `PutARPSettings`, `ApplyARP(ctx, tenant, ops, audit)`, `AddressFilter.MAC`, `PortLinkData.Addresses`, `SetAddressLinks`
- [x] T010 [P] Implement in `internal/repo/repodb/arp.go` (+ address columns/filters in `db.go`, scan job columns incl. jsonb)
- [x] T011 [P] Implement in `internal/memstore/arp.go` (+ `memstore.go` parity)
- [x] T012 Implement `internal/scan/snmp/arp.go` (walk ipNetToPhysicalPhysAddress, fallback ipNetToMediaPhysAddress, decoder, cap/partial) and wire `CollectARP` into `Discover`; `Fake.SetARP` (T002–T004)
- [x] T013 Add event types to `internal/audit/audit.go` (T006)

**Checkpoint**: storage, ARP collection and audit vocabulary ready.

---

## Phase 3: User Story 1 — Addresses get their MAC from ARP tables (Priority: P1) 🎯 MVP

**Goal**: scans fill address MACs from router/L3 ARP tables under provenance rules.

**Independent test**: Fake router with an ARP table for three IPs in two subnets → three addresses filled with source arp; agent/manual MACs untouched; unknown IP in a subnet creates an address.

### Tests first

- [x] T014 [P] [US1] Planner tests in `internal/arpplan/plan_test.go`: provenance table (data-model §5) — fill, update, touch, conflict, clear_conflict, create; outside-subnet ignored; same IP from two devices (deterministic winner + conflict count); audit rows per op with neutral keys
- [x] T015 [P] [US1] Planner fuzz `FuzzPlan` in `internal/arpplan/fuzz_test.go` (random entries/addresses: never panics, never emits an op changing an `agent`/`manual` MAC — SC-003)
- [x] T016 [P] [US1] Executor tests in `internal/scan/executor_test.go`: scan with SNMP collects ARP from answering devices (CollectARP set only when settings enabled), applies the plan, records scan counters and `arp_run`; disabled settings → no ARP, status visible; partial device counted
- [x] T017 [P] [US1] Address API tests in `internal/addresses/addresses_test.go` and `internal/httpapi`: user MAC sets `manual` and clears conflict; empty clears source; `mac_source`/`origin`/`link` in body ignored; host sync writes `agent` (hostsync test)

### Implementation

- [x] T018 [US1] Implement `internal/arpplan/{arpplan.go,filter.go,plan.go}` (normalise, provenance planning, audit rows, counters) to pass T014–T015 (filters for US3 may land here but are tested in Phase 5)
- [x] T019 [US1] ARP phase in `internal/scan/executor.go`: after SNMP discovery collect observations from discovered devices, load settings/subnets/addresses/network MACs, plan, `ApplyARP`, set scan counters, then correlation
- [x] T020 [US1] Provenance writers: `internal/addresses/addresses.go` (manual) and the host-sync apply path (agent) in `internal/repo/repodb/hostsync.go` + `internal/memstore/hostsync.go`
- [x] T021 [US1] OpenAPI: address fields, scan job ARP fields in `api/openapi/ipam.yaml`; regenerate `ui/src/api/schema.d.ts`
- [x] T022 [P] [US1] UI: MAC source/last seen/conflict on the address list and detail (`ui/src/views/addresses/*`, `ui/src/api/types.ts`); ARP line in the scan views (`ui/src/views/scans/snmp.ts`, `ui/src/views/scans/index.vue`, subnet drawer scan message); vitest in `ui/tests/unit/arp.spec.ts`

**Checkpoint**: MVP — agentless addresses carry MACs.

---

## Phase 4: User Story 2 — Addresses show their switch port (Priority: P1)

**Goal**: correlation links addresses with MACs to access ports.

**Independent test**: address MAC on an access port (few MACs) → link; only on a trunk → none; bonded across two switches → two links; stale → cleared.

### Tests first

- [x] T023 [P] [US2] Rank/Correlate tests in `internal/portlink/portlink_test.go`: address hosts linked with the same rules; address whose MAC equals a host-reported interface MAC skipped (interface covers it); switch/router own MACs never linked; re-confirm/supersede/stale for address links with `port_linked`/`port_unlinked` (subject address)
- [x] T024 [P] [US2] HTTP tests: address `link` object; switch interfaces `behind_addresses`; device detail bound addresses show links

### Implementation

- [x] T025 [US2] Extend `internal/portlink/{rank.go,portlink.go}` (Host/Link `AddressID`, BuildInput adds addresses, Correlate writes `SetAddressLinks`)
- [x] T026 [US2] Repo: `PortLinkData.Addresses` + `SetAddressLinks` in repodb/memstore; `behind_addresses` query for switch interfaces
- [x] T027 [US2] OpenAPI + handlers for `link` and `behind_addresses` (`internal/httpapi/handlers.go`, `api/openapi/ipam.yaml`)
- [x] T028 [P] [US2] UI: "Connected to" column/section on addresses, behind-addresses on switch ports in `ui/src/views/devices/detail.vue`, device addresses tab links; vitest

---

## Phase 5: User Story 3 — Trust and noise control (Priority: P2)

**Goal**: proxy ARP, virtual-router and network-device MACs never applied; per-tenant settings.

**Independent test**: one MAC for 20 IPs and a VRRP MAC → 0 addresses changed, counted by reason; exclude a device → its entries counted `excluded_device`; disable → no ARP.

### Tests first

- [ ] T029 [P] [US3] Filter tests in `internal/arpplan/filter_test.go`: multicast, broadcast, zero, VRRP v4/v6, HSRP v1/v2, network-device MAC, proxy threshold (boundary = threshold, threshold+1), excluded device; reasons counted (SC-004)
- [ ] T030 [P] [US3] Settings HTTP tests in `internal/httpapi/arp_test.go`: GET defaults (`ipam:read`), PUT (`subnets:manage`, CSRF, 16 KiB), validation (threshold bounds, ≤ 256 devices, unknown device id → 422), audit `arp_settings_updated`
- [ ] T031 [P] [US3] OpenAPI contract test for the settings routes in `api/openapi/openapi_test.go`

### Implementation

- [ ] T032 [US3] Complete filters in `internal/arpplan/filter.go` (T029)
- [ ] T033 [US3] Settings service + routes in `internal/httpapi/arp.go` (service logic in a small `internal/arpcfg` package), OpenAPI schema `ARPSettings`
- [ ] T034 [P] [US3] UI: ARP settings card (enabled, proxy threshold, excluded devices picker) on the host sync page (`ui/src/views/hostsync/index.vue`) with store `ui/src/stores/arp.ts`; vitest

---

## Phase 6: User Story 4 — MAC provenance and search (Priority: P3)

### Tests first

- [ ] T035 [P] [US4] Address list `mac` query tests (memstore + HTTP; repodb in T038): notations `aa:bb`, `AA-BB`, `aabb.cc`, 2–12 hex, invalid → 422

### Implementation

- [ ] T036 [US4] MAC query parsing in `internal/httpapi/handlers.go`, filter in repodb (expression index) and memstore; OpenAPI param
- [ ] T037 [P] [US4] UI: MAC search box on the address list, source badge tooltip (device, last seen); vitest

---

## Phase 7: Polish, Release & Cross-Cutting Concerns

- [ ] T038 Integration tests (testcontainers) in `internal/repo/repodb/arp_integration_test.go`: 0007→0008 upgrade with backfill (agent/manual), RLS isolation of `ipam_arp_settings`, `ApplyARP` transaction (rollback on failure), address links, MAC search via the index
- [ ] T039 [P] Performance test: planner with 5,000 entries × 10 devices and 5,000 addresses completes well under 1 s (`internal/arpplan/bench_test.go`)
- [ ] T040 [P] README section "ARP-based MAC linking" (sources, provenance, filters, settings, permissions, audit)
- [ ] T041 Run `go vet ./...`, `go test -race ./...`, `sg docker -c 'make test-integration'`, `make cover` (arpplan 100 %), `make vuln`, UI lint/unit/build
- [ ] T042 Update quickstart results and tick tasks in `specs/022-arp-mac-linking/tasks.md`
- [ ] T043 Release (user-confirmed): PR → merge → tag `v4.5.0`; bump `IPAM_IMAGE` in go-tangra-docker `.env.example`; production deploy on explicit request

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 → US1 (MVP) → US2 → US3 → US4 → Phase 7.
- US2 depends on US1 (addresses need MACs to link). US3 filters are exercised
  by US1's planner but completed/tested in Phase 5; US4 is independent after
  Phase 2.
- Within a phase: tests (seen failing) → store/pure code → services →
  HTTP/OpenAPI → UI.

## Parallel Example: Phase 2

```text
T002 T003 T004   (snmp ARP decoder/fake tests)
T005 T006        (repo/audit tests)
then T010 ∥ T011 after T007–T009
```

## Implementation Strategy

1. MVP = Phase 1 + 2 + US1: agentless addresses get MACs (visible value, no
   linking yet).
2. US2 delivers the requested linking; release both together.
3. US3 hardens trust (ship in the same release: without it proxy ARP can
   pollute data); US4 convenience.
4. One release (ipam v4.5.0) at the end.

## Notes

- ARP data is untrusted: never overwrite `agent`/`manual` MACs (SC-003).
- Audit detail keys must avoid guarded substrings (`snmp`, `secret`,
  `credential`, `password`).
- Commit after each phase; no Claude attribution trailer in commits.
