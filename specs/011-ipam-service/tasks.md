# Tasks: IPAM Service

**Feature**: 011-ipam-service | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

Organized by phase; user-story phases are independently testable. Tests are
MANDATORY and precede implementation (Constitution IV). `[P]` = parallelizable.
Module path: `github.com/go-freya/freya/services/ipam`. Mirror services/inventory
for platform wiring; add the IPAM active-operations subsystem (scan/ipmi/kvm/warden)
behind interfaces + fakes and a pure `ipnet` allocation library.

## Phase 1: Setup (Shared Infrastructure)

- [X] T001 Create the module skeleton `services/ipam/` per plan.md (cmd/ipamsvc, api/{openapi,proto/ipam/v1}, internal/{app,config,store,repo,memstore,sealed,authz,audit,ipnet,subnets,addresses,devices,vlans,locations,groups,scan,scan/icmp,scan/snmp,scan/tcp,ipmi,kvm,warden,stats,backup,dnscfg,events,stream,httpapi,grpcapi}, pkg/{ipammanifest,ipamclient}, ui, deploy).
- [X] T002 Add `services/ipam/go.mod` (module .../services/ipam, Go 1.26) with replaces for ../.. ../auth ../gateway ../lcm ../warden; add gosnmp/gosnmp, bougou/go-ipmi, golang.org/x/net, gorilla/websocket; seed go.sum from services/inventory.
- [X] T003 [P] Add buf.yaml/buf.gen.yaml + `api/proto/ipam/v1/*.proto` stubs (Subnet/IpAddress/Device/Vlan/Location/IpGroup/HostGroup/IpScan/System services) + Makefile codegen (mirror services/inventory).
- [X] T004 [P] Add `services/ipam/Dockerfile` (build UI, embed -tags ui, build ipamsvc, `setcap cap_net_raw+ep` on the binary for ICMP) + `Makefile` (test/cover/vuln/generate/build/image) mirroring inventory.
- [X] T005 [P] Scaffold `services/ipam/ui/` (Vue 3 + Vite + Vuetify MF remote named `ipam`) from services/inventory/ui (package.json, vite base /m/ipam/, main.ts, api client BASE /api/ipam/v1, remote/{routes,nav}, embed.go/embed_stub.go).

## Phase 2: Foundational (Blocking Prerequisites)

- [X] T006 Typed, validated config `internal/config/config.go` (+ test) — sections db, valkey, kek, warden (endpoint), scan (max_hosts=1024, concurrency, timeout_ms, workers, retry), allocation (skip_first/skip_last, reserved ranges), ipmi (timeout), kvm (token_ttl, session), events, gateway, mesh_enroll, limits_ipam. Default/Load/Validate/Warnings + duration helpers.
- [X] T007 Store migrations `internal/store/migrations/`: 0001_schema.sql (ipam_subnets/ip_addresses/devices/device_interfaces/device_interface_links/device_packages/vlans/locations/ip_groups(+members)/host_groups(+members)/dns_configs with ALL unique constraints from data-model.md), 0002_scan_audit.sql (ipam_ip_scan_jobs queue + ipam_audit_events hypertable), 0003_rls.sql (per-tenant RLS on every table + ipam_app grants).
- [X] T008 Store models + repos `internal/store/{models.go,repos.go}` + interface `internal/repo/repo.go` (all entities; subnet tree, address allocate-insert-on-conflict, scan-job ClaimDueJobs FOR UPDATE SKIP LOCKED, group membership, stats aggregations, tenant ids, audit append).
- [X] T009 [P] In-memory `internal/memstore/memstore.go` implementing repo.Store (filters, unique-conflict simulation, scan-job claim, group members, FailNext) for tests.
- [X] T010 [P] `internal/sealed/` envelope-seal/open + redaction helpers + `sealed_test.go` (100%).
- [X] T011 [P] `internal/authz/` Subjects{TenantID,UserID,Roles,ActorKind} + IsAdmin (admin/owner) + IsPlatformAdmin + RequireTenant/RequirePlatformAdmin + API-permission checks + `authz_test.go` (100%, incl. platform-admin gating).
- [X] T012 [P] `internal/ipnet/` — PURE CIDR/allocation library: parse CIDR → network/broadcast/mask/prefix/total; enumerate host IPs (exclude network/broadcast); FirstFree(excludes: gateway/reserved/skip_first/skip_last/skip_list/allocated); BulkFree(n); Overlaps(cidr,cidr); GatewayInRange; Utilization. + `ipnet_test.go` + `ipnet_fuzz_test.go` (arbitrary CIDRs/skip sets never panic; first-free always in-range + unallocated). 100%.
- [X] T013 [P] `internal/audit/` writer adapter (action vocabulary per data-model) + redaction of credential/secret/snmp/ipmi/owner/contact fields.
- [X] T014 `internal/events/` publisher (ipam.ip_address.created/updated/deleted/scanned + ipam.scan.* to platform:events:<tenant>, scanned distinction) + `internal/stream/` SSE relay (copy inventory).
- [X] T015 `internal/warden/` — warden secret-reference client: GetSecret(ref)→value (fetch-at-use, mesh mTLS), ListSecrets→metadata; never persists/logs values. + a Fake for tests.
- [X] T016 App build/wire/run `internal/app/app.go` — freya.New + mesh enroll, store/KEK, authz, warden client, events, gateway registration via pkg/ipammanifest, mesh HTTP+gRPC surfaces, the scan-executor worker pool, the KVM proxy mount, admin health/readiness; refuses to start insecure.
- [X] T017 `cmd/ipamsvc/main.go` + bootstrap subcommand (config, migrate, run) with ui.Remote() wiring (the paperless GUI-404 lesson).
- [X] T018 [P] `pkg/ipammanifest/manifest.go` — routes/permissions (ipam:read, subnets/addresses/devices/vlans/locations/groups:manage, addresses:allocate, scan:run, dns:manage, backup:manage, power:control, kvm:access) + Grants/Abilities/Nav (Subnets, IP Addresses, Devices, VLANs, Locations, Groups, Scans, Dashboard) + Routes() from OpenAPI + SeedPermissions.
- [X] T019 `api/openapi/ipam.yaml` (all routes w/ x-freya-permission; power/kvm gated) + embed.go; contract test `tests/contract/openapi_test.go` (parses, every mounted route declared).
- [X] T020 `internal/repo/repodb/` implementing the store over TimescaleDB + integration test (testcontainers) for schema/RLS/unique-constraints/scan-queue/tree (`//go:build integration`).

## Phase 3: User Story 1 — Subnets and IP allocation (Priority: P1) 🎯 MVP

### Tests (write first, must fail)
- [X] T021 [P] [US1] Contract test `tests/contract/subnets_addresses_test.go` — subnet CRUD/tree/stats, allocate/bulk/find shapes; duplicate/overlap/full rejected; tenant isolation.
- [X] T022 [P] [US1] Unit test `internal/subnets/subnets_test.go` — create derives network/broadcast/total; overlap + gateway-in-range validation; delete-with-addresses guard; tree.
- [X] T023 [P] [US1] Unit test `internal/addresses/addresses_test.go` — AllocateNext first-free (excludes network/broadcast/gateway/reserved/allocated), Bulk, duplicate rejected via unique conflict, full→no-available; utilization.

### Implementation
- [X] T024 [US1] `internal/subnets/subnets.go` — Service: CRUD, GetTree, GetStats, overlap/gateway validation, delete guard (uses ipnet).
- [X] T025 [US1] `internal/addresses/addresses.go` — Service: CRUD, AllocateNext (ipnet.FirstFree + insert-on-conflict retry), BulkAllocate, Find, publish created/updated/deleted events.
- [X] T026 [US1] HTTP handlers `internal/httpapi/{subnets,addresses}.go` + register + OpenAPI entries.
- [X] T027 [P] [US1] gRPC `internal/grpcapi/` SubnetService + IpAddressService (CRUD/tree/stats/allocate/bulk/find) + register.
- [X] T028 [P] [US1] UI: `ui/src/views/subnets/` (list + tree + utilization) + `ui/src/views/addresses/` (list + allocate) + stores + api client.

**Checkpoint**: US1 demoable — model subnets, allocate conflict-free addresses (MVP).

## Phase 4: User Story 2 — Devices, interfaces, packages (Priority: P1)

### Tests (write first, must fail)
- [X] T029 [P] [US2] Contract test `tests/contract/devices_test.go` — device CRUD, interfaces, addresses, packages; unique name; tenant isolation.
- [X] T030 [P] [US2] Unit test `internal/devices/devices_test.go` — CRUD + counts, interface add/remove, package sync (update/security counts), address linking.

### Implementation
- [X] T031 [US2] `internal/devices/devices.go` — Service: device CRUD, interfaces (create/delete/list), GetAddresses, package Sync/List/Stats, computed counts.
- [X] T032 [US2] HTTP handlers `internal/httpapi/devices.go` + OpenAPI; gRPC DeviceService (CRUD/interfaces/addresses/packages).
- [X] T033 [P] [US2] UI: `ui/src/views/devices/` (list + detail with interfaces + packages tabs) + store.

## Phase 5: User Story 3 — VLANs, locations, groups (Priority: P2)

### Tests (write first, must fail)
- [X] T034 [P] [US3] Contract test `tests/contract/org_test.go` — vlan CRUD + subnets, location tree, ip/host group CRUD + members + CheckIp.
- [X] T035 [P] [US3] Unit test `internal/{vlans,locations,groups}/*_test.go` — vlan uniqueness (id+name), location tree recompute + counts, group membership matcher (address/range/subnet), CheckIp.
- [X] T036 [P] [US3] Fuzz test `internal/groups/membership_fuzz_test.go` — arbitrary IPs vs arbitrary member sets never panic; range/subnet matching correct.

### Implementation
- [X] T037 [US3] `internal/vlans/vlans.go` (CRUD, GetSubnets) + `internal/locations/locations.go` (CRUD, GetTree) + `internal/groups/groups.go` (ip/host groups CRUD, members, CheckIpInGroup, ListDeviceHostGroups).
- [X] T038 [US3] HTTP handlers `internal/httpapi/{vlans,locations,groups}.go` + OpenAPI; gRPC Vlan/Location/IpGroup/HostGroup services.
- [X] T039 [P] [US3] UI: `ui/src/views/{vlans,locations,groups,host-groups}/` (locations incl. tree + rack visualization) + stores.

## Phase 6: User Story 4 — Network discovery scanning (Priority: P2)

### Tests (write first, must fail)
- [X] T040 [P] [US4] Unit test `internal/scan/executor_test.go` — a worker claims a pending job, sweeps (via a fake Sweeper), upserts alive IPs, marks completed; over-large subnet refused; cancel; retry-with-backoff.
- [X] T041 [P] [US4] Unit test `internal/scan/orchestrate_test.go` — SNMP-enabled scan creates/updates devices+interfaces+links (via a fake Discoverer); scanned events published; DNS-update path.
- [X] T042 [P] [US4] Fuzz test `internal/scan/target_fuzz_test.go` — the scan-target enumerator never yields network/broadcast, never exceeds the bound, never panics on arbitrary CIDRs.
- [X] T043 [P] [US4] Contract test `tests/contract/scan_test.go` — StartScan/Get/List/Cancel shapes; over-large/IPv6 refused; subnet scan.

### Implementation
- [X] T044 [US4] Active-op clients behind interfaces + fakes: `internal/scan/icmp/` (raw-socket Sweeper + Pinger, one shared socket, receiver goroutine), `internal/scan/snmp/` (Discoverer: v2c/v3 walks + FDB/LLDP), `internal/scan/tcp/` (PortScanner). Each with a Fake.
- [X] T045 [US4] `internal/scan/scan.go` + `executor.go` — scan Service (Start/Get/List/Cancel) + worker pool (ClaimDueJobs, bound ≤max_hosts, concurrency, per-probe timeout, progress, reverse-DNS, upsert IPs, publish scanned events, retry/backoff, cancel); tenant-scoped + target-confined + system subject.
- [X] T046 [US4] Wire ping + suggest into addresses: `PingAddress` (icmp.Pinger) + `SuggestAvailableAddresses` (ipnet candidates verified via icmp+tcp); subnet ScanSubnet (synchronous).
- [X] T047 [US4] HTTP handlers `internal/httpapi/{scans,addresses-ping-suggest}.go` + OpenAPI; gRPC IpScanService + IpAddress Ping/Suggest.
- [X] T048 [P] [US4] UI: `ui/src/views/scans/` (start/list/progress/cancel) + live updates via SSE (`stores/live.ts`).

## Phase 7: User Story 5 — Out-of-band power & KVM (Priority: P3)

### Tests (write first, must fail)
- [X] T049 [P] [US5] Unit test `internal/ipmi/ipmi_test.go` (via a fake BMC) — power status/sensors/SEL/actions map correctly; creds fetched from a fake warden, never returned.
- [X] T050 [P] [US5] Security test `internal/devices/power_authz_test.go` — power/IPMI/KVM refused for non-platform-admin; target confined to the tenant's own device; every action audited; no credential leak.
- [X] T051 [P] [US5] Unit test `internal/kvm/kvm_test.go` — session mint (short-lived token), token validation, and that the proxy keeps BMC creds server-side (fake BMC http).

### Implementation
- [X] T052 [US5] `internal/ipmi/` — BMC client (Info/PowerStatus/Sensors/SEL/Power) behind an interface + fake; creds via the warden client at use time.
- [X] T053 [US5] `internal/kvm/` — token store + reverse-proxy handler (login to BMC server-side, inject session, proxy console + WebSocket, self-signed BMC TLS isolated); StartKvmSession (platform-admin) → {token, console_url}.
- [X] T054 [US5] Device power/kvm HTTP handlers `internal/httpapi/power.go` (platform-admin: GET /devices/{id}/power, POST power, sensors, sel, kvm-session) + the /api/ipam/kvm/{id}/... proxy mount + OpenAPI; gRPC Device Power/StartKvmSession; audit every op.
- [X] T055 [P] [US5] UI: `ui/src/views/devices/ipmi-kvm.vue` (power controls + sensors + embedded KVM console) — platform-admin gated.

## Phase 8: User Story 6 — DNS config, statistics, backup (Priority: P3)

### Tests (write first, must fail)
- [X] T056 [P] [US6] Unit test `internal/stats/stats_test.go` — totals, overall utilization, devices-by-type; per-tenant.
- [X] T057 [P] [US6] Unit test `internal/backup/backup_test.go` — export/import round-trip (all entities), ids preserved, skip vs overwrite, schema version, NO secrets, no cross-tenant leak.
- [X] T058 [P] [US6] Contract test `tests/contract/dns_stats_backup_test.go` — dns-config get/update/test, /stats, backup export/import shapes.

### Implementation
- [X] T059 [US6] `internal/dnscfg/` (get/update/test — live reverse lookup) + `internal/stats/stats.go` + HTTP `internal/httpapi/{dns,statistics}.go` + gRPC SystemService (Health/Stats/DnsConfig).
- [X] T060 [US6] `internal/backup/backup.go` + HTTP `internal/httpapi/backup.go` (export/import mode) + OpenAPI; secrets/refs excluded.
- [X] T061 [P] [US6] UI: `ui/src/views/dashboard/` (stats widgets) + DNS-config + backup controls.

## Phase 9: Platform integration & polish

- [X] T062 Stack wiring `deploy/stack/`: add an `ipam` DB + `ipam_app` role to init-db.sql; an `ipam` Valkey user; an `ipam` compose service (enrolls, mounts policy+kek, `cap_add: [NET_RAW]` for ICMP, depends on warden) + an `ipam-token` mint init + `configs/ipam.yaml`.
- [X] T063 Gateway allow-list: add `spiffe://example.org/svc/ipam=/api/ipam;ipam` to gateway-bootstrap; confirm registers (registered:true) + the IPAM menu renders.
- [X] T064 `services/ipam/deploy/policy.yaml` (gateway-forwards; module callers of the ipam.v1 services; ipam calls warden) + `deploy/kek.dev`.
- [X] T065 [P] `services/ipam/deploy/README.md` (server ops, active-ops security + CAP_NET_RAW, warden secret refs, power/KVM platform-admin) + update `deploy/stack/README.md` to list ipam.
- [X] T066 [P] `pkg/ipamclient/` typed Go client for module-to-module use + test.
- [X] T067 Coverage gate: `go -C services/ipam test ./...` ≥80% overall (with the integration harness), 100% on sealed/authz/ipnet; `govulncheck` clean; wire scripts/coverage-gate.sh into make cover (exclude the raw-network client impls scan/icmp,snmp,tcp,ipmi,kvm real paths like inventory excludes the agent pkgs — the interfaces/orchestration are tested via fakes).
- [X] T068 Stack smoke test: bring up the stack, confirm ipam registers (registered:true), create a subnet + allocate an address end-to-end, and (lab permitting) run a scan of a tiny subnet.
- [X] T069 [P] Fuzz + negative tests for CIDR/allocation math (ipnet), the IP-group membership matcher, the scan-target enumerator, and the backup import parser (Constitution IV).

## Dependencies & sequencing

- Setup (P1) + Foundational (P2) block everything.
- US1 (P1, subnets+allocation) is the MVP. US2 (P1, devices) attaches to US1. US3 (P2)
  organizes US1/US2. US4 (P2, scanning) populates US1/US2 and needs the active-op
  clients. US5 (P3, power/KVM) is highest-risk, platform-admin, built on US2 devices +
  warden. US6 (P3) rounds out.
- The active-operations subsystem (T044/T045/T052/T053) + the pure ipnet allocation
  library (T012) + warden client (T015) are the IPAM-specific novel work vs prior modules.

## Implementation strategy

MVP first: Setup + Foundational + US1 (subnets + conflict-free allocation) → demoable.
Then US2 (devices) → US3 (VLANs/locations/groups) → US4 (scanning) → US5 (power/KVM) →
US6 (dns/stats/backup) → Phase 9 (stack + coverage + smoke + fuzz).

## Summary

- **Total tasks**: 69 across 9 phases.
- **Per story**: US1=8 (T021-T028), US2=5 (T029-T033), US3=6 (T034-T039), US4=9 (T040-T048), US5=7 (T049-T055), US6=6 (T056-T061).
- **Parallelizable**: [P]-marked tasks (distinct files) — tests, UI views, clients, docs.
- **MVP scope**: Setup + Foundational + US1.
- **Novel vs prior modules**: pure ipnet allocation lib, active-ops subsystem (scan/icmp/snmp/tcp/ipmi/kvm) behind interfaces+fakes, warden secret-reference client, platform-admin-gated power/KVM.
