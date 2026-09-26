# Contract: ipam.v1 proto changes (020)

`sdk/api/proto/ipam/v1/ipam.proto` (this repo), released as ipam SDK
**`sdk/v4.1.0`** (additive; `buf breaking` against `sdk/v4.0.0`). No new
service and no new RPC: the host sync runs in-process and is operated over
HTTP; the mesh API only exposes the new state to other modules (e.g. dns).
`DeviceService.SyncPackages` stays `Unimplemented` (SR-004: no agent path).

```proto
message Device {
  // 1–32 unchanged
  string source = 33;                // "manual" | "scan" | "host_report"
  string inventory_host_id = 34;
  string virtualization_kind = 35;
  string hypervisor_device_id = 36;
  string update_status = 37;         // unknown|up_to_date|updates_available|unsupported|error
  string report_state = 38;          // "" | "reported" | "not_reported"
  int64 last_report_at = 39;         // unix seconds, 0 = never
  int64 guest_count = 40;            // computed
}

message DeviceInterface {
  // 1–18 unchanged
  string report_state = 19;
}

message IPAddress {
  // 1–22 unchanged
  string report_state = 23;
  string previous_device_id = 24;
  int64 moved_at = 25;               // unix seconds, 0 = never moved
  bool conflict = 26;
}

message Subnet {
  // 1–26 unchanged
  string origin = 27;                // "manual" | "host_sync"
}
```

Mappers `internal/grpcapi/mapper.go` map the new fields both ways; the
`Create`/`Update` RPCs ignore `source`, `inventory_host_id`, `report_state`,
`hypervisor_device_id`, `previous_device_id`, `moved_at`, `origin` from
callers (server-owned fields), which a contract test asserts.
