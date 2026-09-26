# Research: Hosts Reported by the Inventory Agent Populate IPAM (020)

Evidence from go-tangra-ipam-v4 (branch `020-ipam-host-sync`, service v4.2.0,
ipam SDK `sdk/v4.0.0`), go-tangra-inventory-v4 `main` (service v4.2.0,
inventory SDK `sdk/v4.0.0`), go-tangra-asset-v4 (inventory sync pattern),
go-tangra-dns-v4 and go-tangra-deployer-v4 (event consumers), the go-tangra
framework (`github.com/go-tangra/go-tangra/v4`), go-tangra-docker
(`policies/*.yaml`), and the v3 sources go-tangra-client and go-tangra-ipam.

## Current state

### Inventory agent and module (go-tangra-inventory-v4)

- Agent: `cmd/inventory-agent/main.go` (one-shot, `-daemon`, Windows service);
  `internal/daemon/daemon.go` submits a **full snapshot every
  `interval_seconds` (default 3600, range 60–604800)** plus on a
  `COMMAND_TYPE_REFRESH` pushed over `IngestService/StreamCommands`. No
  snapshot hash/dedup is implemented (the `state_file` comment mentions one,
  `internal/config/config.go:349`). Agent config is `AgentConfig`
  (`config.go:341-419`, YAML `KnownFields(true)`).
- Collectors (`internal/collector`, all best effort, never fail):
  `collector.go` orders identity → SMBIOS hardware (`siderolabs/go-smbios`)
  → OS/networks/disks (gopsutil v4) → programs/services/users/monitors.
  `collectNetworks` (`osinfo.go:44-63`) only fills `Name`, `MAC`, `Up` and
  `IPAddresses` (CIDR strings from gopsutil) — no bound, no context.
  Linux programs: `dpkg-query`/`rpm -qa` with a 20 s timeout
  (`software_linux.go`); Windows: registry + PowerShell (`software_windows.go`).
- **Absent today**: interface kind/speed/gateway/DHCP (the proto and the
  `inventory_network_interfaces` table already have `subnet`, `gateway`,
  `dns`, `dhcp`, `speed_bps`, `type`, `up` — never filled), primary address,
  virtualization, BMC, Proxmox guests, reboot-required, automatic updates,
  available package versions. `Patch` (Windows hotfixes) exists in the proto
  but is never collected.
- Proto: single file `sdk/api/proto/inventory/v1/inventory.proto` (package
  `inventory.v1`, go_package `…/go-tangra-inventory/sdk/v4/api/proto/inventory/v1`).
  Highest field numbers: `Inventory` 23, `NetworkInterface` 10, `OSInfo` 8,
  `Program` 6, `Host` 21. Services: `IngestService` (Enroll, SubmitInventory,
  StreamCommands), `InventoryHostService` (ListHosts, GetHost,
  GetHostByIdentity, TagHost, RetireHost, DeleteHost),
  `InventorySnapshotService` (GetSnapshot, ListSnapshots, GetLatestByHost,
  DiffSnapshots, ListChanges, DeleteSnapshot), `InventoryStatisticsService`,
  `InventoryAgentService`. No `google.api.http`; REST is hand-written
  (`api/openapi/inventory.yaml`). buf v2 with STANDARD lint minus the
  platform naming exceptions; `make generate`.
- Three mappers must stay in step for any new field: agent
  `internal/sender/mapper.go` (store → proto), edge
  `internal/ingest/ingest.go:202-384` (`inventoryFromProto`), mesh
  `internal/grpcapi/mapper.go` (store → proto), plus the SDK
  `sdk/pkg/inventoryclient/inventory.go` (`toInventory`).
- Ingest edge (`:9977`, `internal/ingest/auth.go`): per-agent credential in
  metadata, tenant always from the verified agent; only a total size bound
  (`limits_inventory.max_snapshot_bytes`, default 8 MiB, `ingest.go:113`), **no
  per-collection count or string-length limits**.
- Storage: `inventory_hosts` (identity precedence hardware_uuid > machine_id >
  hostname; `last_seen` bumped by every snapshot; index
  `hosts_last_seen (tenant_id, last_seen)`), `inventory_snapshots` hypertable
  with authoritative `payload jsonb`, normalised component tables (0003),
  RLS on all tables (0004, `tenant_isolation` with `app.system`). Next
  migration: **0005**. Change history: `internal/diff/diff.go` (network keyed
  by MAC, software by name+version).
- Events: `inventory.snapshot.received`, `inventory.host.changed`
  (`{host_id, change_count}`), agent online/offline, XADD to the per-tenant
  Valkey stream `platform:events:<tenant>` (`internal/stream/hub.go:58`),
  content-free, for the browser SSE relay.
- Mesh API tenant handling: `internal/grpcapi/server.go:32-52` `caller()`
  requires a SPIFFE peer and a uuid `tenant_id` **request field**; which
  service may call which RPC is decided only by `deploy/policy.yaml`
  (inbound rules on the callee: `gateway-forwards` `*`, `asset-sync`
  ListHosts/GetHost/Health). No RPC lists tenants.
- SDK `sdk/pkg/inventoryclient`: ListHosts (paged by cursor), GetHost,
  GetLatestSnapshot, DiffSnapshots, GetStatistics, RefreshInventory,
  MintEnrollmentToken. Only tag `sdk/v4.0.0`; asset pins it.
- Coverage: `scripts/coverage-gate.sh` ≥ 80 % total, 100 % for
  `internal/authz`, `internal/sealed`, `internal/enroll`; COVERPKG excludes
  `internal/collector`, `internal/sender`, `internal/daemon`,
  `internal/winsvc`. Fuzz: `FuzzEnrollToken`, `FuzzSubmitMapper`, `FuzzDiff`.
  CI (`.github/workflows/ci.yaml`) runs vet/race tests (service + sdk), UI,
  buf lint, an agent cross-compile matrix (linux/windows × amd64/arm64,
  `CGO_ENABLED=0`); the cover gate is Makefile-only.
- Direct deps include gopsutil v4, go-smbios, `golang.org/x/sys`; **no IPMI
  and no netlink library**.

### IPAM module (this repo)

- Schema `internal/store/migrations/0001_schema.sql`: `ipam_devices`
  (UNIQUE `(tenant_id, name)`; already has `primary_ip`, `primary_ipv6`,
  `management_ip`, `os_type`, `os_version`, `serial_number`,
  `reboot_required`, `unattended_upgrades`, `last_seen`),
  `ipam_device_interfaces` (UNIQUE `(device_id, name)`, flat link columns
  `remote_device_id/remote_interface_id/remote_port_name/link_source/link_vlan/link_last_seen`),
  `ipam_device_interface_links` (UNIQUE `(interface_id, remote_device_id,
  link_source)`), `ipam_ip_addresses` (UNIQUE `(tenant_id, address)` —
  one address per tenant, no VRF), `ipam_subnets` (UNIQUE `(tenant_id,
  name)`), `ipam_device_packages` (UNIQUE `(tenant_id, device_id, name)`,
  `current_version/available_version/needs_update/is_security_update/package_manager`).
  0002 scan queue + `ipam_audit_events` hypertable; 0003 RLS on every
  table (`app.tenant_id` / `app.system`). Next migration: **0004**.
- Repo contract `internal/repo/repo.go` (tenant-scoped; system scope only for
  scan claiming, audit and `TenantIDs`). `repodb.DB.tenant/system`
  (`internal/repo/repodb/db.go:36-41`). `TenantIDs` is derived from IPAM's own
  rows (`db.go:2072`) — a tenant with inventory hosts but no IPAM data is
  invisible to it.
- Scan path (`internal/scan/executor.go`): `persistDevice` →
  `UpsertDeviceByName` (merges and **overwrites** `management_ip` with the
  scanned IP, `device_type`, `os_version`), `UpsertInterfaceByName`,
  `ReplaceInterfaceLinks`. SNMP (`internal/scan/snmp/snmp.go`) stores bridge
  FDB entries as links on the **switch** interface with
  `remote_port_name = <learned MAC>`, `link_source = snmp_fdb`, `link_vlan`;
  LLDP as `remote_port_name = "<sysName> <portId>"`, `link_source = lldp`.
- `internal/ipnet` (100 % gate): Parse, Hosts, FirstFree, BulkFree, Overlaps,
  Within, Subdivide, Contains, GatewayInRange, InRange — **no
  longest-prefix (most specific) helper and no network-of-address helper**.
- Audit: `internal/audit/audit.go` defines a closed vocabulary and an async
  buffered `Writer` whose detail guard drops keys containing `secret`,
  `credential`, `snmp`, `ipmi`, `password`, `owner`, `contact`. **The Writer
  is not wired anywhere** (`grep` finds no `NewWriter`/`Record` call outside
  tests); only `httpapi/power.go` logs OOB actions to the module log.
- Events: `internal/events` publishes `ipam.ip_address.{created,updated,
  deleted,scanned}` and `ipam.scan.*` to `platform:events:<tenant>`; the dns
  module consumes the address events (`go-tangra-dns-v4/internal/ipamsync`).
- Devices API: `DeviceService.SyncPackages` returns Unimplemented on the mesh
  (`internal/grpcapi/servers.go:465`); HTTP `POST
  /api/ipam/v1/devices/{id}/packages/sync` (`internal/httpapi/handlers.go:521`,
  `devices:manage`) replaces packages from a JSON body.
- Permissions (`pkg/ipammanifest/manifest.go`): 13 (`ipam:read`,
  `devices:manage`, …), module roles administrator/operator/viewer (feature
  019, permissions module-scoped as `ipam:<res>:<act>` in auth).
- Policy `deploy/policy.yaml`: inbound only (`gateway-forwards`,
  `dns-ipam-sync`).
- Coverage gate `scripts/coverage-gate.sh`: ≥ 80 %, 100 % for
  `internal/authz`, `internal/sealed`, `internal/ipnet`. Fuzz:
  `FuzzParseAllocate`, `FuzzScanTargets`, `FuzzMembership`. Integration tests
  carry `//go:build integration` next to the code
  (`internal/repo/repodb/integration_test.go`).
- UI (`ui/`, Vue 3 + `@go-tangra/ui`, vitest): `views/devices/detail.vue` has
  tabs interfaces/packages/addresses/oob; interface column "Neighbor" shows
  `remote_port_name`.

### Pattern to reuse: asset → inventory (go-tangra-asset-v4)

- `internal/invclient/invclient.go`: `Client` interface, `Mesh` over
  `inventoryclient`, paging `Limit 500` by cursor, every failure wrapped as
  `ErrUnavailable`; `Fake` with `Down`.
- `internal/invsync/{invsync.go,diff.go}`: load (fails before any write when
  inventory is down) → match host id tag → serial → hostname → diff →
  execute; errors per host collected, loop continues.
- Wiring: `lazyInventory` dials `Freya.Client(ctx, cfg.Inventory.Service)` on
  first use (no mutex — a data race; IPAM must use `sync.Once`/mutex).
- Trigger: only on request, for one tenant. No background job.
- Policy rule lives in **inventory's** `deploy/policy.yaml` (`asset-sync`).

### Platform event bus

- Per-tenant Valkey streams `platform:events:<tenant>`; the hub writes
  `{to, type, data, at}`; no consumer groups, no XACK anywhere.
- Cross-module consumers: dns ← ipam (`internal/ipamsync/consumer.go`) and
  deployer ← lcm. Both start at `XLast` (no replay), keep offsets in memory,
  take their tenant list from static config, and treat the event only as a
  trigger (re-read over mTLS).

### v3 reference (go-tangra-client, go-tangra-ipam v3)

- Client collectors: stdlib `net.Interfaces()` (name, MAC, IPs/CIDRs; skip
  down/loopback; no kind/speed/DHCP/gateway); primary = first IPv4 in
  enumeration order; virtualization from `/.dockerenv`, `/proc/1/cgroup`,
  `/proc/version` (WSL), `/sys/hypervisor/type`, DMI vendor/product/BIOS
  maps; IPMI via go-ipmi `NewOpenClient` (`/dev/ipmi0`), channels 1–11,
  `GetChannelInfo` (LAN medium) + `GetLanConfig`, 15 s budget; Proxmox guests
  from `/etc/pve/qemu-server/*.conf` (`name:`) and `/etc/pve/lxc/*.conf`
  (`hostname:`), `net*` lines, MAC regex, stop at the first `[` section;
  reboot from `/run/reboot-required` or `needrestart -b -k`; automatic
  updates = apt Unattended-Upgrade=1 **and** `apt-daily-upgrade.timer`
  enabled, or dnf-automatic timers; packages: **`apt update` on every run**,
  `apt -s upgrade` `Inst` lines, security = origin contains "security";
  dnf `makecache`, `check-update`, `updateinfo list security`; apk `-u
  list`; pacman `checkupdates`; **no timeouts on package commands**.
  Exclusions (client side only): `lo`, `docker0`, `br-*`, `veth*`,
  `virbr*`, `cni*`, `flannel*`, `calico*`, `weave*`.
- Server: interfaces materialised from metadata JSON (BMC MACs as `ipmi`,
  `ipmi-2`…); addresses moved last-writer-wins without conflict detection;
  subnets auto-created client-side as `auto-<cidr>`; hypervisor links by
  guest MAC (`link_source = "hypervisor"`, stale after 3 days); FDB
  correlation: per switch the port with the fewest MACs, skip ports with more
  than **16** MACs, only SERVER devices, links pruned after 14 days; no LLDP
  logic.

## Findings that change the plan

1. **The IPAM audit writer is not wired** — no IPAM operation is audited in
   v4 today. The host sync cannot rely on it; and the async writer can drop
   events (`Dropped()`), which contradicts SC-005 (100 % audited). → D11.
2. **`ipam_device_interface_links` UNIQUE `(interface_id, remote_device_id,
   link_source)` makes FDB persistence fail** for any switch port that learned
   two or more MACs (all FDB rows have `remote_device_id = ''`), and the error
   aborts `persistDevice` for the remaining interfaces. US5 depends on this
   data. → D17 (migration 0005 + regression test).
3. **The scan path overwrites host-reported fields** (`UpsertDeviceByName`
   sets `management_ip` to the scanned address) when a reported host answers
   SNMP with its host name as sysName. → D9 (scan does not overwrite
   host-report-owned fields).
4. IPAM's `TenantIDs` cannot discover tenants that have inventory hosts but no
   IPAM rows. → D5.
5. The agent reports hourly by default; IPAM can only be as fresh as the
   latest report (SC-003 is measured from the report).
6. Audit detail keys containing `owner`, `ipmi` or `snmp` are silently
   dropped by the guard → the host sync uses `previous_device_id`,
   `bmc_*`, never `previous_owner`/`ipmi_*`.

## Decisions

### D1 — Inventory proto extension (additive, `sdk/v4.1.0`)

**Decision**: extend `inventory.v1` additively (full text in
[contracts/inventory-grpc.md](contracts/inventory-grpc.md)):

- `NetworkInterface`: fill the existing `type` (9, now the interface
  **kind**: `ethernet|wireless|bond|bridge|vlan|virtual|loopback|other`),
  `speed_bps` (8), `gateway` (5), `dhcp` (7, any IPv4 address dynamic), `up`
  (10); new `repeated InterfaceAddress addresses = 11` (`address`,
  `prefix_length`, `family`, `dhcp`, `temporary`, `deprecated`, `scope`),
  `bool default_route = 12`, `string master = 13` (bond/bridge),
  `uint32 vlan_id = 14`. `ip_addresses` (3, CIDR strings) stays filled for
  old consumers.
- `Inventory`: `string primary_ipv4 = 24`, `string primary_ipv6 = 25`,
  `Virtualization virtualization = 26`, `Bmc bmc = 27`,
  `repeated HypervisorGuest hypervisor_guests = 28`,
  `UpdateState update_state = 29`, `CollectionLimits truncated = 30`.
- `OSInfo.family = 9` (`linux|windows`); `Program.available_version = 7`,
  `Program.security_update = 8`.
- New `HostReportService` (D4) and `HostReport` projection.

**Backward compatibility**: proto3 additive fields; an old agent's snapshot
decodes with zero values → IPAM falls back to parsing `ip_addresses` CIDRs,
leaves kind empty, virtualization `unknown`, update status `unknown`
(US edge "older agents"). A new agent talking to an old inventory server loses
the new fields (the edge mapper drops unknown fields) → **inventory server is
upgraded before agents** (Rollout). `buf breaking` against `sdk/v4.0.0` must
pass.

**SDK**: IPAM needs the new messages and client methods → inventory SDK
**`sdk/v4.1.0`** (minor, additive), IPAM `go.mod` adds
`github.com/go-tangra/go-tangra-inventory/sdk/v4 v4.1.0` (first-party; asset
may stay on v4.0.0).

**Rationale**: the fields that already exist in the schema cost nothing to
fill; per-address metadata (prefix, DHCP, temporary) cannot be expressed in
CIDR strings; a separate top-level message per concern keeps the diff
categories clean. **Alternatives**: a JSON `metadata` blob like v3 —
rejected: unvalidated, invisible to proto tooling, the reason v3 needed
server-side re-parsing; a new `ExtendedInventory` message — rejected: two
payloads per snapshot and three mappers to duplicate.

### D2 — Inventory storage of the new data

**Decision**: the snapshot `payload jsonb` stays authoritative: the new
fields are added to `store.Inventory` (`internal/store/models.go`) and flow
into the payload. The normalised `inventory_network_interfaces` columns that
already exist (`subnet`, `gateway`, `dhcp`, `speed_bps`, `type`, `up`) are
filled on insert. Migration **0005_host_reports.sql** adds to
`inventory_hosts`: `report_digest text NOT NULL DEFAULT ''`,
`report_changed_at timestamptz` and indexes `(tenant_id, report_changed_at)`
and `(report_changed_at)`. Change history: `internal/diff/diff.go` gains
categories `virtualization`, `bmc`, `guest`, `update` and compares the new
network and program fields (FR-007).
**Rationale**: no new component tables are needed for IPAM (it reads the
projection); the digest columns make "what changed for IPAM" a cheap indexed
query (D4). **Alternatives**: new normalised tables for guests/BMC/updates —
rejected (YAGNI, nobody queries them relationally); IPAM computing changes
from `last_seen` — rejected: every hourly snapshot bumps `last_seen`, so IPAM
would re-fetch every host every hour.

### D3 — Agent collection per platform

Full contract: [contracts/agent-collection.md](contracts/agent-collection.md).

**Linux networking** — sysfs + rtnetlink through the standard library:
`/sys/class/net/<if>/{type,speed,operstate,address,bonding/,bridge/,wireless/,
phy80211,device,master}` and `/proc/net/vlan/config` for kind, speed, master
and VLAN id; `syscall.NetlinkRIB(RTM_GETADDR)` for addresses with prefix and
flags (`IFA_F_PERMANENT` absent → dynamic/DHCP for IPv4,
`IFA_F_TEMPORARY`, `IFA_F_DEPRECATED`, scope) and
`syscall.NetlinkRIB(RTM_GETROUTE)` for the default routes (gateway, output
interface, metric) → `default_route`, `gateway`, `primary_ipv4/6` (first
global address of the lowest-metric default-route interface).
**Alternatives**: `vishvananda/netlink` — rejected (large dependency for two
dumps; stdlib `syscall` already parses netlink messages); gopsutil only —
rejected (no flags, no routes); parsing `ip -j` — rejected (iproute2 not
guaranteed, subprocess per collection).

**Windows networking** — `golang.org/x/sys/windows.GetAdaptersAddresses`
(already a dependency): `IfType` → kind (6 ethernet, 71 wireless, 24
loopback, 131/53 virtual; "Multiplexor" description → bond; Hyper-V
`vEthernet` → virtual), `OperStatus`, `TransmitLinkSpeed`, `Flags &
IP_ADAPTER_DHCP_ENABLED`, `FirstGatewayAddress`, unicast addresses with
`OnLinkPrefixLength` and `SuffixOrigin` (random → temporary),
`Ipv4Metric`/`Ipv6Metric` for the primary. **Alternative**: WMI
`Win32_NetworkAdapterConfiguration` via PowerShell — rejected (slow,
text-parsing, the collector already avoids WMI libraries).

**Virtualization** — one pure rule set `DetectVirtualization(facts)` fed by:
Linux `/.dockerenv`, `/run/.containerenv`, `/proc/1/environ` `container=`,
`/proc/1/cgroup`, `/proc/version` (WSL), `/sys/hypervisor/type`, cpuinfo
`hypervisor` flag; both platforms: SMBIOS system manufacturer/product/BIOS
vendor already collected by `hardware.go` (QEMU/KVM, VMware, Microsoft
"Virtual Machine", Xen, VirtualBox, Amazon EC2, Google, Parallels,
DigitalOcean). Output `role` (`physical|vm|container|unknown`) and `kind`
(`kvm|vmware|hyperv|xen|virtualbox|lxc|docker|podman|wsl|aws|gce|…`).
**Alternatives**: `systemd-detect-virt` — rejected (not on every host,
subprocess); gopsutil `VirtualizationRole` — rejected (reports `host` on a KVM
hypervisor, which is the physical case we must not mislabel).

**BMC (Linux only)** — `github.com/bougou/go-ipmi v0.8.1` in-band
(`NewOpenClient`, `/dev/ipmi0`), 10 s total budget: `GetChannelInfo` for
channels 1–11 (LAN medium only) and **only** LAN configuration parameters
3 (IP), 4 (IP source), 5 (MAC), 6 (subnet mask), 12 (default gateway) and
20 (VLAN id), one `Client.GetLanConfigParamFor(ctx, ch, param)` call each —
never `GetLanConfig`/`GetLanConfigParams`, which in v0.8.1 also read
parameter 16, the community string (`cmd_get_lan_config_params.go:103-125`),
and never user/password/cipher-suite commands. Requires
root and the `ipmi_devintf` + `ipmi_si` kernel modules; the agent never loads
modules; absence → BMC omitted (US2 scenario 2). Windows: omitted (the
Microsoft IPMI driver path is not worth the complexity; Edge Cases already
limit Windows to network/identity/virtualization).
**Dependency justification (Constitution VI)**: purpose — IPMI KCS/OpenIPMI
message framing and LAN parameter decoding; pure Go (no cgo), MIT, maintained
(releases in 2025–2026), already a direct dependency of IPAM v4 (same
version, `govulncheck` clean there); alternatives rejected: shelling out to
`ipmitool` (often absent, text parsing, would print the community string with
`lan print`), writing our own ioctl framing (custom protocol code, larger
audit surface). Only the agent build imports it (Linux build tag).

**Proxmox guests (Linux only)** — port the v3 parser: `/etc/pve/qemu-server/*.conf`
(`name:`) and `/etc/pve/lxc/*.conf` (`hostname:`) — these paths resolve to
the local node, so each cluster node reports its own guests; MACs from
`net<N>:` lines (`virtio=`/`hwaddr=`), stop at the first `[snapshot]`
section; bounds: ≤ 1000 guests, file ≤ 64 KiB, ≤ 32 NICs per guest.

**Update state (Linux only)** — first detected manager of apt → dnf → yum →
apk → pacman; **the agent never refreshes package lists by default**
(`refresh_package_lists: false`); it reads the lists the OS timers keep
fresh:
- apt: `apt-get -s -o Debug::NoLocking=1 dist-upgrade` (LANG=C), `Inst`
  lines; security = the origin field contains `-security` (Debian/Ubuntu
  convention).
- dnf/yum: `dnf -q -C check-update` (exit 100 = updates) and
  `dnf -q -C updateinfo list --updates security` (cache only, `-C`).
- apk: `apk -u list` (cached index); no security classification.
- pacman: `checkupdates` (pacman-contrib; syncs a private temp db, never the
  system db); absent → status `unknown`; no security classification.
- reboot required: `/run/reboot-required` or `/var/run/reboot-required`;
  RHEL family `needs-restarting -r` (exit 1); fallback `needrestart -b -k`
  (`NEEDRESTART-KSTA >= 2`); otherwise unknown.
- automatic updates: apt `APT::Periodic::Unattended-Upgrade "1"` and
  `apt-daily-upgrade.timer` enabled; dnf-automatic timers; `yum-cron`
  service; otherwise false.
- bounds: 60 s per command, 120 s for the whole update collection, ≤ 5000
  pending packages; with `refresh_package_lists: true` the agent refreshes at
  most once per 24 h.
**Rationale**: v3's `apt update` on every sync loads mirrors and fights the
OS's own timers and dpkg lock; unbounded commands could hang the agent.
**Alternatives**: PackageKit D-Bus — rejected (not installed on servers);
distribution security trackers (OVAL/USN feeds) — rejected (network access
from the agent, large parsing surface).

**Code layout**: pure parsers/rules go to a new package
`internal/agentfacts` (covered by the gate, fuzzed); OS glue stays in
`internal/collector` (excluded from the gate like today) with build tags
`_linux.go`/`_windows.go`/`_other.go`.

**Bounds in the agent**: ≤ 256 interfaces, ≤ 64 addresses per interface,
≤ 1000 guests, ≤ 5000 pending packages; excess counted in
`Inventory.truncated` (CollectionLimits) — never silently dropped.

### D4 — How IPAM obtains host data: `HostReportService` + digests

**Decision**: inventory builds a **projection** of the latest snapshot for
IPAM (`internal/hostreport` in inventory): identity (host id, hostname,
serial, manufacturer, model, OS), interfaces, primary addresses,
virtualization, BMC, guests, update state and **only the packages with an
available update** (not the full software list), plus truncation counters. At
ingest it computes `report_digest = sha256(deterministic proto marshal of the
projection)` and bumps `report_changed_at` only when the digest changes.
New service `inventory.v1.HostReportService`:

| RPC | Scope | Purpose |
|---|---|---|
| `ListReportTenants{changed_since}` → `{tenant_ids}` | system (cross-tenant), ≤ 10 000 ids | tenants with a report change since T |
| `ListHostReports{tenant_id, changed_since, view, limit ≤ 200, cursor}` → `{reports, next_cursor}` | tenant | `FULL` projections or `DIGEST` rows (`host_id, status, report_digest, report_changed_at`); pages also bounded to 3 MiB |
| `GetHostReport{tenant_id, host_id}` → `HostReport` | tenant | re-sync one host |

`ListReportTenants` is additionally restricted in the handler to the service
names in inventory config `host_reports.consumers` (default `["ipam"]`),
independent of the mesh policy (Constitution III).
**Rationale**: IPAM reads exactly what it needs (least privilege, small
messages, SR-002 bounds at the source); digests let the hourly reconcile
compare 1000 hosts in one small page series and fetch only differences.
**Alternatives**: `ListHosts` + `GetLatestByHost` per host (asset style) —
rejected: full payload including every installed program for every host,
no change signal; a push from inventory into IPAM — rejected: SR-004 spirit
(IPAM decides what it writes) and it would make inventory depend on IPAM.

### D5 — Trigger: changed-since polling, not the event bus

**Decision**: IPAM polls. A single `hostsync.Poller` runs every
`host_sync.poll_interval` (default 60 s): `ListReportTenants(changed_since =
global watermark)` → for each enabled tenant `ListHostReports(FULL,
changed_since = tenant watermark)` → apply. A **reconcile** runs per tenant
every `full_interval` (tenant setting, default 60 min): `ListReportTenants(0)`
(all tenants with hosts) → `ListHostReports(DIGEST)` → fetch and apply hosts
whose digest differs from the device's stored digest, and mark devices whose
inventory host disappeared or is retired as not reported. Watermarks use
inventory's `report_changed_at` values (inventory clock; no skew), are stored
per tenant and advanced only after the page was applied.
**Rationale**: FR-008 (after each change + hourly). A change is picked up
within one poll interval (≤ 60 s, well inside SC-001/SC-003's 5 minutes).
The platform bus has no consumer groups or persistent offsets, readers start
at `XLast` and lose events across restarts, and streams are per tenant so a
consumer needs the tenant list anyway (dns uses a static config list); a poll
would be required as the safety net regardless. Polling one indexed query per
minute is cheap and needs no Valkey coupling or trust in bus contents.
**Alternatives**: subscribe to `inventory.host.changed` per tenant stream
(dns pattern) + hourly poll — rejected for this feature (two mechanisms,
unreplayable, forged-event surface on a shared Valkey, static tenant list);
can be added later as a latency optimisation without changing the apply
path. Poll `ListHosts(last_seen_from)` — rejected (D2).

**Tenant discovery**: from `ListReportTenants` (tenants with hosts) — not
IPAM's `TenantIDs` (misses tenants without IPAM rows) and not a static list.
A tenant seen for the first time gets a settings row with defaults
(enabled, 60 min, default exclusions).

### D6 — Per-tenant execution, transactions and idempotency

**Decision**:
- Tenants run in parallel bounded by `host_sync.workers` (default 2, max 8);
  hosts of one tenant are applied **sequentially** (address moves between two
  hosts of one tenant are then ordered by report time).
- Each host is applied in **one tenant-scoped transaction** (`repodb.tenant`,
  RLS applies): `pg_advisory_xact_lock(hashtext('ipam.hostsync:'||tenant))`
  (serialises several IPAM replicas), `SELECT enabled FROM
  ipam_hostsync_settings … FOR SHARE` (aborts if disabled — SR-006, see D14),
  load state, plan, write, **insert audit rows in the same transaction**
  (D11), store `report_digest`/`last_report_at` on the device, commit;
  realtime events are published after commit.
- Planning is a **pure function** `hostplan.Plan(state, report, settings,
  now) → []Op` (package `internal/hostplan`); applying an identical report
  to the resulting state yields zero ops (tested property). A device whose
  stored digest equals the report digest is skipped before loading state
  (unless the run is a forced re-sync).
- Unique-violation races (e.g. two replicas creating the same subnet) →
  the transaction is retried once; persistent failures are recorded as a
  host issue and the watermark is not advanced past that host.
**Rationale**: per-host atomicity means a report is either fully applied and
audited or not at all; sequential per tenant removes intra-tenant races
without row-level lock choreography. **Alternatives**: one transaction per
tenant page — rejected (long locks, one bad host blocks 100); applying via
the existing service layer (`devices.Service`, `addresses.Service`) —
rejected: they authorize a user subject and write in separate transactions.

### D7 — Matching a report to a device (FR-009)

**Decision** (in `hostplan.Match`):
1. device with `inventory_host_id = host.id` (partial UNIQUE per tenant);
2. else devices with `serial_number` equal (case-insensitive, trimmed) to the
   reported system serial, **only if** the serial is not a placeholder
   (`""`, `0`, `none`, `default string`, `to be filled by o.e.m.`, `system
   serial number`, `not specified`, `123456789`, all-zero/`x`) and exactly
   one such device exists and it is not linked to another inventory host;
3. else the device whose `name` equals the hostname (case-insensitive),
   **only if** the hostname is not generic (`localhost`,
   `localhost.localdomain`, `ubuntu`, `debian`, `raspberrypi`, `centos`,
   `fedora`, `unknown`, empty) and the device is not linked to another
   inventory host;
4. else create. If the name is taken, the new device is named
   `<hostname> (<first 8 chars of host id>)` (UNIQUE `(tenant_id, name)`).
The inventory host id is recorded on the device (`inventory_host_id`), after
which only rule 1 applies; a renamed host renames the device (US1, edge
"renamed host").
**Rationale**: spec Assumptions (identity authoritative, serial/name only
once) and edge cases (generic names never merged). **Alternatives**: MAC
matching — rejected for the first match (MACs move with NICs and VMs clone
them); v3 name-only adoption — rejected (merges "localhost").

### D8 — Field ownership (reported data wins, admin fields never touched)

| Record | Written by the sync | Never written by the sync |
|---|---|---|
| Device | name, device_type (rule below), virtualization_kind, os_type, os_version, manufacturer, model, serial_number, primary_ip, primary_ipv6, management_ip (only when a BMC address is reported), reboot_required, unattended_upgrades, update_status, hypervisor_device_id, last_seen, source, inventory_host_id, report_state, last_report_at, report_digest | description, tags, location_id, rack_*, device_height_u, asset_tag, status, contact (sealed), ipmi_secret_ref, firmware_version, host-group membership, created_by |
| Interface | name, mac_address, interface_type (kind), speed_mbps, enabled, report_state, and link columns in US5 | description, if_index of SNMP interfaces |
| Address | subnet_id, device_id, interface_name, mac_address, hostname, is_primary, last_seen, report_state, previous_device_id, moved_at, move_count, conflict, status on create (`active`) and `address_type` on create (`host`) | description, note, tags, owner (sealed), dns_name, ptr_record, has_reverse_dns, lease_expiry, status/address_type of existing rows |
| Subnet | created only (name = CIDR, `origin = host_sync`, `created_by = hostsync`, parent_id = most specific containing subnet) | everything on existing subnets |

Device type rule: `vm`/`container` when the report says so; `physical` →
`server` on create, and on update only when the current type is `vm` or
`container` (a physical host an administrator typed `workstation`,
`storage`, `firewall`… keeps its type); `unknown` (old agents) → `server`
on create, unchanged on update.
**Rationale**: FR-010–FR-016, SC-004, and the spec decision that fields hosts
never report are untouchable. Enforced by `hostplan` producing only the
column set above and by a store-level update statement that lists exactly
these columns (tests on both layers).

### D9 — Scan and host sync coexistence

**Decision**: `scan.persistDevice` stops overwriting host-report-owned fields:
`UpsertDeviceByName` gains a guard — for a device with `source =
'host_report'` it only fills empty fields and bumps `last_seen`; SNMP-created
devices get `source = 'scan'`. Host sync never touches devices it did not
match (switches, routers).
**Rationale**: finding 3; otherwise every SNMP scan of a reporting Linux host
would replace its BMC management address with the host's own address.

### D10 — Addresses: placement, moves, conflicts, release, exclusions

- **Classification** (`ipnet.Classify`): record global unicast and ULA;
  never loopback, link-local (`169.254/16`, `fe80::/10`), multicast,
  unspecified; IPv6 addresses flagged `temporary` or `deprecated` are
  skipped (FR-022, edge IPv6).
- **Placement**: `ipnet.MostSpecific(subnetCIDRs, ip)` (longest prefix that
  contains the address; ties → lowest id) over the tenant's subnets. None →
  create the reported network `ipnet.NetworkOf(ip, prefix)` as a subnet
  named after its CIDR (`origin = host_sync`); IPv6 `/128` and IPv4 `/32`
  reports without a containing subnet use `/64` resp. `/32` (a `/128` is
  never a LAN); a name collision appends ` (auto)`.
- **Moves** (FR-013, SR-003): an address row owned by another device moves to
  the reporting device; `previous_device_id`, `moved_at` are set; the audit
  records `address_moved` with `previous_device_id` and `device_id`. If the
  address moved `conflict_moves` times (default 3) within `conflict_window`
  (default 24 h), `conflict = true` and `address_conflict` is audited; the
  flag clears after a full window without moves or when an administrator
  clears it (`POST /ip-addresses/{id}/clear-conflict`, `addresses:manage`).
  An unowned address (scan-discovered or manual without device) is
  **claimed** (audit `address_updated`, not a move).
- **Release** (FR-014): an address this device previously reported and that
  is absent from the new report is released: `device_id = NULL`,
  `interface_name = ''`, `is_primary = false`, `previous_device_id =
  device`, `report_state = 'not_reported'`; row kept (status unchanged).
- **Host gone** (edge): host deleted or retired in inventory → device,
  its reported interfaces and addresses get `report_state = 'not_reported'`,
  links kept, nothing deleted.
- **Primary**: `is_primary` true for the reported `primary_ipv4`/`primary_ipv6`,
  false for the device's other reported addresses.
- **Exclusions** (FR-022): interfaces with kind `loopback` are always
  excluded; per-tenant glob patterns (Go `path.Match`, ≤ 64 patterns, each ≤
  64 chars) default to `docker*`, `br-*`, `veth*`, `virbr*`, `cni*`,
  `flannel*`, `cali*`, `weave*`, `vxlan*`, `kube-*`, `cilium*`,
  `podman*`, `fwbr*`, `fwpr*`, `fwln*`, `tap*`, `vnet*`. Excluded interfaces
  produce neither interface rows nor addresses.
**Rationale**: FR-012–FR-014, FR-022, SC-002. The dns module follows address
events, so moves/releases publish `ipam.ip_address.updated` like manual edits.

### D11 — Audit: transactional rows in the existing hypertable

**Decision**: every change the sync makes is written as an
`ipam_audit_events` row **inside the apply transaction**
(`actor_kind = system`, `actor_id = hostsync`, `detail` with `before`/`after`
of each changed field, `inventory_host_id`, `run_id`, and `trigger`
(`poll|reconcile|resync:<user id>`)). New vocabulary in
`internal/audit/audit.go`: `interface_created`, `interface_updated`,
`interface_not_reported`, `address_moved`, `address_released`,
`address_conflict`, `address_conflict_cleared`, `packages_updated`,
`hypervisor_linked`, `hypervisor_unlinked`, `port_linked`, `port_unlinked`,
`device_not_reported`, `hostsync_settings_updated`,
`hostsync_resync_requested`, `hostsync_run`; subject kinds `package`,
`hostsync`. Existing `device_created/updated`, `address_created/updated`,
`subnet_created` are reused. Detail keys pass the existing guard (no
`owner`, `ipmi`, `snmp`, `secret` substrings). Package changes are audited
as one `packages_updated` event per device with counts and the changed
names (≤ 200 names, then a count) to bound row size.
**Rationale**: SC-005 needs 100 %; the buffered writer may drop. The generic
wiring of the audit writer for user operations is a pre-existing gap (finding
1) and **out of scope**; the new admin endpoints (settings, re-sync,
clear-conflict) audit through the same transactional path.
**Alternatives**: `audit.Writer` — rejected (drops under back-pressure,
not wired); the framework `audit.Emitter` — kept for authz denials only
(security stream), not for data-change history.

### D12 — Package update state (US4)

**Decision**: reuse `ipam_device_packages`: for host-reported devices the
table holds the **pending updates** (`needs_update = true`,
`is_security_update`, `current_version`, `available_version`,
`package_manager`) replaced per report (bounded 5000; excess counted as an
issue). New device column `update_status` (`unknown|up_to_date|updates_available|unsupported`);
`reboot_required`/`unattended_upgrades` reuse existing columns. Windows and
old agents → `unknown`. The existing HTTP `packages/sync` stays for manual
devices; on host-reported devices the next report wins.
**Rationale**: the table already has every needed column and the UI already
renders it. **Alternatives**: a new table — rejected (duplicate); storing all
installed packages — rejected (inventory owns the software list; FR-018 asks
for updates).

### D13 — Hypervisor guests (US3)

**Decision**: device column `hypervisor_device_id` (FK, `ON DELETE SET NULL`)
and table `ipam_hypervisor_guests` (per host device: `guest_ref` (VMID),
`name`, `kind`, `macs text[]`, `guest_device_id` nullable, `last_reported_at`;
UNIQUE `(host_device_id, guest_ref)`; GIN index on `macs`). On a host report
the guest rows are replaced; each guest MAC is looked up among interfaces of
other devices in the tenant — exactly one device → link
(`hypervisor_device_id`, audit `hypervisor_linked`); several → not linked,
issue `duplicate_mac`. On a guest's own report its interface MACs are looked
up in `ipam_hypervisor_guests.macs` → link (US3 scenario 3). A guest no
longer reported by its host is unlinked (`hypervisor_unlinked`).
**Rationale**: FR-017; v3 stored links as fake interface links with a 3-day
reaper — a first-class column is simpler and queryable both ways.

### D14 — Sync settings, status and control (US6)

**Decision**: table `ipam_hostsync_settings` (one row per tenant: `enabled`
default true, `full_interval_minutes` 15–1440 default 60,
`excluded_interfaces text[]` default D10 list, state columns
`changed_since`, `last_poll_at`, `last_reconcile_at`, `status`
(`ok|degraded|disabled`), `last_error` (sanitised code, no free text from
inventory), counters). HTTP (details in
[contracts/ipam-http.md](contracts/ipam-http.md)):
`GET/PUT /api/ipam/v1/host-sync/settings`, `GET /host-sync/status`,
`POST /host-sync/resync` (all hosts), `POST /devices/{id}/host-sync`
(one host), `GET /devices/{id}/host-sync` (report source, last report,
issues), `GET /devices/{id}/guests`, `POST /ip-addresses/{id}/clear-conflict`.
Global kill switch `host_sync.enabled` in the service config (default true).
**Disable is immediate (SR-006)**: the PUT that sets `enabled=false` updates
the row, which waits for any apply transaction holding `FOR SHARE`; every
later apply sees `false` and aborts — no change commits after the disable
commits. Re-enabling schedules a reconcile of that tenant (US6 scenario 2).
**Permissions**: status and per-device report info → `ipam:read`; re-sync
(one/all) and clear-conflict → existing `devices:manage` /
`addresses:manage` (re-sync only applies what the host reports, like editing
devices); **settings → new `hostsync:manage`** (module-scoped
`ipam:hostsync:manage` in auth after feature 019), granted to built-in
owner/admin and the IPAM administrator role (`PermissionRefs()`), not to the
operator role: turning automatic writes off/on and changing exclusions is a
tenant-wide policy decision. CASL ability `{manage, HostSync}` requires
`hostsync:manage`.
**Alternatives**: reuse `devices:manage` for settings — rejected (an
operator could silently disable the audit-producing sync); per-host enable
flags — rejected (YAGNI; exclusions + tenant switch cover the spec).

### D15 — Validation and bounds in IPAM (SR-002)

**Decision**: package `internal/hostreport` (100 % gate, fuzzed) converts an
inventory `HostReport` into a normalised report and a list of issues:
tenant id must equal the requested tenant (else the report is rejected —
cross-tenant guard); host id uuid; hostname ≤ 253 bytes, printable UTF-8,
no control characters; interface names ≤ 64 bytes, same charset; MAC
normalised to lower-case colon form (6 bytes; zero, broadcast and multicast
MACs dropped); addresses parsed with `net/netip`, prefix within family range;
guest refs ≤ 64, guest names ≤ 128; package names ≤ 256, versions ≤ 128;
counts: interfaces ≤ 256, addresses ≤ 1024 total, guests ≤ 1000, packages ≤
5000, BMC ports ≤ 8. Invalid entries are skipped and returned as issues
(`field`, `reason`, `count`); limits exceeded → the first N kept, excess
counted (`truncated`). Issues are stored on the device's host-sync record
(latest 50) and visible in `GET /devices/{id}/host-sync`. Output strings are
rendered by Vue text interpolation only (no `v-html`).
**Rationale**: every layer validates (Constitution III); inventory also
validates at its ingest edge (D16) but IPAM does not rely on it.

### D16 — Validation at the inventory ingest edge

**Decision**: `internal/ingest` gains `validateExtended(inv)`: the same count
bounds and string lengths for the new fields; excess entries are truncated
and counted into `Inventory.truncated` (the snapshot is still stored — the
existing data must not be lost for one oversized list); structurally
invalid values (bad MAC/IP) are dropped. The total size bound (8 MiB) is
unchanged. `FuzzSubmitMapper` is extended to the new fields.

### D17 — Switch-port correlation (US5)

**Decision**: package `internal/portlink` (pure ranking + store apply), run
after each completed scan with SNMP (`scan.processJob` hook) and after each
host-sync run for tenants with switches. Migration **0005** first replaces the
broken link uniqueness with `UNIQUE (interface_id, link_source,
remote_device_id, remote_port_name)` (finding 2). Algorithm:
1. From `ipam_device_interface_links` with `link_source = snmp_fdb` on
   interfaces of devices typed `switch`: MAC → per switch the port (switch
   interface) with the fewest distinct MACs, with that VLAN.
2. A port is an uplink and ignored if it learned the MAC of another switch's
   interface, or its LLDP neighbour is a switch device, or its distinct MAC
   count exceeds `max_macs_per_port` (default 16 — an access port to a
   hypervisor carries the host and its guests; spec "ports with many MACs").
3. For each host-reported interface MAC: the candidate with the fewest MACs
   across switches wins (v3 behaviour for daisy chains); ties → not linked.
4. LLDP precedence: an LLDP link on a switch port whose `remote_port_name`
   system name equals the host device name (case-insensitive) or whose port
   id equals the host interface MAC/name links that port directly with
   `link_source = lldp`, overriding FDB inference.
5. The host interface's flat link columns are set
   (`remote_device_id = switch`, `remote_interface_id`, `remote_port_name =
   switch interface name`, `link_source`, `link_vlan`, `link_last_seen`);
   the switch-port view resolves "device behind" by reverse lookup
   (`remote_interface_id`). Links not re-confirmed for 14 days are cleared.
   Audit `port_linked`/`port_unlinked`.
**Rationale**: FR-019 and US5; reuses existing SNMP data (Assumptions).

### D18 — Inventory service policy, gateway and production

**Decision**:
- `go-tangra-inventory-v4/deploy/policy.yaml` new inbound rule:
  ```yaml
  - id: ipam-hostsync
    from: ["spiffe://example.org/svc/ipam"]
    to: ["inventory"]
    operations: ["/inventory.v1.HostReportService/ListReportTenants",
                 "/inventory.v1.HostReportService/ListHostReports",
                 "/inventory.v1.HostReportService/GetHostReport",
                 "/grpc.health.v1.Health/Check"]
    effect: allow
  ```
- IPAM's `deploy/policy.yaml` gets no new rule (policies are inbound; IPAM
  is the client) — only its header comment names inventory as a callee.
- go-tangra-docker: the rule must be added to **`policies/inventory.yaml`**
  (copied by `prod-init.sh` to `prod/policies/` with the real trust domain)
  when the new inventory image is pinned; `policies/ipam.yaml` needs only the
  header comment. This feature does not edit go-tangra-docker (task marked
  for the user).
- The gateway's `gateway-forwards: *` rule also reaches `HostReportService`;
  it is not exposed to browsers (no manifest `Methods`), and
  `ListReportTenants` refuses non-consumer identities in the handler
  (tenant-scoped RPCs from the gateway would need a user token path that does
  not exist for this service).

### D19 — Observability

Structured logs per run (`run_id`, tenant, hosts fetched/applied/skipped,
duration) and per failed host (`inventory_host_id`, reason code); OTel
instruments via `Freya.Metrics().Meter("ipam.hostsync")` on the non-public
admin listener: `hostsync_hosts_total{outcome}`,
`hostsync_changes_total{kind}`, `hostsync_entries_skipped_total{reason}`,
`hostsync_apply_seconds`, `hostsync_degraded` (per tenant count). The run id
is added to the outgoing context so inventory's logs correlate. No report
content (addresses, MACs) is logged at info level.

### D20 — Performance (SC-006) and concurrency

Budget: 1000 hosts in one tenant ≤ 15 min. Fetch: 10 pages × 100 FULL
reports (≤ 3 MiB each, IPAM client `MaxCallRecvMsgSize` 8 MiB). Apply: one
transaction per host, ~20–40 statements plus a batched package replace
(`unnest` insert) — target p95 ≤ 150 ms → ≈ 2.5 min sequential. IPAM
interactive traffic keeps its pool: the sync uses at most `workers` (2)
connections of `db.max_conns` (16) and sleeps `host_sync.pace` (default
10 ms) between hosts. Verified by an integration benchmark with 1000
synthetic reports (T-level task, asserts < 15 min and p95 of a concurrent
`GET /devices` under 200 ms on the test machine).

### D21 — Release and rollout order

1. **inventory** v4.3.0 + **inventory SDK `sdk/v4.1.0`**: proto, ingest
   validation, projection/digest, migration 0005, `HostReportService`, policy
   rule, agent collectors. Deploy the server first; agents afterwards
   (operators rebuild with `make agent` — there are no published agent
   artifacts). Old agents keep working (D1).
2. **ipam** v4.3.0 (+ **ipam SDK `sdk/v4.1.0`** for the new message fields):
   `go.mod` inventory SDK v4.1.0, migrations 0004/0005, host sync, UI,
   manifest permission `hostsync:manage` (registered with auth on start).
   Until inventory v4.3.0 is deployed, `ListReportTenants` returns
   Unimplemented → sync status `degraded`, nothing written.
3. **go-tangra-docker** (user): add `ipam-hostsync` to
   `policies/inventory.yaml`, pin inventory 4.3.0 and ipam 4.3.0, add the
   `host_sync` section to the IPAM config, load `ipmi_devintf`/`ipmi_si` on
   BMC hosts (documentation).
Tags, PR merges and pins require explicit user confirmation.

## Constitution check notes

- **I**: sync enabled by default is a product decision (spec); it is not an
  insecure option. Insecure variants: none added. The agent's BMC collection
  and package list refresh default to their least invasive behaviour
  (read-only params; no refresh).
- **II**: IPAM → inventory over SPIFFE mTLS, callee policy + handler
  allow-list for the cross-tenant RPC; admin endpoints behind the gateway
  with module-scoped permissions; no agent path to IPAM (SR-004).
- **III**: bounds at agent, ingest edge, projection and IPAM (D3, D15, D16);
  gRPC message size bounds; OpenAPI body limits on the new endpoints.
- **IV**: fuzz targets — inventory `FuzzProxmoxConf`, `FuzzAptSimulate`,
  `FuzzDnfOutput`, `FuzzApkPacmanOutput`, `FuzzNetlinkAddr`,
  `FuzzBmcLanParams`, extended `FuzzSubmitMapper`, `FuzzHostReport`
  (projection); IPAM `FuzzNormalizeReport`, `FuzzExclusionPattern`,
  `FuzzMostSpecific`, `FuzzPlan` (idempotence + admin-field invariants).
- **V**: transactional audit (D11), metrics on the admin listener (D19).
- **VI**: new direct dependencies — inventory: `github.com/bougou/go-ipmi`
  (justified in D3); ipam: `github.com/go-tangra/go-tangra-inventory/sdk/v4`
  (first-party). No new UI dependency.
- **VII**: typed config sections (`host_sync` in IPAM, `host_reports` in
  inventory, agent `collect_*`); one pure planner; complexity recorded in
  plan.md.

## STRIDE threat model

| Threat | Scenario | Mitigation |
|---|---|---|
| **S**poofing | Forged agent reports addresses of other hosts (address hijack) | Agents authenticate to inventory with per-agent credentials (unchanged); the tenant comes from the credential, never the payload; IPAM only reads through inventory's SPIFFE identity (SR-001/SR-004). Residual risk (a compromised enrolled host lies about its own addresses) is made visible: every move audited with the previous device, repeated moves flagged as conflicts (SR-003), revocation via `RevokeAgent`. |
| **S** | Another mesh service calls `HostReportService` or fakes inventory | mTLS + inbound policy rule limited to `svc/ipam`; handler allow-list for `ListReportTenants`; IPAM dials inventory by service name through the framework pool (SVID-verified). |
| **T**ampering | Report overwrites administrator data | Fixed column set per record (D8), admin fields never in the update statements; tests assert unchanged admin fields (SC-004). |
| **T** | Event injection on the shared Valkey bus | Not used as input (D5 polling); IPAM's own published events carry no secrets and are content-free as before. |
| **T** | SNMP scan clobbers host-reported fields / host sync clobbers switches | D9 guard; host sync only touches matched devices. |
| **R**epudiation | Mass changes hidden from administrators | Transactional audit rows with before/after and actor `hostsync` (100 %), run summaries, trigger user recorded for manual re-sync. |
| **I**nformation disclosure | Cross-tenant leakage | Every tenant RPC carries the tenant id; IPAM rejects a report whose tenant differs from the requested one; all IPAM writes in tenant-scoped RLS transactions; `ListReportTenants` returns ids only, to IPAM only. |
| **I** | BMC credential exposure | Agent reads only LAN params 3/4/5/6/12/20 (never 16 community string, never users/passwords); proto has no credential field; negative test asserts the selector set; `ipmi_secret_ref` untouched by the sync. |
| **I** | Logs leak addresses/MACs broadly | Info logs carry counts and ids only; debug logs off by default. |
| **D**enial of service | Oversized/malformed reports (hundreds of interfaces, thousands of packages) | Agent caps + ingest caps + IPAM caps (D3/D15/D16), 8 MiB snapshot bound, 3 MiB page bound, per-host transaction, bounded workers and pacing; fuzzing of every parser. |
| **D** | Inventory down or slow | `ErrUnavailable` → status degraded, exponential back-off (max 10 min), IPAM data untouched; per-call timeout 30 s. |
| **E**levation of privilege | Operator disables the sync or changes exclusions to hide hosts | Settings require `hostsync:manage` (owner/admin/IPAM administrator only), audited. |
| **E** | Agent writes into IPAM directly | No IPAM endpoint accepts agent credentials; `SyncPackages` stays Unimplemented on the mesh; the only writer is the in-process sync (SR-004). |
