# Data Model: Hosts Reported by the Inventory Agent Populate IPAM (020)

Two repositories change: **inventory** (`go-tangra-inventory-v4`, agent
report + projection) and **ipam** (this repo, host sync). Proto text is in
[contracts/inventory-grpc.md](contracts/inventory-grpc.md) and
[contracts/ipam-grpc.md](contracts/ipam-grpc.md).

## 1. Inventory

### 1.1 Domain structs (`internal/store/models.go`)

```go
// NetIface (existing; Type/SpeedBps/Gateway/DHCP/Up now filled)
type NetIface struct {
    Name, MAC   string
    IPAddresses []string        // CIDR strings (kept for old consumers)
    Subnet, Gateway string
    DNS         []string
    DHCP        bool            // any IPv4 address dynamic
    SpeedBps    uint64
    Type        string          // kind: ethernet|wireless|bond|bridge|vlan|virtual|loopback|other
    Up          bool
    Addresses   []IfAddress     // NEW
    DefaultRoute bool           // NEW: carries a default route
    Master      string          // NEW: bond/bridge master
    VLANID      uint32          // NEW
}
type IfAddress struct {      // NEW
    Address      string // no prefix, netip canonical form
    PrefixLength uint32
    Family       string // ipv4|ipv6
    DHCP, Temporary, Deprecated bool
    Scope        string // global|site|link|host
}
type Virtualization struct { Role, Kind, Source string }         // role: physical|vm|container|unknown
type Bmc struct {
    Address string; PrefixLength uint32; Gateway string
    IPSource string                                                  // static|dhcp|bios|other|""
    VLANID uint32
    Ports []BmcPort                                                  // ≤ 8
}
type BmcPort struct { Channel uint32; MAC, Address string }
type HypervisorGuest struct { ID, Name, Kind, Platform string; MACs []string } // kind: vm|container; platform: proxmox
type UpdateState struct {
    PackageManager string                 // apt|dnf|yum|apk|pacman|""
    Status   string                       // unknown|up_to_date|updates_available|unsupported|error
    RebootRequired, AutomaticUpdates string // tristate: unknown|true|false
    SecurityClassified bool               // manager provides security info
    CheckedAt time.Time
    PendingCount, SecurityCount uint32
}
type CollectionLimits struct { Interfaces, Addresses, Guests, Packages, BmcPorts uint32 } // entries dropped

// Inventory gains:
PrimaryIPv4, PrimaryIPv6 string
Virtualization   Virtualization
Bmc              *Bmc           // nil = none/unreadable
HypervisorGuests []HypervisorGuest
UpdateState      UpdateState
Truncated        CollectionLimits
// OSInfo gains Family string (linux|windows); Program gains AvailableVersion string, SecurityUpdate bool
```

No credential-bearing field exists in any of these structs (SR-005).

### 1.2 Host report projection (`internal/hostreport`, NEW)

`Project(host store.Host, snap store.Snapshot) HostReport` — pure:

| HostReport field | Source |
|---|---|
| `tenant_id`, `host` (id, hostname, system_serial, manufacturer, model, os_name, os_version, status, last_seen) | `inventory_hosts` |
| `snapshot_id`, `collected_at`, `agent_version` | snapshot summary |
| `os_family` | `OSInfo.Family` (fallback: `windows` if os name contains "windows", else `linux`) |
| `network_interfaces` | `Inventory.Networks` (full NetIface incl. addresses) |
| `primary_ipv4`, `primary_ipv6`, `virtualization`, `bmc`, `hypervisor_guests`, `update_state`, `truncated` | same-named fields |
| `pending_updates` | `installed_programs` where `available_version != ""` → `{name, installed_version, available_version, security}` |
| `report_digest` | `sha256` of the deterministic marshal of every field above except `host.last_seen`, `snapshot_id`, `collected_at` |

### 1.3 Migration `internal/store/migrations/0005_host_reports.sql`

```sql
-- +goose Up
ALTER TABLE inventory_hosts
  ADD COLUMN report_digest     text NOT NULL DEFAULT '' CHECK (report_digest = '' OR report_digest ~ '^[0-9a-f]{64}$'),
  ADD COLUMN report_changed_at timestamptz;
CREATE INDEX hosts_report_changed        ON inventory_hosts (tenant_id, report_changed_at);
CREATE INDEX hosts_report_changed_global ON inventory_hosts (report_changed_at); -- ListReportTenants (system scope)
-- +goose Down
DROP INDEX IF EXISTS hosts_report_changed_global;
DROP INDEX IF EXISTS hosts_report_changed;
ALTER TABLE inventory_hosts DROP COLUMN IF EXISTS report_changed_at, DROP COLUMN IF EXISTS report_digest;
```

Ingest (`internal/snapshots/ingest.go`) after `InsertSnapshot`: compute the
projection digest; if it differs from `report_digest`, set both columns
(same transaction as the host upsert). Existing hosts start with `''`/NULL;
`ListHostReports` with `changed_since = 0` includes NULL rows and computes
the digest on read.

## 2. IPAM

### 2.1 Migration `internal/store/migrations/0004_host_sync.sql`

```sql
-- +goose Up
-- Devices: provenance, inventory link, virtualization, hypervisor, report state.
ALTER TABLE ipam_devices
  ADD COLUMN source               text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','scan','host_report')),
  ADD COLUMN inventory_host_id    uuid,
  ADD COLUMN virtualization_kind  text NOT NULL DEFAULT '' CHECK (char_length(virtualization_kind) <= 32),
  ADD COLUMN hypervisor_device_id uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  ADD COLUMN update_status        text NOT NULL DEFAULT 'unknown'
      CHECK (update_status IN ('unknown','up_to_date','updates_available','unsupported','error')),
  ADD COLUMN report_state         text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported')),
  ADD COLUMN last_report_at       timestamptz,
  ADD COLUMN report_digest        text NOT NULL DEFAULT '',
  ADD CONSTRAINT devices_hypervisor_not_self CHECK (hypervisor_device_id IS DISTINCT FROM id);
CREATE UNIQUE INDEX devices_inventory_host ON ipam_devices (tenant_id, inventory_host_id) WHERE inventory_host_id IS NOT NULL;
CREATE INDEX devices_hypervisor ON ipam_devices (tenant_id, hypervisor_device_id);
CREATE INDEX devices_source     ON ipam_devices (tenant_id, source);

-- Interfaces: reported / no longer reported.
ALTER TABLE ipam_device_interfaces
  ADD COLUMN report_state text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported'));

-- Addresses: report state, last move, conflict flag.
ALTER TABLE ipam_ip_addresses
  ADD COLUMN report_state       text NOT NULL DEFAULT '' CHECK (report_state IN ('','reported','not_reported')),
  ADD COLUMN previous_device_id uuid,          -- no FK: history survives device deletion
  ADD COLUMN moved_at           timestamptz,
  ADD COLUMN move_count         int  NOT NULL DEFAULT 0 CHECK (move_count >= 0),
  ADD COLUMN move_window_start  timestamptz,
  ADD COLUMN conflict           boolean NOT NULL DEFAULT false;
CREATE INDEX addresses_conflict ON ipam_ip_addresses (tenant_id) WHERE conflict;

-- Subnets: created by the host sync.
ALTER TABLE ipam_subnets
  ADD COLUMN origin text NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','host_sync'));

-- Guests reported by a hypervisor host (matched or not).
CREATE TABLE ipam_hypervisor_guests (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  host_device_id   uuid NOT NULL REFERENCES ipam_devices(id) ON DELETE CASCADE,
  guest_ref        text NOT NULL CHECK (char_length(guest_ref) BETWEEN 1 AND 64),
  name             text NOT NULL DEFAULT '' CHECK (char_length(name) <= 128),
  kind             text NOT NULL CHECK (kind IN ('vm','container')),
  platform         text NOT NULL DEFAULT 'proxmox' CHECK (char_length(platform) <= 32),
  macs             text[] NOT NULL DEFAULT '{}' CHECK (cardinality(macs) <= 32),
  guest_device_id  uuid REFERENCES ipam_devices(id) ON DELETE SET NULL,
  last_reported_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (host_device_id, guest_ref)
);
CREATE INDEX hypervisor_guests_host ON ipam_hypervisor_guests (tenant_id, host_device_id);
CREATE INDEX hypervisor_guests_macs ON ipam_hypervisor_guests USING gin (macs);

-- Per-tenant sync settings and state (one row per tenant).
CREATE TABLE ipam_hostsync_settings (
  tenant_id             uuid PRIMARY KEY,
  enabled               boolean NOT NULL DEFAULT true,
  full_interval_minutes int  NOT NULL DEFAULT 60 CHECK (full_interval_minutes BETWEEN 15 AND 1440),
  excluded_interfaces   text[] NOT NULL DEFAULT ARRAY['docker*','br-*','veth*','virbr*','cni*','flannel*','cali*',
                          'weave*','vxlan*','kube-*','cilium*','podman*','fwbr*','fwpr*','fwln*','tap*','vnet*']
                          CHECK (cardinality(excluded_interfaces) <= 64),
  changed_since         timestamptz,           -- watermark (inventory clock)
  last_poll_at          timestamptz,
  last_reconcile_at     timestamptz,
  reconcile_requested   boolean NOT NULL DEFAULT false, -- set by resync-all / re-enable
  status                text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','degraded','disabled')),
  last_error            text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 64), -- code only
  hosts_reported        int  NOT NULL DEFAULT 0,
  hosts_failed          int  NOT NULL DEFAULT 0,
  updated_by            text NOT NULL DEFAULT '',
  updated_at            timestamptz NOT NULL DEFAULT now()
);

-- Per-device last report outcome (issues shown to administrators).
CREATE TABLE ipam_hostsync_device_state (
  device_id         uuid PRIMARY KEY REFERENCES ipam_devices(id) ON DELETE CASCADE,
  tenant_id         uuid NOT NULL,
  inventory_host_id uuid NOT NULL,
  snapshot_id       text NOT NULL DEFAULT '' CHECK (char_length(snapshot_id) <= 64),
  collected_at      timestamptz,
  applied_at        timestamptz,
  trigger           text NOT NULL DEFAULT '' CHECK (char_length(trigger) <= 80),
  changes           int  NOT NULL DEFAULT 0,
  issues            jsonb NOT NULL DEFAULT '[]'::jsonb  -- ≤ 50 {field, reason, count}
);
CREATE INDEX hostsync_device_state_tenant ON ipam_hostsync_device_state (tenant_id);

-- RLS + grants for the new tables (same policy as 0003).
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['ipam_hypervisor_guests','ipam_hostsync_settings','ipam_hostsync_device_state']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on') WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid OR current_setting('app.system', true) = 'on')$p$, t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO ipam_app', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS ipam_hostsync_device_state;
DROP TABLE IF EXISTS ipam_hostsync_settings;
DROP TABLE IF EXISTS ipam_hypervisor_guests;
ALTER TABLE ipam_subnets DROP COLUMN IF EXISTS origin;
ALTER TABLE ipam_ip_addresses DROP COLUMN IF EXISTS conflict, DROP COLUMN IF EXISTS move_window_start,
  DROP COLUMN IF EXISTS move_count, DROP COLUMN IF EXISTS moved_at, DROP COLUMN IF EXISTS previous_device_id,
  DROP COLUMN IF EXISTS report_state;
ALTER TABLE ipam_device_interfaces DROP COLUMN IF EXISTS report_state;
ALTER TABLE ipam_devices DROP CONSTRAINT IF EXISTS devices_hypervisor_not_self,
  DROP COLUMN IF EXISTS report_digest, DROP COLUMN IF EXISTS last_report_at, DROP COLUMN IF EXISTS report_state,
  DROP COLUMN IF EXISTS update_status, DROP COLUMN IF EXISTS hypervisor_device_id,
  DROP COLUMN IF EXISTS virtualization_kind, DROP COLUMN IF EXISTS inventory_host_id, DROP COLUMN IF EXISTS source;
```

Existing rows: devices `source='manual'` (the scan path sets `'scan'` from
now on, D9), empty report state, `update_status='unknown'`.

### 2.2 Migration `internal/store/migrations/0005_interface_links_unique.sql` (US5)

```sql
-- +goose Up
-- 0001's UNIQUE (interface_id, remote_device_id, link_source) rejects a second
-- FDB MAC on the same switch port (all FDB rows have remote_device_id = '').
-- The generated constraint name exceeds 63 bytes and is truncated by
-- PostgreSQL, so it is looked up instead of spelled out.
-- +goose StatementBegin
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'ipam_device_interface_links'::regclass AND contype = 'u';
  IF c IS NOT NULL THEN
    EXECUTE format('ALTER TABLE ipam_device_interface_links DROP CONSTRAINT %I', c);
  END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ipam_device_interface_links
  ADD CONSTRAINT interface_links_uniq UNIQUE (interface_id, link_source, remote_device_id, remote_port_name);
CREATE INDEX interface_links_source ON ipam_device_interface_links (tenant_id, link_source);
-- +goose Down
DROP INDEX IF EXISTS interface_links_source;
ALTER TABLE ipam_device_interface_links DROP CONSTRAINT interface_links_uniq,
  ADD CONSTRAINT interface_links_iface_remote_source_key UNIQUE (interface_id, remote_device_id, link_source);
```

The migration test asserts exactly one unique constraint exists on the table
before the change. Guest ↔ hypervisor relations do not use link rows; they
live in `ipam_devices.hypervisor_device_id` (D13).

### 2.3 Go models (`internal/store/models.go`)

- `Device`: `Source`, `InventoryHostID`, `VirtualizationKind`,
  `HypervisorDeviceID`, `UpdateStatus`, `ReportState`, `LastReportAt`,
  `ReportDigest` (not in JSON), computed `GuestCount`.
- `DeviceInterface`: `ReportState`.
- `IPAddress`: `ReportState`, `PreviousDeviceID`, `MovedAt`, `MoveCount`
  (not in JSON), `MoveWindowStart` (not in JSON), `Conflict`.
- `Subnet`: `Origin`.
- NEW `HypervisorGuest`, `HostSyncSettings`, `HostSyncStatus`,
  `HostSyncDeviceState`, `HostSyncIssue{Field, Reason string; Count int}`.
- Constants: `SrcManual|SrcScan|SrcHostReport`, `RepReported|RepNotReported`,
  `UpdUnknown|UpdUpToDate|UpdAvailable|UpdUnsupported|UpdError`,
  `OriginManual|OriginHostSync`, `DevInterfaceKindManagement = "management"`,
  `LinkMACTable = "snmp_fdb"` (existing), `LinkLLDP` (existing).

### 2.4 Repo contract (`internal/repo/repo.go`)

New sub-interface `repo.HostSyncStore` (implemented by `repodb.DB` and
`memstore.Mem`; `repo.Store` embeds it):

```go
// Settings (tenant scope; List* is system scope).
EnsureHostSyncSettings(ctx, tenantID string) (store.HostSyncSettings, error) // insert defaults if absent
GetHostSyncSettings(ctx, tenantID string) (store.HostSyncSettings, error)
UpdateHostSyncSettings(ctx, s store.HostSyncSettings, audit store.AuditRow) error // same tx
ListHostSyncSettings(ctx) ([]store.HostSyncSettings, error)                    // system scope
SaveHostSyncState(ctx, tenantID string, st store.HostSyncStatus) error

// Apply one host in one transaction: advisory lock, enabled check (FOR SHARE),
// load → callback plans → writes + audit rows. Returns ErrSyncDisabled when disabled.
ApplyHostReport(ctx, tenantID string, fn func(tx HostTx) error) error
```

`HostTx` exposes only what the planner's ops need, all bound to the
transaction and tenant: `FindDeviceForHost(hostID, serial, name)`,
`LoadDeviceState(deviceID)` (device, interfaces, reported addresses,
packages, guests, device state), `FindAddresses(addrs []string)`,
`SubnetCIDRs()`, `FindInterfacesByMAC(macs)`, `FindGuestsByMAC(macs)`,
`InsertDevice`, `UpdateDeviceReported(d, cols)` (fixed column list, D8),
`UpsertInterfaceReported`, `MarkInterfaces`, `CreateSubnetAuto`,
`UpsertAddressReported`, `ReleaseAddresses`, `ReplacePendingPackages`,
`ReplaceGuests`, `SetHypervisor`, `SaveDeviceState`, `AppendAudit`.

## 3. Entities and state transitions

### Device (report_state)

```
''(manual/scan) ──first matching report──▶ reported
reported ──host retired/deleted in inventory (reconcile)──▶ not_reported
not_reported ──new report for the same inventory host──▶ reported
```
`source` becomes `host_report` on first match and never reverts
automatically (an administrator may unlink a device — out of scope; the
inventory host id stays authoritative).

### Interface (report_state)

`'' → reported` (created or matched by name) · `reported → not_reported`
(absent from a report) · `not_reported → reported` (re-appears).

### Address (report_state / device ownership)

```
absent ──reported──▶ reported(device=D)            audit address_created
reported(D') ──reported by D──▶ reported(D)         audit address_moved{previous_device_id: D'}
unowned (device NULL) ──reported by D──▶ reported(D) audit address_updated (claim)
reported(D) ──absent from D's report──▶ not_reported(device NULL, previous_device_id D)  audit address_released
reported(D) ──D's host gone──▶ not_reported(device D kept)                               audit address_updated
moves ≥ conflict_moves within conflict_window ──▶ conflict=true  audit address_conflict
conflict ──window without moves / admin clear──▶ conflict=false  audit address_conflict_cleared
```

### Hypervisor guest

`reported(unmatched)` → `matched(guest_device_id)` when a device interface
with one of its MACs exists (either report order) → row removed when the
host stops reporting it (guest device's `hypervisor_device_id` cleared).

### Sync settings/status

`enabled=true, status ok` ⇄ `status degraded` (inventory unavailable,
`last_error` code, back-off) · `enabled=false → status disabled` (no apply
commits after the update commits) · re-enable → `reconcile_requested=true`.

## 4. Validation rules and limits (IPAM `internal/hostreport`, D15)

| Item | Rule | Limit |
|---|---|---|
| tenant | report tenant == requested tenant, uuid | reject report |
| host id | uuid | reject report |
| hostname | printable UTF-8, no control chars, trimmed | ≤ 253 bytes |
| interface name | same charset | ≤ 64 bytes, ≤ 256 interfaces |
| MAC | 6 bytes → `aa:bb:cc:dd:ee:ff`; drop zero/broadcast/multicast | — |
| address | `netip.ParseAddr`; prefix 0–32 / 0–128; classification D10 | ≤ 64 per interface, ≤ 1024 total |
| kind | closed set, else `other` | — |
| speed | ≤ 1 Tbit/s → `speed_mbps` | — |
| virtualization role/kind | closed set / `[a-z0-9-]{1,32}` | — |
| BMC | address/gateway parse, prefix 1–32, ports | ≤ 8 ports |
| guest | ref `[A-Za-z0-9._-]{1,64}`, name ≤ 128, kind vm/container, MACs | ≤ 1000 guests, ≤ 32 MACs each |
| package | name ≤ 256 printable, versions ≤ 128 | ≤ 5000 |
| exclusion pattern (settings) | `path.Match` valid, `[A-Za-z0-9*?._:-]{1,64}` | ≤ 64 |

Skipped entries become issues `{field, reason, count}`; truncation counters
from the agent/inventory are added as issues `truncated_<list>`.

## 5. Configuration

IPAM (`internal/config/config.go`, YAML `host_sync`):

```yaml
host_sync:
  enabled: true                 # global kill switch
  inventory_service: inventory
  poll_interval_seconds: 60     # 10–3600
  workers: 2                    # 1–8 tenants in parallel
  page_size: 100                # 1–200
  pace_ms: 10                   # pause between hosts
  request_timeout_seconds: 30
  conflict_moves: 3             # 2–100
  conflict_window_hours: 24     # 1–168
  max_macs_per_port: 16         # US5, 1–256
  link_stale_days: 14           # US5
```

Inventory (`internal/config/config.go`): `host_reports: {consumers:
["ipam"], max_page_bytes: 3145728}`; agent `AgentConfig`:
`collect_bmc: true`, `collect_updates: true`, `refresh_package_lists:
false`, `update_timeout_seconds: 120` (30–600).
