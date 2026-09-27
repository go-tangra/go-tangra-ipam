# Contract: IPAM HTTP + audit — ARP-based MAC linking (022)

## New routes

### GET /api/ipam/v1/arp/settings — `ipam:read`

200 `ARPSettings`:

```json
{"enabled": true, "excluded_devices": ["01a0…"], "proxy_threshold": 8,
 "updated_by": "u-1", "updated_at": "2026-09-27T10:00:00Z"}
```

### PUT /api/ipam/v1/arp/settings — `subnets:manage`, CSRF, body ≤ 16 KiB

Request (`additionalProperties: false`): `enabled` (bool), `excluded_devices`
(uuid[] ≤ 256, each an existing device of the tenant), `proxy_threshold`
(2..256). 200 settings; 422 `validation_failed` with `detail.field`.

## Changed responses

- **Addresses** (`GET /ip-addresses`, `GET /ip-addresses/{id}`, device
  addresses): new read-only fields `mac_source` (`manual|agent|arp|""`),
  `mac_source_device_id`, `mac_seen_at`, `mac_conflict`, `origin` (`arp|""`),
  `link` `{switch_id, switch_name, port_id, port_name, vlan, source,
  last_seen}` or absent, and `links` — every per-switch link `{switch_id,
  switch_name, port_id, port_name, vlan, source, last_seen, primary}`,
  primary first (a host bonded across a switch pair has one per switch;
  `link` is the primary), absent when none.
- **Address list** gains query `mac` (2–12 hex digits after stripping
  `:-.` and spaces; partial match). Invalid → 422 `validation_failed`
  (`detail.field = mac`).
- **Address create/update**: a non-empty `mac_address` sets
  `mac_source=manual` and clears `mac_conflict`; an empty one clears the MAC
  and its source. `mac_source`, `origin`, `link`, `links` in the body are ignored.
- **Device interfaces** (`GET /devices/{id}/interfaces`) of switches gain
  `behind_addresses` `[{address_id, address, hostname}]` (addresses linked
  as primary or per-switch link; `behind_device_*` likewise), and host
  interfaces gain `links` (as for addresses; the flat `remote_*`/`link_*`
  fields are the primary).
- **Scan jobs**: `arp_devices`, `arp_partial`, `arp_entries`, `arp_applied`,
  `arp_created`, `arp_conflicts`, `arp_ignored` (object reason → count).

## Audit events (actor `system`/`scan` unless noted)

| Event | Subject | Detail keys |
|---|---|---|
| `mac_learned` | address | `address`, `mac`, `source_device_id`, `job_id` |
| `mac_changed` | address | `address`, `mac`, `previous_mac`, `source_device_id`, `job_id` |
| `mac_conflict` | address | `address`, `mac` (stored), `observed_mac`, `source_device_id` |
| `address_created` | address | `address`, `origin: arp`, `mac`, `subnet_id`, `source_device_id` |
| `arp_run` | scan job | counters as in the scan job, `ignored` map |
| `port_linked` / `port_unlinked` | address | as for interfaces (`switch_device_id`, `switch_interface_id`, `vlan`, `source`, `reason`) |
| `port_linked` / `port_unlinked` | interface, address | a secondary per-switch link added / removed: same keys plus `secondary: true` |
| `arp_settings_updated` (actor user) | tenant | `enabled`, `excluded_count`, `proxy_threshold` |
