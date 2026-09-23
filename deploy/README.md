# IPAM service — operations

The **ipam** service is a tenant-scoped **IP Address Management** platform module.
It manages hierarchical subnets (with utilization), IP addresses (first-free and
bulk allocation, conflict detection, find/suggest), devices (interfaces, L2 links,
OS packages), VLANs, a physical location hierarchy, and IP/host groups; and it
performs **active network operations** — asynchronous discovery scanning
(ICMP/SNMP/TCP), ping, out-of-band IPMI/BMC power control, and a KVM console
proxy. It registers with the application gateway (browser API under `/api/ipam`)
and exposes a service-to-service gRPC API (`ipam.v1`, not gateway-proxied).

## Running

```
ipamsvc -config deploy/container.yaml     # run (applies migrations)
ipamsvc bootstrap -config <cfg>           # apply migrations and exit
```

In the containerized platform stack it comes up with one command; see
`deploy/stack/README.md`. The service:

- enrolls for its mesh SVID (`spiffe://<td>/svc/ipam`) over lcm,
- migrates its TimescaleDB schema (per-tenant RLS; all unique constraints from the
  source preserved as the conflict-detection layer),
- serves the browser API (via the gateway) and `ipam.v1` gRPC on `:9985`, with
  admin health/readiness on `:9820`,
- runs the scan-executor worker pool,
- registers routes/permissions/abilities/nav with the gateway and seeds its API
  permissions into auth,
- mounts the token-gated **KVM console proxy** at `/bmc/`.

## Active network operations (the key security surface)

These reach real hosts and are authorized, tenant-scoped, bounded and audited:

- **Discovery scanning** — an async worker pool sweeps a subnet's host addresses
  via ICMP (raw sockets; the binary is granted `cap_net_raw+ep`), bounded to at
  most `scan.max_hosts` (1024) IPv4 hosts, with per-probe timeout and bounded
  concurrency; alive hosts are reverse-resolved and upserted as IP records and
  `ipam.ip_address.scanned` events are published. Optional **SNMP** discovery
  (using the subnet's credential reference) creates/updates devices and
  interfaces and correlates Layer-2 links. Retry-with-backoff and cancel.
- **Ping / suggest** — ICMP reachability and free-address suggestion (ICMP + TCP).
- **IPMI/BMC power** — read info/power-status/sensors/event-log and control power
  (on/off/cycle/reset/soft/diag). **Platform-admin only.**
- **KVM console proxy** — a platform admin starts a session; a token-gated proxy
  streams the device BMC HTML5 console under the platform origin so the browser
  never sees BMC credentials.

Sandboxing: active operations are tenant-scoped and constrained to the tenant's
own subnets/devices; power/KVM/IPMI require the platform-admin role; every active
operation is audited; the raw-socket capability is the only elevated host
privilege and is confined to the scanner.

## Configuration

`container.yaml` sections: `db`, `valkey`, `kek` (envelope key), `warden`
(secret-reference service), `scan` (max_hosts, concurrency, timeout, workers,
retries), `allocation` (skip_first/skip_last, reserved ranges), `ipmi`
(timeout), `kvm` (token/session TTLs), `events`, `gateway`, `mesh_enroll`, and
`limits_ipam`. Framework `server`/`admin`/`discovery` supply the mesh listeners.

## Secrets

BMC/IPMI and SNMP credentials are NEVER stored in `ipam_*` columns. A device
holds only `ipmi_secret_ref` and a subnet holds `snmp_secret_ref` — ids of
secrets held in **warden**. Values are fetched at use time via the warden client
and never returned, logged, audited or exported. Owner/contact fields are
sealed/redacted.

## Allocation & conflict detection

First-free allocation enumerates the CIDR, excludes network/broadcast/gateway,
configured reserved and skip-first/skip-last ranges, and already-allocated
addresses, and picks the lowest free (race-safe via the `(tenant_id,address)`
unique index — a raced insert retries the next free). Utilization = used/total.
Conflict detection (duplicate IP/VLAN/subnet/device/group) is enforced by the
preserved unique constraints; subnet overlap and gateway-in-range are validated.

## Backup

`POST /api/ipam/v1/backup/export` exports the tenant's subnets, addresses,
devices, VLANs, locations and groups, versioned by schema; secret references'
values are never included. `POST /api/ipam/v1/backup/import` recreates them
(mode `skip` or `overwrite`), preserving ids.

## UI

The remote under `services/ipam/ui` is built on the shared kit `@freya/ui` (FlyonUI + Zod,
see `docs/frontend.md`): forms validate through Zod schemas in `src/schemas/`, the
shell provides the theme and shared singletons, and `npm run lint` runs
`check-no-legacy`. Rebuild the image after UI changes; the Dockerfile builds `ui/kit`
first.
