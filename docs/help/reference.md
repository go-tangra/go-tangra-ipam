---
title: Reference
summary: Fields and states of subnets, addresses, devices and scans; permissions and roles; API routes, errors and limits.
kind: reference
order: 60
tags: [ipam, reference, permissions, api, limits, states]
requires: ipam:read
---
## Subnet fields

| Field | Meaning |
|---|---|
| `name` | required |
| `cidr` | IPv4 or IPv6 block, for example `10.0.0.0/24` |
| `parent_id` | the containing subnet; the block must lie strictly inside it |
| `status` | `active` (default), `reserved`, `deprecated`, `deleted` |
| `gateway` | a host address inside the block; never allocated |
| `dns_servers`, `description` | free text |
| `vlan_id`, `location_id` | the VLAN and location |
| `ip_version`, `network_address`, `broadcast_address`, `mask`, `prefix_length` | derived from `cidr` |
| `total_addresses`, `used_addresses`, `available_addresses`, `utilization` | computed: all addresses of the block, recorded ones, the difference, and used/total in % |
| `snmp` | effective SNMP state: `none`, `own` or `inherited` (with the source subnet); never a credential |
| `origin` | `manual`, or `host_sync` for subnets the host sync created |

## IP address fields

| Field | Meaning |
|---|---|
| `address`, `subnet_id` | the address and its subnet; an address is recorded once per tenant |
| `status` | `active` (default), `reserved`, `dhcp`, `deprecated`, `offline` |
| `address_type` | `host` (default), `gateway`, `broadcast`, `network`, `virtual`, `anycast` |
| `hostname`, `mac_address`, `description` | |
| `device_id`, `interface_name` | the device and interface using it |
| `last_seen` | last time a scan or the host sync saw it |
| `report_state` | empty, `reported` or `not_reported` (host sync) |
| `conflict` | set by the host sync when the address moves between devices too often |
| `mac_source` | `manual`, `agent` or `arp`; read only |
| `mac_conflict` | an ARP-observed MAC that disagrees with a manual or agent MAC; read only |
| `origin` | `arp` for addresses created from an ARP table; read only |
| `link`, `links` | the switch and port the address sits behind; read only |

## Device fields

| Field | Values |
|---|---|
| `device_type` | `server`, `vm`, `router`, `switch`, `firewall`, `load_balancer`, `access_point`, `storage`, `printer`, `phone`, `workstation`, `container`, `other` |
| `status` | `active`, `planned`, `staged`, `decommissioned`, `offline`, `failed`, `available` |
| `source` | `manual`, `scan` or `host_report`; read only |
| `report_state` | `reported` or `not_reported` for host-reported devices |
| `hardware_summary` | CPU, memory and disk summary reported by the inventory agent; read only |

Devices also carry manufacturer, model, serial number, asset tag, location,
rack and position, primary and management IPs, OS and firmware. Hardware has
no write route: it comes only from the inventory agent.

## Other records

| Record | Values |
|---|---|
| VLAN `status` | `active`, `reserved`, `deprecated` |
| location `location_type` | `region`, `country`, `city`, `datacenter`, `building`, `floor`, `room`, `rack`, `site`, `branch` |
| location `status` | `active`, `planned`, `decommissioned` |
| IP group member `member_type` | `address`, `range`, `subnet` |
| group `status` | `active`, `inactive` |

## Scan job

| State | Meaning | Final |
|---|---|:-:|
| `pending` | waiting for a worker, or waiting for a retry | |
| `scanning` | running | |
| `completed` | finished | yes |
| `failed` | failed and no retries left | yes |
| `cancelled` | cancelled by a user | yes |

Fields: `subnet_id`, `progress`, `status_message`, `total_addresses`,
`scanned_count`, `alive_count`, `new_count`, `updated_count`,
`snmp_status` (`not_requested`, `no_live_hosts`, `no_credentials`,
`credentials_unreadable`, `ran`), `snmp_source_subnet_id`, `snmp_probed`,
`snmp_discovered_count`, `snmp_no_answer`, `snmp_rejected`, `arp_status`
(`ran`, `disabled`, `failed`), `arp_devices`, `arp_partial`, `arp_entries`,
`arp_applied`, `arp_created`, `arp_conflicts`, `arp_ignored` (by reason:
`invalid`, `multicast`, `virtual_router`, `network_device`, `proxy_arp`,
`outside_subnets`, `excluded_device`), `triggered_by` (`manual` or `auto`),
`retry_count`, `max_retries`.

## Permissions and roles

| Permission | Allows |
|---|---|
| `ipam:read` | read everything: subnets, addresses, devices, VLANs, locations, groups, scans, settings, the live event stream; suggest free addresses |
| `subnets:manage` | create, change, split and delete subnets; set and clear SNMP credentials; change the ARP settings |
| `addresses:manage` | create, change and release addresses; clear address conflicts |
| `addresses:allocate` | allocate the next free address and bulk allocate |
| `devices:manage` | devices, interfaces and packages; attach a BMC secret; re-sync hosts |
| `vlans:manage`, `locations:manage`, `groups:manage` | VLANs, locations, IP and host groups |
| `scan:run` | start and cancel scans; ping; test SNMP credentials |
| `dns:manage` | change and test the tenant DNS settings |
| `backup:manage` | export and import the tenant's IPAM data |
| `power:control` | BMC power status and actions, sensors, event log |
| `kvm:access` | open a KVM console |
| `hostsync:manage` | turn the host sync on or off, change its reconcile interval and interface exclusions |

`power:control` and `kvm:access` are not enough on their own: the caller also
needs the `platform-admin`, `admin` or `owner` role. Changing the DNS
settings likewise needs the `admin` or `owner` role besides `dns:manage`.

| Module role | Permissions |
|---|---|
| IPAM administrator | all 14 |
| IPAM operator | `ipam:read`, `addresses:allocate`, `scan:run` |
| IPAM viewer | `ipam:read` |

Built-in roles: `owner` and `admin` hold all 14; `operator` holds all but
`power:control`, `kvm:access` and `hostsync:manage`; `member` and `auditor`
hold `ipam:read`.

## API

The browser API is under `/api/ipam/v1`, through the gateway. Changing
requests need the `X-CSRF-Token` header. Request bodies with unknown fields
are refused.

| Route | Permission |
|---|---|
| `GET /subnets`, `/subnets/tree`, `/subnets/{id}`, `/subnets/{id}/stats`, `/subnets/{id}/snmp` | `ipam:read` |
| `POST /subnets`, `PUT /subnets/{id}`, `DELETE /subnets/{id}`, `POST /subnets/{id}/split`, `PUT` and `DELETE /subnets/{id}/snmp` | `subnets:manage` |
| `POST /subnets/{id}/scan`, `POST /subnets/{id}/snmp/test` | `scan:run` |
| `GET /ip-addresses`, `/ip-addresses/{id}`, `/ip-addresses/find`, `/ip-addresses/suggest` | `ipam:read` |
| `POST /ip-addresses/allocate`, `POST /ip-addresses/bulk-allocate` | `addresses:allocate` |
| `POST /ip-addresses`, `PUT` and `DELETE /ip-addresses/{id}`, `POST /ip-addresses/{id}/clear-conflict` | `addresses:manage` |
| `POST /ip-addresses/{id}/ping` | `scan:run` |
| `GET /ip-scans`, `/ip-scans/{id}` | `ipam:read` |
| `POST /ip-scans`, `POST /ip-scans/{id}/cancel` | `scan:run` |
| `GET /devices` and their sub-resources | `ipam:read` |
| changes to devices, interfaces, packages and `/devices/{id}/bmc`; `POST /host-sync/resync` | `devices:manage` |
| `GET` and `POST /devices/{id}/power`, `GET /devices/{id}/sensors`, `/devices/{id}/sel` | `power:control` |
| `POST /devices/{id}/kvm-session` | `kvm:access` |
| `GET /arp/settings`, `/host-sync/settings`, `/host-sync/status`, `/dns-config` | `ipam:read` |
| `PUT /arp/settings` | `subnets:manage` |
| `PUT /host-sync/settings` | `hostsync:manage` |
| `PUT /dns-config`, `POST /dns-config/test` | `dns:manage` |
| `POST /backup/export`, `POST /backup/import` | `backup:manage` |
| `GET /stats`, `GET /stream` | `ipam:read` |

`POST /subnets` and `PUT /subnets/{id}` accept `?allow_overlap=true` to skip
the overlap check. Lists take `page`, `page_size` (at most 200), `sort` and
`order`.

### Errors

| Status | `reason` | Typical cause |
|---|---|---|
| 400 | `malformed_body` | invalid JSON or an unknown field |
| 400 | `bad_request` | scan of an IPv6 or too large subnet; unknown power action |
| 403 | `forbidden` | the permission or role is missing |
| 404 | `not_found` | no such record in your tenant |
| 409 | `conflict` | duplicate, subnet still holds addresses, scan already active or already finished |
| 413 | `body_too_large` | the request body is over its limit |
| 422 | `validation_failed` | a field is invalid, the block overlaps or lies outside its parent |
| 429 | `rate_limited` | more than 10 SNMP tests a minute |
| 507 | `no_available_address` | the subnet has no free address left |

## Limits

| Limit | Value |
|---|---|
| list page size | 200 |
| JSON request body | 64 KiB; less on some routes (split and SNMP credentials 4 KiB, BMC reference 1 KiB); import 256 MiB |
| hosts per scan | `scan.max_hosts` (default 1024), IPv4 only |
| scan retries | `scan.max_retries` (default 3) |
| SNMP credential values | 1–256 characters; v3 passwords at least 8 |
| SNMP tests | 10 a minute per user |
| ARP entries read per device per scan | 65,536 |
| ARP excluded devices | 256 |
| host sync excluded interface patterns | 64 |
| host sync reconcile interval | 15–1440 minutes |
