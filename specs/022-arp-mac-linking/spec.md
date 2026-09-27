# Feature Specification: Link Agentless Hosts to Switch Ports via ARP Tables

**Feature Branch**: `022-arp-mac-linking`

**Created**: 2026-09-27

**Status**: Draft

**Input**: User description: "The v3 had an option to make a 'link' between devices based on MAC addresses. I'd like the same functionality." → after analysis: "Go ahead with the spec for the ARP-based linking."

## Context

v3 and v4 both link a host to the switch port it is plugged into by matching
the host's MAC address against the MACs the switches have learned on their
ports (bridge forwarding tables, read over SNMP during a scan). Both only
know a host's MAC when an agent on that host reports it (v3 `tangra-client`,
v4 inventory agent). In production today the switches' side is populated
(hundreds of learned MACs from seven switches), but only one host runs the
agent, so almost nothing can be linked; and hosts that can never run an agent
— printers, access points, cameras, BMC/IPMI boards, appliances, servers
without the agent — could never be linked in v3 either.

Routers, firewalls and layer-3 switches already know the MAC of every host
they talk to: their ARP (IPv4) and neighbour (IPv6) tables map IP addresses
to MAC addresses. IPAM already reaches these devices over SNMP with the
subnet's credentials (feature 021). Reading those tables gives IPAM the MAC
of every active address in the routed networks, with no agent, and the
existing switch-port matching can then link each of those addresses to its
switch port.

## Clarifications decided by default (see Assumptions)

- Addresses, not devices, are the unit for agentless hosts: IPAM records the
  MAC and the switch port on the **IP address**; it does not invent devices.
- Agent-reported and manually entered MACs always win over ARP-learned ones.
- ARP entries for IPs that fall inside a known subnet but have no address
  record yet create an address record marked as discovered by ARP.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Scanned addresses get their MAC from router ARP tables (Priority: P1)

A network administrator scans the network-devices subnet with SNMP discovery.
Besides discovering the switches, router and firewall, IPAM reads their ARP
and neighbour tables. Afterwards the IP address list of every routed subnet
(servers, IPMI, office, ...) shows a MAC address for each active host, with
the source "ARP (router MikroTik)" and when it was last seen.

**Why this priority**: Without MACs on the agentless addresses nothing can be
linked; this alone also answers "which MAC is on this IP?" — a common IPAM
question.

**Independent Test**: With an SNMP simulator exposing an ARP table for three
IPs in two known subnets, a scan fills those three addresses' MACs with
source ARP and the reporting device, and reports how many ARP entries were
read and applied.

**Acceptance Scenarios**:

1. **Given** a router discovered by SNMP whose ARP table holds entries for IPs
   in known subnets, **When** a scan with SNMP discovery completes, **Then**
   each matching address shows the MAC, source "ARP", the reporting device and
   the time it was last seen.
2. **Given** an address whose MAC was reported by the inventory agent or
   entered manually, **When** ARP reports a different MAC for that IP,
   **Then** the stored MAC is not changed and the disagreement is recorded as
   a conflict visible on the address.
3. **Given** an ARP entry for an IP inside a known subnet with no address
   record, **When** the scan applies ARP data, **Then** an address record is
   created (status active, origin "ARP") with that MAC.
4. **Given** ARP entries for IPs outside every known subnet, **When** applied,
   **Then** they are ignored (no subnet is created).
5. **Given** an address whose MAC was learned from ARP earlier, **When** a later
   scan reports a new MAC for it, **Then** the MAC is updated and the change is
   audited with the previous and new MAC.
6. The scan result reports, for the ARP phase: devices read, entries read,
   addresses updated, addresses created, entries ignored (outside subnets,
   incomplete/invalid, multicast/broadcast), conflicts.

---

### User Story 2 - Agentless addresses show the switch port they are connected to (Priority: P1)

After the scan, the IP address list and the address details show "Connected
to MSW-RACK2 port 14 (VLAN 30)" for a printer, an IPMI board and a server
without the agent. The switch's port list shows which address (and hostname)
sits behind each access port.

**Why this priority**: This is the v3 capability the user asked for, extended
to hosts that can never run an agent.

**Independent Test**: With ARP giving an address a MAC that a simulated
switch has learned on an access port (few MACs), the address shows that
port; with the MAC only on a trunk (many MACs), no port is shown.

**Acceptance Scenarios**:

1. **Given** an address with a MAC (from ARP, agent or manual entry) that a
   switch has learned on an access port, **When** the correlation runs after a
   scan, **Then** the address shows the switch, port and VLAN, and when the
   link was last confirmed.
2. **Given** a MAC learned on several switches, **Then** the port with the
   fewest learned MACs on each switch is used, and a host bonded across two
   switches shows a link to each (same rules as the existing host links).
3. **Given** a MAC only seen on ports carrying more than the configured number
   of MACs (uplinks, hypervisor trunks), **Then** no link is shown.
4. **Given** a MAC that belongs to a network device's own interface (a switch
   or router MAC), **Then** it is never linked as a host.
5. **Given** an address bound to a device (manually or by the host sync),
   **Then** the device's interface shows the same link as the address.
6. **Given** a link not re-confirmed for the configured staleness period
   (default 14 days), **Then** it is cleared.
7. **Given** a switch port, **When** viewed, **Then** it lists the addresses
   (and devices) linked to it.

---

### User Story 3 - Trust and noise control (Priority: P2)

The firewall answers ARP for a whole range (proxy ARP) and a pair of routers
share a virtual MAC (VRRP). The administrator does not want every address in
that range to show the firewall's MAC or the virtual-router MAC.

**Why this priority**: Without these guards ARP data would attach the same
MAC to dozens of addresses and produce false links; operators must be able
to trust the data.

**Independent Test**: An ARP table where one MAC answers for 20 IPs and one
entry uses a VRRP MAC; neither is applied to addresses, both are counted as
ignored with a reason.

**Acceptance Scenarios**:

1. **Given** one MAC appearing for more than a threshold number of IPs in one
   scan (default 8), **Then** those entries are not applied (proxy ARP / router
   MAC) and are counted as ignored with that reason.
2. **Given** a MAC in a well-known virtual-router range (VRRP, HSRP) or
   multicast/broadcast/all-zero, **Then** it is ignored.
3. **Given** a MAC that belongs to any known network device interface,
   **Then** it is not applied to other addresses.
4. Per tenant, an administrator can disable ARP collection, and can exclude
   devices from being used as ARP sources.

---

### User Story 4 - See where a MAC came from and look it up (Priority: P3)

The administrator searches the address list by MAC (full or partial) to find
which IP and port a device with a known MAC is on, and sees for every MAC
whether it came from the agent, a manual entry or ARP, from which device and
when.

**Why this priority**: Makes the collected data useful day to day; not
required for linking itself.

**Independent Test**: Search by a partial MAC returns the matching addresses
with source and link.

**Acceptance Scenarios**:

1. **Given** addresses with MACs, **When** searching by a full or partial MAC
   (any common notation), **Then** matching addresses are listed with IP,
   hostname, MAC source, last seen and connected port.
2. **Given** any MAC shown, **Then** its source (agent, manual, ARP) and, for
   ARP, the reporting device and last-seen time are visible.

### Edge Cases

- Incomplete ARP entries (no MAC) and invalid MACs are skipped and counted.
- The same IP reported with different MACs by two routers in one scan: the
  most recently seen entry wins; the disagreement is counted as a conflict.
- An IP moves to a new MAC (host replaced): the ARP-sourced MAC is updated,
  the change audited, and the old link cleared at the next correlation.
- A device exposes only the legacy ARP table or only the newer
  IP-to-physical table; both are supported, IPv4 and IPv6 neighbour entries.
- Very large ARP tables (thousands of entries) are read within the scan's
  SNMP time budget; a device that times out part-way contributes what was
  read and the phase reports it as partial.
- ARP data of one tenant's devices is only applied to that tenant's
  addresses.
- A network device reached through a subnet whose credentials are
  unavailable is skipped with a reason; the rest of the scan continues.
- Addresses created from ARP are never deleted automatically; when their MAC
  is no longer seen they keep the last MAC with its last-seen time.

## Requirements *(mandatory)*

### Functional Requirements

**Collecting ARP/neighbour data**

- **FR-001**: During a scan with SNMP discovery, IPAM MUST read the IPv4 ARP
  and IPv6 neighbour tables of every network device that answered SNMP in
  that scan (routers, firewalls, layer-3 switches — any device that exposes
  the tables), using the same effective credentials.
- **FR-002**: Both the legacy ARP table and the newer IP-to-physical table MUST
  be supported; entries MUST be normalised (IP canonical form, MAC lower-case
  colon notation).
- **FR-003**: Entries MUST be ignored when the MAC is incomplete, invalid,
  multicast, broadcast, all-zero, in a virtual-router range (VRRP/HSRP), belongs
  to a known network-device interface, or answers for more than the proxy-ARP
  threshold of IPs in the same scan (default 8, configurable); each ignored
  entry is counted by reason.
- **FR-004**: Only entries whose IP falls inside a subnet of the same tenant
  MUST be applied.

**Applying MACs to addresses**

- **FR-005**: Each address MUST record its MAC source: `agent` (host sync),
  `manual`, or `arp` (with the reporting device and last-seen time).
- **FR-006**: ARP MUST fill an empty MAC and MUST update a MAC whose source is
  `arp`; it MUST NOT change a MAC whose source is `agent` or `manual`. A
  disagreement with an agent/manual MAC MUST be recorded as a MAC conflict on
  the address (visible, cleared when they agree again).
- **FR-007**: An ARP entry for an IP inside a known subnet without an address
  record MUST create one (status active, origin `arp`, MAC source `arp`).
- **FR-008**: Setting a MAC for the first time, changing it, and creating an
  address from ARP MUST be audited (actor system/scan, previous and new MAC,
  reporting device); a per-scan summary MUST also be recorded.
- **FR-009**: Existing MACs entered by users before this feature MUST be
  treated as `manual`; MACs written by the host sync as `agent`.

**Linking to switch ports**

- **FR-010**: The switch-port correlation MUST consider every address that has
  a MAC (any source), in addition to host-reported interfaces, using the same
  rules: fewest-MAC port per switch, per-switch links (MLAG), maximum MACs per
  access port, switch/router own MACs excluded, staleness clearing.
- **FR-011**: The address list and address details MUST show the connected
  switch, port and VLAN with the last-confirmed time; an address bound to a
  device MUST show the same link on that device's interface.
- **FR-012**: A switch port view MUST list the addresses and devices linked to
  it.

**Control and visibility**

- **FR-013**: The scan result MUST report the ARP phase: devices read (and
  skipped/partial with reason), entries read, applied, created, ignored by
  reason, conflicts.
- **FR-014**: Per tenant, administrators with the existing host/scan
  management permission MUST be able to turn ARP collection off and to
  exclude devices as ARP sources; the default is on.
- **FR-015**: The address list MUST be searchable by full or partial MAC in
  common notations (colon, dash, dot, none).

### Security Requirements

- **SR-001**: ARP collection MUST use only the effective SNMP credentials of
  the device's own subnet (feature 021) and only read-only SNMP operations.
- **SR-002**: ARP data MUST be validated and bounded before use (maximum
  entries per device per scan, e.g. 65,536; malformed values dropped).
- **SR-003**: ARP data MUST never cross tenants.
- **SR-004**: Credentials MUST never appear in logs, results or audit (feature
  021 rules unchanged).

### Key Entities

- **Address MAC provenance**: per address — MAC, source (agent/manual/arp),
  reporting device (for arp), last seen, conflict flag with the other MAC.
- **ARP observation** (per scan, transient): reporting device, IP, MAC,
  interface/VLAN if exposed.
- **Address link**: address → switch, port, VLAN, source (bridge table), last
  confirmed — the address-level counterpart of the existing interface link.
- **ARP settings** (per tenant): enabled, excluded source devices, proxy-ARP
  threshold.
- **ARP phase result** (part of the scan result): counters and reasons.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After one scan of the network-devices subnet, at least 90 % of
  the active addresses in the routed subnets whose gateway was read show a
  MAC address.
- **SC-002**: Every address whose MAC is learned on an access port of a
  scanned switch shows that switch port after the scan (100 %, same rules as
  host links).
- **SC-003**: No agent-reported or manually entered MAC is ever overwritten by
  ARP data (0 occurrences in tests).
- **SC-004**: Proxy-ARP and virtual-router MACs are applied to 0 addresses.
- **SC-005**: An administrator can find the IP and switch port of a device from
  its MAC in under 30 seconds using the address search.
- **SC-006**: The ARP phase adds no more than 60 seconds to a scan of a subnet
  containing up to 10 network devices with up to 5,000 ARP entries in total.

## Assumptions

- The unit for agentless hosts is the IP address; IPAM does not create devices
  from ARP data (creating devices remains manual, SNMP discovery or host sync).
- The existing switch-port correlation (feature 020, US5) and its settings
  (maximum MACs per access port, staleness) are reused and extended from
  interfaces to addresses; no second correlation engine.
- ARP is collected as part of scans with SNMP discovery (manual or scheduled),
  not by a separate poller; freshness therefore follows the scan cadence.
- MAC vendor (OUI) lookup is out of scope for this feature.
- The existing permissions (`scan:run` to run scans, `hostsync:manage` or
  `subnets:manage` for the per-tenant ARP settings — decided in planning) are
  sufficient; no new permission is expected.

## Dependencies

- Feature 021 (SNMP credentials on subnets) — ARP tables are read with the
  device subnet's effective credentials.
- Feature 020 (host sync, switch-port correlation) — the correlation is
  extended to addresses.
