# Contract: audit vocabulary and realtime events (020)

## Audit rows (`ipam_audit_events`, written in the apply transaction)

Common columns for sync changes: `actor_kind = system`, `actor_id =
hostsync`, `outcome = ok`. Common `detail` keys: `inventory_host_id`,
`run_id`, `trigger` (`poll` | `reconcile` | `resync:<user id>` |
`resync_all:<user id>`), `changes` (map field → `{before, after}`).
Detail keys must not contain `owner`, `contact`, `secret`, `credential`,
`snmp`, `ipmi`, `password` (the existing guard drops them).

| action | subject_kind | subject_id | extra detail |
|---|---|---|---|
| `device_created` (existing) | device | device id | `match: created` |
| `device_updated` (existing) | device | device id | `match: host_id|serial|name`, `changes` |
| `device_not_reported` NEW | device | device id | `inventory_status: retired|deleted` |
| `interface_created` NEW | interface | interface id | `device_id`, `name`, `kind` |
| `interface_updated` NEW | interface | interface id | `changes` |
| `interface_not_reported` NEW | interface | interface id | `device_id` |
| `subnet_created` (existing) | subnet | subnet id | `cidr`, `origin: host_sync`, `parent_id` |
| `address_created` (existing) | address | address id | `address`, `device_id`, `interface` |
| `address_updated` (existing) | address | address id | `changes` (incl. claim of an unowned address) |
| `address_moved` NEW | address | address id | `address`, `previous_device_id`, `device_id`, `move_count` |
| `address_released` NEW | address | address id | `address`, `previous_device_id` |
| `address_conflict` NEW | address | address id | `address`, `move_count`, `window_hours` |
| `address_conflict_cleared` NEW | address | address id | `by: window|user` (user actor when cleared by hand) |
| `packages_updated` NEW | package | device id | `pending`, `security`, `added[]`, `removed[]` (≤ 200 names + counts) |
| `hypervisor_linked` NEW | device | guest device id | `hypervisor_device_id`, `guest_ref` |
| `hypervisor_unlinked` NEW | device | guest device id | `hypervisor_device_id` |
| `port_linked` NEW (US5) | interface | host interface id | `switch_device_id`, `switch_interface_id`, `vlan`, `source` |
| `port_unlinked` NEW (US5) | interface | host interface id | `reason: stale|superseded` |
| `hostsync_run` NEW | hostsync | run id | `hosts_fetched`, `hosts_applied`, `hosts_failed`, `changes` |
| `hostsync_settings_updated` NEW | hostsync | tenant id | actor = user; `changes` |
| `hostsync_resync_requested` NEW | hostsync | device id or `all` | actor = user |

Subject kinds added: `package`, `hostsync`. Validation (`audit.Validate`)
accepts the new types; `audit.Known` lists them.

## Realtime events (`platform:events:<tenant>`, published after commit)

The sync publishes the existing address events so that the dns module keeps
following IPAM:

- `ipam.ip_address.created` — new reported address.
- `ipam.ip_address.updated` — moved, claimed, released, primary/hostname
  change (`action` in the payload: `moved|claimed|released|updated`).

New content-free events for the UI:

- `ipam.hostsync.applied` `{device_id, changes}`
- `ipam.hostsync.status` `{state}` (tenant state change ok/degraded/disabled)

Payloads carry ids and counts only (`internal/events`), never MACs,
package names or BMC data.
