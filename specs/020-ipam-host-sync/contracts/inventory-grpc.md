# Contract: inventory.v1 proto changes (020)

Repository go-tangra-inventory-v4, file
`sdk/api/proto/inventory/v1/inventory.proto`, released as inventory SDK
**`sdk/v4.1.0`**. Additive only: `buf breaking --against` the `sdk/v4.0.0`
tag must pass; `buf lint` with the repository's existing exceptions.

## Existing messages — new or newly filled fields

```proto
message OSInfo {
  // 1–8 unchanged
  string family = 9; // "linux" | "windows"
}

message Program {
  // 1–6 unchanged
  string available_version = 7; // newer version available from the package manager; "" = none/unknown
  bool security_update = 8;     // the available version is a security update
}

message NetworkInterface {
  string name = 1;
  string mac = 2;
  repeated string ip_addresses = 3; // CIDR strings, kept for old consumers
  string subnet = 4;
  string gateway = 5;               // NOW FILLED: default gateway via this interface
  repeated string dns = 6;
  bool dhcp = 7;                    // NOW FILLED: any IPv4 address is dynamic (DHCP)
  uint64 speed_bps = 8;             // NOW FILLED where known, 0 = unknown
  string type = 9;                  // NOW FILLED: kind ethernet|wireless|bond|bridge|vlan|virtual|loopback|other
  bool up = 10;
  repeated InterfaceAddress addresses = 11; // ≤ 64
  bool default_route = 12;          // this interface carries a default route
  string master = 13;               // bond or bridge this interface is enslaved to
  uint32 vlan_id = 14;              // for kind vlan
}

message InterfaceAddress {
  string address = 1;        // canonical text, no prefix
  uint32 prefix_length = 2;
  string family = 3;         // "ipv4" | "ipv6"
  bool dhcp = 4;             // dynamically assigned (DHCP/DHCPv6/SLAAC)
  bool temporary = 5;        // IPv6 privacy address
  bool deprecated = 6;       // IPv6 preferred lifetime expired
  string scope = 7;          // "global" | "site" | "link" | "host"
}

message Inventory {
  // 1–23 unchanged
  string primary_ipv4 = 24;  // first global IPv4 of the lowest-metric default-route interface
  string primary_ipv6 = 25;
  Virtualization virtualization = 26;
  Bmc bmc = 27;              // absent = no BMC or not readable
  repeated HypervisorGuest hypervisor_guests = 28; // ≤ 1000
  UpdateState update_state = 29;
  CollectionLimits truncated = 30;
}

message Virtualization {
  string role = 1;   // "physical" | "vm" | "container" | "unknown"
  string kind = 2;   // kvm|vmware|hyperv|xen|virtualbox|lxc|docker|podman|wsl|aws|gce|… ("" for physical)
  string source = 3; // detection evidence label, e.g. "dmi", "cgroup", "hypervisor-type"
}

// Out-of-band controller. There is deliberately NO credential, user, cipher
// or community-string field (SR-005).
message Bmc {
  string address = 1;
  uint32 prefix_length = 2;
  string gateway = 3;
  string ip_source = 4;         // "static" | "dhcp" | "bios" | "other" | ""
  uint32 vlan_id = 5;
  repeated BmcPort ports = 6;   // ≤ 8
}
message BmcPort {
  uint32 channel = 1;
  string mac = 2;
  string address = 3;
}

message HypervisorGuest {
  string id = 1;                // VMID
  string name = 2;
  string kind = 3;              // "vm" | "container"
  string platform = 4;          // "proxmox"
  repeated string macs = 5;     // ≤ 32
}

message UpdateState {
  string package_manager = 1;   // apt|dnf|yum|apk|pacman|""
  string status = 2;            // unknown|up_to_date|updates_available|unsupported|error
  string reboot_required = 3;   // "unknown" | "true" | "false"
  string automatic_updates = 4; // "unknown" | "true" | "false"
  bool security_classified = 5; // manager reports security updates
  int64 checked_at = 6;         // unix seconds
  uint32 pending_count = 7;
  uint32 security_count = 8;
}

message CollectionLimits {      // entries dropped because a bound was reached
  uint32 interfaces = 1;
  uint32 addresses = 2;
  uint32 guests = 3;
  uint32 packages = 4;
  uint32 bmc_ports = 5;
}
```

Old agents: every new field is its zero value; consumers treat `""` roles
and statuses as `unknown`.

## New service `HostReportService`

Mesh only (not proxied by the gateway, no manifest `Methods`). Callers
authorised by inventory `deploy/policy.yaml` rule `ipam-hostsync`.

```proto
service HostReportService {
  // Tenants having at least one host whose report changed after
  // changed_since (0 = every tenant with hosts). System scope: the handler
  // additionally requires the caller's service name to be listed in
  // host_reports.consumers (default ["ipam"]) → PermissionDenied otherwise.
  rpc ListReportTenants(ListReportTenantsRequest) returns (ListReportTenantsResponse);
  // Host reports of one tenant, ordered by (report_changed_at, host id).
  rpc ListHostReports(ListHostReportsRequest) returns (ListHostReportsResponse);
  // Latest report of one host.
  rpc GetHostReport(GetHostReportRequest) returns (HostReport);
}

message ListReportTenantsRequest {
  int64 changed_since = 1;           // unix milliseconds (inventory clock)
}
message ListReportTenantsResponse {
  repeated string tenant_ids = 1;    // ≤ 10000
  int64 max_changed_at = 2;          // unix ms, next watermark
}

enum HostReportView {
  HOST_REPORT_VIEW_UNSPECIFIED = 0;  // = FULL
  HOST_REPORT_VIEW_FULL = 1;
  HOST_REPORT_VIEW_DIGEST = 2;       // host, status, report_digest, report_changed_at only
}

message ListHostReportsRequest {
  string tenant_id = 1;              // uuid, required
  int64 changed_since = 2;           // unix ms; 0 = all hosts (incl. retired, flagged by status)
  HostReportView view = 3;
  int32 limit = 4;                   // 1–200, default 100
  string cursor = 5;                 // opaque, from next_cursor
}
message ListHostReportsResponse {
  repeated HostReport reports = 1;   // page also bounded by host_reports.max_page_bytes (3 MiB)
  string next_cursor = 2;            // "" = last page
}

message GetHostReportRequest {
  string tenant_id = 1;
  string host_id = 2;
}

message HostReport {
  string tenant_id = 1;
  Host host = 2;                     // existing Host message (id, hostname, system_serial, status, …)
  string snapshot_id = 3;
  int64 collected_at = 4;            // unix seconds
  int64 report_changed_at = 5;       // unix ms
  string report_digest = 6;          // hex sha256 of the projection
  string agent_version = 7;
  string os_family = 8;
  repeated NetworkInterface network_interfaces = 9;
  string primary_ipv4 = 10;
  string primary_ipv6 = 11;
  Virtualization virtualization = 12;
  Bmc bmc = 13;
  repeated HypervisorGuest hypervisor_guests = 14;
  UpdateState update_state = 15;
  repeated PendingUpdate pending_updates = 16; // ≤ 5000
  CollectionLimits truncated = 17;
}

message PendingUpdate {
  string name = 1;
  string installed_version = 2;
  string available_version = 3;
  bool security = 4;
}
```

Errors: `InvalidArgument` (tenant/host not a uuid, limit out of range, bad
cursor), `NotFound` (host or its snapshot missing — snapshot retention),
`PermissionDenied` (`ListReportTenants` from a non-consumer),
`Unauthenticated` (no SPIFFE peer). A `DIGEST` view never carries interface,
package or guest data.

## SDK (`sdk/pkg/inventoryclient`)

```go
func (c *Client) ListReportTenants(ctx context.Context, changedSince time.Time) (ids []string, maxChanged time.Time, err error)
func (c *Client) ListHostReports(ctx context.Context, tenantID string, f ReportFilter) (reports []HostReport, next string, err error)
func (c *Client) GetHostReport(ctx context.Context, tenantID, hostID string) (HostReport, error)

type ReportFilter struct { ChangedSince time.Time; Digest bool; Limit int; Cursor string }
type HostReport struct { TenantID string; Host Host; SnapshotID string; CollectedAt, ChangedAt time.Time;
  Digest, AgentVersion, OSFamily string; Interfaces []NetworkInterface; PrimaryIPv4, PrimaryIPv6 string;
  Virtualization Virtualization; BMC *BMC; Guests []HypervisorGuest; Updates UpdateState;
  PendingUpdates []PendingUpdate; Truncated CollectionLimits }
```

`inventory.go` `toInventory` maps the new `Inventory` fields for existing
`GetLatestSnapshot` callers.

## Change history

`internal/diff/diff.go` new categories `virtualization`, `bmc`, `guest`
(keyed by guest id), `update` (update state); `network` compares `type`,
`speed_bps`, `gateway`, `dhcp`, `addresses`; `software` compares
`available_version`/`security_update`. Exposed through the existing
`ListChanges`/`DiffSnapshots` (FR-007).
