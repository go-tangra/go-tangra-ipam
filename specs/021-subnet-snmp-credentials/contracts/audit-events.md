# Contract: audit events (021)

Written as `ipam_audit_events` rows in the same transaction as the change
(tests: after the probe). Actor kind `user`. Subject kind `subnet`, subject
id = subnet id. Detail keys avoid the guarded substrings (`snmp`,
`credential`, `secret`, `password`), which the audit guard would drop.

| Event type | When | Detail |
|---|---|---|
| `snmp_credentials_set` | PUT on a subnet without own credentials | `protocol_version`, `security_level` |
| `snmp_credentials_replaced` | PUT on a subnet with own credentials | `protocol_version`, `security_level`, `previous_version` |
| `snmp_credentials_cleared` | DELETE removed own credentials | `previous_version` |
| `snmp_credentials_tested` | POST …/snmp/test | `target`, `outcome`, `source_subnet_id` |

Outcome of the row: `ok` for set/replace/clear; for tests `ok` when the
probe answered, else `error` (the category is in `detail.outcome`).
