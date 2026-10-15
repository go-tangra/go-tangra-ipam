---
title: Plan subnets
summary: Nest subnets, split a block into equal children, change or delete a subnet, and give a subnet SNMP credentials that its children inherit.
kind: guide
order: 20
tags: [ipam, subnets, split, snmp, credentials]
requires: subnets:manage
---
Subnets are the backbone of the address plan. Open **IPAM → Subnets** to see
them as a list or, with **Tree**, nested under their parents. Choose a subnet
to open it: the drawer shows its utilization, details, child subnets and the
latest scan, and has the buttons **Scan**, **Split**, **Add child**, **Edit**
and **Delete**.

## Nesting and overlap

- A subnet with a **Parent subnet** must lie strictly inside it (a longer
  prefix of the same address family).
- Apart from its own parents and children, a subnet may not overlap any
  other subnet of the tenant. A save that would overlap is refused with
  `validation_failed`.
- The **Gateway**, if set, must be a host address inside the block.
- **Status** is `active` (the default), `reserved`, `deprecated` or
  `deleted`. It is a label: it does not stop allocation or scans.

**Add child** opens a new-subnet form with the parent preset.

## Split a subnet

**Split** carves a subnet into equal children of a longer prefix:

1. Pick one of the quick divisions (for example `4 × /26`) or type a
   **Child prefix length**.
2. Choose **Preview**. The drawer lists the blocks it **Will create** and
   the ones it **Skipped** because they overlap an existing subnet.
3. Choose **Create N subnets**.

Each child is named `<parent name> — <block>`, inherits the parent's VLAN and
location, and has status `active`. Because existing blocks are skipped, you
can split a partly divided parent again. If no free block of that size is
left, the split is refused.

## Change or delete a subnet

**Edit** changes any field, including the CIDR, as long as the rules above
still hold. Recorded addresses and child subnets are not moved or checked
again when you change the CIDR, so narrow a block only after you have moved
what lies outside the new range. Editing a subnet never changes its SNMP
credentials.

**Delete** removes a subnet that holds no recorded addresses. While it still
holds addresses the delete is refused with `conflict`: release them first on
**IP Addresses**. Through the API, `DELETE /api/ipam/v1/subnets/<id>?force=true`
deletes the subnet together with its addresses and scan jobs. Child subnets
are kept and become top-level subnets.

## SNMP credentials

Scans can discover switches, routers and firewalls over SNMP. The credentials
are set per subnet, on the **SNMP credentials** card of the subnet's **Edit**
form:

1. Choose **Set** (or **Replace**).
2. Pick **Version**:
   - **SNMP v2c**: a **Community**;
   - **SNMP v3**: a **User**, a **Security level** (`authNoPriv` or
     `authPriv`), an **Authentication protocol** and password and, for
     `authPriv`, a **Privacy protocol** and password. Passwords need at
     least 8 characters.
3. Choose **Save credentials**.

MD5, SHA (SHA-1) and DES work but are marked *weak protocol*.

The values are encrypted and never shown again, not to administrators
either, and are not part of exports. The card only says whether credentials
exist and where they come from:

- **own** — set on this subnet;
- **inherited** — taken from the nearest parent subnet that has them;
- **none** — neither this subnet nor any parent has credentials.

So setting credentials once on a large parent covers all its children.
**Clear** removes the subnet's own credentials; it then inherits again.

### Test the credentials

Type an address inside the subnet in **Test SNMP against** and choose **Test
SNMP** (needs `scan:run`). The answer is one of:

| Result | Meaning |
|---|---|
| Answered: *sysName* — *sysDescr* | the device answered with the credentials |
| No response (timeout) | nothing answered on UDP 161 in time |
| Credentials rejected | authentication failure, unknown user or privacy failure |
| No credentials configured | neither the subnet nor a parent has credentials |
| Credentials are configured but unreadable | re-enter them |

You can run at most 10 tests a minute.

Related: [scans](scans.md), [reference](../reference.md#subnet-fields).
