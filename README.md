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

SNMP and BMC credentials are never stored by ipam. Subnets and devices hold only
warden secret references (`snmp_secret_ref`, `ipmi_secret_ref`), resolved at use
time and never logged, audited or exported. The gRPC binding to warden's
`warden.v1.Secrets` is not wired yet (`internal/warden`): until it is, secret
resolution fails closed and SNMP/IPMI/KVM operations that need a credential are
refused.

Operations: [`deploy/README.md`](deploy/README.md).
Design history: `specs/011-ipam-service` (walkthrough in `quickstart.md`).

## Place in the platform

```
go-tangra/go-tangra          platform module + @go-tangra/ui kit
        |
go-tangra-auth  <---->  go-tangra-portal (gateway)  <---->  go-tangra-ipam
                              |                              |       ^
                         go-tangra-lcm (SVIDs)        go-tangra-warden   dns (ipam sdk)
```

- Built on `github.com/go-tangra/go-tangra/v4` (mTLS transports, identity,
  service policy, audit, observability).
- Verifies platform tokens and registers its permissions, module roles and
  built-in role grants with the auth SDK (`github.com/go-tangra/go-tangra-auth/sdk/v4`).
- Registers with the gateway through the portal SDK
  (`github.com/go-tangra/go-tangra-portal/sdk/v4`), which fronts the browser API
  (`/api/ipam`), the KVM proxy (`/bmc/`) and the federated UI remote.
- Enrolls for its workload identity with lcm (`github.com/go-tangra/go-tangra-lcm/sdk/v4`).

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
| `internal/scan` | scan executor and ICMP/SNMP/TCP probes |
| `internal/ipmi`, `internal/kvm` | BMC power/inventory and the KVM console proxy |
| `internal/warden` | secret-reference client (plus an in-memory fake) |
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
`internal/{authz,sealed,ipnet}`. Generated code, SQL bindings, wiring and the
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

## API permissions

`ipam:read`, `subnets:manage`, `addresses:manage`, `addresses:allocate`,
`devices:manage`, `vlans:manage`, `locations:manage`, `groups:manage`,
`scan:run`, `dns:manage`, `backup:manage`, `power:control`, `kvm:access`. The
gateway enforces the per-route permission from the manifest; power, IPMI and
KVM additionally require the platform-admin role.

## Roles

The module registers its permissions with auth at start and every five
minutes, together with ready-made module roles that auth offers in every
tenant (locked; administrators assign them or clone them into custom roles):

| Role | Display name | Permissions |
|---|---|---|
| `administrator` | IPAM administrator | all 13, including `power:control` and `kvm:access` (the handlers still require platform-admin for those) |
| `operator` | IPAM operator | `ipam:read`, `addresses:allocate`, `scan:run` |
| `viewer` | IPAM viewer | `ipam:read` |

Built-in role grants (scoped to IPAM by auth): `owner` and `admin` hold every
permission; `operator` holds everything except `power:control` and
`kvm:access`; `member` and `auditor` hold `ipam:read`.

## Versioning

- Service releases are tagged `vX.Y.Z`. CI publishes the image as `X.Y.Z`,
  `X.Y`, `X` and `sha-<short>`. There is no `latest` tag.
- The SDK is released separately with `sdk/vX.Y.Z` tags. These tags never build an image.
- v4.0.0 rebuilds the service on the go-tangra v4 platform. The v3 line stays on
  the `v3` branch and its `v3.x` tags.
