---
title: Discover hosts with scans
summary: Start, follow and cancel discovery scans; read their ping, SNMP and ARP results; understand retries and the ARP settings.
kind: guide
order: 40
tags: [ipam, scans, discovery, snmp, arp]
requires: scan:run
---
A scan pings every host address of one IPv4 subnet and records the hosts that
answer. With SNMP it also asks the network devices it finds about themselves
and about their ARP tables. Scans run in the background; **IPAM → Scans**
lists them, newest first, and updates live.

## Start a scan

You need `scan:run`. Either:

- open a subnet on **IPAM → Subnets** and choose **Scan**. SNMP discovery runs
  automatically when the subnet has credentials, its own or inherited; or
- choose **Start scan** on **IPAM → Scans**, pick the **Subnet** and turn
  **SNMP discovery** on or off.

The **Update DNS** switch is stored with the scan but has no effect yet.

A scan is refused when:

| Answer | Cause |
|---|---|
| `bad_request` | the subnet is IPv6, or it has more host addresses than the operator allows (`scan.max_hosts`, by default 1024: a /22) |
| `conflict` | a scan of this subnet is already pending or running |

## What a scan does

1. **Sweep.** Pings every host address of the subnet (not the network or
   broadcast address).
2. **Record.** Every host that answered is recorded in the subnet with
   status `active` and its last-seen time, and its reverse DNS name is looked
   up. An address that already exists only gets its last-seen time, and its
   hostname when the lookup found one; a host-reported address keeps its
   hostname and MAC. An `offline` address that answers becomes `active`. Other
   fields (status, description, device) are never changed, and addresses that
   do not answer are not removed.
3. **SNMP** (when enabled). Queries the live hosts with the subnet's
   effective credentials, read only, and records the devices that answer,
   their interfaces and switch ports. The credentials are read once when the
   scan starts.
4. **ARP** (when SNMP ran and ARP collection is on). Reads the ARP and
   neighbour tables of the devices that answered SNMP and fills in MAC
   addresses; see [ARP settings](#arp-settings).
5. After an SNMP scan, IPAM works out which switch port each known MAC sits
   behind.

## Read the result

| Column | Meaning |
|---|---|
| **Status** | `pending`, `scanning`, `completed`, `failed`, `cancelled` |
| **Progress** | 0–100 % |
| **Alive** | hosts that answered the ping |
| **New** | of those, addresses that were not recorded before |
| **SNMP** | `not requested`; `skipped` with a reason (no live hosts, no credentials, credentials unreadable); or `ran` with the credential source and the number of hosts probed, discovered, silent and rejected |
| **ARP** | `disabled`, `failed`, or devices and entries read, MACs applied, addresses created, conflicts and ignored entries by reason |

`rejected` hosts in the SNMP column answered but refused the credentials:
check them with **Test SNMP** on the subnet
([SNMP credentials](subnets.md#snmp-credentials)).

## Retries and cancel

When a scan fails (for example the database is briefly unavailable), it goes
back to `pending` with the message `retry scheduled: …` and runs again after
5 seconds, then 10, 20, ... up to `scan.max_retries` times (by default 3).
Then it is `failed`.

**Cancel** stops a `pending` or `scanning` job; it ends `cancelled`.
Cancelling a finished scan answers `conflict`.

## ARP settings

The **ARP-based MAC linking** card on **IPAM → Host sync** decides how scans
use ARP tables (changing it needs `subnets:manage`):

- **Collect ARP tables during scans** — on by default.
- **Ignore a MAC answering for more than (IPs per scan)** — a MAC that
  answers for more addresses than this (2–256, default 8) is treated as proxy
  ARP and ignored.
- **Never use as ARP source** — up to 256 devices whose tables are ignored.

Entries are also ignored when the MAC is invalid, multicast or broadcast, a
VRRP or HSRP virtual-router MAC, the interface MAC of a network device, or
the IP lies outside every subnet of the tenant. An address learned from ARP
inside a known subnet that had no record gets one (status `active`, origin
`arp`).

## Scheduled scans

When the operator connects IPAM to the scheduler, a scheduled task of type
`ipam:scan-network` queues scans of one subnet (`subnetId` or `cidr`) or of
every subnet (`all`, the default). Subnets that are IPv6, too large or
already being scanned are skipped. Such scans show trigger `auto`.

Related: [subnets](subnets.md), [addresses](addresses.md).
