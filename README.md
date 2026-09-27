# go-tangra-ipam

Tenant-scoped IP address management for the
[go-tangra v4 platform](https://github.com/go-tangra/go-tangra).

Hierarchical subnets with utilization, IP addresses (first-free and bulk
allocation, conflict detection, find/suggest), devices (interfaces, L2 links, OS
packages), VLANs, a location hierarchy and IP/host groups. The module also runs
**active network operations**: asynchronous discovery scans (ICMP/SNMP/TCP),
ping, IPMI/BMC power control and a token-gated KVM console proxy. Power, IPMI
and KVM are platform-admin only; every active operation is tenant-scoped,
bounded and audited.

**SNMP credentials** live on subnets, sealed by ipam itself and write-only;
child subnets inherit them (see [SNMP credentials](#snmp-credentials)). BMC
credentials are not stored by ipam: devices hold only a warden secret reference
(`ipmi_secret_ref`), resolved at use time and never logged, audited or
exported. The gRPC binding to warden's `warden.v1.Secrets` is not wired yet
(`internal/warden`): until it is, secret resolution fails closed and IPMI/KVM
operations that need a credential are refused.

**Host sync**: hosts running the inventory agent keep their devices,
interfaces, addresses (in auto-created subnets when needed), BMC management
addresses, hypervisor guests, pending updates and switch ports current in IPAM
(see [Host sync](#host-sync)).

**ARP-based MAC linking**: scans with SNMP discovery read the ARP and
neighbour tables of the routers, firewalls and layer-3 switches that answer,
record the MAC of every active address (never overwriting an agent or manual
MAC) and link agentless hosts to their switch port (see
[ARP-based MAC linking](#arp-based-mac-linking)).

Operations: [`deploy/README.md`](deploy/README.md).
Design history: `specs/011-ipam-service` (walkthrough in `quickstart.md`).

## Place in the platform

```
go-tangra/go-tangra          platform module + @go-tangra/ui kit
        |
go-tangra-auth  <---->  go-tangra-portal (gateway)  <---->  go-tangra-ipam
                              |                              |       ^
                         go-tangra-lcm (SVIDs)        go-tangra-warden   dns (ipam sdk)
                                                             go-tangra-inventory (host reports)
```

- Built on `github.com/go-tangra/go-tangra/v4` (mTLS transports, identity,
  service policy, audit, observability).
- Verifies platform tokens and registers its permissions, module roles and
  built-in role grants with the auth SDK (`github.com/go-tangra/go-tangra-auth/sdk/v4`).
- Registers with the gateway through the portal SDK
  (`github.com/go-tangra/go-tangra-portal/sdk/v4`), which fronts the browser API
  (`/api/ipam`), the KVM proxy (`/bmc/`) and the federated UI remote.
- Enrolls for its workload identity with lcm (`github.com/go-tangra/go-tangra-lcm/sdk/v4`).
- Reads host reports from inventory (`github.com/go-tangra/go-tangra-inventory/sdk/v4`,
  `inventory.v1.HostReportService`, inventory >= 4.3.0).

## Modules in this repository

| Module | Path | Consumers |
|---|---|---|
| `github.com/go-tangra/go-tangra-ipam/v4` | `/` | the service (`cmd/ipamsvc`) and `pkg/ipammanifest` |
| `github.com/go-tangra/go-tangra-ipam/sdk/v4` | `sdk/` | other services: the `ipam.v1` protobuf API and `pkg/ipamclient` (mTLS client) |

The service builds against the in-repo SDK through
`replace github.com/go-tangra/go-tangra-ipam/sdk/v4 => ./sdk`. Consumers use the
SDK's published `sdk/vX.Y.Z` tag.

## Layout

| Path | Purpose |
|------|---------|
| `cmd/ipamsvc` | service binary (serve, `bootstrap`: migrate and exit; `version`) |
| `internal/app` | wiring: config, platform, store, events, HTTP/gRPC, gateway lease, auth registration (permissions, module roles), scan workers |
| `internal/{subnets,addresses,devices,vlans,locations,groups}` | domain services |
| `internal/ipnet` | pure CIDR/IP arithmetic that bounds allocation and scans |
| `internal/scan` | scan executor, ICMP/SNMP/TCP probes and the SNMP credentials test |
| `internal/snmpcred` | pure SNMP credential rules: validation, sealing binding, inheritance, error scrubbing |
| `internal/ipmi`, `internal/kvm` | BMC power/inventory and the KVM console proxy |
| `internal/warden` | secret-reference client (plus an in-memory fake) |
| `internal/{invclient,hostreport,hostplan,hostsync}` | host sync: inventory client, report validation, pure planner, poller/reconcile/apply and admin service |
| `internal/portlink` | links reported host interfaces and addresses with a MAC to switch ports from SNMP FDB/LLDP data |
| `internal/{arpplan,arpcfg}` | ARP-based MAC linking: pure planner (filters, MAC provenance) and per-tenant settings |
| `internal/{authz,sealed,audit,stream,backup,stats,dnscfg}` | authorization, sealed owner data, audit vocabulary, event stream, tenant backup, statistics, DNS settings |
| `internal/{repo,store,memstore}` | repository, SQL bindings (RLS, goose migrations), in-memory store |
| `pkg/ipammanifest` | gateway manifest built from the OpenAPI document |
| `ui` | Vue 3 + FlyonUI federated remote on `@go-tangra/ui` |
| `api/openapi`, `sdk/api/proto` | contracts (`ipam.yaml`, `ipam.v1`) |
| `deploy` | policy, operations notes and a development key |

## Build and test

You need Go 1.26, Node 22, Docker (for the integration suite and the image), and a
GitHub token with `read:packages` to install `@go-tangra/ui` from GitHub Packages.

```bash
go build ./... && go vet ./... && go test -race ./...
(cd sdk && go vet ./... && go test -race ./...)
(cd sdk && buf lint)
make test-integration                     # -tags integration, TimescaleDB via testcontainers (needs Docker)
make lint cover vuln

cd ui
export NODE_AUTH_TOKEN=$(gh auth token)   # ui/.npmrc only references this variable
npm ci && npm run lint && npm run test:unit && npm run build
```

The unit coverage gate requires at least 80 % overall and 100 % for
`internal/{authz,sealed,ipnet,hostreport,hostplan}`. `make fuzz` runs every fuzz
target (`FUZZTIME`, default 10 s each). Generated code, SQL bindings, wiring and the
raw network probes are covered by the integration suite instead. The Playwright
specs in `ui/tests/e2e` need a running platform and operator credentials; they
skip otherwise.

## Run

The service runs in the go-tangra platform stack (`deploy/stack` in
[go-tangra](https://github.com/go-tangra/go-tangra)), next to TimescaleDB, Valkey,
the gateway, lcm and warden. The stack mounts its configuration at
`/app/deploy/container.yaml` and the development key-encryption key at
`/app/deploy/kek.dev`. `deploy/kek.dev` in this repository is a development key
only; it is excluded from the image.

```bash
ipamsvc bootstrap -config deploy/container.yaml    # apply migrations and exit
ipamsvc -config deploy/container.yaml              # serve (applies migrations)
```

## Container image

The image is `ghcr.io/go-tangra/go-tangra-ipam`, built by
`.github/workflows/ci.yaml`. It carries `ipamsvc` with the embedded UI remote.

```bash
docker buildx build --secret id=npm_token,env=NODE_AUTH_TOKEN \
  --build-arg APP_VERSION=4.0.0 -t go-tangra-ipam:dev .
docker run --rm go-tangra-ipam:dev version
```

The image runs `ipamsvc -config deploy/container.yaml` as user `app`
(uid 10001) and ships `deploy/policy.yaml`. It contains no configuration and no
key material: deployments mount their own `deploy/container.yaml` and
key-encryption key. `ipamsvc` carries the file capability `cap_net_raw+ep` so
ICMP discovery scans work without root; the container must keep `NET_RAW` in its
capability set (Docker's default; the platform stack adds it explicitly).

## Host sync

The inventory agent reports each host's interfaces (kind, speed, addresses with
prefix and flags, gateway), primary addresses, virtualization, BMC LAN
settings (no credentials), Proxmox guests and Linux update state to the
inventory module. IPAM pulls a projection of each host's latest report from
inventory over the mesh (`inventory.v1.HostReportService`, SPIFFE mTLS):

- a **poll** every `host_sync.poll_interval_seconds` (60) fetches the reports
  that changed since the tenant's watermark; a per-tenant **reconcile** (tenant
  setting, default hourly) compares digests of every host and marks devices of
  retired or deleted hosts as *no longer reported* (nothing is deleted);
- each host is applied in **one tenant transaction** holding a per-tenant
  advisory lock and `FOR SHARE` on the tenant settings, so disabling the sync
  stops every later change; every change is written as an audit row in the
  same transaction (actor `system`/`hostsync`);
- the device is matched by inventory host id, else a unique real serial, else a
  non-generic hostname; reported data wins for the reported fields only —
  description, tags, location, rack, asset tag, status, contact, BMC secret
  reference, firmware and groups are never changed; addresses are placed in the
  most specific subnet (a subnet named after the reported network is created
  with origin `host_sync` when none contains it), moved from other devices with
  the previous device audited, flagged as a conflict after repeated moves, and
  released (not deleted) when the host stops reporting them; loopback,
  link-local, temporary IPv6 and container/virtual bridge interfaces (tenant
  exclusion patterns) are not recorded;
- after SNMP scans and host-sync runs, host interface MACs are correlated with
  the switches' forwarding tables and LLDP neighbours to show the switch port
  (and VLAN) each host is connected to.

Configuration (`host_sync` section, all optional):

```yaml
host_sync:
  enabled: true                 # global kill switch (false: nothing is applied)
  inventory_service: inventory
  poll_interval_seconds: 60     # 10-3600
  workers: 2                    # tenants in parallel, 1-8
  page_size: 100                # 1-200
  pace_ms: 10                   # pause between hosts
  request_timeout_seconds: 30
  conflict_moves: 3             # moves within the window that flag a conflict, 2-100
  conflict_window_hours: 24     # 1-168
  max_macs_per_port: 16         # switch ports with more MACs are never inferred, 1-256
  link_stale_days: 14           # switch-port links not re-confirmed are cleared
```

Per tenant, administrators enable or disable the sync, set the reconcile
interval and edit the interface exclusions (`/ipam/host-sync`, permission
`hostsync:manage`); anyone with `devices:manage` can re-sync one host or all.
Until inventory >= 4.3.0 is deployed and its policy has the `ipam-hostsync` rule
(see `deploy/README.md`), the sync reports `degraded` and writes nothing.

Audit vocabulary of the sync: `device_created`, `device_updated`,
`device_not_reported`, `interface_created`, `interface_updated`,
`interface_not_reported`, `subnet_created`, `address_created`,
`address_updated`, `address_moved`, `address_released`, `address_conflict`,
`address_conflict_cleared`, `packages_updated`, `hypervisor_linked`,
`hypervisor_unlinked`, `port_linked`, `port_unlinked`, `hostsync_run`,
`hostsync_settings_updated`, `hostsync_resync_requested`.

## SNMP credentials

A subnet can hold its own SNMP credentials (feature 021): SNMP v2c (community)
or SNMP v3 (user, `authNoPriv` or `authPriv`, authentication protocol MD5,
SHA-1, SHA-224/256/384/512 and password, privacy protocol DES or AES-128/192/256
and password; MD5, SHA-1 and DES are labelled weak). v3 passwords need at least
8 characters; no value may be empty or longer than 256 characters.

- **Storage**: table `ipam_subnet_snmp` (migration 0006, row-level security):
  version, level and protocols in clear, the secret values as one envelope
  blob sealed with the module KEK and bound to tenant and subnet, so a blob
  copied to another row or tenant does not open. Deleting the subnet deletes
  its credentials.
- **Write-only**: no response, event, backup, audit row or log line carries a
  community, v3 user or password; reads return only whether credentials are
  configured, their version, level and where the effective ones come from.
  Editing a subnet never touches its credentials.
- **Inheritance**: a subnet without its own credentials uses those of its
  nearest ancestor that has them (never across tenants). Subnet responses carry
  the read-only `snmp` summary (`none`, `own` or `inherited` with the source
  subnet) and `snmp_version` is the effective version.
- **Scans**: a scan with SNMP discovery resolves and opens the effective
  credentials once, when it starts, and records the SNMP phase on the job:
  `snmp_status` (`not_requested`, `no_live_hosts`, `no_credentials`,
  `credentials_unreadable`, `ran`), `snmp_source_subnet_id`, `snmp_probed`,
  `snmp_no_answer`, `snmp_rejected` and `snmp_discovered_count`.
- **Endpoints**: `GET /api/ipam/v1/subnets/{id}/snmp` (`ipam:read`),
  `PUT` set/replace and `DELETE` clear (`subnets:manage`),
  `POST /api/ipam/v1/subnets/{id}/snmp/test` (`scan:run`): probes one usable
  address inside the subnet within the SNMP timeout plus 2 seconds, at most 10
  tests per user per minute, and answers `ok` (sysName, sysDescr),
  `no_response`, `auth_failed`, `unknown_user`, `privacy_failed`,
  `no_credentials`, `credentials_unreadable` or `error`.
- **Audit**: `snmp_credentials_set`, `snmp_credentials_replaced`,
  `snmp_credentials_cleared`, `snmp_credentials_tested`, with neutral detail
  keys only (`protocol_version`, `security_level`, `previous_version`,
  `target`, `outcome`, `source_subnet_id`).
- **Backup**: exports carry the summary only; an import leaves SNMP
  unconfigured and lists the subnets to re-enter in `snmp_credentials_required`.
- **Legacy**: the never-functional warden reference `snmp_secret_ref` is no
  longer written or returned; at start ipam logs how many subnets still carry
  one so their credentials can be entered again.

## ARP-based MAC linking

Agentless hosts (printers, access points, cameras, BMCs, servers without the
inventory agent) get their MAC and switch port from the network (feature 022).

- **Collection**: a scan with SNMP discovery also walks each answering
  device's `ipNetToPhysicalTable` (IPv4 and IPv6 neighbours), falling back to
  the legacy `ipNetToMediaTable`, with the subnet's effective SNMP credentials
  and read-only requests, at most 65,536 entries per device per scan (a capped
  or interrupted read counts as partial).
- **Filters**: entries are ignored and counted by reason when the MAC is
  incomplete or invalid (`invalid`), multicast or broadcast (`multicast`),
  a VRRP or HSRP virtual-router MAC (`virtual_router`), a network device's own
  interface MAC (`network_device`), answers for more than the proxy threshold
  of IPs in one scan (`proxy_arp`, default 8), comes from an excluded device
  (`excluded_device`) or the IP is outside every subnet of the tenant
  (`outside_subnets`). ARP data never crosses tenants.
- **Provenance**: every address records `mac_source` (`agent` from the host
  sync, `manual` from the address API, `arp`), and for ARP the reporting
  device (`mac_source_device_id`) and `mac_seen_at`. ARP fills an empty MAC and
  updates a MAC it learned itself; it never changes an agent or manual MAC, and
  records a disagreement in `mac_conflict` instead (cleared when they agree
  again). An IP inside a known subnet without an address record gets one
  (status active, `origin: arp`); such addresses are never deleted
  automatically. MACs that existed before migration 0008 were backfilled as
  `agent` (host-reported addresses) or `manual`. The same IP reported by
  several devices: the last device in id order wins and the disagreement
  counts as a conflict.
- **Switch ports**: the switch-port correlation links every address with a
  MAC (any source) with the same rules as reported interfaces (fewest-MAC
  port, maximum MACs per access port, uplinks and network-device MACs
  excluded, stale links cleared). Addresses carry `link` (switch, port, VLAN,
  source, last seen); an address whose MAC a reported interface carries shows
  that interface's link; switch ports list `behind_addresses`.
- **Scan result**: `arp_status` (`ran`, `disabled`, `failed`), `arp_devices`,
  `arp_partial`, `arp_entries`, `arp_applied`, `arp_created`, `arp_conflicts`
  and `arp_ignored` (reason to count).
- **Search**: `GET /api/ipam/v1/ip-addresses?mac=` takes a full or partial
  MAC in colon, dash, dot or bare notation (2-12 hex digits; otherwise 422
  with `detail.field = mac`).
- **Settings**: `GET /api/ipam/v1/arp/settings` (`ipam:read`) and `PUT`
  (`subnets:manage`): `enabled` (default on), `excluded_devices` (up to 256
  existing devices never used as ARP sources) and `proxy_threshold` (2-256).
  Stored in `ipam_arp_settings` (row-level security); a tenant that never
  saved them uses the defaults.
- **Audit**: `mac_learned`, `mac_changed`, `mac_conflict`, `address_created`
  (`origin: arp`), `port_linked`/`port_unlinked` for addresses, one `arp_run`
  summary per scan (actor system/scan) and `arp_settings_updated` (the user),
  with neutral detail keys (`address`, `mac`, `previous_mac`, `observed_mac`,
  `source_device_id`, `job_id`).

## API permissions

`ipam:read`, `subnets:manage`, `addresses:manage`, `addresses:allocate`,
`devices:manage`, `vlans:manage`, `locations:manage`, `groups:manage`,
`scan:run`, `dns:manage`, `backup:manage`, `power:control`, `kvm:access`,
`hostsync:manage`. The gateway enforces the per-route permission from the
manifest; power, IPMI and KVM additionally require the platform-admin role.

## Roles

The module registers its permissions with auth at start and every five
minutes, together with ready-made module roles that auth offers in every
tenant (locked; administrators assign them or clone them into custom roles):

| Role | Display name | Permissions |
|---|---|---|
| `administrator` | IPAM administrator | all 14, including `power:control`, `kvm:access` (the handlers still require platform-admin for those) and `hostsync:manage` |
| `operator` | IPAM operator | `ipam:read`, `addresses:allocate`, `scan:run` |
| `viewer` | IPAM viewer | `ipam:read` |

Built-in role grants (scoped to IPAM by auth): `owner` and `admin` hold every
permission; `operator` holds everything except `power:control`, `kvm:access`
and `hostsync:manage`; `member` and `auditor` hold `ipam:read`.

## Versioning

- Service releases are tagged `vX.Y.Z`. CI publishes the image as `X.Y.Z`,
  `X.Y`, `X` and `sha-<short>`. There is no `latest` tag.
- The SDK is released separately with `sdk/vX.Y.Z` tags. These tags never build an image.
- v4.0.0 rebuilds the service on the go-tangra v4 platform. The v3 line stays on
  the `v3` branch and its `v3.x` tags.
