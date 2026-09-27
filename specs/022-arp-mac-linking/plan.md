# Implementation Plan: Link Agentless Hosts to Switch Ports via ARP Tables

**Branch**: `022-arp-mac-linking` | **Date**: 2026-09-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/022-arp-mac-linking/spec.md`

## Summary

During scans with SNMP discovery, `snmp.Discover` also walks each answering
device's IP-to-physical table (IPv4 + IPv6, falling back to the legacy ARP
table), bounded to 65,536 entries. A pure `internal/arpplan` planner
normalises and filters the entries (invalid, multicast, VRRP/HSRP,
network-device MACs, proxy ARP, excluded devices, IPs outside tenant
subnets) and plans per-address operations under MAC provenance rules
(agent/manual never overwritten, ARP fills/updates its own MACs, conflicts
recorded, missing addresses created with origin `arp`). The plan is applied in
one tenant transaction with audit rows. The existing switch-port correlation
(feature 020) is extended from host interfaces to addresses and writes new
address link columns. Operators see MAC source, last seen, conflicts and
"connected to" on addresses, addresses behind switch ports, an ARP line per
scan, can search by MAC, and can disable ARP or exclude devices per tenant.

## Technical Context

**Language/Version**: Go 1.26 (IPAM), TypeScript/Vue 3 (IPAM UI remote)

**Primary Dependencies**: existing only — go-tangra/v4, pgx/goose,
`github.com/gosnmp/gosnmp v1.43.2`, `@go-tangra/ui` 4.2.1

**Storage**: PostgreSQL/TimescaleDB with RLS — IPAM migration **0008**
(address provenance + link columns and backfill, `ipam_arp_settings`, scan
job ARP counters, MAC search index)

**Testing**: `go test -race` with memstore and the SNMP `Fake` (extended with
ARP tables); pure planner tests + fuzz (`arpplan`), OID index decoder tests +
fuzz (`scan/snmp`), correlation tests for address hosts, negative tests
(agent/manual MAC never overwritten, proxy/VRRP/network MACs never applied,
cross-tenant isolation, malformed rows, cap/partial), testcontainers
integration (0007→0008 upgrade with backfill, RLS, ApplyARP transaction, MAC
search), OpenAPI contract test, vitest (address list/detail, switch ports,
scan line, settings)

**Target Platform**: Linux container (freya-stack)

**Project Type**: single repo (go-tangra-ipam-v4): service + UI remote

**Performance Goals**: ARP phase ≤ 60 s extra for 10 devices / 5,000 entries
(SC-006); planner O(entries + addresses); one transaction per scan

**Constraints**: read-only SNMP; agent/manual MACs never overwritten (SC-003);
forward-only migration; no proto change; coverage gates — existing 100 %
packages + new `internal/arpplan` at 100 %; ≥ 80 % total

**Scale/Scope**: ≤ 65,536 ARP entries per device per scan; 1 migration, 1 new
package, 2 new HTTP operations, 7 audit event types, no new permission

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1 design — all PASS.*

- [x] **I. Secure by Default**: ARP only with existing per-subnet SNMP
      credentials, read-only; untrusted data never overrides agent/manual MACs.
- [x] **II. Zero Trust**: no new service-to-service path; settings endpoints
      behind the gateway with module-scoped permissions and CSRF.
- [x] **III. Boundary Validation**: OID index/octet decoding validated and
      fuzzed; entry cap; MAC/IP normalisation; OpenAPI schemas with bounds;
      DB CHECK constraints.
- [x] **IV. Test-First**: tests listed first per phase; negative and fuzz
      tests enumerated; new pure package at 100 %.
- [x] **V. Observability**: audit per MAC change/creation/link, per-scan
      summary, scan ARP counters with ignored reasons.
- [x] **VI. Supply Chain**: no new dependency.
- [x] **VII. Simplicity**: reuse Discover session and the existing correlator;
      one pure planner; no poller.
- [x] **Threat Model**: STRIDE in [research.md](research.md#stride-threat-model).

## Project Structure

### Documentation (this feature)

```text
specs/022-arp-mac-linking/
├── spec.md  plan.md  research.md  data-model.md  quickstart.md
├── contracts/ipam-http.md
├── checklists/requirements.md
└── tasks.md
```

### Source Code

```text
go-tangra-ipam-v4
  internal/store/migrations/0008_arp_mac_linking.sql
  internal/store/models.go                       # IPAddress provenance/link, ARPSettings, scan counters
  internal/repo/repo.go                          # ApplyARP, ARP settings, SetAddressLinks, PortLinkData.Addresses, MAC filter
  internal/repo/repodb/{db.go,arp.go,portlink.go,hostsync.go}
  internal/memstore/{memstore.go,arp.go,portlink.go,hostsync.go}
  internal/scan/snmp/{snmp.go,arp.go}            # CollectARP, ARP walk + OID index decoder
  internal/arpplan/                              # NEW pure planner (100 %)
  internal/scan/executor.go                      # ARP phase between SNMP discovery and correlation
  internal/portlink/{portlink.go,rank.go}        # address hosts + SetAddressLinks
  internal/addresses/addresses.go                # manual provenance, MAC search
  internal/hostsync (apply path)                 # agent provenance
  internal/audit/audit.go                        # new event types
  internal/httpapi/{arp.go,handlers.go}          # settings routes, mac query, behind_addresses
  api/openapi/ipam.yaml
  ui/src/views/addresses/*, ui/src/views/devices/detail.vue,
  ui/src/views/scans/{index.vue,snmp.ts}, ARP settings card (host sync page),
  ui/src/api/types.ts, ui/src/stores/{addresses.ts,scans.ts,arp.ts}
  scripts/coverage-gate.sh, README.md
```

**Structure Decision**: SNMP only collects; the pure planner decides every
change (provable at 100 % and fuzzed); the store executes a plan in one
transaction; the correlator is reused with addresses as a second host kind.

## Rollout

1. **ipam v4.5.0** (minor: new behaviour + API): migration 0008 (backfill
   provenance) on start; ARP enabled by default.
2. **go-tangra-docker**: bump `IPAM_IMAGE`; no config change.
3. Operators scan the network-devices subnet; optional tuning of the proxy
   threshold / excluded devices.

Merges, tags, pins and the production deploy are user-confirmed steps.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| MAC provenance columns on addresses | ARP data is untrusted and must never override agent/manual MACs (SC-003) | Without provenance, ARP would either never update or overwrite everything |
| Address link columns (in addition to interface links) | Agentless hosts have no interface row; the address is the unit (spec decision) | Creating synthetic devices/interfaces pollutes the device inventory |
| Scan job `arp_ignored` jsonb | Operators need ignored counts by reason without seven more columns | A single "ignored" counter hides proxy-ARP/VRRP problems |
