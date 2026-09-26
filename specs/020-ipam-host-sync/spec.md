# Feature Specification: Hosts Reported by the Inventory Agent Populate IPAM

**Feature Branch**: `020-ipam-host-sync`

**Created**: 2026-09-26

**Status**: Draft

**Input**: User description: "The v3 version has a go-tangra-client; the idea of the client is to collect a lot of information about the host and give it to the ipam module for processing. In v4 we have inventory-agent but without this functionality."

**Decisions taken with the user**: the v4 inventory agent (already installed on
hosts) collects the additional data; IPAM obtains host data from the inventory
module (no second agent on hosts). When a host's report disagrees with what
IPAM holds, the report wins and every change is audited; fields that hosts
never report (tags, description, location, groups) are never changed by it.

## Context

In v3 a Linux daemon (`go-tangra-client`) on every host kept IPAM current: it
created the host as a device (server, virtual machine or container, with OS,
serial number and primary address), created missing subnets, recorded every
address with its MAC and interface, recorded the host's BMC/IPMI address,
linked Proxmox guests to their hypervisor, and reported pending updates and
whether a reboot is needed. IPAM then worked out which switch port each host
is connected to from the switches' MAC tables.

In v4 the inventory agent runs on Linux and Windows hosts and reports rich
hardware and software data to the inventory module, but its network data is
thin (interface name, MAC and addresses only) and nothing reaches IPAM. IPAM
administrators therefore maintain devices and addresses by hand or by
scanning, and the v3 capabilities above are lost.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Hosts and their addresses appear in IPAM automatically (Priority: P1)

An administrator installs the inventory agent on a server. Within minutes the
server appears in IPAM as a device with its name, type, operating system,
manufacturer, model and serial number; its interfaces are listed with MAC
addresses; every address it holds is recorded in the right subnet and linked
to the device and interface; its primary address is marked. If the host is
in a network IPAM does not know yet, the subnet is created for it. When the
host later gets a new address or loses one, IPAM follows.

**Why this priority**: This is the core v3 capability: IPAM stays true to
what is actually deployed without manual entry.

**Independent Test**: With inventory and IPAM running, enroll an agent on a
test host; IPAM shows the device, its interfaces, its addresses (each in a
subnet, one primary) and an audit trail of the creation; change an address on
the host and the next report updates IPAM.

**Acceptance Scenarios**:

1. **Given** a host reporting for the first time, **When** its report is
   received, **Then** IPAM creates a device with the reported identity and
   hardware fields, one interface per reported interface, and one address per
   reported address, linked to the device and interface.
2. **Given** a reported address in a network no IPAM subnet contains,
   **When** the report is processed, **Then** a subnet for that network is
   created and marked as created automatically.
3. **Given** a reported address inside several nested subnets, **When** it is
   recorded, **Then** it is placed in the most specific one.
4. **Given** a device IPAM already holds for this host (matched by the
   inventory host, else serial number, else name), **When** a new report
   arrives, **Then** that device is updated, not duplicated.
5. **Given** an address in the report that IPAM records against another
   device, **When** the report is processed, **Then** the address is moved to
   the reporting device and the audit records the previous device.
6. **Given** an address the host no longer reports, **When** the report is
   processed, **Then** the address is released from the device (kept, marked
   as no longer reported) rather than deleted.
7. **Given** an administrator's tags, description, location or groups on the
   device, **When** reports arrive, **Then** they are unchanged.

---

### User Story 2 - Out-of-band management (BMC/IPMI) is recorded (Priority: P2)

For a physical server with a BMC, IPAM records the BMC's address as the
device's management address, lists the BMC network ports with their MACs,
and records the BMC address in its subnet.

**Why this priority**: Operators rely on management addresses for power and
console access (IPAM already offers BMC power and KVM); v3 filled them in
automatically.

**Independent Test**: On a server with a BMC, the agent's report yields a
device whose management address is the BMC address and whose interfaces
include the BMC ports.

**Acceptance Scenarios**:

1. **Given** a host with a BMC configured on the network, **When** it
   reports, **Then** the device's management address is the BMC address and
   the BMC ports appear as interfaces of kind management with their MACs.
2. **Given** a host without a BMC or where the BMC cannot be read, **When**
   it reports, **Then** nothing BMC-related is changed and the report is
   otherwise processed.
3. **Given** BMC data, **When** it is collected, **Then** no BMC credential
   or secret is read or transmitted.

---

### User Story 3 - Virtual machines, containers and their hypervisors (Priority: P2)

IPAM shows whether a device is a physical server, a virtual machine or a
container. On a Proxmox host, the agent reports the guests it runs; IPAM
links each guest device to its host, so an administrator sees on a host
which guests it carries and on a guest which host it runs on.

**Why this priority**: Knowing where a workload runs matters for outages and
maintenance; v3 provided it for Proxmox.

**Independent Test**: A Proxmox host with two guests (one running the agent,
one not) reports; IPAM links the reporting guest's device to the host and
lists the other guest by name and MAC on the host.

**Acceptance Scenarios**:

1. **Given** a host running as a VM or container, **When** it reports,
   **Then** the device type is VM or container, with the virtualization kind
   (e.g. KVM, VMware, Hyper-V, LXC).
2. **Given** a Proxmox host reporting its guests with their NIC MACs,
   **When** a guest device in IPAM has one of those MACs, **Then** the guest
   is linked to the host.
3. **Given** a guest that is not (yet) in IPAM, **When** the host reports it,
   **Then** it is listed on the host with its identifier, name and MACs, and
   linked automatically once it appears.

---

### User Story 4 - Update state and pending updates (Priority: P2)

For each Linux host IPAM shows whether a reboot is required, whether
automatic updates are enabled, and the installed packages with an available
newer version, highlighting security updates.

**Why this priority**: v3 used IPAM as the patch overview for the fleet.

**Independent Test**: On a host with pending updates, IPAM's device view
lists the packages with available versions and marks security updates;
after updating and rebooting, the next report clears them.

**Acceptance Scenarios**:

1. **Given** a host with pending updates, **When** it reports, **Then** IPAM
   lists each package with installed and available versions and flags
   security updates.
2. **Given** a host that needs a reboot, **When** it reports, **Then** the
   device shows reboot required; after a reboot the next report clears it.
3. **Given** a host whose package manager is not supported, **When** it
   reports, **Then** the update state is shown as unknown, not as "up to
   date".

---

### User Story 5 - Which switch port a host is connected to (Priority: P3)

IPAM already reads switches' MAC tables and neighbour data by SNMP. With host
MACs known, IPAM shows on each host interface the switch and port it is
connected to (and the VLAN), and on each switch port the device behind it.

**Why this priority**: Very useful for troubleshooting but depends on SNMP
scanning being configured; the other stories stand alone.

**Independent Test**: After an SNMP scan of a switch whose MAC table contains
a reported host MAC, the host interface shows "connected to <switch> <port>".

**Acceptance Scenarios**:

1. **Given** a switch port whose MAC table has exactly one host MAC (not an
   uplink), **When** correlation runs, **Then** the host interface and the
   switch port are linked, with the VLAN.
2. **Given** a switch port with many MACs (an uplink or trunk), **When**
   correlation runs, **Then** it is not linked to a single host.
3. **Given** neighbour data (LLDP) naming the host, **When** available,
   **Then** it takes precedence over MAC-table inference.

---

### User Story 6 - Visibility and control of the sync (Priority: P2)

An administrator sees, per device, that it is managed by host reports, when
the last report was processed and from which inventory host; can trigger a
re-sync of one host or all; and can turn the automatic sync off for the
tenant. Every change the sync makes is in the audit log.

**Why this priority**: Automatic writes into IPAM must be observable and
controllable.

**Independent Test**: The device view shows "reported by inventory, last at
…"; a manual re-sync updates it; with sync disabled, new reports change
nothing; the audit lists each created/updated/moved record.

**Acceptance Scenarios**:

1. **Given** a device maintained by host reports, **When** viewed, **Then**
   it shows its source, the inventory host and the time of the last report.
2. **Given** sync disabled for the tenant, **When** reports arrive, **Then**
   IPAM is not changed, and re-enabling applies the latest reports.
3. **Given** an administrator with IPAM manage permission, **When** they
   trigger a re-sync of a host, **Then** its latest report is applied.
4. **Given** any change made by the sync, **When** the audit is viewed,
   **Then** it shows what changed, on which record, the previous value where
   one was replaced, and that the actor is the host sync.

---

### Edge Cases

- A host that is removed from inventory: its device and addresses are kept
  and marked as no longer reported; nothing is deleted automatically.
- Two hosts report the same address (duplicate IP, failover address, VRRP):
  the most recent report owns it; the audit records each move; frequent
  moves are shown as a conflict on the address.
- Addresses of container bridges and virtual links (e.g. docker0, br-*,
  veth, cni, virbr) are not recorded by default; loopback and link-local
  never are.
- A host with no serial number and a generic name (e.g. "localhost"): matched
  by its inventory host only; never merged into another device by name.
- A renamed host: matched by inventory host, so the device is renamed, not
  duplicated.
- A report larger than the sync accepts (hundreds of interfaces or
  thousands of packages): processed up to documented limits; the excess is
  reported, not silently dropped.
- IPv6: global addresses are recorded like IPv4; temporary/privacy addresses
  are not.
- Inventory module unavailable: IPAM keeps its data, retries later, and shows
  the sync as degraded.
- Agents older than this feature (no extended network data): their reports
  are still applied with what they contain.
- Windows hosts: network, identity and virtualization data are applied;
  update state and Proxmox guests are Linux-only.

## Requirements *(mandatory)*

### Functional Requirements

**Agent collection (inventory module)**

- **FR-001**: The agent MUST report, per interface: name, MAC, kind
  (ethernet, wireless, bond, bridge, VLAN, virtual, loopback), up/down, speed
  where known, each address with its prefix length, the default gateway, and
  whether the address was assigned by DHCP where the OS exposes it.
- **FR-002**: The agent MUST report the host's primary address (the address
  of the interface carrying the default route).
- **FR-003**: The agent MUST report whether the host is physical, a VM or a
  container, and the virtualization kind.
- **FR-004**: The agent MUST report, when readable without credentials, the
  BMC's address, netmask, gateway and MACs; it MUST NOT read or send BMC
  credentials.
- **FR-005**: On Proxmox hosts the agent MUST report the guests (identifier,
  name, kind VM/container, NIC MACs).
- **FR-006**: On Linux the agent MUST report reboot-required, whether
  automatic updates are enabled, and for installed packages the available
  version and whether it is a security update, for apt, dnf/yum, apk and
  pacman.
- **FR-007**: The inventory module MUST store and expose the new data like
  the existing inventory (snapshots and change history).

**IPAM host sync**

- **FR-008**: IPAM MUST obtain the latest report of each inventory host of
  the tenant after each change reported by inventory and additionally at a
  regular interval (default hourly) to catch missed changes.
- **FR-009**: IPAM MUST match a report to a device by the inventory host
  identity recorded on the device; else by serial number; else by name
  within the tenant; and record the inventory host identity on the device.
- **FR-010**: IPAM MUST create or update the device (name, type,
  virtualization kind, OS and version, manufacturer, model, serial, primary
  address, management address, reboot-required, automatic updates, last
  seen) from the report.
- **FR-011**: IPAM MUST create or update the device's interfaces (name, MAC,
  kind, speed, enabled) and mark interfaces the host no longer reports.
- **FR-012**: IPAM MUST record every reported global address in the most
  specific containing subnet, creating a subnet for the reported network when
  none contains it, linked to device, interface and MAC, with the primary
  flag and host name.
- **FR-013**: When a reported address is recorded against another device,
  IPAM MUST move it to the reporting device (reported data wins) and audit
  the previous owner.
- **FR-014**: IPAM MUST mark addresses and interfaces no longer reported
  instead of deleting them.
- **FR-015**: IPAM MUST NOT change tags, descriptions, location, groups or
  any other field hosts do not report.
- **FR-016**: IPAM MUST record BMC data as the management address and as
  management interfaces.
- **FR-017**: IPAM MUST link guest devices to their hypervisor device by NIC
  MAC, and list unmatched guests on the host.
- **FR-018**: IPAM MUST store package update state per device and show it,
  marking security updates.
- **FR-019**: IPAM MUST correlate host MACs with switch MAC tables and
  neighbour data from its scans to link host interfaces with switch ports,
  ignoring ports with many MACs.
- **FR-020**: Administrators MUST be able to see per device the report
  source and last sync, trigger a re-sync of one host or all, and enable or
  disable the automatic sync for the tenant (default: enabled).
- **FR-021**: Every change made by the sync MUST be audited with the host
  sync as actor.
- **FR-022**: The sync MUST exclude loopback, link-local, temporary IPv6 and,
  by default, container/virtual bridge interfaces, with the exclusion list
  configurable per tenant.

### Security Requirements *(mandatory — Constitution: Development Workflow)*

- **Trust boundaries crossed**: host agent → inventory ingest edge (existing,
  agent credential over TLS); inventory → IPAM over the service mesh
  (service identity); administrator browser → IPAM through the gateway.
- **Data classification**: infrastructure inventory (addresses, MACs,
  hardware identifiers, software versions) — internal, integrity-relevant;
  no credentials.
- **Authentication/Authorization**: agents authenticate to inventory only
  (unchanged); IPAM reads from inventory with its service identity under a
  service policy limited to read operations; admin actions require IPAM
  permissions (view for sync status, manage for re-sync and settings).
- **Threat scenarios**: a compromised or forged agent report claiming
  addresses of other hosts (address hijack in IPAM records); oversized or
  malformed reports (DoS, injection into names shown in the UI); cross-tenant
  leakage; the sync overwriting administrator data; BMC credential exposure;
  mass changes hidden from administrators.
- **SR-001**: IPAM MUST only read host data of the same tenant, from the
  inventory module's service identity, over the mesh.
- **SR-002**: Reported values MUST be validated and bounded (addresses,
  prefixes, MACs, names, counts) before they change IPAM; invalid entries are
  skipped and reported.
- **SR-003**: Address moves between devices MUST be audited and visible, and
  repeated moves of one address MUST be flagged as a conflict.
- **SR-004**: No agent MUST be able to write to IPAM directly.
- **SR-005**: The agent MUST NOT read or transmit BMC or other credentials.
- **SR-006**: Disabling the sync MUST stop all changes by it immediately.

### Key Entities *(include if feature involves data)*

- **Host report (inventory)**: extended network interfaces (kind, speed,
  prefixes, gateway, DHCP), primary address, virtualization, BMC, hypervisor
  guests, update state and package update availability.
- **Device (IPAM, extended)**: source (manual / host report / scan), linked
  inventory host, virtualization kind, hypervisor link, last report time.
- **Device interface (IPAM, extended)**: kind, reported / no longer reported,
  connected switch port and VLAN.
- **IP address (IPAM, extended)**: reported / no longer reported, last moved
  from, conflict flag.
- **Subnet (IPAM, extended)**: created automatically by the host sync.
- **Package update state (IPAM)**: package, installed and available version,
  security flag, per device.
- **Sync settings (IPAM, per tenant)**: enabled, interval, interface
  exclusions.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A newly enrolled host appears in IPAM with its device,
  interfaces and addresses within 5 minutes of its first report.
- **SC-002**: 100 % of reported global addresses are recorded in a subnet
  and linked to the device and interface.
- **SC-003**: A host's address change is reflected in IPAM within 5 minutes
  of the report.
- **SC-004**: No field outside FR-010/011/012/016/018 is ever changed by the
  sync (verified by tests on administrator-set fields).
- **SC-005**: 100 % of sync changes appear in the audit with before/after.
- **SC-006**: A fleet of 1,000 hosts is fully synced within 15 minutes after
  enabling the sync, without degrading IPAM's interactive responsiveness.
- **SC-007**: Every v3 client capability listed in Context is available in
  v4 (checked item by item).

## Assumptions

- The inventory agent is the only agent on hosts; hosts without it are not
  synced (scans still work).
- Matching by inventory host identity is authoritative; serial and name
  matching are only used the first time.
- Automatically created subnets are named after their network and can be
  renamed or reorganised by administrators without breaking the sync.
- Removed hosts are not deleted from IPAM; administrators delete devices when
  they decommission hosts (retention is out of scope).
- Switch-port correlation uses data from IPAM's existing SNMP scans; no new
  switch collection is added.
- Package update detection on Windows is out of scope (inventory already
  reports Windows patches).
- The sync is enabled by default for every tenant once IPAM and inventory are
  both deployed.
