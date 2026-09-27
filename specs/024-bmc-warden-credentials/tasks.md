---

description: "Task list for 024 BMC Credentials from Warden for Power, KVM and Sensors"
---

# Tasks: BMC Credentials from Warden for Power, KVM and Sensors

**Input**: Design documents from `specs/024-bmc-warden-credentials/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: MANDATORY (Constitution IV). In every phase the tests are listed
first and must be written and seen failing before the implementation tasks
of that phase. Negative security tests and fuzz tests are listed explicitly.

**Paths**: relative to go-tangra-ipam-v4 unless prefixed with
`warden:` (go-tangra-warden-v4, branch `024-ipam-secret-access`) or
`docker:` (go-tangra-docker, branch `v4`, local commit only).

**Release tasks** (tags, PR merges, stack pins, production deploy) require
explicit user confirmation before they are executed.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US3 from spec.md

---

## Phase 1: Setup

- [x] T001 Add `github.com/go-tangra/go-tangra-warden/sdk/v4 v4.0.0` to `go.mod`/`go.sum` (published; same version as ticket and dns)
- [x] T002 [P] Add `internal/bmc` to the 100 % list in `scripts/coverage-gate.sh`

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the real Warden client, IPMI error classes, audit types, the store method and the `bmc` decision package that every story needs.

### Tests first

- [x] T003 [P] Warden client tests in `internal/warden/client_test.go` against an in-process `warden.v1.Secrets` server (bufconn): `Meta` maps name/username/folder_path/host_url; `Credentials` combines Get + GetPassword; the outgoing `authorization` metadata is exactly `Bearer <user token>`; **negative**: no user token → `ErrNoUserToken` and zero RPCs; non-UUID ref → `ErrNotFound` and zero RPCs; `NotFound`/`InvalidArgument` → `ErrNotFound`, `PermissionDenied` "forbidden" (Warden's per-secret refusal)/`Unauthenticated` → `ErrForbidden`, `PermissionDenied` with another message (mesh policy refusal) → `ErrPolicyDenied` (is `ErrUnavailable`), `Unavailable`/deadline → `ErrUnavailable`; returned errors never contain the password or Warden's message; `Credentials` prints `[REDACTED]` via `%v`, `%+v`, `%#v` and slog
- [x] T004 [P] Fake/Unavailable tests in `internal/warden/warden_test.go`: per-token denial, missing secret, availability switch, call log records (token, op, ref), `Unavailable` returns `ErrUnavailable` for both calls; `WithUserToken`/`UserToken` round trip
- [x] T005 [P] IPMI classification tests in `internal/ipmi/classify_test.go`: timeout/deadline/refused/no route/i-o timeout → `ErrUnreachable`; rakp/authcode/integrity/unauthorized/invalid password → `ErrAuthFailed`; nil → nil; other → unchanged; `Fake.Err` with either sentinel passes through
- [x] T006 [P] Audit test in `internal/audit/audit_test.go`: `bmc_reference_set|changed|cleared` known; neutral keys `reference`/`previous_reference`/`reference_name` survive the guard
- [x] T007 [P] Store tests in `internal/memstore/bmc_test.go`: `SetDeviceBMCRef` returns previous ref, writes the audit row, other tenant → `ErrNotFound` and no row
- [x] T008 [P] Integration test in `internal/repo/repodb/bmc_integration_test.go` (tag `integration`): `SetDeviceBMCRef` updates only the column, appends the audit row in the same transaction, RLS hides another tenant's device (`ErrNotFound`)
- [x] T009 [P] `bmc` package tests in `internal/bmc/bmc_test.go`: address resolution (management IP wins; reported `bmc` then `bmc-2`; IPv4 before IPv6; `not_reported` ignored; primary IP never used; none → `ErrNoAddress`); `Status` for none / ok / forbidden / not_found / unavailable with `ready` and first `reason`; `Set` validates via Warden `Meta` as the caller and stores + audits set/changed, same ref → no write; forbidden/not found/unavailable → nothing stored; `Clear` audits only when something was removed; `Resolve` order (not configured → no address → Warden) and **negative**: Warden refusal returns before any BMC use; `Creds` maps `host_url` `lanplus://h:6230` → protocol 2.0 port 6230, `lan://` → 1.5, other → auto/623; `Reason(err)` table for every sentinel incl. KVM login errors
- [x] T010 [P] Fuzz target `FuzzReferenceInput` in `internal/bmc/fuzz_test.go` (strict JSON decode + `ValidReference` never panics; accepted refs are canonical UUIDs)

### Implementation

- [x] T011 Rewrite `internal/warden/warden.go`: `Client{Meta, Credentials}`, `SecretMeta`, `Credentials` (redacting), errors (`ErrEmptyRef`, `ErrNotFound`, `ErrForbidden`, `ErrUnavailable`, `ErrNoUserToken`), `WithUserToken`/`UserToken`, gRPC client over `wardenv1.SecretsClient` with 5 s timeout and code mapping, `Fake` (per-token deny, `SetUnavailable`, `Calls`), `Unavailable` (T003, T004)
- [x] T012 [P] Add `ErrUnreachable`, `ErrAuthFailed`, `Classify` to `internal/ipmi` and apply it in the real client's `connect` (T005)
- [x] T013 [P] Add the three event types to `internal/audit/audit.go` (T006)
- [x] T014 Add `SetDeviceBMCRef` to `internal/repo/repo.go`, implement in `internal/repo/repodb/bmc.go` (tenant tx, update + audit insert) and `internal/memstore/bmc.go` (T007, T008)
- [x] T015 Implement `internal/bmc/bmc.go` (+ `address.go`, `reason.go`): `Service{Store, Warden, Now}`, `Address`, `Status`, `Set`, `Clear`, `Resolve` (device, address, `warden.Credentials`), `IPMICreds`, `Reason`/`Status code` tables, `ValidReference` (T009, T010)

**Checkpoint**: Warden reachable with the user's identity, decisions proven at 100 %.
(Build kept green: `power.go`/`grpcapi` already fetch via `Credentials` with the
forwarded token, the two `/warden-secrets` handlers answer 501 until US1
removes them, and `app.go` wires `warden.Unavailable` instead of a Fake —
research D12. `FuzzReferenceInput` joined `make fuzz`.)

---

## Phase 3: User Story 1 — Attach a Warden secret as a device's BMC credentials (Priority: P1) 🎯 MVP

**Goal**: an administrator picks a Warden secret they can read; the device stores only the reference and shows its name and BMC address.

**Independent test**: PUT a readable secret → status shows name/username/address; an unreadable one → 403 and nothing stored.

### Tests first

- [x] T016 [P] [US1] HTTP tests in `internal/httpapi/bmc_test.go`: GET status none/ok/forbidden/not_found/unavailable (per viewer token), reported address source; PUT ok → 200 status + audit `bmc_reference_set`, change → `bmc_reference_changed` with previous; **negative**: PUT unreadable secret → 403 `bmc_secret_forbidden` and device unchanged; unknown → 422 `bmc_secret_not_found`; Warden down → 503 `warden_unavailable`; non-UUID → 422 `validation_failed`; unknown field → 400 `malformed_body`; other tenant's device → 404; DELETE → 204 + `bmc_reference_cleared`, DELETE again → 204 without row
- [x] T017 [P] [US1] Device body tests in `internal/devices/devices_test.go` and `internal/httpapi/bmc_test.go`: **negative**: POST with `ipmi_secret_ref` → 422 `detail.field=ipmi_secret_ref`; PUT with a different ref → 422; PUT without the field keeps the ref; PUT with the same ref ok; gRPC `DeviceService.Update` with a different ref → `InvalidArgument` (`internal/grpcapi`)
- [x] T018 [P] [US1] Backup tests in `internal/backup/backup_test.go`: **negative**: import of a file carrying `ipmi_secret_ref` creates the device without it; overwrite of an existing device keeps its current ref
- [x] T019 [P] [US1] OpenAPI contract test (existing route/permission test in `internal/httpapi`): `/devices/{id}/bmc` GET `ipam:read`, PUT/DELETE `devices:manage` + CSRF, PUT body limit 1024; `/warden-secrets*` absent
- [x] T020 [P] [US1] Manifest test in `pkg/ipammanifest/manifest_test.go`: ability `configure DeviceBmc` requires `devices:manage`
- [x] T021 [P] [US1] UI unit tests `ui/tests/unit/bmc.spec.ts` (DeviceBmcCard, WardenSecretPicker): card renders none / name+username+folder / no access / not found / unavailable and address + source; Attach/Change/Clear only with `configure DeviceBmc`; picker searches Warden (`/api/warden/v1/secrets/search?q=`), lists root on open, never requests `/password`, shows Warden 403/404/503 as explained states, saves via PUT and shows `bmc_secret_forbidden`

### Implementation

- [x] T022 [US1] `internal/devices/devices.go`: `ErrBMCRefReadOnly` on create with a ref / update with a different ref; keep stored ref otherwise; map to 422 `detail.field` in `internal/httpapi` and `InvalidArgument` in `internal/grpcapi`
- [x] T023 [US1] `internal/httpapi/bmc.go`: GET/PUT/DELETE `/devices/{id}/bmc` via `bmc.Service` with the caller's token in the context; `failBMC` reason mapping; remove the `/warden-secrets` handlers from `handlers.go`; `Deps.BMCRefs *bmc.Service`
- [x] T024 [US1] `api/openapi/ipam.yaml`: `DeviceBmcStatus`, `DeviceBmcInput` schemas, the three operations with responses from contracts/ipam-http.md; drop `/warden-secrets*`; regenerate `ui/src/api/schema.d.ts`
- [x] T025 [P] [US1] `internal/backup/backup.go`: import drops the ref; overwrite keeps the existing one
- [x] T026 [P] [US1] `pkg/ipammanifest/manifest.go`: ability `configure DeviceBmc` → `devices:manage`
- [x] T027 [US1] `internal/app/app.go`: build `bmc.Service`; `warden.Unavailable{}` instead of a Fake when the Warden connection fails
- [x] T028 [US1] UI: `ui/src/api/types.ts` (BmcStatus, WardenSecretItem), `ui/src/api/bmc.ts` (Warden search/list), `ui/src/stores/devices.ts` (bmc, setBmc, clearBmc), `ui/src/components/WardenSecretPicker.vue`, `ui/src/components/DeviceBmcCard.vue`, `ui/src/views/devices/detail.vue` (card on Overview, BMC row with the secret name)

**Checkpoint**: references are set only through Warden validation and are visible per viewer.

---

## Phase 4: User Story 2 — Power, KVM and sensors use the Warden secret (Priority: P1)

**Goal**: power status/actions, sensors, SEL and KVM fetch the password from Warden per action for the signed-in user.

**Independent test**: with a readable secret the fake BMC receives the Warden username/password; with an unreadable one the request is refused and the fake BMC saw nothing.

### Tests first

- [x] T029 [P] [US2] HTTP tests in `internal/httpapi/power_test.go` (rewrite of the 422/404/500 expectations in `power_more_test.go`; fixtures seed the reference through the store): success passes Warden's username/password (and host_url port/protocol) to the BMC fake and the caller's token to Warden; password rotated in the fake between two calls → second call uses the new one (no cache); reported BMC address used when no management IP; **negative**: user denied in Warden → 403 `bmc_secret_forbidden` and `bmc.Fake` recorded no call for power/sensors/SEL/action/KVM; tenant admin (non platform-admin) still 403 `forbidden` without any Warden call
- [x] T030 [P] [US2] Audit tests in the same file: POST power ok → `power_action` row (action, outcome ok, target address); refused → outcome `refused` + reason; BMC failure → `error` + reason; KVM session → `kvm_session_started`; no row for status/sensors/SEL
- [x] T031 [P] [US2] gRPC tests in `internal/grpcapi/power_test.go`: `PowerStatus`/`Power`/`StartKvmSession` forward the incoming `authorization` metadata to Warden; **negative**: without it → `PermissionDenied` and no BMC call; not configured → `FailedPrecondition`

### Implementation

- [x] T032 [US2] Rewrite `internal/httpapi/power.go` on `bmc.Service.Resolve`: token into context, platform-admin first, audit rows via the store for actions/KVM, module log for reads, no primary-IP fallback
- [x] T033 [US2] `internal/grpcapi/servers.go`: power/KVM RPCs via `bmc.Service` with the forwarded token; reason → gRPC code (research D14); `grpcapi.Deps.BMCRefs`
- [x] T034 [US2] ~~`internal/kvm/kvm.go` login error classes~~ — not needed: `StartSession` only mints the one-time token and never contacts the BMC; the web login happens later in the token-gated `/bmc/` proxy, whose failures already answer 502 there. The session route therefore has only the Resolve reasons (warden/reference/address); `TestOOBReasons` skips the BMC-side reasons for it

**Checkpoint**: the broken capability works with per-user Warden credentials.

---

## Phase 5: User Story 3 — Clear states instead of errors (Priority: P1)

**Goal**: every failure cause has its own API reason and an explained state in the tab.

**Independent test**: each of the eight causes yields its reason on all four routes and a distinct message in the tab.

### Tests first

- [x] T035 [P] [US3] Table test in `internal/httpapi/power_test.go`: for each reason (not configured, no address, forbidden, not found, Warden unavailable, BMC unreachable, BMC auth failed, BMC error) × {GET power, POST power, GET sensors, GET SEL, POST kvm-session} the documented status + reason; `detail.address` only for BMC reasons; body never contains the password
- [x] T036 [P] [US3] UI unit tests in `ui/tests/unit/bmc.spec.ts` (Power / KVM tab) and `views.spec.ts`: status not ready → explained state with corrective action and no power/sensor calls; each reason text; attach link only with `configure DeviceBmc`; power buttons only with `control Power`, KVM only with `access Kvm`; per-call failure shows the reason text (not a generic error)

### Implementation

- [x] T037 [US3] `ui/src/views/devices/ipmi-kvm.vue` + `ui/src/api/bmc.ts` reason texts: load `/bmc` status first, explained states, per-call reason mapping
- [x] T038 [US3] OpenAPI response docs for the power/sensors/SEL/KVM routes (409/403/503/504/502 reasons) — landed with T024

**Checkpoint**: no bare 422 remains.

---

## Phase 6: Mesh policy and stack configuration

### Tests first

- [ ] T039 [P] `warden:internal/app/policy_test.go`: the default `deploy/policy.yaml` has rule `ipam-bmc-secrets` from `spiffe://example.org/svc/ipam` with exactly `/warden.v1.Secrets/Get` and `/warden.v1.Secrets/GetPassword`, and no other rule names ipam

### Implementation

- [ ] T040 `warden:deploy/policy.yaml`: add rule `ipam-bmc-secrets` (commit on branch `024-ipam-secret-access`)
- [ ] T041 [P] `docker:policies/warden.yaml`: same rule (local commit on `v4`, not pushed)
- [ ] T042 [P] `deploy/policy.yaml` (ipam) header comment: ipam calls warden `Secrets/Get` + `GetPassword` with the user's token; the allowing rule `ipam-bmc-secrets` lives in warden's policy; verify `warden: ["warden:9843"]` in `docker:configs/ipam.yaml` and go-tangra `deploy/stack/configs/ipam.yaml` (no change expected)

---

## Phase 7: Polish & cross-cutting

- [ ] T043 [P] Leak test `internal/httpapi/bmc_leak_test.go` (SR-002, SC-003): attach, status, power status/action/sensors/SEL/KVM with success and every failure (BMC errors echoing the password), backup export, events, audit rows and a JSON log handler — none contains the password or the user token
- [ ] T044 [P] README: BMC credentials section (Warden secret, policy rule, reasons)
- [ ] T045 Gates: `go vet ./...`, `go test -race ./...`, `make test-integration`, `make cover` (100 % packages incl. `internal/bmc`), `make vuln`; UI `npm run lint`, `npm run test:unit`, `npm run build`; warden `go test -race ./internal/app/`
- [ ] T046 Update memory/spec status (tasks ticked, deviations recorded in research.md D13)

---

## Phase 8: Release (user-confirmed — not executed in this feature run)

- [ ] T047 Warden: PR `024-ipam-secret-access` → main, tag v4.4.2 (image default policy)
- [ ] T048 IPAM: PR `024-bmc-warden-credentials` → main, tag v4.7.0
- [ ] T049 go-tangra-docker: push the policy commit; bump `IPAM_IMAGE` (and `WARDEN_IMAGE`)
- [ ] T050 Production: add rule `ipam-bmc-secrets` (production trust domain) to `prod/policies/warden.yaml` on the server, restart warden, deploy ipam 4.7.0
- [ ] T051 Live verification in freya-stack: quickstart scenarios 1–3 (needs operator sign-in and a real/simulated BMC)

---

## Dependencies & Execution Order

- Phase 1 → Phase 2 → US1 → US2 → US3; Phase 6 is independent of Phases 3–5
  (policy only); Phase 7 after US3.
- Within a phase: tests (seen failing) → store/pure code → services →
  HTTP/OpenAPI → UI.

## Parallel Example: Phase 2

```text
T003 T004 (warden)  T005 (ipmi)  T006 (audit)  T007 T008 (store)  T009 T010 (bmc)
then T011 ∥ T012 ∥ T013 ∥ T014, then T015
```

## Implementation Strategy

1. MVP = Phases 1–4: references can be attached and power/KVM/sensors work.
2. US3 makes every failure explained (required: the reported bug).
3. Policy (Phase 6) ships with the release; without it the mesh refuses
   every IPAM → Warden call and the tab says `warden_unavailable` (the
   module log names the mesh refusal), never "no access".
4. One ipam minor release + one warden patch release.

## Notes

- Never log or return the password or the user token; tests assert it.
- Audit detail keys must not contain `secret`, `credential`, `ipmi`,
  `password` (the guard drops them).
- Commit after each phase; no attribution trailer in commits.
