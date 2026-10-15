---
title: Operate IPAM
summary: Configuration keys and defaults, the host sync, scheduled scans and KVM consoles, the service policies IPAM needs, and fixes for common problems.
kind: reference
order: 90
tags: [ipam, operations, configuration, policy, troubleshooting]
audience: operator
---
IPAM runs as `ipamsvc` (image `ghcr.io/go-tangra/go-tangra-ipam`).

```sh
ipamsvc -config /app/deploy/container.yaml             # serve; applies migrations
ipamsvc bootstrap -config /app/deploy/container.yaml   # apply migrations and exit
ipamsvc version
```

At start it enrolls for its SVID `spiffe://<trust domain>/svc/ipam`, migrates
its TimescaleDB schema, serves the browser API (through the gateway), the
`ipam.v1` gRPC API and the KVM console proxy (`/bmc/`), registers with the
gateway and registers its permissions and roles with auth (again every five
minutes).

Scans and ping send ICMP from the IPAM container. The binary carries the
`cap_net_raw` file capability; the container must keep `NET_RAW`.

## Configuration

The file is YAML. Unknown keys are refused, so a typo stops the service at
start with `config: <file>: ...`. Besides the framework keys (`service_name`,
`trust_domain`, `env`, `identity`, `authz`, `server`, `admin`, `discovery`,
`limits`) IPAM reads:

| Key | Default | Notes |
|---|---|---|
| `db.dsn` | — | required; in production `sslmode=verify-full` (or `verify-ca`) |
| `db.migrate_dsn` | `db.dsn` | owner connection used for migrations |
| `db.max_conns` | 16 | |
| `valkey.addresses` | — | required; the platform event bus |
| `valkey.username`, `valkey.password`, `valkey.ca_file` | — | Valkey user `ipam` over TLS |
| `valkey.allow_plaintext` | false | development only; refused in production |
| `kek.source` | `file` | `file` (with `kek.path`) or `env` (with `kek.env`): the 32-byte key that seals SNMP credentials and contact fields |
| `warden.service` | `warden` | where BMC passwords are read |
| `scan.max_hosts` | 1024 | 1–1048576; largest subnet (host addresses) a scan accepts |
| `scan.concurrency` | 50 | 1–4096 pings in flight per scan |
| `scan.timeout_ms` | 1000 | 10–60000 per ping |
| `scan.snmp_timeout_ms` | 5000 | 500–30000 per SNMP request and credentials test |
| `scan.workers` | 3 | 1–256 scans in parallel |
| `scan.max_retries` | 3 | 0–100 |
| `allocation.skip_first`, `allocation.skip_last` | 0 | 0–65536 addresses never allocated at the low and high end of every subnet |
| `ipmi.timeout_seconds` | 15 | 1–300 per BMC call |
| `kvm.token_ttl_seconds` | 60 | 5–3600, single-use console start token |
| `kvm.session_seconds` | 3600 | 60–86400, console session |
| `kvm.console_origin` | empty | the gateway console origin, for example `https://portal.example.com:8444` |
| `gateway.service` | `gateway` | mesh name of the gateway; the only peer allowed to fetch the help pages |
| `gateway.issuer` | — | required, an `https://` origin: the platform token issuer |
| `mesh_enroll.*` | — | `enabled`, `enroll_url`, `lcm_grpc`, `tenant_id`, `token_file`, `state_file`, `insecure` (refused in production) |
| `host_sync.*` | see below | |
| `task_scheduler.enabled` | false | register `ipam:scan-network` with the scheduler |
| `task_scheduler.service` | `scheduler` | |

These keys are accepted and range-checked but currently have no effect:
`warden.endpoint`, `allocation.reserved_ranges`, `events.enabled`,
`limits_ipam.max_request_bytes` and `limits_ipam.max_backup_bytes`.

### Host sync

IPAM pulls host reports from inventory and keeps devices, interfaces,
addresses, guests, hardware and switch-port links up to date.

| Key | Default | Range |
|---|---|---|
| `host_sync.enabled` | true | global switch; false applies nothing |
| `host_sync.inventory_service` | `inventory` | |
| `host_sync.poll_interval_seconds` | 60 | 10–3600 |
| `host_sync.workers` | 2 | 1–8 tenants in parallel |
| `host_sync.page_size` | 100 | 1–200 |
| `host_sync.pace_ms` | 10 | 0–10000 pause between hosts |
| `host_sync.request_timeout_seconds` | 30 | 1–300 |
| `host_sync.conflict_moves` | 3 | 2–100 moves that flag an address conflict |
| `host_sync.conflict_window_hours` | 24 | 1–168 |
| `host_sync.max_macs_per_port` | 16 | 1–256; ports with more MACs are never used for links |
| `host_sync.link_stale_days` | 14 | 1–365 |

Each tenant can also turn the sync off, set the full reconcile interval and
edit the interface exclusions on **IPAM → Host sync** (`hostsync:manage`).

## Service policies

IPAM's own policy must admit the gateway; the usual rule also covers the help
pages (`GET /docs/v1/bundle`):

```yaml
rules:
  - id: gateway-forwards
    from: ["spiffe://<trust domain>/svc/gateway"]
    to: ["ipam"]
    operations: ["*"]
    effect: allow
```

It also admits `dns` (`/ipam.v1.IpAddressService/Get`, `/Find` and
`/ipam.v1.SubnetService/Get`, for DNS record sync) and `scheduler`
(`/scheduler.v1.TaskExecutor/ExecuteTask`).

Other modules must admit `spiffe://<trust domain>/svc/ipam`:

| Module | Operations | Why |
|---|---|---|
| inventory | `/inventory.v1.HostReportService/ListReportTenants`, `/ListHostReports`, `/GetHostReport`, `/grpc.health.v1.Health/Check` | host sync; inventory must also list `ipam` in `host_reports.consumers` |
| warden | `/warden.v1.Secrets/Get`, `/warden.v1.Secrets/GetPassword` | BMC credentials, read as the signed-in user |
| scheduler | its `modules-register` rule | registering `ipam:scan-network` |

Peers need a `discovery.static` entry (`inventory`, `warden`, `scheduler`).

## KVM consoles

The BMC console is vendor JavaScript, so it is served only on the gateway's
console listener, never on the portal origin. Set `kvm.console_origin` to that
origin. Console sessions live in the IPAM process: run one instance (or
sticky routing) for consoles.

## Troubleshooting

**The service refuses to start with `config: ...`.** The message names the
key and its allowed range, for example `db.dsn must use sslmode=verify-full
(or verify-ca) in production` or `gateway.issuer must be an https origin`.

**The service refuses to start with `help docs: ...`.** The help pages built
into the binary are invalid. `tangra-docs lint` in CI catches this; rebuild
from a commit that passed CI.

**Host sync shows `degraded`.** The last error says why:
`permission_denied` (inventory's policy lacks the IPAM rule),
`inventory_outdated` (inventory older than 4.3.0) or `inventory_unavailable`
(unreachable). Nothing is written while it is degraded.

**Power, sensors and KVM answer `warden_unavailable`.** IPAM cannot reach
warden, or warden's policy does not allow `Secrets/Get` and
`Secrets/GetPassword` for IPAM. The log says `warden client unavailable` at
start when there is no connection.

**The Power / KVM tab says "KVM console origin not configured".**
`kvm.console_origin` is empty; the log warns about it at start.

**Scans retry and fail with `icmp: raw socket unavailable (needs CAP_NET_RAW)`.**
The container lacks `NET_RAW`. Add it to the container's capabilities.

**Scans complete but find no live hosts.** ICMP is blocked between IPAM and
the subnet, or the hosts do not answer ping.

**The log says `legacy snmp_secret_ref present`.** Some subnets still carry
the old warden reference for SNMP, which is no longer used. Enter their SNMP
credentials again on the subnet.

**Scheduled scans never run.** Check `task_scheduler.enabled`,
`discovery.static.scheduler` and the scheduler's `modules-register` rule.
