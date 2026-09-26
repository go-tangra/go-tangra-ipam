# Implementation Plan: Hosts Reported by the Inventory Agent Populate IPAM

**Branch**: `020-ipam-host-sync` | **Date**: 2026-09-26 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/020-ipam-host-sync/spec.md`

## Summary

The v4 inventory agent (already on every host) collects what the v3
go-tangra-client used to send to IPAM: per-interface kind, speed, addresses
with prefix and DHCP/temporary flags, default gateway and primary addresses
(Linux via sysfs + stdlib rtnetlink, Windows via `GetAdaptersAddresses`),
virtualization role/kind, BMC LAN settings read without credentials (Linux,
go-ipmi, six read-only LAN parameters), Proxmox guests, and Linux update
state (reboot required, automatic updates, pending and security updates
from apt/dnf/yum/apk/pacman without refreshing package lists). The inventory
module stores it in the snapshot payload, keeps a digest of an IPAM-oriented
**projection** per host, and serves it over a new mesh-only
`inventory.v1.HostReportService` (tenants changed since, host reports
changed since, one host report). IPAM gains an in-process **host sync**:
a 60-second changed-since poll plus a per-tenant reconcile (default hourly)
pull reports over SPIFFE mTLS, validate and bound them, and apply each host
in one tenant-scoped transaction through a pure planner — match by inventory
host id → serial → name, devices/interfaces/addresses/subnets created or
updated (reported data wins, administrator fields never touched), moves and
releases instead of deletes, conflicts for flapping addresses, BMC as
management address, hypervisor links, pending updates — with every change
written as an audit row in the same transaction. Administrators see source
and last report per device, re-sync one host or all, and switch the sync
off per tenant (immediately effective). Switch-port correlation reuses the
existing SNMP FDB/LLDP data after fixing the link uniqueness defect.

## Technical Context

**Language/Version**: Go 1.26 (inventory module + agent, IPAM), TypeScript/Vue 3 (IPAM and inventory UI remotes)

**Primary Dependencies**: go-tangra/v4 framework (Freya runtime, SPIFFE
client pool, policy, metrics), pgx/goose, gopsutil v4 and go-smbios
(existing, inventory), `golang.org/x/sys` (existing; Windows adapters),
stdlib `syscall` netlink (Linux), **`github.com/bougou/go-ipmi v0.8.1`**
(new direct dependency of the inventory agent; already used by IPAM),
**inventory SDK `sdk/v4.1.0`** (new first-party dependency of IPAM),
`@go-tangra/ui`

**Storage**: PostgreSQL/TimescaleDB with RLS — inventory migration **0005**
(`inventory_hosts.report_digest`, `report_changed_at`); IPAM migrations
**0004** (device/interface/address/subnet columns, `ipam_hypervisor_guests`,
`ipam_hostsync_settings`, `ipam_hostsync_device_state`) and **0005** (link
uniqueness fix, US5)

**Testing**: `go test -race` unit tests with memstore and fakes
(inventory client, IPMI client, command runner, fake sysfs/procfs trees),
negative security tests, fuzz targets for every parser (agent facts,
ingest mapper, projection, IPAM report normalisation, planner, exclusion
patterns, most-specific subnet), testcontainers integration (IPAM migration
upgrade from 0003, apply against real PostgreSQL with RLS, end-to-end
inventory + IPAM through the real gRPC service with bufconn/mTLS test
identities), contract tests (proto `buf breaking`, OpenAPI routes and
permissions), vitest (UI), a 1000-host benchmark (SC-006)

**Target Platform**: Linux containers (freya-stack) for both modules; agent
on Linux (amd64/arm64) and Windows (amd64/arm64)

**Project Type**: multi-repo platform change — go-tangra-inventory-v4
(agent, ingest, projection, SDK, UI), go-tangra-ipam-v4 (host sync, API, UI);
production notes for go-tangra-docker

**Performance Goals**: new host visible in IPAM ≤ 5 min after its first
report (SC-001; poll 60 s); 1000 hosts fully synced ≤ 15 min after enabling
(SC-006; target ≈ 3 min at p95 150 ms per host); interactive IPAM p95
unchanged (≤ 2 DB connections used by the sync, paced)

**Constraints**: additive protos only (old agents and old consumers keep
working); forward-only migrations; reported data never touches
administrator fields (SC-004); 100 % of changes audited (SC-005); no agent
path into IPAM (SR-004); agent collection bounded in time (BMC 10 s,
network 5 s, updates 120 s) and size; coverage gates — inventory 100 % for
`internal/authz`, `internal/sealed`, `internal/enroll` + new
`internal/hostreport`; IPAM 100 % for `internal/authz`, `internal/sealed`,
`internal/ipnet` + new `internal/hostreport`, `internal/hostplan`; ≥ 80 %
total in both

**Scale/Scope**: up to ~1000 hosts per tenant, ≤ 256 interfaces / 1024
addresses / 1000 guests / 5000 pending packages per host; 2 repos, ~4 new
packages per repo, 2+2 migrations, 8 new HTTP operations, 1 new permission

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1 design — all PASS.*

- [x] **I. Secure by Default**: no insecure opt-out added; agent defaults are
      least invasive (read-only BMC parameters, no package list refresh);
      settings changes require the new `hostsync:manage`; global kill switch
      `host_sync.enabled` and per-tenant disable (SR-006).
- [x] **II. Zero Trust**: IPAM → inventory over SPIFFE mTLS, inbound policy
      rule `ipam-hostsync` limited to three RPCs + health, handler
      allow-list for the cross-tenant `ListReportTenants`; admin endpoints
      behind the gateway with module-scoped permissions; agents still only
      reach the inventory ingest edge (SR-004).
- [x] **III. Boundary Validation**: proto contracts with documented bounds;
      validation at the agent (caps), the ingest edge (D16), the projection
      and again in IPAM (`internal/hostreport`, D15); OpenAPI schemas with
      `additionalProperties: false`, patterns and body limits; gRPC receive
      size and page byte bounds; per-call timeouts.
- [x] **IV. Test-First**: every phase lists tests first; negative tests
      (forged/cross-tenant reports, oversized lists, malformed MAC/IP/names,
      BMC credential never requested, admin fields untouched, disabled sync
      writes nothing, non-consumer `ListReportTenants`, permission checks);
      fuzz targets for every parser; new pure packages at 100 %.
- [x] **V. Observability**: transactional audit rows for every sync change
      (D11), run summaries, OTel metrics on the admin listener (D19), run id
      propagated; no report contents in info logs.
- [x] **VI. Supply Chain**: go-ipmi justified in research D3 (pure Go, MIT,
      already in IPAM, `govulncheck` clean); inventory SDK is first-party; no
      netlink/WMI library; `govulncheck` in both repos.
- [x] **VII. Simplicity**: one poller, one pure planner, typed config
      sections; polling chosen over bus subscription; complexity recorded
      below.
- [x] **Threat Model**: STRIDE in [research.md](research.md#stride-threat-model).

## Project Structure

### Documentation (this feature)

```text
specs/020-ipam-host-sync/
├── spec.md  plan.md  research.md  data-model.md  quickstart.md
├── contracts/{inventory-grpc.md,agent-collection.md,ipam-http.md,ipam-grpc.md,policy.md,audit-events.md}
├── checklists/requirements.md
└── tasks.md
```

### Source Code

```text
go-tangra-inventory-v4 (inventory/)
  sdk/api/proto/inventory/v1/inventory.proto (+ generated)   # D1, HostReportService
  sdk/pkg/inventoryclient/{inventoryclient.go,inventory.go,hostreport.go}  # SDK v4.1.0
  internal/agentfacts/                       # NEW pure parsers/rules: netlink attrs, sysfs kind,
                                             #   virtualization, proxmox conf, apt/dnf/apk/pacman,
                                             #   reboot/auto-updates, BMC LAN params, bounds
  internal/collector/{network_linux.go,network_windows.go,network_other.go,
                      virt.go,virt_linux.go,bmc_linux.go,bmc_other.go,
                      guests_linux.go,updates_linux.go,updates_other.go,collector.go}
  internal/config/config.go                  # AgentConfig collect_*, host_reports section
  internal/store/models.go                   # new structs (data-model §1.1)
  internal/store/migrations/0005_host_reports.sql
  internal/sender/mapper.go, internal/ingest/{ingest.go,validate.go}, internal/grpcapi/mapper.go
  internal/hostreport/                       # NEW projection + digest (100 %)
  internal/snapshots/ingest.go               # digest/report_changed_at on ingest
  internal/repo/repo.go, internal/repo/repodb/db.go, internal/memstore/memstore.go
  internal/grpcapi/hostreports.go            # NEW HostReportService server
  internal/diff/diff.go                      # new categories
  internal/app/app.go                        # register service, consumers allow-list
  deploy/policy.yaml, scripts/coverage-gate.sh, Makefile
  ui/src/views/hosts/detail.vue, ui/src/api/types.ts
  README.md, deploy/README.md

go-tangra-ipam-v4 (this repo)
  sdk/api/proto/ipam/v1/ipam.proto (+ generated)   # ipam SDK v4.1.0 fields
  internal/store/migrations/{0004_host_sync.sql,0005_interface_links_unique.sql}
  internal/store/models.go
  internal/repo/repo.go (HostSyncStore), internal/repo/repodb/{db.go,hostsync.go}, internal/memstore/{memstore.go,hostsync.go}
  internal/ipnet/ipnet.go                    # MostSpecific, NetworkOf, Classify (100 %)
  internal/invclient/                        # NEW mesh client over inventoryclient (+ Fake)
  internal/hostreport/                       # NEW validate/normalise reports (100 %)
  internal/hostplan/                         # NEW pure planner: match, ops, audit entries (100 %)
  internal/hostsync/                         # NEW poller, reconcile, per-tenant runner, resync, status
  internal/portlink/                         # NEW US5 FDB/LLDP correlation
  internal/scan/executor.go                  # D9 guard, portlink hook
  internal/audit/audit.go                    # vocabulary
  internal/events/events.go                  # hostsync events
  internal/config/config.go                  # host_sync section
  internal/httpapi/{hostsync.go,handlers.go}, internal/grpcapi/mapper.go
  internal/app/app.go                        # wiring, worker
  api/openapi/ipam.yaml, pkg/ipammanifest/manifest.go
  ui/src/views/{devices/detail.vue,devices/index.vue,addresses/index.vue,hostsync/index.vue},
  ui/src/stores/hostsync.ts, ui/src/api/{types.ts,schema.d.ts}, ui/src/remote/{routes.ts,nav.ts}
  scripts/coverage-gate.sh, Makefile, deploy/policy.yaml (comment), README.md
```

**Structure Decision**: inventory owns collection, storage and the
projection contract; IPAM owns matching, planning, writing and control.
The planner is pure so that the security properties (admin fields,
idempotence, bounds) are proven by unit and fuzz tests independent of the
database; the store layer only executes planned ops in one transaction.

## Rollout

1. **inventory** v4.3.0 + inventory SDK **`sdk/v4.1.0`** (proto, ingest
   validation, projection + digest, migration 0005, `HostReportService`,
   policy rule `ipam-hostsync`). Deploy the server, then roll out new agents
   (`make agent`). Old agents keep reporting.
2. **ipam** v4.3.0 + ipam SDK **`sdk/v4.1.0`**: migrations 0004 and 0005,
   host sync (enabled by default), UI, permission `hostsync:manage`. Before
   inventory 4.3.0 is live the sync is `degraded` and writes nothing.
3. **go-tangra-docker** (user): add `ipam-hostsync` to
   `policies/inventory.yaml`, pin both images, optional `host_sync` config.

Tags, merges and pins are user-confirmed steps (tasks.md Phase 9).

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| New inventory service `HostReportService` with a cross-tenant `ListReportTenants` | IPAM must discover tenants with hosts and fetch only changed, bounded projections | `ListHosts` + `GetLatestByHost` per host moves every installed program every hour and has no change signal; IPAM's `TenantIDs` misses tenants without IPAM rows; static tenant lists (dns) miss new tenants |
| Digest + `report_changed_at` in inventory | Hourly snapshots of 1000 unchanged hosts must not cause 1000 fetches and applies per hour | `last_seen` changes on every snapshot |
| Transactional audit rows instead of the audit writer | SC-005 requires 100 %; the buffered writer drops and is not wired | Writer + best effort would violate SC-005 |
| Per-tenant advisory lock + `FOR SHARE` on settings | Several IPAM replicas; SR-006 "disable stops changes immediately" | Leader election adds a coordination service; a flag checked outside the transaction races with the disable |
| Separate pure `hostplan` package | Proves SC-004/idempotence/bounds with unit + fuzz tests at 100 % | Planning inside SQL-writing code cannot be fuzzed without a database |
