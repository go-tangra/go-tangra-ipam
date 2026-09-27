# Data Model: ARP-based MAC linking (022)

## 1. `ipam_ip_addresses` additions (migration 0008)

| Column | Type | Notes |
|---|---|---|
| `mac_source` | text NOT NULL DEFAULT '' | `''` (no MAC), `manual`, `agent`, `arp` — CHECK |
| `mac_source_device_id` | uuid NULL | device whose ARP table reported the MAC (`arp`) |
| `mac_seen_at` | timestamptz NULL | last time the MAC was confirmed (any source) |
| `mac_conflict` | text NOT NULL DEFAULT '' | ARP MAC disagreeing with an `agent`/`manual` MAC |
| `origin` | text NOT NULL DEFAULT '' | `''` or `arp` (created from ARP) — CHECK |
| `link_switch_id` | uuid NULL | switch device the address is connected to |
| `link_port_id` | uuid NULL | switch interface |
| `link_port_name` | text NOT NULL DEFAULT '' | |
| `link_vlan` | int NOT NULL DEFAULT 0 | |
| `link_source` | text NOT NULL DEFAULT '' | `snmp_fdb` \| `lldp` |
| `link_last_seen` | timestamptz NULL | |

Backfill in the migration: `mac_address <> '' AND report_state <> ''` →
`agent`; other `mac_address <> ''` → `manual`. Index:
`(tenant_id, regexp_replace(lower(mac_address), '[^0-9a-f]', '', 'g'))`.

## 2. `ipam_arp_settings` (migration 0008, RLS like 0004)

| Column | Type | Notes |
|---|---|---|
| `tenant_id` | uuid PK | |
| `enabled` | boolean NOT NULL DEFAULT true | |
| `excluded_devices` | uuid[] NOT NULL DEFAULT '{}' | ≤ 256 |
| `proxy_threshold` | int NOT NULL DEFAULT 8 | 2..256 |
| `updated_by` | text NOT NULL DEFAULT '' | |
| `updated_at` | timestamptz NOT NULL DEFAULT now() | |

Absent row = defaults.

## 3. `ipam_ip_scan_jobs` additions (migration 0008)

`arp_devices` int, `arp_partial` int, `arp_entries` bigint, `arp_applied`
bigint, `arp_created` bigint, `arp_conflicts` bigint (all NOT NULL DEFAULT 0),
`arp_ignored jsonb NOT NULL DEFAULT '{}'` — reason → count with reasons
`invalid`, `multicast`, `virtual_router`, `network_device`, `proxy_arp`,
`outside_subnets`, `excluded_device`.

## 4. Go types

- `snmp.ARPEntry {IP, MAC string; IfIndex int}`; `DiscoveredDevice.ARP
  []ARPEntry`, `DiscoveredDevice.ARPPartial bool`; `Creds.CollectARP bool`.
- `arpplan.Observation {DeviceID string; Entries []snmp.ARPEntry}`.
- `arpplan.Input {Observations; Subnets []store.Subnet; Addresses
  []store.IPAddress; NetworkMACs map[string]bool; Settings}`.
- `arpplan.Op {Kind (fill|update|conflict|clear_conflict|create|touch);
  AddressID, IP, SubnetID, MAC, PreviousMAC, SourceDeviceID string}`.
- `arpplan.Plan {Ops []Op; Audit []store.AuditRow; Ignored map[string]int;
  Entries, Conflicts int}`.
- `store.IPAddress` gains the §1 fields (`Link *AddressLink` in JSON).
- `store.ARPSettings`, `store.IPScanJob` ARP counters.
- `portlink.Host` gains `AddressID`; `portlink.Link` gains `AddressID`.

## 5. Provenance rules (FR-005/006/009)

| Stored source | ARP MAC equal | ARP MAC different | No stored MAC |
|---|---|---|---|
| `''` | — | — | `fill` → source `arp` |
| `arp` | `touch` | `update` (audited) | — |
| `agent` / `manual` | `touch` + `clear_conflict` | `conflict` (store in `mac_conflict`) | — |
| no address | — | — | `create` (origin `arp`) |

Writers: address API sets `manual` (or clears), host sync sets `agent`.
