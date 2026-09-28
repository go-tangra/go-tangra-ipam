# Contract: audit events (024)

Rows in `ipam_audit_events`, actor kind `user`, actor id = the user. Detail
keys avoid the guarded substrings (`secret`, `credential`, `ipmi`,
`password`, …) which the audit guard would drop. No row ever carries a
password or a token.

| Event type | Subject | When | Outcome / reason | Detail |
|---|---|---|---|---|
| `bmc_reference_set` | `device`, device id | PUT …/bmc on a device without a reference | `ok` | `reference`, `reference_name` |
| `bmc_reference_changed` | `device`, device id | PUT …/bmc replacing a different reference | `ok` | `reference`, `previous_reference`, `reference_name` |
| `bmc_reference_cleared` | `device`, device id | DELETE …/bmc removed a reference | `ok` | `previous_reference` |
| `power_action` | `power`, device id | POST …/power | `ok`; `refused` (reason: `bmc_not_configured`, `bmc_no_address`, `bmc_secret_forbidden`, `bmc_secret_not_found`); `error` (reason: `warden_unavailable`, `bmc_unreachable`, `bmc_auth_failed`, `bmc_error`, `bad_request`) | `action`; `target` = BMC address when known |
| `kvm_session_started` | `kvm`, device id | POST …/kvm-session | as `power_action` | `target` = BMC address when known |

Reference rows are written in the same transaction as the column change.
Power/KVM rows are appended after the outcome is known. Power status,
sensors and SEL reads are logged to the module log (`ipam oob operation`,
action, actor, tenant, device, request id) — not audit rows. Warden writes
its own `secret_read` / `secret_password_read` rows for every call.
