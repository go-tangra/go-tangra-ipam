# Feature Specification: SNMP Credentials on Subnets for Network Device Discovery

**Feature Branch**: `021-subnet-snmp-credentials`

**Created**: 2026-09-27

**Status**: Draft

**Input**: User description: "In v3 in the IPAM module it was possible to attach SNMP credentials to the subnetworks and to be used for network device discovery. I'd like the same functionality in v4."

**Decisions taken with the user (2026-09-27)**:

- SNMP credentials are stored **by IPAM itself, encrypted at rest**, and are
  write-only: once saved they are never shown again, only whether they are
  configured. They must be usable by scans nobody is watching (scheduled or
  queued scans), so they cannot depend on a signed-in user.
- **Child subnets inherit** the credentials of the nearest parent subnet that
  has them, unless they have their own; the subnet view shows where the
  effective credentials come from.

## Context

In v3 every subnet could carry SNMP settings: the SNMP version (none, v2c or
v3), a v2c community string, or a v3 user with authentication and privacy
passwords and protocols. A scan of the subnet with "SNMP discovery" enabled
used those settings against every live host to recognise switches, routers
and other network devices, record them with their interfaces, and learn which
host MACs sit behind which switch port.

In v4 the scan still offers "SNMP discovery", and the subnet has a field meant
to point at a credential kept in the warden (secrets) module, but:

- the subnet form offers no way to set SNMP credentials at all;
- the link to the warden module was never completed, so a scan never obtains
  credentials and SNMP discovery silently finds nothing;
- the warden module only releases a secret on behalf of a signed-in user and
  holds a single password per secret, which fits neither unattended scans nor
  SNMPv3 (user, two passwords, two protocols).

Network administrators therefore cannot discover network devices in v4, and
the switch-port correlation added by feature 020 has no switch data to work
with unless SNMP scans run.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Attach SNMP credentials to a subnet and discover its network devices (Priority: P1)

A network administrator opens the management subnet `10.1.112.0/24`, chooses
SNMP v2c and enters the community string, and saves. The form then shows
"SNMP: v2c, configured" and never displays the community again. They start a
scan of the subnet with SNMP discovery enabled; the switches and routers in
that subnet that answer SNMP appear as devices with manufacturer, model,
firmware and their interfaces, and the scan result reports how many devices
SNMP discovered.

**Why this priority**: This is the v3 capability that is missing today and
the reason for the request; everything else refines it.

**Independent Test**: Configure v2c credentials on a subnet containing an
SNMP-enabled device (or a simulated agent), run a scan with SNMP discovery,
and confirm the device appears with its interfaces while the credentials are
never returned by any screen, export or API response.

**Acceptance Scenarios**:

1. **Given** a subnet without SNMP credentials, **When** an administrator with
   subnet-management permission selects v2c, enters a community and saves,
   **Then** the subnet shows SNMP v2c as configured and the community is not
   displayed or returned anywhere afterwards.
2. **Given** a subnet with v2c credentials and a live SNMP device in it,
   **When** a scan with SNMP discovery runs, **Then** the device is recorded
   (name, type, manufacturer, model, firmware) with its interfaces, and the
   scan reports the number of devices discovered by SNMP.
3. **Given** a subnet with credentials, **When** the administrator edits other
   subnet fields (name, description, tags) and saves without touching the SNMP
   section, **Then** the stored credentials are unchanged.
4. **Given** a user without subnet-management permission, **When** they view
   the subnet, **Then** they can see whether SNMP is configured and its version
   but cannot change or clear it.

---

### User Story 2 - SNMPv3 with authentication and privacy (Priority: P1)

A security-conscious network team uses SNMPv3 only. The administrator selects
v3, enters the user name, chooses the security level (authentication only, or
authentication and privacy), the authentication protocol and password, and the
privacy protocol and password. Discovery then works against devices configured
for that user.

**Why this priority**: Many production networks disable v2c; without v3 the
feature is unusable for them. v3 was supported in v3 of the platform.

**Independent Test**: Configure v3 authPriv credentials on a subnet with a v3
agent; scan with SNMP discovery and confirm the device is discovered; confirm
a wrong privacy password yields no discovery and a clear reason.

**Acceptance Scenarios**:

1. **Given** v3 is selected, **When** the administrator chooses
   "authentication and privacy", **Then** user, authentication protocol,
   authentication password, privacy protocol and privacy password are all
   required before saving.
2. **Given** v3 with "authentication only", **When** saved, **Then** no privacy
   fields are required or stored.
3. **Given** valid v3 authPriv credentials, **When** a scan with SNMP discovery
   runs against a matching agent, **Then** the device is discovered as with v2c.
4. The authentication protocols offered include MD5, SHA-1 and the SHA-2 family
   (SHA-224, SHA-256, SHA-384, SHA-512); the privacy protocols include DES and
   AES-128, AES-192 and AES-256. Weak choices (MD5, SHA-1, DES) remain available
   for old devices but are marked as weak.

---

### User Story 3 - Child subnets inherit credentials from their parent (Priority: P2)

The administrator sets v3 credentials once on the supernet `10.0.0.0/8`.
Every subnet below it that has no credentials of its own now uses those
credentials for SNMP discovery, and each child subnet shows "SNMP: v3,
inherited from 10.0.0.0/8". One lab subnet uses a different community; the
administrator sets its own v2c credentials there, which override the inherited
ones for that subnet and its own children.

**Why this priority**: Large networks share one SNMP profile; inheritance
removes repetitive, error-prone per-subnet entry, including for subnets that
the host sync (feature 020) creates automatically.

**Independent Test**: Set credentials on a parent only; scan a child and
confirm discovery uses the parent's credentials and the child shows the
inherited source; set own credentials on the child and confirm they win.

**Acceptance Scenarios**:

1. **Given** a parent with credentials and a child without, **When** the child
   is viewed, **Then** it shows the effective version and "inherited from
   <parent name/CIDR>", and a scan of the child uses the parent's credentials.
2. **Given** a grandchild without credentials under a child with its own
   credentials, **When** it is scanned, **Then** the nearest ancestor with
   credentials (the child) is used.
3. **Given** a child that inherits, **When** the parent's credentials are
   cleared, **Then** the child falls back to the next ancestor with
   credentials, or to none.
4. **Given** a child with its own credentials, **When** the administrator
   clears them, **Then** the child inherits again from its nearest ancestor.
5. **Given** a subnet created automatically by the host sync, **When** its
   parent has credentials, **Then** it inherits them like any other subnet.

---

### User Story 4 - Test credentials before relying on them (Priority: P2)

Before starting a full scan the administrator uses "Test SNMP" on the subnet,
enters one device address from that subnet, and gets an immediate answer:
success with the device's system name and description, or a clear failure
reason (no response / timeout, authentication failure, unknown user, wrong
privacy settings, no credentials configured).

**Why this priority**: SNMP failures are otherwise silent (a scan simply finds
nothing); a quick test saves long trial-and-error scans.

**Independent Test**: With credentials set, test against a responsive agent
(success with sysName) and against a non-SNMP host (timeout reason).

**Acceptance Scenarios**:

1. **Given** effective credentials (own or inherited), **When** the
   administrator tests against an address inside the subnet, **Then** the
   result shows success with the device's system name and description, or a
   failure category, within the configured SNMP timeout plus a small margin.
2. **Given** an address outside the subnet, **When** tested, **Then** the test
   is refused with a clear message (the subnet's credentials only apply to its
   own addresses).
3. **Given** no effective credentials, **When** tested, **Then** the result
   says no credentials are configured for this subnet or its parents.
4. The test never displays, returns or logs the credential values, and each
   test is recorded in the audit log (who, subnet, target address, outcome).

---

### User Story 5 - Understand why SNMP discovery did nothing (Priority: P2)

An administrator runs a scan with SNMP discovery and sees "SNMP: 0 devices".
The scan result explains why: SNMP skipped because no credentials are
configured for this subnet or its parents; or credentials present but no host
answered; or N hosts rejected the credentials (authentication failure).

**Why this priority**: In v4 today SNMP silently does nothing; operators need
to tell configuration errors apart from networks without SNMP devices.

**Independent Test**: Scan with SNMP enabled on a subnet without credentials
and confirm the skip reason; scan with wrong credentials and confirm the
authentication-failure count.

**Acceptance Scenarios**:

1. **Given** no effective credentials, **When** a scan with SNMP discovery
   runs, **Then** the ping/discovery part of the scan still completes and the
   scan result states that SNMP was skipped for lack of credentials.
2. **Given** credentials, **When** the scan completes, **Then** the result
   shows hosts probed, devices discovered, hosts that did not answer and hosts
   that rejected the credentials, and whether the credentials were the
   subnet's own or inherited (and from which subnet).
3. **Given** the credentials cannot be decrypted (for example after a key
   problem), **When** a scan runs, **Then** SNMP is skipped with a distinct
   reason and the rest of the scan completes.

---

### User Story 6 - Replace or clear credentials safely (Priority: P3)

The team rotates its community string. The administrator opens the SNMP
section, chooses "Replace", enters the new value and saves; or chooses "Clear"
to remove the subnet's own credentials. Every change is recorded in the audit
log as "SNMP credentials set / replaced / cleared" with the version, never the
values.

**Why this priority**: Routine lifecycle management; lower priority than
discovery itself but required for safe operation.

**Independent Test**: Replace and clear credentials; confirm the audit log
shows the three kinds of events with version only and no secret material.

**Acceptance Scenarios**:

1. **Given** configured credentials, **When** the administrator replaces them,
   **Then** the new credentials must be entered in full (the old values are
   never pre-filled) and are used by the next scan.
2. **Given** configured own credentials, **When** cleared, **Then** they are
   deleted (not just hidden) and the subnet inherits or has none.
3. **Given** any set/replace/clear, **Then** an audit entry records actor,
   subnet, action and SNMP version, and contains no credential values.

### Edge Cases

- A subnet is moved under a different parent (parent changed): its effective
  credentials follow the new ancestry immediately; the scan uses the ancestry
  at the moment it starts.
- A parent subnet with credentials is deleted: children lose that inheritance
  and fall back to the next ancestor with credentials or none.
- Switching a subnet from v3 to v2c (or back) replaces all stored fields; no
  fields of the previous version remain stored.
- A community or password containing unusual characters (spaces, quotes,
  non-ASCII) is stored and used exactly as entered; empty values are rejected.
- SNMPv3 passwords shorter than 8 characters are rejected (protocol minimum).
- Very long values (over 256 characters) are rejected.
- Tenant isolation: credentials of one tenant's subnet are never usable for,
  visible in, or inherited by another tenant's subnets.
- Tenant backup/export never contains credentials (only "configured" and the
  version); restoring a backup leaves SNMP unconfigured and says so.
- Subnets that today carry the unused warden reference field: after the
  upgrade they show "not configured" (the reference was never functional) and
  the migration reports how many subnets had one, so operators can re-enter
  credentials.
- A scan already running when credentials change keeps the credentials it
  started with; the next scan uses the new ones.
- The encryption key is rotated or unavailable: credentials that cannot be
  decrypted are reported as "configured but unreadable" and SNMP is skipped
  with that reason; re-entering them fixes it.
- IPv6 subnets: SNMP discovery over IPv6 addresses works the same way where
  the scan already probes IPv6 hosts.

## Requirements *(mandatory)*

### Functional Requirements

**Credentials on a subnet**

- **FR-001**: A subnet MUST be able to hold its own SNMP credentials of one of
  these kinds: none, SNMP v2c (community), or SNMP v3 (user, security level
  authentication-only or authentication-and-privacy, authentication protocol
  and password, and for authentication-and-privacy a privacy protocol and
  password).
- **FR-002**: The supported v3 authentication protocols MUST include MD5,
  SHA-1, SHA-224, SHA-256, SHA-384 and SHA-512; the supported privacy
  protocols MUST include DES, AES-128, AES-192 and AES-256. MD5, SHA-1 and DES
  MUST be labelled as weak in the interface.
- **FR-003**: Saving credentials MUST validate completeness per kind (all
  required fields present, v3 passwords at least 8 characters, no value over
  256 characters, no empty values) and reject invalid input with a message
  naming the field.
- **FR-004**: Only users with the subnet-management permission MUST be able to
  set, replace or clear SNMP credentials; users who can view subnets MUST see
  only whether credentials are configured, the version, the security level and
  the effective source.
- **FR-005**: Editing a subnet without touching its SNMP section MUST leave its
  stored credentials unchanged.
- **FR-006**: Replacing credentials MUST require entering all fields of the new
  credentials; existing values MUST never be pre-filled or revealed.
- **FR-007**: Clearing a subnet's own credentials MUST delete them permanently.

**Secrecy**

- **FR-008**: Credential values (community, passwords) MUST be stored encrypted
  at rest by IPAM and MUST NOT depend on a signed-in user to be used.
- **FR-009**: Credential values MUST never appear in any response to a browser
  or another module, in listings, subnet details, scan results, event-stream
  messages, tenant backups/exports, audit entries, logs or error messages. The
  v3 user name is also treated as secret.
- **FR-010**: Credential values MUST be decrypted only for the duration of an
  SNMP operation (scan or test) and never cached in readable form beyond it.

**Inheritance**

- **FR-011**: A subnet without its own credentials MUST use the credentials of
  its nearest ancestor subnet (by the parent relationship) that has its own
  credentials; if none has, the subnet has no effective credentials.
- **FR-012**: The subnet list and detail views MUST show the effective SNMP
  state of each subnet: not configured, own (with version), or inherited (with
  version and the source subnet's name and CIDR).
- **FR-013**: Effective credentials MUST be determined when a scan or test
  starts, reflecting the current ancestry (after moves, deletions and changes).
- **FR-014**: Inheritance MUST never cross tenants.

**Discovery**

- **FR-015**: A scan with SNMP discovery enabled MUST use the subnet's
  effective credentials to probe every live host found by the scan, and record
  each responding device with its identity (name, type, manufacturer, model,
  firmware) and interfaces, as the existing SNMP discovery does.
- **FR-016**: The scan result MUST report for the SNMP phase: whether it ran or
  why it was skipped (no effective credentials, credentials unreadable, SNMP
  discovery not requested, no live hosts), the credential source (own or
  inherited from which subnet), hosts probed, devices discovered, hosts that
  did not answer and hosts that rejected the credentials.
- **FR-017**: Scans started without a signed-in user watching (queued, retried
  or scheduled scans) MUST be able to use the effective credentials.
- **FR-018**: A scan in progress MUST keep using the credentials it started
  with even if they change during the scan.

**Testing credentials**

- **FR-019**: Users with the scan-run permission MUST be able to test the subnet's effective credentials against one address inside
  the subnet, receiving success (system name and description) or a failure
  category: no response, authentication failure, unknown user, decryption /
  privacy failure, no credentials configured, address outside the subnet.
- **FR-020**: A credentials test MUST complete or time out within the
  configured SNMP timeout plus 2 seconds, and MUST be rate-limited per user to
  prevent use as a network probe (at most 10 tests per minute).

**Audit and lifecycle**

- **FR-021**: Setting, replacing and clearing credentials, and each credentials
  test, MUST be audited with actor, tenant, subnet, action, SNMP version and
  (for tests) target address and outcome — never credential values.
- **FR-022**: Tenant backup/export MUST record only whether credentials were
  configured and their version; restoring MUST leave SNMP unconfigured and
  report which subnets need credentials re-entered.
- **FR-023**: The existing, never-functional warden-reference field on subnets
  MUST no longer be used for discovery; the upgrade MUST report the number of
  subnets that had such a reference so operators know to enter credentials.
  (The field may be retained, unused, for a possible future warden-backed
  option; it is not shown in the interface.)

### Security Requirements

- **SR-001**: Encryption at rest MUST use IPAM's existing key-encryption-key
  scheme (per-record data key wrapped by the module's key), the same protection
  already used for other sealed fields.
- **SR-002**: Write-only semantics: no API operation returns stored credential
  values; the "configured" marker is the only read-back.
- **SR-003**: SNMP operations MUST target only addresses inside the subnet whose
  credentials are used, so credentials of one subnet can never be sent to hosts
  of another network through the test or scan functions.
- **SR-004**: Credential values MUST be excluded from all logs, including debug
  logs and error details returned by the SNMP library.
- **SR-005**: The audit detail guard MUST reject any attempt to record
  credential fields.

### Key Entities

- **Subnet SNMP credentials**: belongs to exactly one subnet of one tenant;
  kind (v2c or v3); v3 security level; protocols; secret values (community,
  user, passwords) held encrypted; when set/replaced and by whom.
- **Effective SNMP credentials** (derived): for a subnet, either its own
  credentials or those of its nearest ancestor with credentials, or none; with
  the source subnet.
- **SNMP phase result** (part of a scan result): ran/skipped with reason,
  credential source, counts of hosts probed, devices discovered, no answer,
  credentials rejected.
- **Credentials test result**: target address, outcome category, system name
  and description on success, duration.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An administrator can attach v2c or v3 credentials to a subnet
  and start a discovery scan in under 2 minutes, without leaving the subnet
  and scan screens.
- **SC-002**: On a subnet containing SNMP-enabled network devices and correct
  credentials, 100% of devices that answer SNMP are discovered by one scan.
- **SC-003**: Credential values are found in 0 places outside their encrypted
  storage: verified across all subnet/scan/test responses, event-stream
  messages, backups, audit entries and logs produced by an end-to-end test run.
- **SC-004**: Setting credentials once on a parent makes discovery work on
  100% of its descendant subnets that have no credentials of their own.
- **SC-005**: Every scan with SNMP discovery requested states a reason whenever
  SNMP discovered 0 devices (no silent "0").
- **SC-006**: A credentials test returns an answer within the SNMP timeout plus
  2 seconds in 100% of cases.

## Assumptions

- The existing SNMP discovery logic (identity, interfaces, forwarding tables
  and neighbour data) is reused unchanged; this feature supplies credentials,
  inheritance, visibility and testing.
- One set of credentials per subnet is sufficient (as in v3); trying several
  credential sets per subnet in turn is out of scope.
- Device-level SNMP credentials (overriding the subnet for one device) are out
  of scope; the subnet (with inheritance) is the unit, as in v3.
- The BMC/IPMI credentials of devices, which also rely on the unfinished
  warden link, are out of scope; they will be addressed separately.
- The configured SNMP timeout and port (default UDP 161) of the scan service
  apply to tests and scans; per-subnet ports are out of scope.
- Scheduled scans, if configured, run without a user; the credentials are
  therefore usable by the IPAM service on its own (decision 1).
- The subnet-management permission (`subnets:manage`) and scan-run permission
  (`scan:run`) already exist; no new permission is introduced.

## Dependencies

- IPAM's module encryption key (already provisioned for sealed fields) must be
  available in every environment; production already has it.
- Feature 020 (host sync) creates subnets automatically; those subnets benefit
  from inheritance without further changes.
