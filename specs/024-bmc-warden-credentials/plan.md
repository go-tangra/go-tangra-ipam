# Implementation Plan: BMC Credentials from Warden for Power, KVM and Sensors

**Branch**: `024-bmc-warden-credentials` | **Date**: 2026-09-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/024-bmc-warden-credentials/spec.md`

## Summary

A device's BMC credentials remain a pointer to a Warden secret (the existing
`ipmi_secret_ref` column). IPAM finishes its Warden client: every call goes
over the Freya SPIFFE mesh to `warden.v1.Secrets` and forwards the signed-in
user's platform token (the one the gateway put on the incoming request) as
`authorization` metadata, so Warden applies its own per-secret check and
audit for that user. `Secrets/Get` supplies metadata (to validate a new
reference and to show name/username), `Secrets/Get` + `Secrets/GetPassword`
supply the username and password right before a power, sensor, SEL or KVM
action. Nothing is cached.

A new `internal/bmc` package owns the decisions: BMC address (management IP,
else the address the inventory agent reported on the device's `bmc`
interface), the reference status for the viewing user, setting/clearing the
reference (validated against Warden for the acting user, audited in the same
transaction) and the closed set of failure reasons. Three dedicated device
routes (`GET/PUT/DELETE /devices/{id}/bmc`) replace setting the reference via
the device body. The existing power/sensors/SEL/KVM routes answer distinct
reasons (`bmc_not_configured`, `bmc_no_address`, `bmc_secret_forbidden`,
`bmc_secret_not_found`, `warden_unavailable`, `bmc_unreachable`,
`bmc_auth_failed`, `bmc_error`). Power actions and KVM sessions become
audit rows.

The secret picker in the IPAM UI calls Warden's existing user API through
the gateway (`/api/warden/v1/secrets/search`, `/api/warden/v1/secrets?root=true`)
with the shell's session, so Warden needs no new RPC and no code change —
only a mesh-policy rule allowing `svc/ipam` exactly `Secrets/Get` and
`Secrets/GetPassword`. The dead `/warden-secrets` routes are removed.

## Technical Context

**Language/Version**: Go 1.26 (IPAM), TypeScript/Vue 3 (IPAM UI remote)

**Primary Dependencies**: adds `github.com/go-tangra/go-tangra-warden/sdk/v4 v4.0.0`
(published; already used by ticket and dns) for the `warden.v1.Secrets`
client stubs; otherwise existing (go-tangra/v4, auth sdk authclient,
bougou/go-ipmi, `@go-tangra/ui` 4.2.1)

**Storage**: PostgreSQL/TimescaleDB with RLS — **no migration**; the existing
`ipam_devices.ipmi_secret_ref` holds the pointer; who/when lives in
`ipam_audit_events` rows written in the same transaction

**Testing**: `go test -race` with memstore and fakes (Warden `Fake` gains
per-token access, availability switch and a call log; a bufconn
`warden.v1.Secrets` test server checks metadata forwarding and code
mapping; the IPMI `Fake` records calls so "BMC never contacted" is
asserted); negative security tests (refused secret is never stored; no BMC
call when Warden refuses; no password in any response/log/event/audit/backup
— leak test; device body cannot change the reference; backup import cannot
inject a reference; missing user token never reaches Warden); fuzz of the
reference input; testcontainers integration for the new store method (RLS
isolation, audit row in the same transaction); OpenAPI route/permission
contract test; vitest for the BMC card, picker and the Power/KVM states

**Target Platform**: Linux container (freya-stack)

**Project Type**: ipam service + UI remote; policy-only changes in
go-tangra-warden-v4 and go-tangra-docker

**Performance Goals**: one Warden `Get` per device-page view (status) and
one `Get` + one `GetPassword` per BMC action; Warden call timeout 5 s;
IPMI timeout unchanged (config `ipmi.timeout_seconds`)

**Constraints**: FR-004 no storage/caching/logging of passwords; SR-001
exact mesh operations; no proto change (no ipam SDK release); coverage
gates — 100 % for `internal/authz`, `internal/sealed`, `internal/ipnet`,
`internal/hostreport`, `internal/hostplan`, `internal/snmpcred`,
`internal/arpplan` + new `internal/bmc`; ≥ 80 % total

**Scale/Scope**: per-device references (no inheritance), user-initiated
use only; 3 new HTTP operations, 2 removed, 3 audit event types, 1 store
method, 1 CASL ability, 1 warden policy rule

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1 design — all PASS.*

- [x] **I. Secure by Default**: fails closed — no Warden connection, no user
      token, or a refusal means no BMC contact; the old fallback to a
      `Fake` Warden client and to the host's primary IP are removed; the
      reference can only be set through a validated, audited route.
- [x] **II. Zero Trust**: IPAM calls Warden only over mTLS with its SPIFFE
      id and the user's platform token; Warden re-verifies the token and
      applies its per-secret Zanzibar check; the Warden policy grants
      `svc/ipam` exactly `Secrets/Get` and `Secrets/GetPassword`.
- [x] **III. Boundary Validation**: typed OpenAPI schema for the reference
      input (`additionalProperties: false`, uuid format), 1 KiB body limit,
      CSRF on mutations, closed reason vocabulary; Warden ids validated
      before any RPC.
- [x] **IV. Test-First**: every phase lists tests first; negative tests
      enumerated above; new decision package at 100 %; fuzz target.
- [x] **V. Observability**: audit rows for reference set/changed/cleared,
      power actions (ok/refused/error with reason) and KVM sessions; module
      log line for reads; Warden audits every metadata read and password
      reveal on its side.
- [x] **VI. Supply Chain**: one new dependency, the platform's own published
      Warden SDK (already in two modules); `make vuln` gate.
- [x] **VII. Simplicity**: no new table, no Warden code change, no new RPC;
      the UI uses Warden's existing API for the picker.
- [x] **Threat Model**: STRIDE in [research.md](research.md#stride-threat-model).

## Project Structure

### Documentation (this feature)

```text
specs/024-bmc-warden-credentials/
├── spec.md  plan.md  research.md  data-model.md  quickstart.md
├── contracts/{ipam-http.md,warden-mesh.md,audit-events.md}
├── checklists/requirements.md
└── tasks.md
```

### Source Code

```text
go-tangra-ipam-v4
  go.mod / go.sum                                   # + warden sdk v4.0.0
  internal/warden/warden.go                         # real client: Meta, Credentials; token ctx; errors; Fake; Unavailable
  internal/ipmi/ipmi.go (+classify)                 # ErrUnreachable / ErrAuthFailed classification
  internal/bmc/                                     # NEW: address, status, set/clear, resolve credentials, reasons (100 %)
  internal/audit/audit.go                           # bmc_reference_set|changed|cleared
  internal/repo/repo.go, repodb/bmc.go, memstore/bmc.go  # SetDeviceBMCRef (+ audit row, same tx)
  internal/devices/devices.go                       # body cannot change the reference
  internal/backup/backup.go                         # import never injects a reference; overwrite keeps it
  internal/httpapi/{bmc.go,power.go,handlers.go,deps.go}  # 3 routes, reasons, audit; /warden-secrets removed
  internal/grpcapi/servers.go                       # power/KVM RPCs via bmc service, forwarded token
  internal/app/app.go                               # wiring; Unavailable fallback instead of Fake
  api/openapi/ipam.yaml, pkg/ipammanifest            # routes, schemas, DeviceBmc ability
  deploy/policy.yaml                                # comment: ipam -> warden rule lives in warden's policy
  ui/src/components/DeviceBmcCard.vue (NEW), ui/src/components/WardenSecretPicker.vue (NEW)
  ui/src/views/devices/{detail.vue,ipmi-kvm.vue}, ui/src/stores/devices.ts, ui/src/api/{types.ts,bmc.ts (NEW)}
  scripts/coverage-gate.sh, README.md
go-tangra-warden-v4 (branch 024-ipam-secret-access)
  deploy/policy.yaml                                # rule ipam-bmc-secrets
  internal/app/policy_test.go (NEW)                 # the default policy grants ipam exactly Get + GetPassword
go-tangra-docker (branch v4, local commit)
  policies/warden.yaml                              # same rule
```

**Structure Decision**: `internal/bmc` is the single place that decides
whether a BMC may be contacted and with which address and credentials; HTTP
and gRPC only translate its reasons. The Warden client is a thin, typed
adapter whose only job is forwarding the user's identity and mapping
Warden's status codes to sentinel errors.

## Rollout

1. **warden v4.4.2** (patch: default policy rule baked into the image).
   Production mounts `prod/policies/warden.yaml`, so production needs the
   same rule added there (with its trust domain) and a warden restart —
   independent of the image.
2. **ipam v4.7.0** (minor: new routes, removed dead routes, new reasons).
   No migration. Devices keep their (empty) references.
3. **go-tangra-docker**: `policies/warden.yaml` rule (committed locally in
   this feature), bump `IPAM_IMAGE` (and `WARDEN_IMAGE` if 1. is released);
   `configs/ipam.yaml` already lists `warden: ["warden:9843"]`.
4. Operators create BMC secrets in Warden (username + password), attach
   them to devices and share them with the admins who may use them.

Merges, tags, pins and the production deploy are user-confirmed steps.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| UI calls another module's API (Warden) directly | Listing needs Warden's per-user visibility; Warden has no mesh List RPC | A new Warden RPC means a Warden feature, SDK tag and release for a list the user API already serves with the same checks |
| Dedicated `/devices/{id}/bmc` routes instead of the device body field | Setting must be validated against Warden for the acting user and audited (FR-002/FR-008) | Validating inside the generic device PUT would couple every device edit to a Warden round trip and hide failures in generic 422s |
