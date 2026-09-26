# Contract: IPAM HTTP — subnet SNMP credentials (021)

All routes behind the gateway; mutating routes require the `X-CSRF` header
(`#/components/parameters/csrf`). No response ever contains a community,
v3 user or password.

## GET /api/ipam/v1/subnets/{id}/snmp — `ipam:read`

200 `SubnetSNMPStatus`:

```json
{
  "own": {"version": 3, "security_level": "authPriv", "auth_protocol": "SHA256",
          "priv_protocol": "AES256", "weak": false,
          "updated_by": "u-1", "updated_at": "2026-09-27T10:00:00Z"},
  "effective": {"state": "own", "version": 3, "security_level": "authPriv",
                "source_subnet_id": "s-1", "source_name": "infra", "source_cidr": "10.1.111.0/24"}
}
```

`own` is null without own credentials; `effective.state` is `none`, `own` or
`inherited`. 404 `not_found`.

## PUT /api/ipam/v1/subnets/{id}/snmp — `subnets:manage`, body ≤ 4 KiB

Request `SubnetSNMPInput` (`additionalProperties: false`):

```json
{"version": 2, "community": "…"}
{"version": 3, "user": "…", "security_level": "authNoPriv", "auth_protocol": "SHA256", "auth_password": "…"}
{"version": 3, "user": "…", "security_level": "authPriv", "auth_protocol": "SHA256", "auth_password": "…",
 "priv_protocol": "AES256", "priv_password": "…"}
```

200 `SubnetSNMPStatus` (as GET). 400 `validation_failed` with
`detail.field`. 404 `not_found`. Audit `snmp_credentials_set` (none → own) or
`snmp_credentials_replaced`.

## DELETE /api/ipam/v1/subnets/{id}/snmp — `subnets:manage`

204; 404 `not_found` (subnet) — clearing a subnet without own credentials is
a no-op 204 without audit. Audit `snmp_credentials_cleared`.

## POST /api/ipam/v1/subnets/{id}/snmp/test — `scan:run`, body ≤ 1 KiB

Request `{"address": "10.1.112.20"}`. 200 `SNMPTestResult`:

```json
{"outcome": "ok", "sys_name": "sw-core-1", "sys_descr": "Cisco IOS …",
 "source_subnet_id": "s-1", "duration_ms": 142}
```

`outcome` ∈ `ok` `no_response` `auth_failed` `unknown_user` `privacy_failed`
`no_credentials` `credentials_unreadable` `error`. 400 `validation_failed`
(`detail.field = address`, address not in the subnet / network / broadcast).
429 `rate_limited` (> 10 tests per user per minute). Audit
`snmp_credentials_tested` (target, outcome).

## Changed responses

- `GET /subnets`, `GET /subnets/{id}`, `GET /subnets/tree`: each subnet gains
  read-only `snmp` = `effective` summary above; `snmp_version` = effective
  version (0 when none); `snmp_secret_ref` is never set.
- `POST/PUT /subnets`: `snmp_secret_ref`, `snmp_version`, `snmp` in the body
  are ignored.
- `GET /scans`, `GET /scans/{id}`: jobs gain `snmp_status`,
  `snmp_source_subnet_id`, `snmp_probed`, `snmp_no_answer`, `snmp_rejected`.

## gRPC

No proto change. `ipam.v1.Subnet.snmp_version` carries the effective
version; `snmp_secret_ref` is always empty.
