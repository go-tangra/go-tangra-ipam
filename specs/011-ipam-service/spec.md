# Feature Specification: IPAM Service

**Feature Branch**: `011-ipam-service`

**Created**: 2026-09-21

**Status**: Draft

**Input**: replicate go-tangra-ipam as a Freya module — a tenant-scoped IP Address Management platform (subnets, IPs, devices, VLANs, locations, groups) with active network operations (discovery scanning, ping, IPMI/BMC power control, KVM console proxy).

## Overview

The **ipam** service is a tenant-scoped **IP Address Management** platform module.
It manages hierarchical **subnets** (with utilization tracking), **IP addresses**
(with first-free and bulk allocation, conflict detection, and free-address
suggestion), **devices** (with network interfaces, discovered Layer-2 links, and
OS package inventory), **VLANs**, a physical **location** hierarchy
(region→rack), and logical **IP/host groups**. It performs **active network
operations** — asynchronous discovery **scanning** (ICMP sweep, SNMP device/
interface discovery, TCP probing), **ping**, out-of-band **IPMI/BMC power
control**, and a **KVM console proxy** — all authorized, tenant-scoped, and
audited. It registers with the application gateway, exposes service-to-service
gRPC, publishes IP/scan events to the platform bus (for DNS synchronization and
live UI), and ships a Module-Federation UI.

Freya adaptations (vs the source): SPIFFE mTLS + gateway platform token replace
the mTLS CN allow-list + HMAC claims; TimescaleDB with per-tenant row-level
security replaces app-level tenant filtering (unique constraints are preserved as
the conflict-detection layer); BMC/IPMI/SNMP credentials are never stored — only
**warden secret references** — and sensitive fields are sealed; active operations
that reach real hosts are gated (power/KVM/IPMI require platform-admin) and
sandboxed to the tenant's own subnets/devices.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Model subnets and allocate IP addresses (Priority: P1)

A network operator creates a subnet from a CIDR, sees its capacity and
utilization, allocates the next free address (or a bulk block), and is prevented
from creating duplicate or out-of-range addresses.

**Why this priority**: This is the MVP and the core of IPAM — without subnets and
conflict-free allocation there is no product. It exercises CIDR modeling,
first-free allocation, utilization, and the uniqueness/conflict guards.

**Independent Test**: Create a subnet, allocate the next free address twice
(getting two distinct addresses), attempt a duplicate (rejected), and read the
subnet's utilization reflecting the two allocations — all scoped to one tenant.

**Acceptance Scenarios**:

1. **Given** a valid CIDR, **When** the operator creates a subnet, **Then** its network/broadcast/mask/total-addresses are derived and utilization starts at zero; a duplicate name in the tenant is rejected, and an overlapping CIDR is flagged.
2. **Given** a subnet, **When** the operator allocates the next free address, **Then** the lowest unused host address (excluding network, broadcast, gateway, and reserved/skip ranges) is returned and recorded; a second allocation returns a different address.
3. **Given** an allocated address, **When** the same address is created again in the tenant, **Then** it is rejected as already allocated; when the subnet is full, allocation returns a "no available addresses" result.
4. **Given** addresses exist, **When** the operator reads subnet stats, **Then** used/available/utilization reflect the allocations, tenant-isolated.

---

### User Story 2 - Track devices, interfaces, and their addresses (Priority: P1)

An operator records a device (type, location, rack position), adds network
interfaces, links IP addresses to it, and reviews its interfaces and OS package
status.

**Why this priority**: Devices are the second pillar of IPAM — IP addresses and
scan results attach to devices, and the UI's device views are core daily use.

**Independent Test**: Create a device, add an interface, attach an address, and
list the device's interfaces and addresses.

**Acceptance Scenarios**:

1. **Given** a device, **When** the operator adds interfaces and links addresses, **Then** the device's interface/address counts reflect them and its interfaces/addresses can be listed.
2. **Given** a device with OS packages synced, **When** package status is read, **Then** update/security-update counts and reboot-required are reported.
3. **Given** two devices in the same tenant, **When** created with the same name, **Then** the second is rejected (unique per tenant).

---

### User Story 3 - Organize with VLANs, locations, and groups (Priority: P2)

An operator models VLANs (1–4094), a location hierarchy (region→rack, viewable as
a tree), and logical IP/host groups, and checks which groups contain a given IP.

**Why this priority**: Organizational structure (VLANs, sites, groups) is
essential for real networks but builds on the core subnet/device model.

**Independent Test**: Create a VLAN and link a subnet; build a location tree and
read it nested; create an IP group with address/range/subnet members and check an
IP against it.

**Acceptance Scenarios**:

1. **Given** a VLAN id in 1–4094, **When** created, **Then** it is unique per tenant by id and by name; its associated subnets can be listed.
2. **Given** locations with parents, **When** the tree is read, **Then** it returns the nested region→rack hierarchy with child/subnet/device/vlan counts.
3. **Given** an IP group with address, range and subnet members, **When** an IP is checked, **Then** the groups whose membership contains it are returned; a host group lists its member devices with denormalized info.

---

### User Story 4 - Discover the network by scanning (Priority: P2)

An operator starts a scan of a subnet; the system asynchronously sweeps live
hosts (ICMP), optionally discovers devices and interfaces via SNMP (and their
Layer-2 links), reverse-resolves hostnames, upserts IP/device records, and
publishes change events — with progress, cancel, and retry.

**Why this priority**: Automated discovery is a headline capability that
populates the inventory, but depends on subnets/devices existing.

**Independent Test**: Start a scan of a small subnet; observe the job progress
from pending→scanning→completed, alive hosts recorded as IP addresses, and a
scanned event published; cancel a running scan.

**Acceptance Scenarios**:

1. **Given** a subnet within the size bound, **When** a scan is started, **Then** a job is created (pending), a worker sweeps host addresses (excluding network/broadcast) with bounded concurrency and per-probe timeout, and progress advances to completed.
2. **Given** SNMP is enabled with credentials, **When** the scan runs, **Then** reachable hosts are probed for device/interface details and Layer-2 neighbor links, creating/updating device and interface records.
3. **Given** a scan discovers or updates addresses, **When** it finishes, **Then** the affected IP records are upserted (last-seen, hostname, PTR) and scanned events are published for DNS synchronization.
4. **Given** a subnet exceeding the host-count bound, **When** a scan is requested, **Then** it is refused as too large; a running scan can be cancelled and a failed scan retries with backoff.

---

### User Story 5 - Out-of-band device control (power & console) (Priority: P3)

A platform administrator reads a server's BMC power state and sensors, issues a
power action (on/off/cycle/reset), and opens a browser KVM console — without the
browser ever seeing the BMC credentials.

**Why this priority**: Out-of-band control is powerful and high-risk (it can
hard-reset production hardware), so it is gated to platform administrators and
built last, on top of the device model.

**Independent Test**: For a device with a BMC credential reference, read its power
status and sensors, issue a power action, and start a KVM session that returns a
token-gated console URL — all requiring platform-admin.

**Acceptance Scenarios**:

1. **Given** a device with a stored BMC credential reference, **When** a platform admin reads power status/sensors/event-log, **Then** they are returned using the credential fetched at use time (never stored in-service, never returned).
2. **Given** platform-admin authority, **When** a power action (on/off/cycle/reset/soft/diag) is issued, **Then** it is executed against the device BMC and audited; a non-admin is refused.
3. **Given** a KVM session is started, **When** the returned console URL is opened, **Then** the console and its live stream are proxied under the platform origin using a short-lived token, and the browser never receives BMC credentials.

---

### User Story 6 - DNS config, statistics, and backup (Priority: P3)

An operator configures the tenant's DNS/reverse-DNS behavior, views fleet
statistics, and exports/imports the tenant's IPAM data.

**Why this priority**: Configuration, reporting and portability round out the
product but are not required for the core flows.

**Independent Test**: Set the tenant DNS config, read statistics (totals,
utilization, devices-by-type), and export then re-import the tenant.

**Acceptance Scenarios**:

1. **Given** DNS servers, **When** the tenant DNS config is set and tested, **Then** a live reverse lookup of a test IP returns a hostname and latency.
2. **Given** data exists, **When** statistics are read, **Then** subnet/address/vlan/device/location totals, overall utilization and devices-by-type are returned for the tenant.
3. **Given** a tenant export, **When** it is imported (skip or overwrite), **Then** subnets, addresses, devices, VLANs, locations and groups are recreated with schema-version handling, and no secrets are included.

### Edge Cases

- A CIDR that overlaps an existing subnet, or a gateway outside the subnet range, is flagged.
- Allocating in a full subnet returns "no available addresses"; allocating excludes network/broadcast/gateway and configured reserved ranges.
- A duplicate IP, VLAN id/name, subnet name, location name/code, device name, or group member is rejected by uniqueness.
- A scan of a subnet exceeding the host bound is refused; IPv6 subnets are not scannable in this version.
- A device with attached addresses (or a subnet with addresses, VLAN with subnets, location with children) refuses deletion unless forced.
- A power/KVM/IPMI request from a non-platform-admin, or targeting a host outside the tenant's own devices/subnets, is refused.
- A BMC that is unreachable or presents a self-signed certificate is handled server-side without leaking credentials.
- Deleting a device cascades its interfaces, links and packages; a subnet delete guards against orphaning addresses.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST model subnets from a CIDR, deriving network/broadcast/mask/prefix and total addresses, in a self-referential hierarchy, with per-tenant unique names and overlap + gateway-in-range validation.
- **FR-002**: System MUST allocate the next free address in a subnet (lowest unused host, excluding network/broadcast/gateway and reserved/skip ranges and already-allocated addresses), support bulk allocation with a hostname prefix, and return a "no available addresses" result when full.
- **FR-003**: System MUST guarantee address uniqueness per tenant (no duplicate IPs) and compute per-subnet used/available/utilization.
- **FR-004**: Users MUST be able to CRUD IP addresses (address, status, type, hostname, MAC, device link, PTR/DNS, lease), find an address, and suggest free addresses verified by active probing.
- **FR-005**: Users MUST be able to CRUD devices (type, status, location, rack position, management IPs, firmware) with per-tenant unique names, add/remove network interfaces, link addresses, and sync OS package inventory (with update/security counts).
- **FR-006**: System MUST record device network interfaces (name, MAC, type, speed, SNMP if-index) and discovered Layer-2 neighbor links (source snmp_fdb/lldp/manual).
- **FR-007**: Users MUST be able to CRUD VLANs (id 1–4094, unique per tenant by id and name) and list a VLAN's subnets.
- **FR-008**: Users MUST be able to CRUD locations in a typed self-referential tree (region…rack) and read the nested tree with counts.
- **FR-009**: Users MUST be able to CRUD IP groups (members: address/range/subnet) and host groups (member devices), manage members, check which groups contain an IP, and list a device's host groups.
- **FR-010**: System MUST run asynchronous subnet scans: a worker pool claims pending jobs, sweeps host addresses via ICMP with bounded concurrency and per-probe timeout, excludes network/broadcast, bounds a job to at most the configured host limit, reverse-resolves alive hosts, upserts IP records, and reports progress; jobs support cancel and retry-with-backoff.
- **FR-011**: System MUST optionally perform SNMP discovery during a scan (using the subnet's credential reference), creating/updating devices and interfaces and correlating Layer-2 links.
- **FR-012**: System MUST support ping (ICMP reachability) for an address and free-address suggestion via ICMP + TCP probing.
- **FR-013**: System MUST support out-of-band IPMI/BMC operations — read info/power-status/sensors/event-log and control power (on/off/cycle/reset/soft/diag) — using a credential fetched at use time from the secret store, restricted to platform administrators.
- **FR-014**: System MUST provide a KVM console session: a platform admin starts a session returning a short-lived token and console URL, and a token-gated proxy streams the device BMC console and its live channel under the platform origin without exposing BMC credentials to the browser.
- **FR-015**: System MUST publish IP/scan change events (created/updated/deleted/scanned, preserving the scanned distinction) to the platform event bus for DNS synchronization and live UI updates.
- **FR-016**: System MUST let a tenant configure DNS/reverse-DNS behavior and test it with a live reverse lookup, and MUST report per-tenant statistics (totals, overall utilization, devices-by-type).
- **FR-017**: System MUST support per-tenant export/import of subnets, addresses, devices, VLANs, locations and groups with schema versioning and skip/overwrite handling, excluding secrets.
- **FR-018**: System MUST register routes, API permissions, UI abilities and navigation with the application gateway and expose service-to-service APIs for other modules.
- **FR-019**: System MUST enforce API permissions (read, subnet/address/device/vlan/location/group/dns management, allocate, scan-run, backup) and gate power-control and KVM access to platform administrators.
- **FR-020**: System MUST record an append-only audit entry for every mutation, allocation, scan, power action and KVM session, with credentials and sensitive fields redacted.

### Security Requirements *(mandatory — Constitution: Development Workflow)*

- **Trust boundaries crossed**: browser API via the application gateway; service-to-service mesh gRPC; the shared event bus; and — uniquely — **outbound active network operations to real external hosts** (ICMP/SNMP/TCP scanning, IPMI power control, KVM console proxy).
- **Data classification**: network topology and device inventory (tenant-confidential); BMC/IPMI/SNMP credentials and owner/contact fields (secret/sensitive); audit records (tamper-evident).
- **Authentication/Authorization**: browser callers use the gateway platform token; module callers use SPIFFE mTLS; API permissions gate every operation; power/KVM/IPMI require platform-admin; RLS isolates tenants.
- **Threat scenarios**: a tenant scanning or power-cycling hosts it does not own; credential theft/leakage (SNMP/BMC secrets); a raw-socket scanner abused to reach arbitrary hosts; forged platform-admin claim to reach power/KVM; cross-tenant leakage of topology or the KVM proxy.
- **SR-001**: Active operations MUST be tenant-scoped and constrained to the tenant's own subnets/devices; a subnet scan MUST be bounded to at most the configured host limit and refuse over-large or IPv6 targets.
- **SR-002**: BMC/IPMI/SNMP credentials MUST NOT be stored in service columns — only secret-store references — MUST be fetched at use time, and MUST never appear in any response, log, audit entry or backup; SNMP passwords and owner/contact fields are sealed/redacted.
- **SR-003**: Power control, IPMI and KVM access MUST require platform-administrator authority; every active operation MUST be authorized and audited with actor, tenant, target and outcome.
- **SR-004**: The KVM proxy MUST keep BMC credentials server-side and stream the console under the platform origin via a short-lived token; the browser MUST never receive BMC credentials, and self-signed BMC certificates MUST be handled without weakening the platform's own trust.
- **SR-005**: All IPAM data MUST be isolated per tenant by row-level security; unique constraints MUST enforce conflict detection (duplicate IP/VLAN/subnet/device/group); trusted worker paths (scan executor) MUST use a scoped system subject, never an unauthenticated bypass.
- **SR-006**: The elevated host capability required for raw-socket scanning MUST be confined to the scanner; all operations MUST be recorded in an append-only, tamper-evident audit trail.

### Key Entities *(include if feature involves data)*

- **Subnet**: a CIDR network in a tenant; hierarchy (parent), VLAN/location links, derived capacity/utilization, SNMP credential reference; unique name per tenant.
- **IP address**: an address in a subnet; status/type, hostname/MAC/PTR, device link, allocation and lease state; unique per tenant (the duplicate guard).
- **Device**: a managed network device/host; type, status, location/rack, management IPs, firmware, OS package rollups, BMC credential reference; unique name per tenant.
- **Device interface / link**: a device NIC (name, MAC, speed, SNMP if-index) and its discovered Layer-2 neighbor links.
- **Device package**: an OS package on a device (current/available version, needs-update, security-update).
- **VLAN**: a VLAN (1–4094) with status and subnet associations; unique per tenant by id and name.
- **Location**: a node in the physical hierarchy (region…rack) with counts and rack size.
- **IP group / host group**: logical groupings of addresses/ranges/subnets, or of devices, with members.
- **IP scan job**: an asynchronous discovery job over a subnet with status/progress/results and retry policy.
- **DNS config**: a tenant's DNS/reverse-DNS settings.
- **Secret reference**: a pointer to a BMC/IPMI or SNMP credential held in the secret store — never the secret itself.

## Success Criteria *(mandatory)*

- **SC-001**: An operator can create a /24 subnet and allocate its first free address in under 30 seconds, with utilization reflecting the allocation immediately.
- **SC-002**: 100% of duplicate-address, over-range, and CIDR-overlap attempts are rejected; no two allocations in a subnet ever return the same address.
- **SC-003**: A scan of a /24 subnet completes and records all reachable hosts as IP addresses within 2 minutes, with progress observable throughout and cancellable.
- **SC-004**: Active operations never reach a host outside the requesting tenant's own subnets/devices (verified), and a subnet scan never exceeds the configured host bound.
- **SC-005**: BMC/IPMI/SNMP credentials never appear in any response, log, audit entry or backup (verified by inspection); the browser never receives BMC credentials during a KVM session.
- **SC-006**: Power-control, IPMI and KVM operations are refused for non-platform-admins in 100% of attempts and are present in the audit trail in 100% of successful cases.
- **SC-007**: The system manages at least 100,000 IP addresses and 10,000 devices per tenant with subnet/device list and utilization queries returning in under 3 seconds.
- **SC-008**: A tenant export re-imports to an equivalent state (subnets, addresses, devices, VLANs, locations, groups) with no cross-tenant leakage and no secrets included.

## Assumptions

- Scanning and ping are IPv4 in this version; IPv6 subnets are modeled but not scanned.
- A subnet scan is bounded (default at most 1024 hosts) to protect the network and the scanner.
- Reserved-range and skip-first/skip-last policies for allocation are configurable with sensible defaults.
- The platform provides tenant identity, the gateway, the event bus, the audit trail, certificate/identity issuance, the secret store (warden) and sealed-secret storage; this feature consumes them.
- Active operations target hosts the tenant owns (its subnets/devices); reaching arbitrary Internet hosts is out of scope.

## Out of Scope

- Acting as a DHCP or DNS server (the service manages records and emits events; DNS sync is a separate consumer).
- Configuration management or software deployment to devices (inventory + power/console only, no configuration push).
- Network flow/traffic monitoring or metrics collection (this is address/asset management, not telemetry).
- Discovery of hosts outside the tenant's declared subnets, or Internet-wide scanning.
