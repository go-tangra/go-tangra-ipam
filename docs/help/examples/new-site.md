---
title: "Example: address plan for a new site"
summary: Reserve a /22 for a new office, split it into four /24 networks, give the whole site one set of SNMP v3 credentials and scan the management network.
kind: example
order: 50
tags: [ipam, example, subnets, split, snmp, scans]
requires: subnets:manage
---
**Goal.** A new office in Sofia gets the block `10.20.0.0/22`. It needs four
networks: servers, users, voice and management. The switches and routers
answer SNMP v3 with one read-only user, and you want IPAM to discover them.

**Plan.** One parent subnet for the site, split into four /24 children. The
SNMP credentials go on the parent only; the children inherit them.

## 1. The site block

On **IPAM → Subnets**, choose **New subnet**:

| Field | Value |
|---|---|
| Name | `sofia` |
| CIDR | `10.20.0.0/22` |
| Location | the Sofia office (create it first under **Locations** if needed) |

The same through the API, `POST /api/ipam/v1/subnets`:

```json
{"name": "sofia", "cidr": "10.20.0.0/22", "location_id": "<location id>"}
```

## 2. Four networks

Open `sofia`, choose **Split**, pick **4 × /24** and choose **Preview**. The
drawer will create `10.20.0.0/24`, `10.20.1.0/24`, `10.20.2.0/24` and
`10.20.3.0/24`, each with the location of `sofia`. Choose **Create 4
subnets**.

Through the API, `POST /api/ipam/v1/subnets/<sofia id>/split` with
`"dry_run": true` previews and without it creates:

```json
{"prefix_length": 24, "dry_run": true}
```

The children are named `sofia — 10.20.0.0/24` and so on. Open each one and
use **Edit** to give it a clear name and gateway:

| CIDR | Name | Gateway |
|---|---|---|
| `10.20.0.0/24` | `sofia-servers` | `10.20.0.1` |
| `10.20.1.0/24` | `sofia-users` | `10.20.1.1` |
| `10.20.2.0/24` | `sofia-voice` | `10.20.2.1` |
| `10.20.3.0/24` | `sofia-mgmt` | `10.20.3.1` |

## 3. One set of SNMP credentials

Open `sofia`, choose **Edit** and, on the **SNMP credentials** card, choose
**Set**: **SNMP v3**, security level **authPriv**, the user, **SHA-256** with
its password and **AES** with its password. Save.

Through the API, `PUT /api/ipam/v1/subnets/<sofia id>/snmp`:

```json
{
  "version": 3,
  "user": "ipam-ro",
  "security_level": "authPriv",
  "auth_protocol": "SHA256",
  "auth_password": "<at least 8 characters>",
  "priv_protocol": "AES",
  "priv_password": "<at least 8 characters>"
}
```

Open `sofia-mgmt`, choose **Edit**: its card says *Inherited from sofia
(10.20.0.0/22)*. Type the address of a switch, for example `10.20.3.2`, in
**Test SNMP against** and choose **Test SNMP**. It should answer with the
switch's name.

## 4. Scan the management network

Open `sofia-mgmt` and choose **Scan**. Because the subnet has inherited
credentials, SNMP discovery runs without asking. On **IPAM → Scans** the job
ends `completed` with, for example:

| Alive | New | SNMP |
|---|---|---|
| 6 | 6 | ran, inherited from `sofia`: probed 6, discovered 3, no answer 3, rejected 0 |

The three devices that answered SNMP are now on **IPAM → Devices**. If they
are routers or layer-3 switches, their ARP tables also fill in the MAC
addresses of hosts in the other Sofia subnets (the **ARP** column counts
them).

## Variations

- **Different credentials for one network.** Set credentials on that child
  itself: a subnet's own credentials win over inherited ones.
- **Reserve servers' addresses.** Choose **Bulk** on **IP Addresses** with
  subnet `sofia-servers` and count 3: you get `10.20.0.2` to `10.20.0.4` (the
  gateway `.1` is skipped).

Related: [plan subnets](../guides/subnets.md), [scans](../guides/scans.md).
