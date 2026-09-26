# Contract: IPAM HTTP API changes (020)

`api/openapi/ipam.yaml` (embedded; routes and permissions registered with the
gateway through `pkg/ipammanifest`). Every write carries the existing CSRF
parameter; bodies have `x-freya-max-body-bytes`; permission refs are
module-scoped in auth (`ipam:<resource>:<action>`, feature 019).

## New permission

```go
{Resource: "hostsync", Action: "manage", Description: "Enable or disable the host sync and edit its interface exclusions"}
```
Grants: `owner`, `admin` (via `PermissionRefs()`); module role
`administrator` (via `PermissionRefs()`); not `operator`, `member`,
`auditor`, nor module roles operator/viewer. CASL:
`{Action: ["manage"], Subject: ["HostSync"], Requires: "hostsync:manage"}`.
Nav: `{Title: "Host sync", Path: "/ipam/host-sync", Icon: "mdi-sync", Order: 765, Requires: "ipam:read"}`.

## Endpoints

| Method | Path | Permission | Body limit | Result |
|---|---|---|---|---|
| GET | `/api/ipam/v1/host-sync/settings` | `ipam:read` | — | `HostSyncSettings` |
| PUT | `/api/ipam/v1/host-sync/settings` | `hostsync:manage` | 16 KiB | `HostSyncSettings` |
| GET | `/api/ipam/v1/host-sync/status` | `ipam:read` | — | `HostSyncStatus` |
| POST | `/api/ipam/v1/host-sync/resync` | `devices:manage` | 1 KiB | 202 `{"scheduled": true}` |
| GET | `/api/ipam/v1/devices/{id}/host-sync` | `ipam:read` | — | `DeviceHostSync` |
| POST | `/api/ipam/v1/devices/{id}/host-sync` | `devices:manage` | 1 KiB | 200 `ResyncResult` |
| GET | `/api/ipam/v1/devices/{id}/guests` | `ipam:read` | — | `{items: HypervisorGuest[]}` |
| POST | `/api/ipam/v1/ip-addresses/{id}/clear-conflict` | `addresses:manage` | 1 KiB | `IPAddress` |

Existing list endpoints gain filters: `GET /devices?source=host_report&report_state=not_reported`,
`GET /ip-addresses?conflict=true&report_state=…`.

## Schemas

```yaml
HostSyncSettings:
  type: object
  additionalProperties: false
  required: [enabled, full_interval_minutes, excluded_interfaces]
  properties:
    enabled: { type: boolean }
    full_interval_minutes: { type: integer, minimum: 15, maximum: 1440 }
    excluded_interfaces:
      type: array
      maxItems: 64
      items: { type: string, pattern: '^[A-Za-z0-9*?._:-]{1,64}$' }
    updated_by: { type: string, readOnly: true }
    updated_at: { type: string, format: date-time, readOnly: true }

HostSyncStatus:
  type: object
  properties:
    enabled: { type: boolean }
    state: { type: string, enum: [ok, degraded, disabled] }
    last_error: { type: string, description: 'code only, e.g. inventory_unavailable' }
    last_poll_at: { type: string, format: date-time }
    last_reconcile_at: { type: string, format: date-time }
    next_reconcile_at: { type: string, format: date-time }
    hosts_reported: { type: integer }
    hosts_failed: { type: integer }
    devices_not_reported: { type: integer }
    addresses_in_conflict: { type: integer }

DeviceHostSync:
  type: object
  properties:
    source: { type: string, enum: [manual, scan, host_report] }
    inventory_host_id: { type: string, format: uuid }
    report_state: { type: string, enum: ['', reported, not_reported] }
    snapshot_id: { type: string }
    collected_at: { type: string, format: date-time }
    applied_at: { type: string, format: date-time }
    trigger: { type: string }
    changes: { type: integer }
    issues:
      type: array
      maxItems: 50
      items: { type: object, properties: { field: {type: string}, reason: {type: string}, count: {type: integer} } }

ResyncResult:
  type: object
  properties:
    applied: { type: boolean }
    changes: { type: integer }
    issues: { $ref: '#/components/schemas/DeviceHostSync/properties/issues' }

HypervisorGuest:
  type: object
  properties:
    guest_ref: { type: string }
    name: { type: string }
    kind: { type: string, enum: [vm, container] }
    platform: { type: string }
    macs: { type: array, items: { type: string } }
    guest_device_id: { type: string, format: uuid }
    guest_device_name: { type: string }
    last_reported_at: { type: string, format: date-time }
```

Additions to existing JSON (`internal/store/models.go` tags):

- `Device`: `source`, `inventory_host_id`, `virtualization_kind`,
  `hypervisor_device_id`, `update_status`, `report_state`, `last_report_at`,
  `guest_count` (computed).
- `DeviceInterface`: `report_state`; the existing link fields are populated
  by US5 (`link_source` `snmp_fdb`|`lldp`), plus computed
  `remote_device_name`.
- `IPAddress`: `report_state`, `previous_device_id`, `moved_at`, `conflict`.
- `Subnet`: `origin`.

## Errors

- `POST /devices/{id}/host-sync` on a device without `inventory_host_id` →
  409 `not_host_reported`; inventory unreachable → 503
  `temporarily_unavailable`; tenant sync disabled → 409 `host_sync_disabled`.
- `PUT /host-sync/settings` with an invalid pattern → 400 `validation`.
- Unknown fields in bodies → 400 (strict decode, as today).
