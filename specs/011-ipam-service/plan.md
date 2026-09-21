# Implementation Plan: IPAM Service

**Branch**: `011-ipam-service` | **Date**: 2026-09-21 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/011-ipam-service/spec.md`

## Summary

A tenant-scoped IP Address Management platform module replicating go-tangra-ipam:
hierarchical **subnets** (utilization), **IP addresses** (first-free + bulk
allocation, conflict detection, suggest), **devices** (interfaces, L2 links, OS
packages), **VLANs**, a **location** tree, **IP/host groups**, DNS config, stats
and backup — plus **active network operations**: asynchronous discovery
**scanning** (ICMP sweep + SNMP device/interface discovery + TCP probe), **ping**,
out-of-band **IPMI/BMC power** control, and a **KVM console proxy**. Registers
with the gateway, exposes service-to-service gRPC, publishes IP/scan events, and
ships a Module-Federation UI.

Freya adaptations: SPIFFE mTLS + gateway platform token (replacing the mTLS CN
allow-list + HMAC claims); TimescaleDB + per-tenant RLS (replacing ent app-level
filtering, preserving unique constraints as the conflict layer); BMC/IPMI/SNMP
credentials held only as **warden references** and fetched at use time; active
operations authorized, tenant-scoped, bounded, platform-admin-gated for
power/KVM, and audited.

## Technical Context

**Language/Version**: Go 1.26 (matches every other Freya service).

**Primary Dependencies**: the Freya framework (`github.com/go-freya/freya`) for
transport (SPIFFE mTLS gRPC + OpenAPI-validated HTTP edge), identity, audit,
sealed envelopes, gateway registration; `pgx` + TimescaleDB for storage; Valkey
for the event bus; the platform warden client (secret references, metadata over
the mesh — never secret values). Active-ops libraries (justified in research.md,
Constitution VI): `golang.org/x/net/icmp` + `.../ipv4` (raw-socket ICMP sweep),
`github.com/gosnmp/gosnmp` (SNMP v2c/v3 discovery), `github.com/bougou/go-ipmi`
(IPMI/BMC), `github.com/gorilla/websocket` + stdlib `net/http/httputil`
(KVM console reverse-proxy). UI: Vue 3 + Vite + Vuetify (Materio) Module-
Federation remote, mirroring services/inventory/ui.

**Storage**: TimescaleDB (Postgres) with per-tenant row-level security on every
`ipam_*` table. Most tables are ordinary relational RLS tables (subnets, ip
addresses, devices, interfaces, links, packages, vlans, locations, groups + members,
dns configs). `ipam_ip_scan_jobs` is a work queue claimed with `FOR UPDATE SKIP
LOCKED`; `ipam_audit_events` is a hypertable. **All unique constraints from the
source are preserved** — they are the conflict-detection layer ((tenant_id,address)
blocks duplicate IPs, etc.).

**Secrets**: BMC/IPMI and SNMP credentials are NEVER stored as columns — devices
hold `ipmi_secret_ref` and subnets hold an `snmp_secret_ref` (warden secret ids).
Values are fetched at use time via the warden client. Any locally-sealed
sensitive fields use the `sealed` package + KEK. Nothing secret is returned,
logged, audited or exported.

**Active operations (the novel surface)**: a scan-executor worker pool claims
pending `ip_scan_jobs`, opens a single shared raw ICMP socket (needs
CAP_NET_RAW; confined to the scanner), sweeps subnet host IPs with bounded
concurrency + per-probe timeout, bounds each job to ≤1024 hosts (IPv4 only),
reverse-resolves alive hosts, upserts IP/device records, and publishes events;
retry-with-backoff + cancel. SNMP discovery (opt-in, subnet credential ref)
creates/updates devices/interfaces and correlates L2 links (FDB/LLDP). IPMI/BMC
power + KVM are platform-admin-gated; the KVM console is a token-gated reverse
proxy that keeps BMC creds server-side. Every active op is authorization-checked
(tenant-scoped; target confined to the tenant's own subnets/devices) and audited.

**Realtime/events**: IP/scan changes publish `ipam.ip_address.{created,updated,
deleted,scanned}` envelopes to `platform:events:<tenant>` for DNS-sync consumers
and the gateway SSE hub.

**Testing**: Go `testing` with a `testrt` runtime + `memstore` fake; contract
tests over the OpenAPI + proto; unit tests per package; an integration suite
(testcontainers: TimescaleDB, Valkey) behind `//go:build integration`; the
scanner/SNMP/IPMI clients are behind interfaces with fakes so allocation, scan
orchestration and authorization are unit-tested without touching the network;
fuzz tests for CIDR/allocation math, the IP-group membership matcher, and the
scan-target enumerator. Coverage gate ≥80% overall, 100% on sealed/authz.

**Target Platform**: Linux server container in `deploy/stack` behind the gateway,
with `cap_net_raw` on the binary for ICMP; active ops reach the lab network.

**Project Type**: Web service (Go backend + gRPC + OpenAPI HTTP) with an active
network-operations subsystem (scanner/SNMP/IPMI/KVM) and a Module-Federation UI.

**Performance Goals**: subnet/device list + utilization queries < 3s at 100k
addresses / 10k devices per tenant; first-free allocation returns in well under a
second; a /24 scan completes within ~2 minutes.

**Constraints**: active ops are tenant-scoped and confined to the tenant's own
subnets/devices; a scan is bounded to ≤1024 IPv4 hosts; power/KVM/IPMI require
platform-admin; credentials are warden-referenced, never stored/returned;
per-tenant RLS; unique constraints enforce conflict detection; the raw-socket
capability is confined to the scanner.

**Scale/Scope**: 100k addresses / 10k devices per tenant; six prioritized user
stories (subnets+allocation, devices+interfaces, VLANs/locations/groups,
scanning, out-of-band power/KVM, DNS/stats/backup).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **I. Secure by Default**: zero-config refuses to start; active ops default to
  tenant-scoped + bounded; power/KVM gated to platform-admin; credentials
  warden-referenced; TLS everywhere; scanner capability confined. PASS.
- **II. Zero Trust Service Communication**: mesh calls SPIFFE mTLS; browser via
  gateway platform token; warden secret access over the mesh; no shared secrets. PASS.
- **III. Least Privilege & Tenant Isolation**: per-tenant RLS on every table;
  active ops confined to the tenant's own resources; scan executor uses a scoped
  system subject; CAP_NET_RAW confined to the scanner. PASS.
- **IV. Test-First with Security Verification (NON-NEGOTIABLE)**: contract/unit/
  integration tests precede implementation per story; active-ops clients behind
  fakes; negative + fuzz tests for allocation, membership and scan-target
  enumeration; authorization + redaction tests. PASS.
- **V. Defense in Depth & Observability**: gateway edge + module authz + RLS;
  bounded scans + rate limits; append-only audit of every mutation/scan/power/KVM;
  health/readiness; events for observability. PASS.
- **VI. Supply-Chain Integrity**: new deps (gosnmp, go-ipmi, x/net/icmp,
  gorilla/websocket) justified in research.md; `go.sum` pinned; `govulncheck` in CI. PASS.
- **VII. Simplicity & Explicitness**: explicit wiring; the active-ops subsystem is
  isolated behind explicit packages (scanner/snmp/ipmi/kvm) with interfaces; the
  high-risk operations are gated and audited. PASS.

No Constitution violations. Security Requirements (SR-001..006) map to research.md
decisions and to test tasks.

## Project Structure

### Documentation (this feature)

```
specs/011-ipam-service/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (OpenAPI + proto + scanner/ipmi/kvm/warden ifaces + events)
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```
services/ipam/
├── go.mod                       # module github.com/go-freya/freya/services/ipam (replaces ../.. ../auth ../gateway ../lcm ../warden)
├── cmd/ipamsvc/                 # run + bootstrap/migrate
├── api/
│   ├── openapi/ipam.yaml        # browser routes (x-freya-permission/x-freya-public)
│   └── proto/ipam/v1/           # gRPC: Subnet/IpAddress/Device/Vlan/Location/IpGroup/HostGroup/IpScan/System services
├── internal/
│   ├── app/                     # wiring (freya.New, stores, services, scan-executor, KVM proxy, gateway reg)
│   ├── config/                  # config (db/valkey/kek/warden/scan/allocation/ipmi/kvm/gateway/enroll)
│   ├── store/ + repo/ + repodb/ # migrations (RLS + unique constraints + scan-job queue + audit hypertable), models, SQL, repo iface
│   ├── memstore/                # in-memory repo fake
│   ├── sealed/ authz/ audit/    # envelope seal, permission + platform-admin checks, audit vocabulary
│   ├── ipnet/                   # CIDR math: enumerate, exclude network/broadcast/gateway, first-free, overlap, utilization (pure, fuzzed)
│   ├── subnets/ addresses/ devices/ vlans/ locations/ groups/  # domain services
│   ├── scan/                    # scan executor (job queue) + orchestration
│   │   ├── icmp/                # raw-socket ICMP sweep + ping (behind a Scanner interface + fake)
│   │   ├── snmp/                # SNMP discovery (behind an interface + fake)
│   │   └── tcp/                 # QuickPortScan for suggest
│   ├── ipmi/                    # BMC power/info/sensors/SEL (behind an interface + fake)
│   ├── kvm/                     # token-gated console reverse-proxy (platform-admin)
│   ├── warden/                  # warden secret-reference client (metadata + fetch-at-use)
│   ├── stats/ backup/ dnscfg/   # statistics, export/import, DNS config
│   ├── events/ stream/          # platform event publisher + SSE relay
│   ├── httpapi/ grpcapi/        # mesh HTTP + gRPC surfaces (+ the /bmc KVM proxy mount)
├── pkg/
│   ├── ipammanifest/            # gateway manifest (routes/permissions/abilities/nav) + SeedPermissions
│   └── ipamclient/              # typed module-to-module gRPC client
├── ui/                          # Vue 3 + Vite + Vuetify MF remote (subnets/addresses/devices/vlans/locations/groups/scans/dashboard/ipmi-kvm)
├── deploy/                      # policy.yaml, kek.dev, README (+ cap_net_raw note)
├── Dockerfile Makefile buf.yaml buf.gen.yaml
```

**Structure Decision**: mirrors services/inventory (proven module layout) plus the
IPAM-specific **active-operations subsystem** — `internal/{scan,ipmi,kvm,warden}`
with the network-touching clients behind interfaces (fakes for tests) — and a pure
`internal/ipnet` CIDR/allocation library that is fuzzed and unit-tested offline.

## Complexity Tracking

The active network-operations subsystem (raw-socket ICMP scanner, SNMP discovery,
IPMI power, KVM reverse-proxy) is substantial surface beyond a data-plane module,
but it is inherent to IPAM and isolated behind explicit, interface-fronted packages
with fakes; the high-risk operations are platform-admin-gated, tenant-confined and
audited. No Constitution violations to justify.
