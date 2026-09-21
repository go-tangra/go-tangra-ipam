# Phase 1 Contracts: IPAM Service

Surfaces: (A) browser/query HTTP API via the gateway under `/api/ipam`; (B)
module-to-module gRPC (`ipam.v1`, SPIFFE mTLS); (C) the token-gated KVM console
proxy; plus (D) events and (E) the active-operation client interfaces.

## A. Browser/query HTTP API — prefix `/api/ipam/v1` (gateway-proxied)

Subnets (perm subnets:manage for writes, ipam:read for reads)
- `GET /subnets` (filters vlan_id/parent_id/location_id/status/ip_version, cursor/limit), `POST /subnets`, `GET/PUT/DELETE /subnets/{id}` (delete `force`), `GET /subnets/tree`, `GET /subnets/{id}/stats`, `POST /subnets/{id}/scan` (scan:run — synchronous discovery).

IP addresses (ipam:read / addresses:manage; allocate = addresses:allocate)
- `GET /ip-addresses` (filters subnet_id/device_id/status/type/prefix/hostname), `POST /ip-addresses`, `GET/PUT/DELETE /ip-addresses/{id}`, `POST /ip-addresses/allocate` (next-free), `POST /ip-addresses/bulk-allocate`, `GET /ip-addresses/find?address=`, `GET /ip-addresses/suggest` (ICMP+TCP verified), `POST /ip-addresses/{id}/ping`.

Devices (devices:manage / ipam:read)
- `GET /devices` (filters device_type/status/location_id/manufacturer/rack_id), `POST /devices`, `GET/PUT/DELETE /devices/{id}` (force), `GET /devices/{id}/addresses`, `GET /devices/{id}/interfaces`, `POST /devices/{id}/interfaces`, `DELETE /devices/{id}/interfaces/{ifid}`, `POST /devices/{id}/packages/sync`, `GET /devices/{id}/packages`, `GET /devices/{id}/host-groups`, `GET /warden-secrets` + `GET /warden-secrets/{id}` (metadata only, via warden).
- Out-of-band (platform-admin): `GET /devices/{id}/power` (power:control), `POST /devices/{id}/power` {action on|off|cycle|reset|soft|diag}, `GET /devices/{id}/sensors`, `GET /devices/{id}/sel`, `POST /devices/{id}/kvm-session` (kvm:access → {token, console_url}).

VLANs (vlans:manage): `GET/POST /vlans`, `GET/PUT/DELETE /vlans/{id}`, `GET /vlans/{id}/subnets`.
Locations (locations:manage): `GET/POST /locations`, `GET/PUT/DELETE /locations/{id}`, `GET /locations/tree`.
IP groups (groups:manage): CRUD `/ip-groups` (+ `include_members`), `POST/DELETE/GET/PUT /ip-groups/{id}/members`, `GET /ip-groups/check?ip=`.
Host groups (groups:manage): CRUD `/host-groups`, member ops.
Scans (scan:run / ipam:read): `POST /ip-scans` (start async), `GET /ip-scans/{id}`, `GET /ip-scans` (filters subnet_id/status), `POST /ip-scans/{id}/cancel`.
System (ipam:read / dns:manage): `GET /health` (public), `GET /stats`, `GET /dns-config`, `PUT /dns-config` (dns:manage), `POST /dns-config/test`.
Backup (backup:manage): `POST /backup/export`, `POST /backup/import` (mode skip|overwrite).
Realtime: `GET /stream` (SSE, ipam:read).

Mutating routes carry the platform CSRF header; the OpenAPI declares body-size
limits. No response includes SNMP/BMC credentials or sealed owner/contact fields.

## B. Module-to-module gRPC — `ipam.v1` (SPIFFE mTLS, not gateway-proxied)
- `SubnetService`: Create/Get/List/Update/Delete, GetTree, GetStats, Scan
- `IpAddressService`: Create/Get/List/Update/Delete, AllocateNext, BulkAllocate, Find, Suggest, Ping
- `DeviceService`: Create/Get/List/Update/Delete, GetAddresses, GetInterfaces, Create/DeleteInterface, SyncPackages, ListPackages, PowerStatus, Power, StartKvmSession
- `VlanService`: Create/Get/List/Update/Delete, GetSubnets
- `LocationService`: Create/Get/List/Update/Delete, GetTree
- `IpGroupService` / `HostGroupService`: CRUD + member ops + CheckIp/ListDeviceHostGroups
- `IpScanService`: Start, Get, List, Cancel
- `SystemService`: Health, GetStats, GetDnsConfig, UpdateDnsConfig
Every request carries `tenant_id`; caller identity from the mTLS peer. Messages
never carry credentials. Power/KVM RPCs require the platform-admin role.

## C. KVM console proxy (token-gated)
`StartKvmSession` (platform-admin) → {token, console_url}. A dedicated HTTP handler
(`/api/ipam/kvm/{device_id}/...`, token-gated, NOT the normal permission chain)
reverse-proxies the device BMC HTML5 console + WebSocket under the platform origin.
The proxy logs into the BMC server-side (creds from warden), injects the session,
and streams; the browser gets only a short-lived cookie/token. Self-signed BMC
certs are accepted only by this isolated client.

## D. Event contract (platform bus `platform:events:<tenant>`)
- `ipam.ip_address.created|updated|deleted|scanned` {id,type,source,timestamp,tenant_id,data{address,subnet_id,hostname,device_id}} — `scanned` distinguishes discovery from manual edits (for DNS sync).
- `ipam.scan.started|completed` {job_id,subnet_id,alive_count,new_count}.
Consumed by DNS-sync + the gateway SSE hub → `/stream`. No credentials in payloads.

## E. Active-operation client interfaces (internal, fronted by fakes)
- `icmp.Pinger.Ping(ctx, ip) (alive bool, rttMs int, err error)`; `icmp.Sweeper.Sweep(ctx, ips []net.IP, concurrency int, timeout) (alive map[string]bool, err error)` — one shared raw socket, receiver goroutine.
- `tcp.PortScanner.Scan(ctx, ip, ports []int, timeout) (open []int, err error)`.
- `snmp.Discoverer.Discover(ctx, ip, creds) (Device, []Interface, []Link, err error)` — v2c/v3 walks + FDB/LLDP.
- `ipmi.BMC.Info/PowerStatus/Sensors/SEL(ctx, host, creds)`, `ipmi.BMC.Power(ctx, host, creds, action)`.
- `kvm.Proxy` — session mint + reverse-proxy handler.
- `warden.Client.GetSecret(ctx, ref) (value, err)` / `ListSecrets(ctx) ([]meta)`.
Each has a Fake used by unit tests so allocation, scan orchestration, authorization,
event-publishing and redaction are tested without touching the network.

## Authorization matrix (enforced at the service boundary)
- reads → ipam:read; entity writes → <entity>:manage; allocate → addresses:allocate;
  scan start/subnet-scan → scan:run; dns update → dns:manage; backup → backup:manage.
- power status/actions + IPMI + KVM → platform-admin (power:control / kvm:access).
- Every active op additionally checks: target subnet/device belongs to the caller's
  tenant, and the reached host is within the tenant's own subnets/device mgmt IPs;
  scans bounded ≤1024 IPv4 hosts. All active ops audited.
