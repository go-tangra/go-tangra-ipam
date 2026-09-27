# Research: ARP-based MAC linking (022)

Findings from `main` @ ipam v4.4.2 and the decisions they led to.

## Findings

- **F1 — correlation input is interfaces only.** `portlink.BuildInput` takes
  switch ports (FDB/LLDP rows in `ipam_device_interface_links`) and *host-
  reported* device interfaces; `Correlate` writes the flat `remote_*`/`link_*`
  columns of those interfaces (`SetInterfaceLinks`) with audit rows. It runs
  after SNMP scans (`executor.go`, `s.linker.Correlate`) and after host-sync
  runs with changes.
- **F2 — addresses already carry `mac_address`, `device_id`,
  `interface_name`**, but nothing writes the MAC except manual edits and the
  host sync; no provenance, no link columns.
- **F3 — SNMP discovery** (`scan/snmp.Client.Discover`) walks system, ifTable,
  bridge FDB and LLDP per host with one gosnmp client; no ARP/neighbour walk.
  The SNMP phase runs 10 hosts in parallel (4.4.1) with a per-host deadline
  (request timeout × attempts + 60 s walk budget).
- **F4 — prod data (2026-09-27)**: 7 switches, 486 FDB MACs, 2 LLDP rows; 1 of
  169 addresses has a MAC; the MikroTik router, Fortinet and L3 Extreme
  switches answer SNMP in 192.168.100.0/24.
- **F5 — address list** filters by subnet, device, status, type, report state;
  no MAC search.
- **F6 — audit guard** drops keys containing secret/credential/snmp/password…;
  `mac`, `previous_mac`, `source_device_id` are fine.

## Decisions

### D1 — ARP collection inside Discover, opt-in per call

`snmp.Creds` gains `CollectARP bool`; when set, `Discover` also walks
`ipNetToPhysicalPhysAddress` (1.3.6.1.2.1.4.35.1.4, index
`ifIndex.addrType.len.addr…`, IPv4+IPv6) and, if that yields nothing, the
legacy `ipNetToMediaPhysAddress` (1.3.6.1.2.1.4.22.1.2, index
`ifIndex.a.b.c.d`), and returns `DiscoveredDevice.ARP []ARPEntry{IP, MAC,
IfIndex}` capped at 65,536 entries (`ARPPartial` when capped or the walk
errs mid-way). Reusing the same client and deadline keeps one SNMP session
per device.

- *Alternative*: a separate ARP poller — rejected (spec assumption: freshness
  follows scans; no new scheduler).

### D2 — Pure planner `internal/arpplan` (100 % coverage gate)

Input: observations (device id, entries), tenant subnets, tenant addresses
(IP → address with MAC, source, conflict), network-device MACs (all
interfaces of SNMP-discovered devices), settings (proxy threshold, excluded
devices). Output: ops per address (`fill`, `update`, `conflict`,
`clear_conflict`, `create`, `touch`) + audit entries + ignored counters by
reason. Rules:

1. Normalise IP (canonical) and MAC (`hostreport.NormalizeMAC`).
2. Ignore: incomplete/invalid, multicast (I/G bit), broadcast, all-zero,
   VRRP `00:00:5e:00:01:xx`/`00:00:5e:00:02:xx`, HSRP `00:00:0c:07:ac:xx` and
   `00:00:0c:9f:fx:xx`, network-device MACs, MAC answering for more than
   `proxy_threshold` distinct IPs in this scan, source device excluded, IP
   outside every tenant subnet (`ipnet.MostSpecific`).
3. Same IP from several devices: the last observation in deterministic order
   (device id) wins; disagreement counted as conflict.
4. Provenance: empty → `fill`; source `arp` and different MAC → `update`;
   source `agent`/`manual` and different → `conflict` (store the ARP MAC in
   `mac_conflict`); equal → `touch` (last seen) and `clear_conflict` if set;
   no address → `create` (status active, origin `arp`).

Fuzz target over the entry decoder + planner.

### D3 — Storage (migration 0008)

`ipam_ip_addresses` gains `mac_source` (`''|manual|agent|arp`),
`mac_source_device_id`, `mac_seen_at`, `mac_conflict`, `origin` (`''|arp`) and
link columns `link_switch_id`, `link_port_id`, `link_port_name`, `link_vlan`,
`link_source`, `link_last_seen`. Backfill: rows with a MAC and host-sync
provenance (`report_state <> ''`) → `agent`, other rows with a MAC → `manual`
(FR-009). New table `ipam_arp_settings (tenant_id PK, enabled,
excluded_devices uuid[] ≤ 256, proxy_threshold 2..256 default 8, updated_by,
updated_at)` with RLS. `ipam_ip_scan_jobs` gains `arp_devices`,
`arp_partial`, `arp_entries`, `arp_applied`, `arp_created`, `arp_conflicts`,
`arp_ignored jsonb` (reason → count). Expression index on the hex-only MAC
for search.

### D4 — Writers keep provenance

Address API: a user-provided non-empty `mac_address` sets `mac_source=manual`
(clears conflict); an empty one clears MAC and source. Host sync apply sets
`agent` whenever it writes a MAC. ARP never touches `agent`/`manual` MACs.

### D5 — Apply in one tenant transaction per scan

`repo.ApplyARP(ctx, tenant, ops, audit)` executes the planned ops and their
audit rows (`mac_learned`, `mac_changed`, `mac_conflict`, `address_created`
with `origin: arp`) plus one `arp_run` summary row, in one transaction, after
SNMP discovery and before correlation.

### D6 — Correlation extended to addresses

`PortLinkData` gains `Addresses []store.IPAddress` (tenant addresses with a
MAC). `BuildInput` adds them as `Host{AddressID, MAC}` — skipping an address
whose MAC equals a host-reported interface MAC (the interface link already
covers it). `Rank` is unchanged apart from carrying `AddressID`. `Correlate`
writes address link columns via `SetAddressLinks` with the same
re-confirm/supersede/stale logic and `port_linked`/`port_unlinked` audit rows
(subject kind `address`). Correlation runs after ARP apply.

**Per-switch links (hosts bonded across a switch pair).** A host with an MLAG /
LACP bond across two switches (prod: ns1 on cs1 port 17 and cs2 port 17, 21
MACs each) is learned on a port of each switch with equal MAC counts; a single
global winner would be a tie and link nothing. Like v3 (`correlateLinks`),
`Rank` now works per switch: on each switch the LLDP neighbour naming the host
wins, else the non-uplink port (≤ `max_macs_per_port`) with the fewest MACs
that learned the MAC — a tie between two ports *of the same switch* links
nothing on that switch. All per-switch links are returned; one is `Primary`
(an LLDP link if any, else the fewest MACs, ties by lowest switch id, then
port id), so two switches tying never means "no link". The primary stays in
the flat link columns (backward compatible); every per-switch link is kept in
`ipam_host_switch_links` (one row per host × switch), replaced in the same
tenant transaction by `SetInterfaceLinks` / `SetAddressLinks` (the host's
complete `Links` set). Links of switches not re-confirmed are kept until the
stale age. Added / removed secondary links are audited as
`port_linked` / `port_unlinked` with `"secondary": true`. Reads: interfaces
and addresses return `links` (primary first); `behind_addresses` and the
"device behind" also come from the per-switch table, so a bonded host shows
under both switches' ports. A daisy-chained access switch keeps the primary;
the upstream switch's port (not detected as an uplink) becomes a secondary
link, as in v3.

### D7 — Visibility

- Address JSON gains `mac_source`, `mac_source_device_id`, `mac_seen_at`,
  `mac_conflict`, `origin`, and `link` {switch id/name, port id/name, vlan,
  source, last_seen}.
- Switch interface responses gain `behind_addresses` (address, hostname) for
  ports that addresses are linked to (FR-012).
- Device detail (addresses tab) shows each bound address's link (FR-011).
- Scan job shows the ARP line (FR-013).

### D8 — Settings API and permission

`GET/PUT /api/ipam/v1/arp/settings` — read `ipam:read`, write
`subnets:manage` (network configuration; same holders as SNMP credentials).

### D9 — MAC search

Address list `mac` query param: strip non-hex, lower-case, 2–12 hex chars,
`LIKE %q%` on the normalised expression (index D3). Invalid input → 422.

### D10 — Performance

ARP walks add one table walk per device; 5,000 entries at ~60 rows per GETBULK
response are ~85 round trips — well under the per-device walk budget. The
planner is O(entries + addresses) in memory; one transaction per scan.

## STRIDE threat model

| Threat | Vector | Mitigation |
|---|---|---|
| **S**poofing | A host poisons a router's ARP to claim an IP | Agent/manual MACs never overwritten (D2.4); conflicts visible; proxy/VRRP filters |
| **T**ampering | Crafted SNMP responses (malformed OIDs/octets) | Decoder validates index/lengths, drops malformed rows; caps (65,536); fuzzed |
| **R**epudiation | Who/what changed a MAC | Audit rows per change with source device + per-scan summary |
| **I**nformation disclosure | ARP data across tenants | Tenant-scoped planner input, RLS on every table, one tenant tx |
| **D**enial of service | Huge ARP tables | Entry cap, per-device deadline, partial flag |
| **E**levation | Settings changed by read-only users | `subnets:manage` for PUT; gateway permission check |
