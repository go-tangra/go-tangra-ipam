---
title: IPAM
summary: Plan and track subnets, IP addresses, devices, VLANs and locations; discover live hosts with scans; keep hosts current from the inventory agent.
kind: guide
order: 0
tags: [ipam, overview, subnets, addresses]
requires: ipam:read
---
IPAM is the IP address management module. It keeps the tenant's address plan
(subnets and the addresses inside them), the devices that use those addresses,
and the VLANs and locations they belong to. It can also look at the network:
discovery scans find live hosts, and the host sync keeps hosts that run the
inventory agent up to date without manual edits.

## Concepts

- **Subnet** — a CIDR block (IPv4 or IPv6) with a name, status, optional
  gateway, VLAN and location. Subnets nest: a child must lie inside its
  parent, and siblings may not overlap. Each subnet shows its utilization:
  recorded addresses out of all addresses in the block.
- **IP address** — one recorded address in a subnet, with a status
  (`active`, `reserved`, `dhcp`, `deprecated`, `offline`), hostname, MAC and
  optional device. An address exists in IPAM once someone allocates or
  creates it, a scan finds it alive, or the host sync or an ARP table reports
  it.
- **Device** — a server, switch, router, VM or other equipment, with
  interfaces, installed packages and addresses.
- **VLAN**, **location** — a VLAN ID with a name, and a place in a location
  tree (region, site, building, room, rack, ...).
- **Groups** — named IP groups (addresses, ranges and subnets) and host
  groups (devices).
- **Scan** — a background job that pings every host address of an IPv4
  subnet, records the ones that answer and, with SNMP, discovers network
  devices and their ARP tables.

## Pages in the portal

| Page | Shows |
|---|---|
| Subnets | the subnet list and tree; open a subnet to scan, split, add a child, edit or delete it |
| IP Addresses | recorded addresses with filters (subnet, status, type, hostname, MAC); **Allocate**, **Bulk**, **Suggest**, **Ping** |
| Devices | devices with their interfaces, packages, addresses, hardware and, for administrators, **Power / KVM** |
| VLANs, Locations, Groups | the supporting records |
| Scans | discovery scans, their progress and their SNMP and ARP results; **Start scan** |
| Host sync | the state of the inventory host sync, its settings and the ARP settings |
| Dashboard | tenant totals |

Every page needs `ipam:read`. What you can change depends on your role; see
[permissions and roles](reference.md#permissions-and-roles).

## Where to go next

- [Get started](getting-started.md): a subnet, an address and a scan in five minutes.
- [Plan subnets](guides/subnets.md): nesting, splitting, SNMP credentials.
- [Allocate and track addresses](guides/addresses.md)
- [Discover hosts with scans](guides/scans.md)
- [Example: address plan for a new site](examples/new-site.md)
- [Reference](reference.md): fields, states, permissions, API and limits.
