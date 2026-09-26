---

description: "Task list for 020 Hosts Reported by the Inventory Agent Populate IPAM"
---

# Tasks: Hosts Reported by the Inventory Agent Populate IPAM

**Input**: Design documents from `specs/020-ipam-host-sync/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: MANDATORY (Constitution IV). In every phase the tests are listed
first and must be written and seen failing before the implementation tasks
of that phase. Negative security tests and fuzz tests are listed explicitly.

**Paths**: paths without a prefix are in this repository
(go-tangra-ipam-v4). Other repositories are prefixed:
`go-tangra-inventory-v4/…` (inventory module, agent and SDK),
`go-tangra-docker/…` (production stack — notes only, not edited here).

**Release tasks** (tags, PR merges, stack pins) require explicit user
confirmation before they are executed.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US6 from spec.md

---

## Phase 1: Setup

**Purpose**: gates, tooling and module wiring both repositories need before any code.

- [ ] T001 [P] Add `internal/hostreport` to `SECURITY_PKGS` in `go-tangra-inventory-v4/scripts/coverage-gate.sh`; add a `fuzz` target to `go-tangra-inventory-v4/Makefile` running every `Fuzz*` for 10 s (`FuzzEnrollToken`, `FuzzSubmitMapper`, `FuzzDiff` + the targets added by this feature) and keep `internal/agentfacts` inside `COVERPKG` (only `internal/collector` glue stays excluded)
- [X] T002 [P] Add `internal/hostreport` and `internal/hostplan` to `SECURITY_PKGS` in `scripts/coverage-gate.sh`; add a `fuzz` target to `Makefile` (existing `FuzzParseAllocate`, `FuzzScanTargets`, `FuzzMembership` + new targets); keep `internal/hostsync`, `internal/invclient`, `internal/portlink` inside `COVERPKG`
- [ ] T003 Add `github.com/go-tangra/go-tangra-inventory/sdk/v4` to `go.mod` with a temporary `replace … => ../go-tangra-inventory-v4/sdk` for development (removed in T121 before release); `go mod tidy`; `GOWORK=off go build ./...`
- [X] T004 [P] Create empty package skeletons with package doc comments stating their security role: `go-tangra-inventory-v4/internal/agentfacts/doc.go`, `go-tangra-inventory-v4/internal/hostreport/doc.go`, `internal/hostreport/doc.go`, `internal/hostplan/doc.go`, `internal/hostsync/doc.go`, `internal/invclient/doc.go`, `internal/portlink/doc.go`

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the inventory contract (whole proto, SDK, projection, `HostReportService`) and the IPAM foundation (schema, store, validation, client, audit) every story builds on.

**⚠️ CRITICAL**: no user story work starts before this phase is complete.

### Tests first — inventory

- [ ] T005 [P] Proto contract check: `buf lint` and `buf breaking --against '.git#tag=sdk/v4.0.0,subdir=sdk'` wired into `go-tangra-inventory-v4/Makefile` (`proto-check`) and `go-tangra-inventory-v4/.github/workflows/ci.yaml` buf job; must fail until T013 is additive-clean
- [ ] T006 [P] Migration test `go-tangra-inventory-v4/internal/repo/repodb/hostreports_integration_test.go` (`//go:build integration`): database at 0004 with hosts → 0005 applies; `report_digest` CHECK rejects non-hex; indexes exist; RLS still isolates `inventory_hosts` for tenant scope and admits `app.system`
- [ ] T007 [P] Ingest validation tests `go-tangra-inventory-v4/internal/ingest/validate_test.go`: counts above bounds truncated and counted in `Inventory.truncated` (257 interfaces, 65 addresses on one interface, 1001 guests, 5001 pending packages, 9 BMC ports); malformed MAC/IP entries dropped; over-long names clipped/dropped; control characters rejected; snapshot still stored; **negative**: payload > `max_snapshot_bytes` still `InvalidArgument`; tenant always from the agent credential even when the payload carries another tenant-looking value
- [ ] T008 [P] Extend `FuzzSubmitMapper` in `go-tangra-inventory-v4/internal/ingest/ingest_fuzz_test.go` to the new fields (no panic, bounds hold after `validateExtended`, round-trip of valid values)
- [ ] T009 [P] Projection tests `go-tangra-inventory-v4/internal/hostreport/hostreport_test.go` (100 %): field mapping per data-model §1.2, only programs with `available_version` become `pending_updates`, digest stable under reordering-free identical input and independent of `last_seen`/`snapshot_id`/`collected_at`, digest changes when any projected field changes, old-agent snapshot (no new fields) projects with `os_family` fallback; `FuzzHostReport` in `go-tangra-inventory-v4/internal/hostreport/hostreport_fuzz_test.go` (no panic, digest deterministic)
- [ ] T010 [P] Service tests `go-tangra-inventory-v4/internal/grpcapi/hostreports_test.go`: `ListReportTenants` watermark semantics and `max_changed_at`; `ListHostReports` FULL/DIGEST views, ordering, cursor paging, `limit` bounds, 3 MiB page bound, retired hosts included with status; `GetHostReport` NotFound for missing host/snapshot; **negative**: `ListReportTenants` from `svc/gateway` or `svc/asset` → `PermissionDenied`; no SPIFFE peer → `Unauthenticated`; non-uuid tenant → `InvalidArgument`; tenant A request never returns tenant B hosts (memstore with two tenants); DIGEST never carries interfaces/packages/guests
- [ ] T011 [P] Ingest digest tests `go-tangra-inventory-v4/internal/snapshots/snapshots_more_test.go`: first snapshot sets `report_digest`/`report_changed_at`; identical second snapshot leaves `report_changed_at` unchanged; changed interface bumps it
- [ ] T012 [P] SDK tests `go-tangra-inventory-v4/sdk/pkg/inventoryclient/hostreport_test.go`: `ListReportTenants`, `ListHostReports` (filter mapping, cursor), `GetHostReport`, `toInventory` of the new `Inventory` fields, against an in-process fake server

### Implementation — inventory

- [ ] T013 Proto per contracts/inventory-grpc.md in `go-tangra-inventory-v4/sdk/api/proto/inventory/v1/inventory.proto` (NetworkInterface 11–14, InterfaceAddress, Inventory 24–30, OSInfo 9, Program 7–8, Virtualization, Bmc, BmcPort, HypervisorGuest, UpdateState, CollectionLimits, HostReportService + messages, HostReportView, PendingUpdate); `make generate`
- [ ] T014 [P] Domain structs per data-model §1.1 in `go-tangra-inventory-v4/internal/store/models.go`
- [ ] T015 [P] Migration `go-tangra-inventory-v4/internal/store/migrations/0005_host_reports.sql` per data-model §1.3
- [ ] T016 Mappers for every new field in `go-tangra-inventory-v4/internal/sender/mapper.go` (store → proto), `go-tangra-inventory-v4/internal/ingest/ingest.go` (`inventoryFromProto`), `go-tangra-inventory-v4/internal/grpcapi/mapper.go` (store → proto), `go-tangra-inventory-v4/sdk/pkg/inventoryclient/inventory.go` (`toInventory`)
- [ ] T017 `validateExtended` in `go-tangra-inventory-v4/internal/ingest/validate.go` called from `SubmitInventory` before `snaps.Ingest` (bounds from contracts/agent-collection.md)
- [ ] T018 Projection + digest in `go-tangra-inventory-v4/internal/hostreport/hostreport.go` (`Project`, `Digest`)
- [ ] T019 Repo: `SetReportDigest`, `ListReportTenants` (system scope), `ListHostReportRows` (tenant scope, keyset on `(report_changed_at, id)`) in `go-tangra-inventory-v4/internal/repo/repo.go`, `go-tangra-inventory-v4/internal/repo/repodb/db.go`, `go-tangra-inventory-v4/internal/memstore/memstore.go`; digest update in `go-tangra-inventory-v4/internal/snapshots/ingest.go`
- [ ] T020 `HostReportService` server in `go-tangra-inventory-v4/internal/grpcapi/hostreports.go` (caller + consumer allow-list for `ListReportTenants`, page byte budget, opaque cursor); config `host_reports{consumers, max_page_bytes}` with validation in `go-tangra-inventory-v4/internal/config/config.go`; registration in `go-tangra-inventory-v4/internal/app/app.go`
- [ ] T021 SDK methods and plain-Go types in `go-tangra-inventory-v4/sdk/pkg/inventoryclient/hostreport.go` per contracts/inventory-grpc.md
- [ ] T022 [P] Policy rule `ipam-hostsync` in `go-tangra-inventory-v4/deploy/policy.yaml` per contracts/policy.md; policy parse test in `go-tangra-inventory-v4/internal/app/policy_test.go` asserting the three RPCs + health and nothing else for `svc/ipam`

### Tests first — IPAM

- [X] T023 [P] Migration test `internal/repo/repodb/hostsync_integration_test.go` (`//go:build integration`): database at 0003 with devices/addresses/subnets → 0004 applies; defaults (`source='manual'`, `update_status='unknown'`, report states `''`); CHECKs reject bad `source`/`report_state`/`update_status`/`full_interval_minutes`; partial UNIQUE on `(tenant_id, inventory_host_id)`; `devices_hypervisor_not_self`; RLS on `ipam_hypervisor_guests`, `ipam_hostsync_settings`, `ipam_hostsync_device_state` isolates tenants and admits `app.system`
- [X] T024 [P] `internal/ipnet/ipnet_test.go` (100 %): `MostSpecific` (longest prefix, nested, no match, ties → lowest id, v4/v6 mixed), `NetworkOf` (v4/v6, /32→/32, /128→/64 fallback rule helper), `Classify` (global, ULA, loopback, link-local v4/v6, multicast, unspecified, IPv4-mapped IPv6); `FuzzMostSpecific` in `internal/ipnet/ipnet_fuzz_test.go` (result always contains the address and no longer containing prefix exists)
- [X] T025 [P] Report validation tests `internal/hostreport/normalize_test.go` (100 %): valid report normalises (MAC canonical form, addresses from `addresses` or, for old agents, from `ip_addresses` CIDRs, kinds closed set); **negative**: report tenant ≠ requested tenant → rejected; non-uuid host id → rejected; zero/broadcast/multicast MACs dropped; bad IPs/prefixes dropped; control characters and over-long hostnames/interface names dropped with issues; 257 interfaces/1025 addresses/1001 guests/5001 packages → first N kept + `truncated_*` issues; inventory truncation counters surfaced as issues; `FuzzNormalizeReport` in `internal/hostreport/normalize_fuzz_test.go` (no panic, every output within bounds, output strings valid UTF-8 without control chars)
- [X] T026 [P] Exclusion pattern tests `internal/hostreport/exclude_test.go`: default list, `path.Match` semantics, loopback kind always excluded, invalid patterns refused; `FuzzExclusionPattern`
- [X] T027 [P] Client tests `internal/invclient/invclient_test.go`: `Mesh` over a fake `inventoryclient` server — paging to exhaustion, every error → `ErrUnavailable`, `PermissionDenied`/`Unimplemented` mapped to distinct status codes (`permission_denied`, `inventory_outdated`), per-call timeout honoured, concurrent first use dials once (race detector); `Fake` (tenant scoping, `Down`)
- [X] T028 [P] Store tests for `HostSyncStore` (settings ensure/get/update with audit row in the same tx, list system scope, `ApplyHostReport` aborts with `ErrSyncDisabled` when disabled, audit rows rolled back with a failed apply) in `internal/memstore/hostsync_test.go` and `internal/repo/repodb/hostsync_integration_test.go`
- [X] T029 [P] Config tests `internal/config/config_test.go`: `host_sync` defaults and bounds (poll 10–3600, workers 1–8, page 1–200, conflict_moves 2–100, window 1–168 h, max_macs_per_port 1–256), unknown keys rejected
- [X] T030 [P] Audit tests `internal/audit/audit_test.go`: new types and subject kinds known and valid; detail keys used by the sync (`previous_device_id`, `bmc_address`, `inventory_host_id`, `changes`) survive the guard; a key named `previous_owner` would be dropped (documents the guard)
- [X] T031 [P] Proto/mapper tests `internal/grpcapi/grpcapi_mapper_more_test.go`: new Device/DeviceInterface/IPAddress/Subnet fields map to proto; **negative**: `Create`/`Update` RPCs ignore server-owned fields (`source`, `inventory_host_id`, `report_state`, `hypervisor_device_id`, `previous_device_id`, `moved_at`, `origin`) sent by a caller

### Implementation — IPAM

- [X] T032 Migration `internal/store/migrations/0004_host_sync.sql` per data-model §2.1
- [X] T033 [P] Models and constants per data-model §2.3 in `internal/store/models.go`; column lists and scans for the new columns in `internal/repo/repodb/db.go` (existing CRUD keeps admin writes working, server-owned fields not writable through `UpdateDevice`/`UpdateAddress` from the API)
- [X] T034 [P] `MostSpecific`, `NetworkOf`, `Classify` in `internal/ipnet/ipnet.go`
- [X] T035 [P] `internal/hostreport/normalize.go` and `internal/hostreport/exclude.go` (D15, data-model §4)
- [X] T036 [P] `internal/invclient/invclient.go` (`Client` interface: `ListReportTenants`, `ListHostReports`, `GetHostReport`; `Mesh` with lazy, mutex-guarded dial via `Freya.Client(ctx, cfg.HostSync.InventoryService)`, `MaxCallRecvMsgSize` 8 MiB; `Fake`)
- [X] T037 `repo.HostSyncStore` + `HostTx` in `internal/repo/repo.go`; implementations `internal/repo/repodb/hostsync.go` (advisory xact lock, `FOR SHARE` on settings, transactional `AppendAudit`) and `internal/memstore/hostsync.go`
- [X] T038 [P] `host_sync` config section with validation in `internal/config/config.go`
- [X] T039 [P] Audit vocabulary and subject kinds per contracts/audit-events.md in `internal/audit/audit.go`
- [X] T040 [P] Proto fields per contracts/ipam-grpc.md in `sdk/api/proto/ipam/v1/ipam.proto`; `make generate`; mappers in `internal/grpcapi/mapper.go`
- [X] T041 [P] Realtime event constants and payload helpers (`ipam.hostsync.applied`, `ipam.hostsync.status`, address `action` values) in `internal/events/events.go`

**Checkpoint**: inventory serves `HostReportService` with projections of the existing (thin) data; IPAM has schema 0004, validated reports and a client. No host is synced yet.

---

## Phase 3: User Story 1 — Hosts and their addresses appear in IPAM automatically (Priority: P1) 🎯 MVP

**Goal**: rich network data from the agent; IPAM creates/updates devices, interfaces, addresses and subnets from reports, moves/releases addresses, never touches administrator fields, audits everything.

**Independent Test**: enroll an agent on a test host → IPAM shows device, interfaces, addresses (each in a subnet, one primary) and an audit trail; change an address on the host → the next report updates IPAM (quickstart steps 1–4).

### Tests for User Story 1 (MANDATORY) ⚠️

- [ ] T042 [P] [US1] Netlink/sysfs parser tests `go-tangra-inventory-v4/internal/agentfacts/network_test.go`: recorded `RTM_NEWADDR`/`RTM_NEWROUTE` messages → addresses with prefix, dhcp (`!IFA_F_PERMANENT`, IPv4), temporary, deprecated, scope; default routes → gateway, interface, metric; primary selection (lowest metric, global, non-temporary); sysfs trees (`testdata/sysfs/*`) → kind (bond/bridge/vlan/wireless/virtual/loopback/ethernet), speed, master, VLAN id; bounds 256/64 with `truncated`; `FuzzNetlinkAddr` in `go-tangra-inventory-v4/internal/agentfacts/network_fuzz_test.go` (arbitrary bytes never panic, never exceed bounds)
- [ ] T043 [P] [US1] Windows mapping tests `go-tangra-inventory-v4/internal/agentfacts/adapters_test.go`: pure `FromAdapters([]Adapter)` over recorded adapter structs (IfType kinds, Multiplexor → bond, vEthernet → virtual, DHCP flag, `SuffixOrigin` random → temporary, gateway, metric-based primary)
- [ ] T044 [P] [US1] Diff tests `go-tangra-inventory-v4/internal/diff/diff_test.go`: network changes on `type`, `speed_bps`, `gateway`, `dhcp`, `addresses` recorded; `FuzzDiff` extended with the new network fields
- [X] T045 [P] [US1] Planner match tests `internal/hostplan/match_test.go`: by `inventory_host_id`; else unique serial (placeholder serials `To Be Filled By O.E.M.`, `0`, `Default string`, all-zero never match); else non-generic hostname (`localhost`, `ubuntu`… never match); device linked to another inventory host never matched; two serial candidates → create; renamed host → rename; name collision → `<hostname> (<8 chars>)`
- [X] T046 [P] [US1] Planner device/interface tests `internal/hostplan/device_test.go`: create with reported identity/hardware/OS fields, `source=host_report`, `report_state=reported`; update changes only the D8 column set; device type rules (unknown → server on create/unchanged on update; physical keeps an admin `workstation`); interfaces created/updated (name, MAC, kind, speed, enabled), absent ones `not_reported`, re-appearing `reported`; excluded interfaces (docker0, veth*, tenant pattern) produce nothing; **negative (SC-004)**: description, tags, location, rack, asset tag, status, contact, `ipmi_secret_ref`, firmware, interface description never appear in any op, for any input
- [X] T047 [P] [US1] Planner address tests `internal/hostplan/address_test.go`: new address in the most specific subnet; nested subnets; no containing subnet → subnet op (`origin=host_sync`, name = CIDR, ` (auto)` on collision, parent = most specific container) then address; IPv6 /128 → /64 subnet; IPv4 /32 → /32; link-local/loopback/temporary/deprecated skipped; primary flag from `primary_ipv4/6`; address of another device → move with `previous_device_id`, `move_count` +1; unowned address → claim; `conflict_moves` moves in the window → conflict op; absent address → release (device NULL, `previous_device_id`, `not_reported`); **negative**: address description/note/tags/dns_name/ptr/status of existing rows never changed
- [X] T048 [P] [US1] Planner property tests `internal/hostplan/plan_test.go` + `FuzzPlan` in `internal/hostplan/plan_fuzz_test.go`: applying a plan then re-planning the same report yields zero ops (idempotence); every op carries an audit entry with before/after; admin-field invariant holds for random reports and random existing state; op count bounded by report bounds
- [X] T049 [P] [US1] Runner tests `internal/hostsync/runner_test.go` (memstore + `invclient.Fake`): poll picks up tenants from `ListReportTenants`, creates default settings for a new tenant, applies FULL reports, advances the tenant watermark only after success, failed host keeps the watermark and records `hosts_failed`; digest equal → skipped; reconcile fetches DIGEST view, applies mismatches, marks devices of deleted/retired hosts `not_reported` (addresses keep device link); inventory `Down` → status degraded, nothing written, back-off grows to 10 min and resets on success; `Unimplemented` → `inventory_outdated`; global `host_sync.enabled=false` → nothing runs; tenants processed in parallel ≤ workers, hosts of one tenant sequential
- [ ] T050 [P] [US1] Apply/store integration `internal/repo/repodb/hostsync_integration_test.go`: a planned report applied in one transaction (device, interfaces, subnets, addresses, device state, audit rows); forced failure mid-apply rolls back everything incl. audit; concurrent apply of two hosts of one tenant serialised by the advisory lock; unique-violation race on subnet creation retried once; **negative (cross-tenant)**: an apply for tenant A cannot read or write tenant B rows (RLS), and a report whose tenant ≠ A is refused before the transaction
- [X] T051 [P] [US1] Scan guard test `internal/scan/executor_test.go`: SNMP discovery of a device named like a host-reported device only fills empty fields and bumps `last_seen` (management IP, type, OS kept); new SNMP devices get `source=scan`
- [X] T052 [P] [US1] Events test `internal/hostsync/events_test.go`: after commit, created/moved/released addresses publish `ipam.ip_address.created|updated` with `action`; nothing published on rollback
- [ ] T053 [US1] End-to-end integration `internal/hostsync/e2e_integration_test.go` (`//go:build integration`): PostgreSQL (testcontainers) + inventory `HostReportService` from `go-tangra-inventory-v4` (SDK server wiring with memstore) over the framework test mesh with SPIFFE test identities; submit a snapshot for a host in tenant A → IPAM device, interfaces, addresses, auto subnet, audit rows; change an address → moved/released; tenant B sees nothing; IPAM identity not allowed by policy → degraded, nothing written

### Implementation for User Story 1

- [ ] T054 [P] [US1] Pure network parsing in `go-tangra-inventory-v4/internal/agentfacts/network.go` (netlink attribute decoding with `syscall.ParseNetlinkMessage`, sysfs kind rules, primary selection, bounds) and `go-tangra-inventory-v4/internal/agentfacts/adapters.go` (Windows adapter mapping)
- [ ] T055 [US1] Collectors `go-tangra-inventory-v4/internal/collector/network_linux.go` (`syscall.NetlinkRIB(RTM_GETADDR/RTM_GETROUTE)`, sysfs reads, 5 s budget), `network_windows.go` (`windows.GetAdaptersAddresses`), `network_other.go` (gopsutil fallback, today's behaviour); replace `collectNetworks` in `go-tangra-inventory-v4/internal/collector/osinfo.go` and set `OSInfo.Family`, `PrimaryIPv4/6`, `Truncated` in `go-tangra-inventory-v4/internal/collector/collector.go`
- [ ] T056 [P] [US1] Network diff comparisons in `go-tangra-inventory-v4/internal/diff/diff.go`; fill `type/speed_bps/gateway/dhcp/up` in the component insert of `go-tangra-inventory-v4/internal/repo/repodb/db.go` (`insertComponents`)
- [ ] T057 [P] [US1] Inventory host view shows kind, speed, gateway, DHCP and per-address flags in `go-tangra-inventory-v4/ui/src/views/hosts/detail.vue` and `go-tangra-inventory-v4/ui/src/api/types.ts`; vitest in `go-tangra-inventory-v4/ui/tests/unit/hosts.spec.ts`
- [X] T058 [US1] Planner in `internal/hostplan/match.go`, `internal/hostplan/device.go`, `internal/hostplan/address.go`, `internal/hostplan/plan.go` (ops + audit entries + event intents; conflict window from config)
- [X] T059 [US1] `HostTx` operations for devices, interfaces, subnets, addresses, device state and audit in `internal/repo/repodb/hostsync.go` and `internal/memstore/hostsync.go` (update statements list exactly the D8 columns)
- [X] T060 [US1] Runner, poller and reconcile in `internal/hostsync/runner.go`, `internal/hostsync/poller.go`, `internal/hostsync/reconcile.go` (watermarks, digests, back-off, pacing, run id, `hostsync_run` audit, events after commit)
- [X] T061 [P] [US1] Metrics (`Freya.Metrics().Meter("ipam.hostsync")`) and structured logs in `internal/hostsync/metrics.go`
- [X] T062 [US1] D9 guard in `UpsertDeviceByName` (`internal/repo/repodb/db.go`, `internal/memstore/memstore.go`) and `source=scan` in `internal/scan/executor.go` `persistDevice`
- [X] T063 [US1] Wiring in `internal/app/app.go`: `invclient.Mesh`, `hostsync.New(...)`, worker appended to `a.workers`, disabled when `host_sync.enabled=false` (startup log line)
- [ ] T064 [P] [US1] Device/address JSON and list filters (`source`, `report_state`, `conflict`) in `internal/httpapi/handlers.go`, `internal/devices/devices.go`, `internal/addresses/addresses.go`; OpenAPI query parameters in `api/openapi/ipam.yaml`; `npm run gen:api` → `ui/src/api/schema.d.ts`
- [ ] T065 [P] [US1] UI: interface kind and "not reported" badges, address "not reported"/"moved from" in `ui/src/views/devices/detail.vue`; conflict and report-state columns/filters in `ui/src/views/addresses/index.vue`; types in `ui/src/api/types.ts`; vitest in `ui/tests/unit/views.spec.ts`

**Checkpoint**: US1 works end to end (quickstart 1–4); the sync can already be switched off with `host_sync.enabled=false`. MVP.

---

## Phase 4: User Story 6 — Visibility and control of the sync (Priority: P2)

**Goal**: per-device source/last report, re-sync one/all, tenant enable/disable (immediate), exclusions, status, audit of settings — administered through the gateway.

**Independent Test**: device view shows "reported by inventory, last at …"; manual re-sync updates it; with sync disabled new reports change nothing; audit lists every change (quickstart 8).

### Tests for User Story 6 (MANDATORY) ⚠️

- [X] T066 [P] [US6] Manifest tests `pkg/ipammanifest/manifest_test.go`: `hostsync:manage` declared, granted to owner/admin and the administrator module role, **not** to operator/member/auditor or the operator/viewer module roles; every new route declares a known permission (contracts/ipam-http.md table); ability `{manage, HostSync}` and nav entry present
- [X] T067 [P] [US6] HTTP tests `internal/httpapi/hostsync_test.go`: GET/PUT settings (validation, strict decode, audit `hostsync_settings_updated` with before/after, actor user), status, resync-all → 202 and `reconcile_requested`, device resync (applies latest report, 409 `not_host_reported`, 409 `host_sync_disabled`, 503 when inventory down), device host-sync info with issues, clear-conflict; **negative**: missing tenant/subjects → refused; CSRF parameter required on writes; oversized bodies → 413; unknown JSON fields → 400
- [X] T068 [P] [US6] OpenAPI contract test `api/openapi/openapi_test.go`: new paths, schemas (`additionalProperties: false`, patterns, bounds), body limits and permissions as in contracts/ipam-http.md
- [X] T069 [P] [US6] Disable race test `internal/repo/repodb/hostsync_integration_test.go` (SR-006): an apply holding `FOR SHARE` blocks the disabling update; after the update commits, every subsequent apply returns `ErrSyncDisabled` and writes no rows; re-enable sets `reconcile_requested` and the next runner cycle applies the latest reports (US6 scenario 2)
- [X] T070 [P] [US6] Runner tests `internal/hostsync/resync_test.go`: re-sync one host ignores the digest shortcut and records `trigger=resync:<user>`; resync-all forces a reconcile with full apply; audit `hostsync_resync_requested`; tenant disabled → runner skips it and status `disabled`
- [ ] T071 [P] [US6] UI tests `ui/tests/unit/hostsync.spec.ts`: settings form read-only without `manage HostSync` ability, pattern validation, status rendering (ok/degraded/disabled); device view shows source, inventory host, last report, re-sync button only with `devices:manage` ability; issues list rendered as text (no HTML injection from reported names)

### Implementation for User Story 6

- [X] T072 [US6] Permission, ability, nav in `pkg/ipammanifest/manifest.go`; OpenAPI paths/schemas in `api/openapi/ipam.yaml`
- [X] T073 [US6] Service layer `internal/hostsync/admin.go` (settings get/update with audit, status, resync one/all, clear-conflict; `authz.RequireTenant`)
- [X] T074 [US6] HTTP handlers `internal/httpapi/hostsync.go` registered from `internal/httpapi/handlers.go`; `Deps.HostSync` in `internal/httpapi/deps.go`; error mapping (`not_host_reported`, `host_sync_disabled`, `temporarily_unavailable`)
- [X] T075 [P] [US6] Store support for status aggregates (`devices_not_reported`, `addresses_in_conflict`) and device host-sync info in `internal/repo/repodb/hostsync.go` and `internal/memstore/hostsync.go`
- [ ] T076 [P] [US6] UI page `ui/src/views/hostsync/index.vue`, store `ui/src/stores/hostsync.ts`, route in `ui/src/remote/routes.ts`, nav in `ui/src/remote/nav.ts`; device summary (source, inventory host, last report, issues) and re-sync action in `ui/src/views/devices/detail.vue`; source column/filter in `ui/src/views/devices/index.vue`; conflict clear action in `ui/src/views/addresses/index.vue`

**Checkpoint**: US1 + US6 — automatic writes are observable and controllable per tenant.

---

## Phase 5: User Story 2 — Out-of-band management (BMC/IPMI) is recorded (Priority: P2)

**Goal**: the agent reads BMC LAN settings without credentials; IPAM records the management address, BMC ports and the BMC address in its subnet.

**Independent Test**: on a server with a BMC the device's management address is the BMC address and its interfaces include the BMC ports (quickstart 5).

### Tests for User Story 2 (MANDATORY) ⚠️

- [ ] T077 [P] [US2] BMC decode tests `go-tangra-inventory-v4/internal/agentfacts/bmc_test.go`: LAN parameter responses (IP, source, MAC, mask → prefix, gateway, VLAN) → `Bmc`; first channel with an IP wins; zero MACs skipped; ≤ 8 ports; `FuzzBmcLanParams` in `go-tangra-inventory-v4/internal/agentfacts/bmc_fuzz_test.go`
- [ ] T078 [P] [US2] Collector test `go-tangra-inventory-v4/internal/collector/bmc_linux_test.go` with a fake IPMI client: **negative (SR-005)**: the set of requested commands is exactly Get Channel Info + LAN parameters {3,4,5,6,12,20}; `GetLanConfig`/`GetLanConfigParams`, parameter 16 and any user/password/cipher command are never called; no `/dev/ipmi*` → `bmc` absent; timeout (10 s budget) → absent; `collect_bmc: false` → never opened
- [ ] T079 [P] [US2] Proto negative test `go-tangra-inventory-v4/sdk/pkg/inventoryclient/bmc_contract_test.go`: `Bmc`/`BmcPort` descriptors contain no field whose name matches `pass|user|secret|community|cipher|key|auth` (guards future edits)
- [X] T080 [P] [US2] Planner tests `internal/hostplan/bmc_test.go`: BMC with address → `management_ip`, interfaces `bmc`, `bmc-2`… (kind `management`, MAC), BMC address recorded in its subnet (auto subnet from prefix; missing prefix → /24) linked to interface `bmc`, not primary, hostname empty; BMC absent or unreadable → no BMC op and existing `management_ip`/BMC interfaces untouched; `ipmi_secret_ref` never changed; audit detail keys use `bmc_*`

### Implementation for User Story 2

- [ ] T081 [P] [US2] `go-tangra-inventory-v4/internal/agentfacts/bmc.go` (parameter decoding, selection)
- [ ] T082 [US2] `go-tangra-inventory-v4/internal/collector/bmc_linux.go` (go-ipmi `NewOpenClient`, `GetChannelInfo`, `GetLanConfigParamFor` per allowed parameter, 10 s budget) and `bmc_other.go`; `AgentConfig.CollectBMC` in `go-tangra-inventory-v4/internal/config/config.go`; `go get github.com/bougou/go-ipmi@v0.8.1` in `go-tangra-inventory-v4/go.mod`
- [ ] T083 [P] [US2] BMC diff category in `go-tangra-inventory-v4/internal/diff/diff.go`; BMC section in `go-tangra-inventory-v4/ui/src/views/hosts/detail.vue`
- [X] T084 [US2] BMC ops in `internal/hostplan/bmc.go` and their store execution in `internal/repo/repodb/hostsync.go` / `internal/memstore/hostsync.go`
- [ ] T085 [P] [US2] UI: management interface badge and BMC address in `ui/src/views/devices/detail.vue`

**Checkpoint**: US2 demonstrable independently of US3/US4.

---

## Phase 6: User Story 3 — Virtual machines, containers and their hypervisors (Priority: P2)

**Goal**: device type and virtualization kind from the agent; Proxmox guests reported and linked to their hypervisor both ways.

**Independent Test**: a Proxmox host with two guests (one with the agent, one without) → the reporting guest is linked, the other listed by name and MAC (quickstart 6).

### Tests for User Story 3 (MANDATORY) ⚠️

- [ ] T086 [P] [US3] Virtualization rule tests `go-tangra-inventory-v4/internal/agentfacts/virt_test.go`: table of fact sets (dockerenv, containerenv, `container=lxc`, cgroup kubepods, WSL, Xen domU vs dom0, SMBIOS vendors incl. Hyper-V "Virtual Machine", EC2, cpuinfo flag only, physical, unreadable SMBIOS → unknown); a KVM hypervisor host with `kvm` loaded stays physical
- [ ] T087 [P] [US3] Proxmox parser tests `go-tangra-inventory-v4/internal/agentfacts/proxmox_test.go`: qemu `name:`/lxc `hostname:`, `net0: virtio=AA:BB…,bridge=vmbr0`, `hwaddr=`, several NICs, snapshot sections ignored, VMID file-name validation, file > 64 KiB and > 1000 guests bounded; `FuzzProxmoxConf` in `go-tangra-inventory-v4/internal/agentfacts/proxmox_fuzz_test.go`; collector test with a temp `/etc/pve` tree never reads `/etc/pve/priv` in `go-tangra-inventory-v4/internal/collector/guests_linux_test.go`
- [X] T088 [P] [US3] Planner tests `internal/hostplan/virt_test.go`: role vm/container → `device_type` vm/container + `virtualization_kind`; physical over existing `vm` → server; guests replaced on the host; guest MAC matching exactly one other device's interface → `hypervisor_linked`; duplicate MAC → no link + `duplicate_mac` issue; guest's own report matches a stored guest MAC → linked (report order independent); guest no longer reported → `hypervisor_unlinked`; host never linked to itself
- [X] T089 [P] [US3] Store/API tests: `internal/repo/repodb/hostsync_integration_test.go` (GIN MAC lookup, cascade on host delete, `ON DELETE SET NULL` on guest device) and `internal/httpapi/hostsync_test.go` (`GET /devices/{id}/guests` lists matched and unmatched guests, tenant-scoped)

### Implementation for User Story 3

- [ ] T090 [P] [US3] `go-tangra-inventory-v4/internal/agentfacts/virt.go` and `go-tangra-inventory-v4/internal/agentfacts/proxmox.go`
- [ ] T091 [US3] Collectors `go-tangra-inventory-v4/internal/collector/virt.go` (SMBIOS facts, both platforms), `virt_linux.go` (file facts), `guests_linux.go`, `guests_other.go`; wiring in `go-tangra-inventory-v4/internal/collector/collector.go`
- [ ] T092 [P] [US3] Diff categories `virtualization`, `guest` in `go-tangra-inventory-v4/internal/diff/diff.go`; virtualization and guests in `go-tangra-inventory-v4/ui/src/views/hosts/detail.vue`
- [X] T093 [US3] Planner `internal/hostplan/virt.go`; guest store ops (`ReplaceGuests`, `FindGuestsByMAC`, `SetHypervisor`) in `internal/repo/repodb/hostsync.go` and `internal/memstore/hostsync.go`; `guest_count` computed in device reads
- [X] T094 [US3] `GET /devices/{id}/guests` in `internal/httpapi/hostsync.go` + `api/openapi/ipam.yaml`
- [ ] T095 [P] [US3] UI: Guests tab (matched → link, unmatched → name/VMID/MACs), "runs on" hypervisor link and virtualization kind in `ui/src/views/devices/detail.vue`; vitest in `ui/tests/unit/views.spec.ts`

**Checkpoint**: US3 demonstrable independently.

---

## Phase 7: User Story 4 — Update state and pending updates (Priority: P2)

**Goal**: Linux hosts report reboot-required, automatic updates and pending/security updates; IPAM shows them; unknown is never shown as up to date.

**Independent Test**: a host with pending updates lists them with versions and security marks; after updating and rebooting the next report clears them (quickstart 7).

### Tests for User Story 4 (MANDATORY) ⚠️

- [ ] T096 [P] [US4] Parser tests `go-tangra-inventory-v4/internal/agentfacts/updates_test.go` with recorded outputs (`testdata/updates/*`): apt `Inst` lines incl. `-security` origins, held/kept-back lines ignored; dnf/yum `check-update` columns + `updateinfo` security set (arch/epoch stripping); apk `-u list`; pacman `checkupdates`; needrestart/needs-restarting/reboot file rules; apt-config/systemctl automatic-update rules; bounds 5000 + truncation; `FuzzAptSimulate`, `FuzzDnfOutput`, `FuzzApkPacmanOutput` in `go-tangra-inventory-v4/internal/agentfacts/updates_fuzz_test.go`
- [ ] T097 [P] [US4] Collector tests `go-tangra-inventory-v4/internal/collector/updates_linux_test.go` with a fake command runner: **negative**: no list refresh (`apt-get update`, `dnf makecache`, `apk update`) is ever executed with `refresh_package_lists: false`; with `true` at most once per 24 h; every command runs with a context deadline (60 s) and the whole collection stops at `update_timeout_seconds`; commands are executed without a shell and with `LANG=C`; unknown manager → `unsupported`; command failure → `error`
- [X] T098 [P] [US4] Planner tests `internal/hostplan/updates_test.go`: pending updates replace the device packages (`needs_update`, `is_security_update`, versions, manager); `update_status`, `reboot_required`, `unattended_upgrades` set; Windows/old agent → `unknown` and packages untouched; `unsupported` shown as such; 5001 packages → 5000 + issue; one `packages_updated` audit event with bounded name lists
- [X] T099 [P] [US4] Integration `internal/repo/repodb/hostsync_integration_test.go`: batched package replace of 5000 rows inside the host transaction stays under the per-host budget (≤ 1 s on CI)

### Implementation for User Story 4

- [ ] T100 [P] [US4] `go-tangra-inventory-v4/internal/agentfacts/updates.go`
- [ ] T101 [US4] `go-tangra-inventory-v4/internal/collector/updates_linux.go` (manager detection, command runner interface, deadlines, optional refresh with timestamp in the agent state file) and `updates_other.go`; `AgentConfig` `collect_updates`, `refresh_package_lists`, `update_timeout_seconds` in `go-tangra-inventory-v4/internal/config/config.go`; `Program.available_version/security_update` merge in `go-tangra-inventory-v4/internal/collector/collector.go`
- [ ] T102 [P] [US4] Diff category `update` and software available-version comparison in `go-tangra-inventory-v4/internal/diff/diff.go`; update state in `go-tangra-inventory-v4/ui/src/views/hosts/detail.vue`
- [X] T103 [US4] Planner `internal/hostplan/updates.go`; batched `ReplacePendingPackages` in `internal/repo/repodb/hostsync.go` and `internal/memstore/hostsync.go`
- [ ] T104 [P] [US4] UI: update status chip (unknown ≠ up to date), reboot required, automatic updates, security highlighting and "security only" filter in `ui/src/views/devices/detail.vue`; device list column in `ui/src/views/devices/index.vue`; vitest in `ui/tests/unit/views.spec.ts`

**Checkpoint**: US4 demonstrable independently.

---

## Phase 8: User Story 5 — Which switch port a host is connected to (Priority: P3)

**Goal**: correlate reported host MACs with SNMP FDB/LLDP data to link host interfaces with switch ports (and VLAN), ignoring uplinks.

**Independent Test**: after an SNMP scan of a switch whose MAC table contains a reported host MAC, the host interface shows "connected to <switch> <port>" (quickstart 9).

### Tests for User Story 5 (MANDATORY) ⚠️

- [X] T105 [P] [US5] Regression test for the link uniqueness defect `internal/repo/repodb/links_integration_test.go` (`//go:build integration`): before 0005 a switch port with two FDB MACs fails `ReplaceInterfaceLinks` (documents the bug); after 0005 it stores both and `persistDevice` continues with the next interfaces; exactly one unique constraint existed before the migration
- [X] T106 [P] [US5] Ranking tests `internal/portlink/rank_test.go`: per switch the fewest-MAC port; port over `max_macs_per_port` ignored; port that learned another switch's MAC or has a switch LLDP neighbour = uplink, ignored; daisy chain → globally fewest wins; tie → no link; LLDP naming the host (sysName or port id = MAC/name) overrides FDB; VLAN carried; `FuzzRank` in `internal/portlink/rank_fuzz_test.go`
- [X] T107 [P] [US5] Apply tests `internal/portlink/portlink_test.go` (memstore): host interface flat link columns set, `port_linked` audit, re-confirmation refreshes `link_last_seen`, links older than `link_stale_days` cleared with `port_unlinked`; switch-port reverse lookup returns the host; only host-reported devices are linked; tenant-scoped (**negative**: FDB data of tenant B never links tenant A hosts)
- [X] T108 [P] [US5] Hook tests `internal/scan/executor_test.go`: correlation runs after a completed scan with SNMP for that tenant and not after a cancelled/failed one; `internal/hostsync/runner_test.go`: correlation runs after a host-sync run for tenants with switches

### Implementation for User Story 5

- [X] T109 [US5] Migration `internal/store/migrations/0005_interface_links_unique.sql` per data-model §2.2; memstore uniqueness aligned in `internal/memstore/memstore.go`
- [X] T110 [US5] `internal/portlink/rank.go` and `internal/portlink/portlink.go` (load FDB/LLDP links and host interfaces in tenant scope, rank, apply with audit)
- [X] T111 [US5] Hooks: after `ScanCompleted` in `internal/scan/executor.go` (via a `LinkCorrelator` interface on `scan.Service`) and after each tenant run in `internal/hostsync/runner.go`; wiring in `internal/app/app.go`
- [ ] T112 [P] [US5] Device interface reads include `remote_device_name` and switch-port "device behind" in `internal/repo/repodb/db.go` / `internal/memstore/memstore.go`; UI "Connected to" column (switch, port, VLAN, source) and switch-port "device behind" in `ui/src/views/devices/detail.vue`; vitest in `ui/tests/unit/views.spec.ts`

**Checkpoint**: all stories functional.

---

## Phase 9: Polish, Release & Cross-Cutting Concerns

- [ ] T113 [P] Docs: `README.md` (host sync overview, config `host_sync`, permissions incl. `hostsync:manage`, audit vocabulary, dependency on inventory ≥ 4.3.0), `deploy/README.md` (policy note), `deploy/policy.yaml` header comment
- [ ] T114 [P] Docs: `go-tangra-inventory-v4/README.md` and `go-tangra-inventory-v4/deploy/README.md` (new agent collection, `collect_*` options, root + `ipmi_devintf`/`ipmi_si` for BMC, no package list refresh by default, `HostReportService`, `host_reports.consumers`, policy rule); correct the claim that `-service install` creates a systemd unit (it does not) and document a systemd unit example
- [ ] T115 [P] Load generator `go-tangra-inventory-v4/tests/loadgen/main.go` (synthetic enrolled hosts submitting realistic reports) and benchmark `internal/hostsync/bench_integration_test.go` (`//go:build integration`): 1000 hosts in one tenant synced < 15 min with p95 apply ≤ 150 ms while concurrent `GET /devices` p95 < 200 ms (SC-006)
- [X] T116 [P] Additional negative security tests `internal/hostsync/security_test.go`: forged report claiming another tenant's host id, report with every string at max length and Unicode control characters, 10× oversized lists, report for a host id already linked to a device in another tenant (independent per tenant), inventory returning a page larger than requested
- [ ] T117 SC-007 capability checklist `specs/020-ipam-host-sync/checklists/v3-parity.md` (each v3 client capability from spec Context → task/test proving it)
- [ ] T118 Security and constitution review of both repos (all seven principles, STRIDE table in research.md re-checked against the code; second reviewer for `internal/hostreport`, `internal/hostplan`, inventory `internal/ingest` and `internal/grpcapi/hostreports.go`)
- [ ] T119 Gates — inventory: `make lint`, `make test`, `make cover` (100 % set incl. `internal/hostreport`, ≥ 80 % total), `make fuzz`, `make test-integration`, `make proto-check`, `make vuln` (go-ipmi reviewed), agent cross-compile, UI lint/unit/build
- [ ] T120 Gates — ipam: `make lint`, `make test`, `make cover` (100 % for `internal/authz`, `internal/sealed`, `internal/ipnet`, `internal/hostreport`, `internal/hostplan`; ≥ 80 % total), `make fuzz`, `make test-integration`, `buf lint` + `buf breaking` (sdk/v4.0.0), `make vuln`, `cd ui && npm run lint && npm run test:unit && npm run build`
- [ ] T121 Release **inventory v4.3.0** and inventory SDK **`sdk/v4.1.0`** — PR, CI, tags (**confirm with the user**); then replace the `go.mod` `replace` in this repo with `github.com/go-tangra/go-tangra-inventory/sdk/v4 v4.1.0` and re-run T120
- [ ] T122 Release **ipam v4.3.0** and ipam SDK **`sdk/v4.1.0`** — PR, CI, tags (**confirm with the user**)
- [ ] T123 Production notes for **go-tangra-docker** (**confirm with the user; not part of this repo's change**): add the `ipam-hostsync` rule to `go-tangra-docker/policies/inventory.yaml` (propagated to `prod/policies/` by `prod-init.sh`), header comment in `go-tangra-docker/policies/ipam.yaml`, pin inventory 4.3.0 and ipam 4.3.0, optional `host_sync` config, BMC kernel modules on hosts
- [ ] T124 Run quickstart.md in freya-stack (new agent on a Linux/Proxmox host with BMC and on a Windows host); record results in `specs/020-ipam-host-sync/quickstart-results.md`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (T001–T004)** → **Foundational (T005–T041)** → user stories.
- Inside Foundational, inventory (T005–T022) and IPAM (T023–T041) run in
  parallel; IPAM's `invclient` (T036) and e2e tests need the proto/SDK from
  T013/T021 (local `replace`, T003).
- **US1 (T042–T065)** depends on Foundational. **US6 (T066–T076)** depends on
  US1's runner/store (T058–T060). **US2, US3, US4** depend on US1's planner
  and store ops and are independent of each other and of US6. **US5
  (T105–T112)** depends on US1 (host interfaces with MACs) and uses the
  existing scan; it is independent of US2–US4 and US6.
- **Polish/Release (T113–T124)**: T121 (inventory release) before T122; T123
  after both releases; T124 last.

### User Story Dependencies

- US1 (P1): Foundational only.
- US6 (P2): US1.
- US2 (P2), US3 (P2), US4 (P2): US1; each adds agent collection (inventory)
  + planner ops (IPAM) and can ship alone.
- US5 (P3): US1 (+ migration 0005 inside the story).

### Within Each User Story

- Tests first, confirmed failing; agent facts (pure) before collectors;
  planner before store ops; store before runner/API; API before UI.
- Inventory tasks of a story can be merged and released ahead of the IPAM
  tasks of the same story (fields stay unused until IPAM applies them).

### Parallel Opportunities

- T001, T002, T004; T005–T012; T023–T031; T014/T015/T022; T033–T036 and
  T038–T041.
- US1: T042–T052 together; T054, T056, T057 in inventory while T058–T059 run
  in IPAM; T061, T064, T065.
- US2, US3 and US4 in parallel after US1 (different agent files and planner
  files: `bmc.go`, `virt.go`, `updates.go`).
- US5 in parallel with US2–US4 and US6.

---

## Parallel Example: User Story 1

```bash
# Tests (all [P], different files):
Task: "Netlink/sysfs parser tests in go-tangra-inventory-v4/internal/agentfacts/network_test.go"
Task: "Windows mapping tests in go-tangra-inventory-v4/internal/agentfacts/adapters_test.go"
Task: "Planner match tests in internal/hostplan/match_test.go"
Task: "Planner device/interface tests in internal/hostplan/device_test.go"
Task: "Planner address tests in internal/hostplan/address_test.go"
Task: "Planner property tests + FuzzPlan in internal/hostplan/plan_test.go"
Task: "Runner tests in internal/hostsync/runner_test.go"
Task: "Scan guard test in internal/scan/executor_test.go"

# Implementation across repositories:
Task: "Pure network parsing in go-tangra-inventory-v4/internal/agentfacts/network.go"
Task: "Planner in internal/hostplan/{match,device,address,plan}.go"
```

## Parallel Example: P2 stories after US1

```bash
Task: "US2 BMC: go-tangra-inventory-v4/internal/agentfacts/bmc.go + internal/hostplan/bmc.go"
Task: "US3 virtualization/guests: go-tangra-inventory-v4/internal/agentfacts/{virt,proxmox}.go + internal/hostplan/virt.go"
Task: "US4 updates: go-tangra-inventory-v4/internal/agentfacts/updates.go + internal/hostplan/updates.go"
Task: "US6 control: internal/hostsync/admin.go + internal/httpapi/hostsync.go + ui/src/views/hostsync/index.vue"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 Setup, Phase 2 Foundational (inventory contract + IPAM foundation).
2. Phase 3 US1: rich network collection, projection, IPAM sync of devices,
   interfaces, addresses and subnets with moves, releases and audit.
3. **STOP and VALIDATE**: quickstart steps 1–4; the global
   `host_sync.enabled` switch is the control until US6.
4. Demo in freya-stack with a locally built inventory + ipam.

### Incremental Delivery

1. Foundation → US1 (MVP) → US6 (control/visibility: recommended before any
   production rollout) → US2 / US3 / US4 in any order → US5.
2. Each story adds agent collection in inventory and planner ops in IPAM
   without changing earlier behaviour; old agents keep working throughout.
3. Release inventory (server first, then agents) before IPAM; IPAM without
   a new inventory stays `degraded` and writes nothing.

### Parallel Team Strategy

- Developer A: inventory agent + ingest (T013–T022, then agentfacts/collectors per story).
- Developer B: IPAM foundation + planner/runner (T023–T041, T058–T063).
- Developer C: IPAM API/UI (T064–T065, US6 UI, then UI tasks of US2–US5).

---

## Notes

- [P] tasks touch different files and have no dependency on an unfinished task.
- Every change the sync makes must be visible in `ipam_audit_events` (SC-005);
  tests assert audit rows next to data rows.
- Never add a credential field to any report message; T079 guards it.
- Commit after each task or logical group; stop at any checkpoint to validate
  the story on its own.
