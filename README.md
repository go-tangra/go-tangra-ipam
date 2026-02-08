# go-tangra-ipam

IP Address Management (IPAM) service providing comprehensive network infrastructure management. Manages subnets, IP addresses, VLANs, devices, locations, and network scanning.

## Features

- **Subnet Management** — Hierarchical subnets with utilization tracking and tree navigation
- **IP Address Management** — CRUD, automatic allocation, bulk allocation, ping checks
- **Device Management** — Full device lifecycle with interfaces, firmware, rack position tracking
- **VLAN Management** — VLAN 1-4094 with status tracking and subnet associations
- **Location Hierarchy** — Region/Country/City/DataCenter/Building/Floor/Room/Rack
- **Network Scanning** — Async TCP port probing + reverse DNS discovery
- **IP/Host Groups** — Logical grouping of addresses, ranges, subnets, and devices
- **Multi-Tenant** — Complete tenant isolation across all resources

## gRPC Services

| Service | Endpoints | Purpose |
|---------|-----------|---------|
| SystemService | Health, Stats, DNS Config | System operations and statistics |
| SubnetService | CRUD, Tree, Stats, Scan | Subnet management with utilization |
| IpAddressService | CRUD, Allocate, BulkAllocate, Find, Ping | IP lifecycle management |
| DeviceService | CRUD, Interfaces, Addresses | Device and NIC management |
| VlanService | CRUD, GetSubnets | VLAN management |
| LocationService | CRUD, Tree | Physical location hierarchy |
| IpGroupService | CRUD, Members, CheckIp | Logical IP grouping |
| HostGroupService | CRUD, Members | Logical device grouping |
| IpScanService | Start, List, Cancel | Network discovery jobs |

**Port:** 9400 (gRPC) with REST endpoints via gRPC-Gateway

## Device Types

Server, Virtual Machine, Router, Switch, Firewall, Load Balancer, Access Point, Storage, Printer, Phone, Workstation, Container

## Network Scanner

```yaml
ipam:
  scan:
    enable_ping: true
    ping_timeout: "2s"
    max_workers: 50
    enable_dns_lookup: true
    dns_timeout: "5s"
  allocation:
    skip_first: 2        # Reserve .0 and .1
    skip_last: 1          # Reserve .255
  validation:
    allow_overlap: false
    require_gateway_in_range: true
```

Scans up to 1024 addresses per job. Probes TCP ports 22, 80, 443, 3389, 445. Auto-creates IP records for discovered hosts.

## Build

```bash
make build-server       # Build binary
make generate           # Generate Ent + Wire + proto descriptors
make docker             # Build Docker image
make docker-buildx      # Multi-platform (amd64/arm64)
make test               # Run tests
make ent                # Regenerate Ent schemas
```

## Docker

```bash
docker run -p 9400:9400 ghcr.io/go-tangra/go-tangra-ipam:latest
```

Runs as non-root user `ipam` (UID 1000). Alpine-based minimal image.

## Dependencies

- **Framework**: Kratos v2
- **ORM**: Ent (PostgreSQL, MySQL)
- **Cache**: Redis
- **Protobuf**: Buf for proto management
