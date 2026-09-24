# Phase 0 Research: IPAM Service

Decisions resolving the Technical Context, each with rationale and alternatives.
Scope was confirmed with the requester: full replica INCLUDING the active network
operations (scanning, ping, SNMP, IPMI/BMC power, KVM console proxy).

## D1. Storage model & conflict detection (TimescaleDB + RLS, unique constraints preserved)

**Decision**: TimescaleDB (Postgres) with per-tenant RLS on every `ipam_*` table.
Most tables are ordinary relational RLS tables. `ipam_ip_scan_jobs` is a work
queue claimed with `FOR UPDATE SKIP LOCKED`; `ipam_audit_events` is a hypertable.
Every unique constraint from the source is preserved: `(tenant_id,address)` on
addresses (duplicate-IP guard), `(tenant_id,name)` on subnets/devices/groups,
`(tenant_id,vlan_id)`+`(tenant_id,name)` on VLANs, `(tenant_id,name)`+`(tenant_id,
code)` on locations, `(device_id,name)` on interfaces, `(tenant_id,device_id,name)`
on packages, member uniqueness on groups.

**Rationale**: The source enforced conflict detection through DB unique indexes,
not application logic; preserving them keeps the guarantees exact and lets RLS
replace the ent SystemViewer bypass. IPAM is relational (not time-series), so most
tables are plain RLS tables; only the job queue and audit benefit from Timescale.

**Alternatives**: ent/app-level tenant filtering (source) — rejected, no RLS,
bypass risk. A single JSON blob — rejected, unqueryable and loses uniqueness.

## D2. CIDR math & first-free allocation (pure `internal/ipnet`, fuzzed)

**Decision**: A pure `ipnet` package does all address math: parse CIDR → network/
broadcast/mask/prefix/total; enumerate host IPs excluding network + broadcast (for
prefixes < /31); first-free = lowest host not in {network, broadcast, gateway,
reserved ranges, skip_first/skip_last, caller skip list, DB-allocated}; bulk = the
next N free; overlap detection between CIDRs; gateway-in-range check; utilization =
used/total. IPv4 for allocation/scan; IPv6 subnets modeled but not scanned.
`AllocateNextAddress`/`BulkAllocate`/`PingAddress` were TODO stubs in the source —
implemented here.

**Rationale**: Allocation correctness is the heart of IPAM and must be
deterministic and testable offline; a pure package is fuzzed (arbitrary CIDRs/skip
sets never panic; first-free is always in-range and unallocated). The DB unique
index is the final race-safe guard (insert-or-conflict retry on concurrent allocation).

## D3. Active operations behind interfaces + fakes (scanner/snmp/tcp/ipmi/kvm)

**Decision**: Every network-touching capability is an interface with a real
implementation and a fake: `icmp.Pinger`/`Sweeper`, `snmp.Discoverer`,
`tcp.PortScanner`, `ipmi.BMC`, `kvm.Proxy`. Domain services and the scan executor
depend on the interfaces, so allocation, scan orchestration, authorization,
event-publishing and error handling are unit-tested with fakes — no network in
unit tests. The real implementations use `x/net/icmp`+`ipv4` (one shared raw
socket, receiver goroutine, bounded senders), `gosnmp` (v2c/v3 walks of sysName/
sysDescr/ifTable + FDB/LLDP), a stdlib TCP dialer (QuickPortScan), `go-ipmi`
(IPMI LAN power/info/sensors/SEL), and `net/http/httputil` + `gorilla/websocket`
(KVM reverse proxy).

**Rationale**: Isolating the risky, environment-dependent code behind interfaces
keeps the domain testable and the blast radius contained, and lets CI run without
raw sockets or real BMCs.

## D4. Authorization & sandboxing of active operations

**Decision**: Active ops are authorization-checked at the service boundary:
(a) tenant-scoping — the target subnet/device must belong to the caller's tenant;
(b) target confinement — scan/ping/SNMP/IPMI/KVM may only reach hosts within the
tenant's own subnets or the tenant's own device management IPs, never an arbitrary
address; (c) platform-admin gating — power control, IPMI and KVM require the
platform-admin role; (d) bounding — a scan is capped at ≤1024 IPv4 hosts and refuses
IPv6/over-large targets; (e) every active op is audited (actor, tenant, target,
outcome). The scan executor runs under a scoped system subject (RLS system pin),
never an unauthenticated bypass.

**Rationale**: These operations reach real hardware and can hard-reset servers; the
spec's SR-001..006 require tenant confinement, platform-admin gating, bounding and
audit. Confining targets to the tenant's declared resources prevents the scanner
from becoming an arbitrary-host probe.

## D5. Secret handling (warden references, never stored)

**Decision**: Devices store `ipmi_secret_ref` and subnets store `snmp_secret_ref`
— ids of secrets held in warden. IPMI/SNMP credentials are fetched at use time via
the warden client (mesh mTLS; the module reads secret VALUES only for the active
op, and only metadata for listing). Nothing secret is persisted in `ipam_*`
columns, returned, logged, audited or exported. Any locally-sealed sensitive field
uses the sealed package + KEK.

**Rationale**: Matches the source's secret-reference pattern and the platform's
warden model; keeps BMC/SNMP credentials out of the IPAM datastore and audit.

## D6. KVM console proxy (token-gated, server-side creds)

**Decision**: `StartKvmSession` (platform-admin) resolves the device BMC IP +
warden creds, mints a short-lived token (in-memory/Valkey token store), and returns
a `console_url` under the platform origin. A token-gated HTTP handler (mounted at a
dedicated path, e.g. `/api/ipam/kvm/{id}/...`) reverse-proxies the BMC HTML5
console and its WebSocket, logging into the BMC server-side and injecting the
session so the browser never receives BMC credentials. Self-signed BMC certs are
accepted only by this isolated proxy client, never by the platform's own trust pool.

**Rationale**: Brings out-of-band console access under the platform's identity/audit
without exposing BMC credentials or weakening platform trust.

## D7. Events for DNS synchronization

**Decision**: Publish `ipam.ip_address.{created,updated,deleted,scanned}` envelopes
`{id,type,source,timestamp,tenant_id,data}` to `platform:events:<tenant>` (Valkey),
preserving the scanned-vs-created distinction so a DNS-sync consumer can create
A/PTR records from discovered hosts. The gateway SSE hub relays them for live UI.

**Rationale**: Matches the source's Redis pub/sub contract for go-tangra-dns; the
scanned distinction lets consumers treat discovery differently from manual edits.

## D8. Config knobs the source declared but stubbed

**Decision**: Implement the allocation/validation knobs the source only declared:
`skip_first`/`skip_last` reservation, reserved ranges, overlap prevention,
gateway-in-range; and the scan bounds (max hosts, concurrency, per-probe timeout,
retries/backoff). `AllocateNext`/`BulkAllocate`/`PingAddress` (source TODO stubs)
are implemented.

## Supply-chain note (Constitution VI)

New third-party deps beyond the Freya baseline: `gosnmp/gosnmp` (SNMP),
`bougou/go-ipmi` (IPMI), `golang.org/x/net` (icmp/ipv4), `gorilla/websocket` (KVM
WS). All widely used; pinned in `go.sum`; `govulncheck` in CI. The raw-socket
capability (CAP_NET_RAW) is a container capability, confined to the scanner.

## STRIDE summary

- **Spoofing**: forged platform-admin to reach power/KVM → gateway platform-token
  role verification (authclient); mesh peers are SPIFFE-identified. Target spoofing
  prevented by tenant-scoping + confinement (D4).
- **Tampering**: unauthorized power actions / cross-tenant edits → platform-admin
  gating, RLS, unique-constraint conflict guards, append-only audit (SR-003/005/006).
- **Repudiation**: append-only tamper-evident audit of every mutation/scan/power/
  KVM with actor+tenant+target+outcome (SR-006).
- **Information disclosure**: BMC/SNMP creds, topology → warden references (never
  stored/returned), sealing/redaction, per-tenant RLS, KVM creds kept server-side
  (SR-002/004/005).
- **Denial of service**: scan floods / hardware abuse → scans bounded (≤1024 hosts,
  concurrency, timeout, retry caps); active ops rate-limited and audited (D3/D4).
- **Elevation of privilege**: reaching arbitrary hosts or another tenant's hardware
  → target confinement to the tenant's own subnets/devices; power/KVM platform-admin
  only; scanner CAP_NET_RAW confined; system worker uses a scoped subject (SR-001/003/005).
