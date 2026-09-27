# Feature Specification: BMC Credentials from Warden for Power, KVM and Sensors

**Feature Branch**: `024-bmc-warden-credentials`

**Created**: 2026-09-27

**Status**: Draft

**Spans**: go-tangra-ipam-v4 (device BMC credentials, power/KVM/sensors), go-tangra-warden-v4 (mesh policy; secret listing if needed), go-tangra-docker (policy)

**Input**: User report: opening the Power / KVM tab of device node-1 fails with `422` on `/devices/{id}/power` and `/devices/{id}/sensors`. Decision (2026-09-27): BMC credentials come from **Warden secrets**, as in v3.

## Context

In v3 a device referenced its BMC/IPMI credentials as a Warden secret; IPAM
fetched the secret from Warden when an administrator used power control, the
KVM console or the sensor readout.

In v4 the device keeps a Warden reference field, but:

- the device form offers no way to pick a Warden secret, so no device has BMC
  credentials (node-1 shows "BMC: none");
- IPAM's Warden client was never finished — fetching a secret always fails —
  so power, KVM and sensors cannot work on any device even if a reference were
  set;
- the endpoints answer a bare `422 validation_failed` without saying why, and
  the tab shows nothing useful.

Unlike SNMP discovery (feature 021, which runs unattended and therefore keeps
its credentials in IPAM), power actions, KVM sessions and sensor readouts are
always started by a signed-in user. Warden releases a secret on behalf of a
signed-in user after checking that user may read it — exactly the v3 model.
A Warden secret holds a username and a password, which is what a BMC needs.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Attach a Warden secret as a device's BMC credentials (Priority: P1)

An administrator opens node-1, chooses "BMC credentials", picks the Warden
secret "zax-5 IPMI" from the secrets they can read, and saves. The device shows
"BMC: zax-5 IPMI (Warden)" with the BMC address it will be used for.

**Why this priority**: Without a way to attach credentials nothing else works.

**Independent Test**: Pick a secret the user can read → the device stores only
the reference and shows the secret's name; a secret the user cannot read is not
offered and is refused if submitted directly.

**Acceptance Scenarios**:

1. **Given** a device, **When** an administrator with device-management
   permission opens the BMC credentials picker, **Then** it lists the Warden
   secrets that administrator can read (name, username, folder — never the
   password) and lets them pick, change or clear one.
2. **Given** a submitted secret the administrator cannot read, **Then** the save
   is refused with a clear message.
3. **Given** a device with a BMC reference, **Then** its page shows the secret's
   name and username (from Warden, metadata only), the BMC address used
   (management IP, or the reported BMC address), and never the password.
4. **Given** a host-reported device whose BMC address is reported by the agent,
   **Then** attaching credentials is allowed and the reported address is used.
5. Every change of a device's BMC reference is audited (who, device, old/new
   reference) without any secret value.

---

### User Story 2 - Power, KVM and sensors use the Warden secret (Priority: P1)

The administrator opens node-1's Power / KVM tab: it shows the power state and
sensor readings, lets them power-cycle the server after confirmation, and
opens the KVM console — IPAM fetched the password from Warden on their behalf
for each action.

**Why this priority**: This is the broken capability.

**Independent Test**: With a reachable (simulated) BMC and a readable secret,
power status, sensors, a power action and a KVM session succeed; with a secret
the current user cannot read, each is refused with "you do not have access to
this device's BMC credentials".

**Acceptance Scenarios**:

1. **Given** a device with a BMC reference, **When** a user with power-control
   permission opens the tab, **Then** the power state and sensors load using
   the secret fetched from Warden for that user.
2. **Given** a user who has power-control permission but cannot read the secret
   in Warden, **Then** the action is refused with an explanatory message and
   nothing is sent to the BMC.
3. **Given** a power action (on, off, cycle, reset), **Then** it requires
   confirmation, is executed with the fetched credentials, and is audited (who,
   device, action, outcome) without credentials.
4. **Given** a KVM session request by a user with KVM permission, **Then** the
   session starts with the fetched credentials, as today's KVM flow intends.
5. The password is fetched per action, used immediately and never stored,
   cached, logged or returned.

---

### User Story 3 - Clear states instead of errors (Priority: P1)

When credentials are missing, the BMC is unreachable, Warden is unavailable or
the user lacks access, the tab says so ("No BMC credentials configured — attach
a Warden secret", "BMC 10.1.112.14 did not answer", "Warden unavailable",
"No access to the BMC credentials") instead of failing silently with a 422 in
the browser console.

**Why this priority**: The current bare errors are what the user hit.

**Independent Test**: Each failure cause produces a distinct, explained state
in the tab and a distinct error reason from the API.

**Acceptance Scenarios**:

1. **Given** a device without a BMC reference or without a BMC address, **Then**
   the API returns a specific reason (credentials not configured / no BMC
   address) and the tab shows it with the action to fix it.
2. **Given** Warden refuses (no access), is unavailable, or the secret was
   deleted, **Then** the API returns distinct reasons and the tab explains them.
3. **Given** the BMC times out or rejects the credentials, **Then** the tab says
   which (unreachable vs. authentication failed).

### Edge Cases

- The referenced Warden secret is deleted or moved: the device shows "secret
  not found"; actions fail with that reason until a new secret is attached.
- The secret's password is rotated in Warden: the next action uses the new
  password automatically (fetched per action).
- The user has access to the device in IPAM but not to the secret in Warden:
  actions are refused; the page still shows that credentials are configured.
- Many users open the tab concurrently: each fetch is on behalf of the
  individual user; nothing is shared between users.
- The KVM console session outlives the password fetch: the password is used
  only to establish the session.
- Devices restored from a backup keep their reference (a pointer, not a
  secret); if the secret does not exist, they show "secret not found".

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Administrators with device-management permission MUST be able to
  set, change and clear a device's BMC credentials as a reference to a Warden
  secret, choosing from the secrets they can read in Warden (metadata only:
  name, username, folder).
- **FR-002**: Setting a reference MUST be refused when the acting user cannot
  read that secret in Warden.
- **FR-003**: The device MUST show whether BMC credentials are configured, the
  secret's name and username (metadata from Warden, fetched for the viewing
  user; "no access" / "not found" otherwise), and the BMC address used.
- **FR-004**: Power status, power actions, sensor readouts and KVM sessions MUST
  obtain the BMC password from Warden at the moment of the action, on behalf of
  the signed-in user, and MUST NOT store, cache, log or return it.
- **FR-005**: If Warden refuses the user, the action MUST be refused without
  contacting the BMC.
- **FR-006**: The power/sensor/KVM endpoints MUST return distinct, documented
  reasons: credentials not configured, no BMC address, no access to the secret,
  secret not found, Warden unavailable, BMC unreachable, BMC authentication
  failed.
- **FR-007**: The Power / KVM tab MUST show those reasons as explained states
  with the corrective action, and only offer actions the user's permissions
  allow.
- **FR-008**: Changes of BMC references and every power action / KVM session
  MUST be audited with actor, device, action and outcome — never a credential.
- **FR-009**: The BMC address MUST be the device's management IP, or else the
  BMC address reported by the inventory agent (feature 020).

### Security Requirements

- **SR-001**: IPAM MUST call Warden only over the authenticated service mesh,
  forwarding the signed-in user's identity so Warden applies its own per-secret
  authorisation and audit; the Warden policy MUST allow IPAM exactly the
  metadata and password operations it needs.
- **SR-002**: No BMC password may appear in any IPAM response, log, event,
  backup or audit entry (verified by a leak test like feature 021's).
- **SR-003**: Power and KVM keep their existing platform-admin permissions
  (`power:control`, `kvm:access`); device-management permission is needed to
  change the reference.

### Key Entities

- **Device BMC reference**: device → Warden secret id (a pointer; the existing
  field), with who/when changed.
- **Warden secret metadata** (read-only, per viewing user): name, username,
  folder, accessible yes/no.
- **BMC action result**: outcome and reason (the FR-006 set).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An administrator can attach a Warden secret to a device and open
  its power state and sensors in under one minute.
- **SC-002**: 100 % of power/KVM/sensor failures show an explained reason in the
  tab (0 bare errors in the browser console for these cases).
- **SC-003**: 0 BMC passwords found in any IPAM response, log, event, backup or
  audit entry in an end-to-end test.
- **SC-004**: A user without read access to the secret is refused in 100 % of
  attempts and the BMC is never contacted.

## Assumptions

- Warden secrets for BMCs store the BMC username in the secret's username
  field and the password as its password; protocol/port options, if needed,
  use the secret's existing metadata fields or defaults (IPMI over LAN 623).
- Listing the secrets a user can read is provided either by Warden's existing
  user-facing API (called by the IPAM UI in the user's session) or by a new
  metadata-only listing operation on Warden — decided in planning.
- Unattended use of BMC credentials (e.g. scheduled power checks) is out of
  scope; all uses are user-initiated.
- Subnet-level or inherited BMC credentials are out of scope (per device, as in
  v3).

## Dependencies

- Warden module (secret metadata and password operations with user
  authorisation) and its mesh policy for the IPAM caller.
- Feature 020 (reported BMC address) for host-reported devices.
