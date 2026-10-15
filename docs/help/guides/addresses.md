---
title: Allocate and track addresses
summary: Take the next free address, allocate several at once, find verified free addresses, ping, release, search by MAC and clear address conflicts.
kind: guide
order: 30
tags: [ipam, addresses, allocation, mac, conflicts]
requires: ipam:read
---
Open **IPAM → IP Addresses** to see every recorded address, sorted and paged
on the server. Filter by **Subnet**, **Status**, **Type**, **Host report**,
**Hostname** or **MAC (full or partial)**. The list updates live when scans
or the host sync record addresses.

## Take the next free address

**Allocate** (needs `addresses:allocate`) records the lowest free host
address of the chosen subnet with status `active`. It never hands out:

- the network and broadcast address of an IPv4 block smaller than /31;
- the subnet's gateway;
- an address that is already recorded in the subnet, whatever its status;
- the first or last addresses an operator reserved for all subnets
  (`allocation.skip_first` / `skip_last`).

Two users allocating at the same time never get the same address. When the
subnet has no free address left, the answer is `no_available_address`.

**Bulk** allocates **Count** addresses in one go, the lowest free ones in
order. If the subnet runs out part way, the addresses allocated so far are
kept.

Leave **Hostname** and **Hostname prefix** empty in these dialogs: the
current server refuses a request that carries them (`malformed_body`).
Through the API, bulk allocation takes `prefix` and names the addresses
`<prefix>0`, `<prefix>1`, and so on:

```json
{"subnet_id": "<subnet id>", "count": 3, "prefix": "web"}
```

sent to `POST /api/ipam/v1/ip-addresses/bulk-allocate`. A single allocation
(`POST /api/ipam/v1/ip-addresses/allocate`) takes `subnet_id` and, optionally,
`start_from` (no address below it) and `skip` (a list of addresses to pass
over).

## Find free addresses without taking them

**Suggest** lists up to **Count** free addresses of a subnet without
recording anything. Each candidate is checked on the network first: it must
not answer ping and must have none of the ports 22, 80, 443 and 3389 open.
An address that is free in IPAM but in use on the wire is skipped, so you
may get fewer suggestions than you asked for.

## Ping, release, look up

- **Ping** (needs `scan:run`) sends one ICMP echo to the address and shows
  the round-trip time or `down`.
- **Release** (needs `addresses:manage`) deletes the address record. The
  address becomes free for allocation again.
- `GET /api/ipam/v1/ip-addresses/find?address=10.0.0.5` returns the record of
  one address.

## Search by MAC

**MAC (full or partial)** accepts 2 to 12 hex digits in any notation:
`0a5c`, `d2-f1`, `00:1a:2b:3c:4d:5e` or `001a.2b3c.4d5e`. Anything else is
refused with `validation_failed`.

Each MAC records where it came from (`mac_source`):

| Source | Set by |
|---|---|
| `manual` | a user, through the address API |
| `agent` | the host sync, from the inventory agent |
| `arp` | a scan with SNMP, from a router's or switch's ARP table |

An ARP-learned MAC never overwrites a manual or agent MAC. When they differ,
the address shows the observed MAC in `mac_conflict` until they agree again.

## Address conflicts

The host sync flags an address as a **conflict** when it moves between
devices too often (by default 3 moves within 24 hours), which usually means a
duplicate IP on the network. Fix the cause, then choose **Clear conflict**
on the address (needs `addresses:manage`).

An address that a host stops reporting is detached from the device and kept
with **Host report** `not reported`; it is never deleted automatically.

Related: [scans](scans.md), [reference](../reference.md#ip-address-fields).
