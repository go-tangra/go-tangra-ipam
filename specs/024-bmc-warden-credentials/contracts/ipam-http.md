# Contract: IPAM HTTP — device BMC credentials (024)

All routes behind the gateway; mutating routes require the CSRF header
(`#/components/parameters/csrf`). No response ever contains a BMC password.
Errors are `{"reason": "<reason>"}` or `{"reason": …, "detail": {…}}`; detail
values are never secrets.

## New routes

### GET /api/ipam/v1/devices/{id}/bmc — `ipam:read`

200 `DeviceBmcStatus` (computed for the viewing user; Warden metadata only):

```json
{"configured": true, "reference": "0192…", "access": "ok",
 "secret": {"name": "zax-5 IPMI", "username": "ADMIN", "folder_path": "/infra/bmc"},
 "address": "10.1.112.14", "address_source": "reported", "ready": true}
{"configured": true, "reference": "0192…", "access": "forbidden",
 "address": "10.1.112.14", "address_source": "reported", "ready": false, "reason": "bmc_secret_forbidden"}
{"configured": false, "address": "10.1.112.14", "address_source": "reported", "ready": false, "reason": "bmc_not_configured"}
```

`access`: `ok` | `forbidden` | `not_found` | `unavailable` (absent when not
configured). `reason` is the first blocking reason in the order
not_configured → no_address → forbidden/not_found/unavailable. 404
`not_found` (device).

### PUT /api/ipam/v1/devices/{id}/bmc — `devices:manage`, body ≤ 1 KiB

Request `DeviceBmcInput` (`additionalProperties: false`):

```json
{"reference": "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"}
```

`reference`: required, UUID. Validated with Warden `Secrets/Get` as the
acting user before anything is stored.

| Status | Reason | Meaning |
|---|---|---|
| 200 | — | `DeviceBmcStatus` after the change |
| 400 | `malformed_body` | not JSON / unknown field |
| 422 | `validation_failed` | reference missing or not a UUID (`detail.field = reference`) |
| 403 | `bmc_secret_forbidden` | acting user cannot read the secret in Warden |
| 422 | `bmc_secret_not_found` | Warden has no such secret |
| 503 | `warden_unavailable` | Warden unreachable |
| 404 | `not_found` | device not found in the tenant |

Audit `bmc_reference_set` (none → ref) or `bmc_reference_changed`
(ref → other ref); setting the same reference again is a no-op without an
audit row.

### DELETE /api/ipam/v1/devices/{id}/bmc — `devices:manage`

204 (no-op when none is set). Audit `bmc_reference_cleared` when one was
removed. 404 `not_found`.

## Changed routes

### GET/POST /devices/{id}/power, GET /devices/{id}/sensors, GET /devices/{id}/sel — `power:control`; POST /devices/{id}/kvm-session — `kvm:access`

Unchanged success bodies. Platform-admin remains required (403
`forbidden`). Failures now answer one of:

| Status | Reason | Detail |
|---|---|---|
| 409 | `bmc_not_configured` | — |
| 409 | `bmc_no_address` | — |
| 403 | `bmc_secret_forbidden` | — |
| 409 | `bmc_secret_not_found` | — |
| 503 | `warden_unavailable` | — |
| 504 | `bmc_unreachable` | `address` |
| 502 | `bmc_auth_failed` | `address` |
| 502 | `bmc_error` | `address` |
| 409 | `bmc_2fa_required` (kvm-session only) | `address` |
| 502 | `bmc_session_limit` (kvm-session only) | `address` |

Amendment (fix/kvm-redfish-login, 2026-09-28): the kvm-session start logs in
to the BMC web UI server-side (Redfish session login, else the legacy form
login), so its login failures answer `bmc_auth_failed`, `bmc_unreachable`,
`bmc_error` and the two KVM-only reasons above; a 2FA-required outcome is
audited `refused`, a session limit `error`. See README "KVM console".

The BMC is contacted only after Warden released the credentials. A power
action or KVM session writes an audit row (`power_action` /
`kvm_session_started`) with outcome `ok`, `refused` or `error` and the
reason. The address fallback to the primary IP is removed.

### POST/PUT /devices, PUT /devices/{id}

`ipmi_secret_ref` in the body may be omitted or equal to the stored value
(kept). Any other value → 422 `validation_failed` with
`detail.field = ipmi_secret_ref` (use `PUT /devices/{id}/bmc`). Device JSON
still returns `ipmi_secret_ref` (a pointer).

## Removed routes

`GET /api/ipam/v1/warden-secrets` and `GET /api/ipam/v1/warden-secrets/{id}`
(never functional; secrets are listed through Warden's API, see below).

## Warden user API used by the IPAM UI (existing, unchanged)

Called by the IPAM UI remote with the shell session through the gateway:

- `GET /api/warden/v1/secrets/search?q=<1..200>&limit=20` — `secrets:read`
- `GET /api/warden/v1/secrets?root=true&limit=20` — `secrets:read`

Only `id`, `name`, `username`, `folder_path` are displayed. The picker never
calls `/password`.
