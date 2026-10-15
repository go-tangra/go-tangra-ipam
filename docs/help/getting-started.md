---
title: Get started
summary: Create a subnet, allocate its first free address and run a discovery scan.
kind: guide
order: 10
tags: [ipam, getting-started, subnets, addresses, scans]
requires: subnets:manage
---
This walk-through creates a subnet, takes an address from it and scans it for
live hosts.

You need `subnets:manage` to create the subnet, `addresses:allocate` to take
an address and `scan:run` to scan. The *IPAM administrator* role has all
three; the built-in `operator` and `admin` roles too.

## 1. Create a subnet

1. Open **IPAM → Subnets** and choose **New subnet**.
2. Fill in a **Name**, for example `office-lan`, and the **CIDR**, for example
   `192.168.10.0/24`.
3. Optionally set the **Gateway** (it must be a host address inside the
   block), **DNS servers**, **VLAN** and **Location**.
4. Choose **Save**. The subnet appears with status `active` and 0%
   utilization.

If the block overlaps a subnet you already have, the save is refused. To
place it inside a bigger block, pick that block as **Parent subnet**; see
[plan subnets](guides/subnets.md).

## 2. Allocate an address

1. Open **IPAM → IP Addresses** and choose **Allocate**.
2. Pick `office-lan` as **Subnet**, leave **Hostname** empty and choose
   **Allocate**.

IPAM records the lowest free host address, here `192.168.10.1` (or
`192.168.10.2` if `.1` is the gateway), with status `active`. The subnet's
utilization goes up by one address.

## 3. Scan the subnet

1. Back in **IPAM → Subnets**, open `office-lan` and choose **Scan**.
2. The drawer follows the scan until it ends: it pings every host address,
   records the ones that answer and looks up their reverse DNS names.
3. Open **IPAM → Scans** to see the job: **Alive** is the number of hosts that
   answered, **New** the addresses that were not recorded before.

## Next steps

- [Allocate in bulk, find free addresses, read conflicts](guides/addresses.md)
- [Add SNMP credentials so scans discover switches and routers](guides/subnets.md#snmp-credentials)
- [Understand scan results](guides/scans.md)
