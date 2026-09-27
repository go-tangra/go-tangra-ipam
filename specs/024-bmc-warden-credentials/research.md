# Research: BMC Credentials from Warden (024)

Findings from the code as of ipam `main` @ v4.6.1 (12dccb8), warden v4.4.1,
go-tangra-docker `v4` @ 4cb8331, and the decisions they led to.

## Findings

- **F1 — IPAM's Warden client is a stub.** `internal/warden/warden.go:89`
  `New` builds a `grpcClient` whose `GetSecret` (`:96–103`) and
  `ListSecrets` (`:105–109`, comment: "warden has no List RPC") return
  `errNotWired` (`:94`). The Warden SDK is not a dependency (`go.mod`).
- **F2 — silent Fake fallback.** `internal/app/app.go:159–164` starts with
  `warden.NewFake()` and only swaps in the real client when the Freya
  connection succeeds; on failure the module keeps an empty Fake, so every
  BMC action looks like "secret not found" instead of "Warden unavailable".
- **F3 — bare 422.** `internal/httpapi/power.go:145–165` (`loadBMC`): host =
  `ManagementIP`, else `PrimaryIP` (`:151–154`); `IPMISecretRef == "" ||
  host == ""` → `422 validation_failed` (`:155–157`); Warden errors go
  through `failSvc` → `404 not_found` / `500 internal`. BMC errors are
  always `500 internal` (`power.go:26–29` etc.).
- **F4 — PrimaryIP fallback is wrong.** The primary IP is the host OS; IPMI
  over LAN to it never reaches a BMC. Feature 020 already records the BMC
  LAN address as an address bound to the device's `bmc` / `bmc-N` interface
  (`internal/hostplan/bmc.go:11–27`, `internal/hostplan/address.go:150–155`)
  — `10.1.112.14` for node-1.
- **F5 — out-of-band ops are not audited.** `power.go:169–181` `auditOOB`
  writes a module log line; the audit vocabulary already has
  `power_action` and `kvm_session_started` (`internal/audit/audit.go`) but
  nothing writes them.
- **F6 — the reference is a plain device field.** `store.Device.IPMISecretRef`
  (`internal/store/models.go:300`) is written by device create and by the
  full-replace update (`internal/repo/repodb/db.go:759`, `ipmi_secret_ref=$23`),
  so any `devices:manage` caller can set any id without Warden validation,
  and a PUT without the field clears it. The gRPC mapper passes it through
  (`internal/grpcapi/mapper.go:575`).
- **F7 — dead listing routes.** `GET /api/ipam/v1/warden-secrets` and
  `/{id}` (`api/openapi/ipam.yaml:642–645`, handlers
  `internal/httpapi/handlers.go:570–603`) call `ListSecrets`, which can
  never work; the UI does not use them (only `ui/src/api/schema.d.ts`).
- **F8 — Warden's mesh surface.** `sdk/api/proto/warden/v1/warden.proto`
  service `Secrets { Get, GetPassword, Check }`. `internal/grpcapi/secrets.go`:
  the peer is an mTLS service, the end user comes from the platform token in
  `authorization` metadata, verified by `authclient.KratosMiddleware`
  (installed for `/warden.v1.Secrets/*`, `internal/app/wire.go:61`). `Get`
  (`:52–69`) requires `read` on the secret and returns metadata only;
  `GetPassword` (`:72–85`) → `Service.Reveal` (`internal/secrets/secrets.go:561`)
  requires `read` and audits `secret_password_read`. Errors: `PermissionDenied`
  (forbidden), `NotFound`, `InvalidArgument`, `Unavailable`
  (`grpcapi/secrets.go:37–49`). No List RPC.
- **F9 — Warden's user API lists and searches.** `GET /api/warden/v1/secrets`
  (`folder_id`, `root`, cursor/limit) and `GET /api/warden/v1/secrets/search?q=`
  (1–200 chars), both `x-freya-permission: secrets:read`, return only
  secrets the user can read, without material
  (warden `api/openapi/warden.yaml:196–213`, `internal/secrets/secrets.go:333,425`).
- **F10 — a module UI may call another module's API.** The UI kit client
  (`@go-tangra/ui/api` `createApi`, `client.d.ts:19`: "Paths starting with
  "/" bypass it") sends absolute paths unchanged to the same origin; the
  gateway routes `/api/warden/v1/*` to Warden with the shell's session and
  enforces Warden's own `x-freya-permission`. No module does this yet, but
  nothing prevents it.
- **F11 — the incoming platform token is available and reusable.** The
  gateway sets `Authorization: Bearer <platform token>` on every proxied
  request (go-freya `services/gateway/internal/proxy/httpproxy/proxy.go:105`);
  IPAM verifies it (`internal/httpapi/middleware.go:70`). Neither IPAM nor
  Warden configures an audience (`authclient.Config.Audience` empty), so
  Warden accepts the same token. Precedent for the outgoing metadata:
  `go-tangra-ticket-v4/internal/secrets/secrets.go:107`
  (`metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok)`),
  there with the module's own token.
- **F12 — mesh policy.** Warden's default policy (`deploy/policy.yaml`,
  baked into the image, Dockerfile `COPY deploy`) allows the gateway (`*`)
  and ticket (`/warden.v1.Secrets/GetPassword`). go-tangra-docker keeps a
  copy in `policies/warden.yaml` that production renders into
  `prod/policies/warden.yaml` and mounts over the image copy. IPAM has no
  rule, so any IPAM → Warden call is refused today.
- **F13 — discovery is already configured.** `go-tangra-docker/configs/ipam.yaml`
  and `go-tangra/deploy/stack/configs/ipam.yaml:22` list
  `warden: ["warden:9843"]`; `warden: { service: warden }` is set.
- **F14 — audit guard.** `internal/audit/audit.go:226` drops detail keys
  containing `secret`, `credential`, `ipmi`, `password`, … so detail keys
  must be neutral (`reference`, `previous_reference`, `action`).
- **F15 — backup.** Export strips the reference (`internal/backup/backup.go:430`),
  but import writes whatever the uploaded file carries (`:316`) and an
  overwrite deletes and recreates the device, losing its reference.
- **F16 — KVM.** `kvm.Manager.StartSession` keeps the credentials in
  process memory bound to the one-time token until the token expires
  (`internal/kvm/kvm.go:131`, TTL `kvm.token_ttl_seconds`, 300 s) to log in
  to the BMC web UI; they are never persisted or logged.
- **F17 — go-ipmi errors.** Connect failures from bougou/go-ipmi are wrapped
  strings: RAKP/auth failures mention `rakp`, `authcode`, `integrity check`,
  status codes like `Unauthorized name`; unreachable BMCs time out
  (`i/o timeout`, `context deadline exceeded`) or are refused.
- **F18 — UI.** `ui/src/views/devices/detail.vue:136` shows
  "BMC: configured/none"; `ipmi-kvm.vue` shows `describe(e)` of any error in
  one alert; no way to pick a secret.

## Decisions

### D1 — Picker lists secrets through Warden's user API (option a)

The IPAM UI calls `GET /api/warden/v1/secrets/search?q=…&limit=20` (typing)
and `GET /api/warden/v1/secrets?root=true&limit=20` (initial list) with the
shell's session (F9, F10). Warden decides visibility and needs no change,
SDK tag or release. The picker shows name, username and folder path only;
it never requests `/password`. A 403 from Warden (user lacks
`secrets:read`) or 404/503 (Warden not installed/unavailable) becomes an
explained state in the picker.

*Rejected*: (b) a new metadata `List`/`Search` RPC on Warden — duplicates
F9, needs a Warden feature + SDK tag + release + policy for no gain.
*Consequence*: the dead IPAM `/warden-secrets` routes (F7) are removed.

### D2 — IPAM forwards the signed-in user's platform token

HTTP handlers put the incoming bearer token into the request context
(`warden.WithUserToken`); the gRPC handlers do the same from incoming
`authorization` metadata. The Warden client appends it as outgoing
`authorization` metadata on each call over the Freya mesh connection
(F11). No token → `ErrNoUserToken`, no RPC. The token is never logged.
IPAM holds no module token for Warden (unattended use is out of scope).

### D3 — Warden client surface and error mapping

```go
Meta(ctx, ref) (SecretMeta, error)          // Secrets/Get
Credentials(ctx, ref) (Credentials, error)  // Secrets/Get + Secrets/GetPassword
```

`Credentials{Username, Password, HostURL}` redacts itself in
`String`/`GoString`/`LogValue`. Mapping: `NotFound`/`InvalidArgument` →
`ErrNotFound`; `PermissionDenied` with Warden's message `forbidden`
(`grpcapi/secrets.go:40`) or `Unauthenticated` → `ErrForbidden`;
`PermissionDenied` with any other message is the Freya mesh policy
refusing `svc/ipam` (`go-tangra/authz/middleware.go:18`, Kratos `denied`)
→ `ErrPolicyDenied`, which `errors.Is` `ErrUnavailable` (a misconfigured
policy must not read as "no access"); anything else, deadline, or
transport error → `ErrUnavailable`. Only the code is kept, never Warden's
message. Per-call timeout 5 s. The reference must
be a UUID (Warden ids) before any RPC. The `Fake` gains per-token denials,
an availability switch and a call log; a separate `Unavailable` client
replaces the silent Fake fallback (F2).

### D4 — Validation on save uses `Secrets/Get`

`PUT /devices/{id}/bmc` calls `Meta` as the acting user: forbidden → 403
`bmc_secret_forbidden`, not found → 422 `bmc_secret_not_found`,
unavailable → 503 `warden_unavailable`; nothing is stored in these cases.
`Get` checks the same `read` relation as `GetPassword` (F8) without
revealing material; `Check` is not needed. Hence the policy grants exactly
`Get` + `GetPassword` (D11).

### D5 — Dedicated reference routes; the device body no longer sets it

`GET /devices/{id}/bmc` (`ipam:read`) returns the status for the viewer;
`PUT` / `DELETE` (`devices:manage`) set/clear it. Device create/update
(HTTP and gRPC) refuse a *change* of `ipmi_secret_ref` with
`devices.ErrBMCRefReadOnly` → 422 `validation_failed`
(`detail.field = ipmi_secret_ref`); an omitted or unchanged value keeps the
stored reference (fixes F6's accidental clearing). The reference stays in
device JSON (a pointer, not a secret).

### D6 — BMC address

1. `device.management_ip` if set (source `management_ip`);
2. else the address bound to this device on interface `bmc` (then `bmc-2`,
   …) with report state not `not_reported`, IPv4 before IPv6 (source
   `reported`);
3. else none → `bmc_no_address`.

The `PrimaryIP` fallback is removed (F4).

### D7 — Reason vocabulary and HTTP status

| Reason | Status | When |
|---|---|---|
| `bmc_not_configured` | 409 | device has no reference |
| `bmc_no_address` | 409 | no management IP and no reported BMC address |
| `bmc_secret_forbidden` | 403 | Warden refused the user (or no user token) |
| `bmc_secret_not_found` | 409 (422 on PUT) | referenced secret deleted / unknown |
| `warden_unavailable` | 503 | Warden unreachable, timed out, not wired |
| `bmc_unreachable` | 504 | BMC did not answer (timeout, refused, no route) |
| `bmc_auth_failed` | 502 | BMC rejected the credentials |
| `bmc_error` | 502 | BMC answered with another failure |

Order of checks: device (404) → reference → address → Warden → BMC, so
the BMC is contacted only after Warden released the credentials (FR-005,
SC-004). The body is `{"reason": …, "detail": {"address": …}}` where the
address is known (no secret values).

### D8 — IPMI error classification

`ipmi.Classify(err)` wraps connect errors as `ErrUnreachable` (net timeout,
`context.DeadlineExceeded`, connection refused, no route, i/o timeout) or
`ErrAuthFailed` (rakp, authcode, integrity check, unauthorized, invalid
user/password, privilege) (F17). The real client applies it in `connect`;
`Fake.Err` can inject either. KVM login failures with 401/403 from the BMC
web UI map to `ErrAuthFailed`, transport errors to `ErrUnreachable`.

### D9 — Audit

Rows in `ipam_audit_events`, actor = user:

- `bmc_reference_set` / `bmc_reference_changed` / `bmc_reference_cleared`,
  subject device, details `reference`, `previous_reference`,
  `reference_name` — written in the same transaction as the column change
  via the new store method `SetDeviceBMCRef(ctx, tenant, device, ref, row)`.
- `power_action` (subject `power`, id = device) for every POST power:
  details `action`, outcome `ok` / `refused` (reason) / `error` (reason).
- `kvm_session_started` (subject `kvm`, id = device), same outcomes.
- Refused/failed attempts carry the D7 reason in `reason`.
- Reads (power status, sensors, SEL) keep the module log line (no audit
  row per tab refresh); Warden audits every password reveal anyway.

Who/when of the reference is read from these rows; no new columns.

### D10 — Credentials lifetime

Fetched per action, passed to the IPMI/KVM client, dropped at the end of
the request. No cache (rotation takes effect immediately, spec edge case).
KVM keeps them only in memory for the token TTL (F16) — unchanged and
documented. Error texts from IPMI/KVM never reach the response (only the
reason) and are not logged with credentials.

### D11 — Mesh policy

Warden rule:

```yaml
- id: ipam-bmc-secrets
  from: ["spiffe://example.org/svc/ipam"]
  to: ["warden"]
  operations: ["/warden.v1.Secrets/Get", "/warden.v1.Secrets/GetPassword"]
  effect: allow
```

in go-tangra-warden-v4 `deploy/policy.yaml` (image default; branch
`024-ipam-secret-access`, released as a warden patch) with a unit test that
the rule is exact, and in go-tangra-docker `policies/warden.yaml` (dev
stack; production renders it into `prod/policies/warden.yaml` — on an
existing server that file must be edited by hand and warden restarted).
IPAM's `deploy/policy.yaml` header comment names the rule. Discovery needs
no change (F13).

### D12 — Fail closed at startup

When `a.Freya.Client(ctx, "warden")` fails, IPAM wires `warden.Unavailable{}`
(every call `ErrUnavailable`) and logs a warning — never a Fake (F2).

### D13 — Backup

Export keeps stripping the reference (feature 011). Import never takes a
reference from the file (it would bypass D4); when an import overwrites an
existing device it keeps that device's current reference. This narrows the
spec's edge case "restored devices keep their reference" to devices that
still exist; a device recreated from a backup shows "not configured".

### D14 — gRPC `ipam.v1` power/KVM RPCs

They use the same `bmc` service and forward the incoming `authorization`
metadata (gateway gRPC ingress carries the user token). Reasons map to
codes: not configured / no address / secret not found → `FailedPrecondition`
(message = reason), forbidden → `PermissionDenied`, Warden unavailable →
`Unavailable`, BMC unreachable → `DeadlineExceeded`, auth failed / BMC
error → `Unavailable` with the reason as message.

### D15 — UI

- `DeviceBmcCard.vue` on the device Overview: "BMC credentials" with the
  secret name, username, folder, access state and the BMC address + source;
  "Attach / Change / Clear" for `configure DeviceBmc` (`devices:manage`).
- `WardenSecretPicker.vue`: modal with a search box → Warden API (D1);
  selecting saves via `PUT /devices/{id}/bmc`; explained errors.
- `ipmi-kvm.vue`: loads `GET /devices/{id}/bmc` first; renders an explained
  state per D7 reason (with the corrective action) instead of firing power
  and sensor calls that must fail; per-call errors map reasons to text.
- `detail.vue` BMC row: "zax-5 IPMI (Warden)" / "none".

## STRIDE threat model

| Threat | Vector | Mitigation |
|---|---|---|
| **S**poofing | Another mesh service calls Warden as IPAM, or IPAM impersonates a user | mTLS SPIFFE identity + Warden policy rule for `svc/ipam` only; Warden re-verifies the forwarded platform token (signature, expiry, revocation) — IPAM cannot mint one |
| **S**poofing | Caller forges a user token in gRPC metadata to IPAM | IPAM's gRPC surface is reachable only via the gateway (IPAM policy); Warden verifies the token itself |
| **T**ampering | Attacker points a device at a secret they cannot read, hoping another admin's action reveals it to the BMC they control | PUT validates the reference for the acting user (D4); each use fetches as the *using* user, so a victim's action sends the victim's permitted secret to that device's BMC address — address changes need `devices:manage`, audited; admins see the address before acting |
| **T**ampering | Reference injected via device body, gRPC Update or backup import | D5 refuses changes outside the dedicated route; D13 ignores imported references |
| **R**epudiation | Admin denies a power cycle or reference change | Audit rows with actor, device, action, outcome, reason (D9); Warden audits every password reveal with the user |
| **I**nformation disclosure | Password in responses, logs, events, audit, backup, errors | No response carries it; `Credentials` redacts in `String`/`LogValue`; IPMI/KVM errors collapse to reasons; audit guard; backup strips; leak test (SR-002) |
| **I**nformation disclosure | Status route reveals secret metadata to a viewer who cannot read it | Metadata fetched as the viewer; Warden refuses → `access: forbidden`, no name/username shown |
| **I**nformation disclosure | Cached credentials served to another user | No cache (D10); KVM token binds the credentials to a one-time token for one session |
| **D**enial of service | Repeated tab loads hammer Warden or the BMC | Status needs one Warden `Get`; power/sensor calls only after the status says ready; Warden timeout 5 s, IPMI timeout bounded; gateway rate limits |
| **E**levation of privilege | Tenant admin powers servers | Power/KVM keep platform-admin (`power:control`, `kvm:access`, SR-003) and Warden read access to the secret is required in addition |
| **E**levation of privilege | IPAM uses Warden beyond BMC needs | Policy grants exactly `Get` + `GetPassword`; still subject to the user's per-secret rights |
