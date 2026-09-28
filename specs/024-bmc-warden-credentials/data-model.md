# Data Model: BMC Credentials from Warden (024)

No migration. Everything below is either an existing column, an audit row
or a computed (never stored) value.

## 1. Device BMC reference (existing column)

`ipam_devices.ipmi_secret_ref text NOT NULL DEFAULT ''` — the Warden secret
id (UUID) or empty. A pointer, never a secret.

| Rule | Where |
|---|---|
| Written only by `SetDeviceBMCRef` (dedicated route), together with its audit row, in one tenant transaction | repodb / memstore |
| Device create with a non-empty value → `ErrBMCRefReadOnly` | `devices.Service.Create` |
| Device update with a different non-empty value → `ErrBMCRefReadOnly`; empty or equal → stored value kept | `devices.Service.Update` |
| Host sync and scans never write it | existing (`repodb/hostsync.go:426`) |
| Backup export strips it; import ignores it; overwrite keeps the existing device's value | `backup` |
| Set only after Warden `Get` succeeded for the acting user | `bmc.Service.Set` |

Store method (repo contract):

```go
// SetDeviceBMCRef replaces the device's BMC reference and appends row in the
// same transaction; returns the previous reference. ErrNotFound when the
// device is not the tenant's.
SetDeviceBMCRef(ctx context.Context, tenantID, deviceID, ref string, row store.AuditRow) (previous string, err error)
```

## 2. BMC status (computed per viewer, `GET /devices/{id}/bmc`)

```go
type bmc.Status struct {
    Configured    bool         `json:"configured"`
    Reference     string       `json:"reference,omitempty"`      // Warden id
    Access        string       `json:"access,omitempty"`         // ok | forbidden | not_found | unavailable ("" when not configured)
    Secret        *SecretInfo  `json:"secret,omitempty"`         // only when access == ok
    Address       string       `json:"address,omitempty"`        // BMC address used
    AddressSource string       `json:"address_source,omitempty"` // management_ip | reported
    Ready         bool         `json:"ready"`                    // configured && address && access == ok
    Reason        string       `json:"reason,omitempty"`         // first blocking D7 reason when !ready
}
type bmc.SecretInfo struct {
    Name       string `json:"name"`
    Username   string `json:"username,omitempty"`
    FolderPath string `json:"folder_path,omitempty"`
}
```

Never contains a password, host URL credentials or TOTP data.

## 3. BMC address (computed)

```text
address = device.management_ip                                  (source management_ip)
       ?: first address A with A.device_id = device.id
              and A.interface_name in (bmc, bmc-2, bmc-3, …) in that order
              and A.report_state != not_reported
              IPv4 before IPv6                                   (source reported)
       ?: none → bmc_no_address
```

## 4. Credentials (transient, per action)

```go
type warden.Credentials struct { Username, Password, HostURL string }
```

Lives for one request. `String()`, `GoString()` and `LogValue()` print
`[REDACTED]`. Mapped to `ipmi.Creds{Username, Password, Protocol, Port}`:
protocol and port come from the secret's `host_url` when it is an
`ipmi://`, `lan://` (1.5) or `lanplus://` (2.0) URL with an optional port;
otherwise protocol auto and port 623. `kvm.Creds{Username, Password}`.

## 5. Failure reasons (closed set)

`bmc_not_configured`, `bmc_no_address`, `bmc_secret_forbidden`,
`bmc_secret_not_found`, `warden_unavailable`, `bmc_unreachable`,
`bmc_auth_failed`, `bmc_error` — see [contracts/ipam-http.md](contracts/ipam-http.md).

Sentinels: `bmc.ErrNotConfigured`, `bmc.ErrNoAddress`, `warden.ErrForbidden`,
`warden.ErrNoUserToken` (→ forbidden), `warden.ErrNotFound`, `warden.ErrPolicyDenied` (→ unavailable),
`warden.ErrUnavailable`, `ipmi.ErrUnreachable`, `ipmi.ErrAuthFailed`,
`kvm` login errors (→ auth failed / unreachable), anything else from the
BMC → `bmc_error`.

## 6. Audit rows (`ipam_audit_events`)

See [contracts/audit-events.md](contracts/audit-events.md). Three new event
types (`bmc_reference_set`, `bmc_reference_changed`,
`bmc_reference_cleared`); `power_action` and `kvm_session_started` start
being written.

## 7. UI types

```ts
interface BmcStatus { configured: boolean; reference?: string; access?: 'ok'|'forbidden'|'not_found'|'unavailable';
  secret?: { name: string; username?: string; folder_path?: string }; address?: string;
  address_source?: 'management_ip'|'reported'; ready: boolean; reason?: BmcReason }
interface WardenSecretItem { id: string; name: string; username?: string; folder_path?: string }  // from Warden's API
```
