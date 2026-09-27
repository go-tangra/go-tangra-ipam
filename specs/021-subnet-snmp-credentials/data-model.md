# Data Model: SNMP Credentials on Subnets (021)

## 1. Table `ipam_subnet_snmp` (migration 0006)

| Column | Type | Notes |
|---|---|---|
| `tenant_id` | text NOT NULL | RLS key |
| `subnet_id` | text NOT NULL | PK with tenant_id; FK → `ipam_subnets(id)` ON DELETE CASCADE |
| `version` | int NOT NULL | 2 (v2c) or 3 |
| `security_level` | text NOT NULL DEFAULT '' | v3: `authNoPriv` \| `authPriv`; v2c: '' |
| `auth_protocol` | text NOT NULL DEFAULT '' | v3: `MD5` `SHA` `SHA224` `SHA256` `SHA384` `SHA512` |
| `priv_protocol` | text NOT NULL DEFAULT '' | authPriv: `DES` `AES` `AES192` `AES256` |
| `sealed` | bytea NOT NULL | envelope blob of the secret JSON (§2), AD `snmp:<tenant>:<subnet>` |
| `updated_by` | text NOT NULL | actor id |
| `updated_at` | timestamptz NOT NULL | |

RLS: same tenant policy shape as migration 0003. CHECK constraints:
`version IN (2,3)`; v2c ⇒ level/protocols empty; v3 ⇒ level in set and
auth_protocol non-empty; authPriv ⇒ priv_protocol non-empty.

## 2. Sealed secret document (never stored in clear, never returned)

```json
{"community": "…"}                                         // v2c
{"user": "…", "auth_password": "…"}                        // v3 authNoPriv
{"user": "…", "auth_password": "…", "priv_password": "…"}  // v3 authPriv
```

## 3. Go types

- `store.SubnetSNMP` — row metadata + `Sealed []byte` (`json:"-"`).
- `store.SNMPSummary` (`json:"snmp,omitempty"` on `store.Subnet`, read-only,
  computed): `State` (`none`|`own`|`inherited`), `Version`, `SecurityLevel`,
  `Weak`, `SourceSubnetID`, `SourceName`, `SourceCIDR`, `UpdatedAt` (own).
- `snmpcred.Input` — request body: `Version`, `Community`, `User`,
  `SecurityLevel`, `AuthProtocol`, `AuthPassword`, `PrivProtocol`,
  `PrivPassword`. `Validate()` returns field-named errors.
- `snmp.Creds` — adds `SecurityLevel`.
- `snmp.Outcome` — `ok`, `no_response`, `auth_failed`, `unknown_user`,
  `privacy_failed`, `error`.

## 4. `ipam_scan_jobs` additions (migration 0006)

| Column | Type | Notes |
|---|---|---|
| `snmp_status` | text NOT NULL DEFAULT '' | `not_requested` `no_live_hosts` `no_credentials` `credentials_unreadable` `ran` |
| `snmp_source_subnet_id` | text NOT NULL DEFAULT '' | subnet whose credentials were used |
| `snmp_probed` | bigint NOT NULL DEFAULT 0 | |
| `snmp_no_answer` | bigint NOT NULL DEFAULT 0 | |
| `snmp_rejected` | bigint NOT NULL DEFAULT 0 | auth_failed + unknown_user + privacy_failed |

## 5. Validation rules (FR-003)

- version ∈ {2,3}.
- v2c: community 1–256 chars; no v3 fields.
- v3: user 1–256; level ∈ {authNoPriv, authPriv}; auth_protocol in set;
  auth_password 8–256; authPriv ⇒ priv_protocol in set and priv_password
  8–256; authNoPriv ⇒ no priv fields.
- Weak: MD5, SHA (SHA-1), DES → `weak: true` in the summary.

## 6. State transitions (per subnet)

`none → own (set)`; `own → own (replace)`; `own → none|inherited (clear)`;
`inherited ↔ none` follows ancestors (no write on the child).
