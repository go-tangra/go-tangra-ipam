# Quickstart: IPAM Service

Validation scenarios proving the feature end to end. Assumes the `deploy/stack`
platform is up (TimescaleDB, Valkey, gateway, auth, lcm, warden) with the ipam
service registered, and a small lab subnet reachable for the scan/active-op scenarios.

## Prerequisites
- ipam service running (gateway `/api/ipam`; scanner has CAP_NET_RAW).
- An operator signed in with ipam:read + the relevant :manage permissions; a
  platform-admin for the power/KVM scenarios.
- For active ops: a reachable lab subnet, and (for power/KVM) a device with a BMC
  and its credentials stored in warden (referenced by ipmi_secret_ref).

## Scenario 1 — Subnets & allocation (US1)
1. Create a subnet from a /24 CIDR; confirm derived network/broadcast/total and zero utilization; a duplicate name and an overlapping CIDR are rejected.
2. Allocate the next free address twice → two distinct host addresses (network/broadcast/gateway/reserved excluded). Re-creating one → rejected (duplicate). Read subnet stats → used/available/utilization reflect the two.
3. Bulk-allocate 5 addresses with a hostname prefix; on a full subnet, allocation returns "no available addresses".

## Scenario 2 — Devices, interfaces, addresses (US2)
1. Create a device (type/location/rack); add two interfaces; attach an address to one; list the device's interfaces and addresses; counts reflect them.
2. Sync OS packages for the device; read package stats (updates/security/reboot-required).

## Scenario 3 — VLANs, locations, groups (US3)
1. Create a VLAN (id 100) and link a subnet; list the VLAN's subnets. Duplicate id or name → rejected.
2. Build a location tree (Region→DC→Rack); read it nested with counts.
3. Create an IP group with an address, a range and a subnet member; check an in-range IP → returns the group. Create a host group with member devices; list its members (enriched).

## Scenario 4 — Network discovery scan (US4)
1. Start a scan of the lab subnet; watch the job pending→scanning→completed with progress; alive hosts appear as IP addresses (with reverse-DNS hostnames); `ipam.ip_address.scanned` events published.
2. Enable SNMP with the subnet's credential reference; confirm discovered devices/interfaces (+ L2 links) are created/updated.
3. Start a scan of an over-large (> bound) or IPv6 subnet → refused. Cancel a running scan → status cancelled.

## Scenario 5 — Out-of-band power & KVM (US5, platform-admin)
1. As platform-admin, read a device's BMC power status and sensors (creds fetched from warden at use time, never returned). As a non-admin → refused.
2. Issue a power action (e.g. cycle) → executed and audited. Inspect the audit trail: actor, tenant, target, outcome present; no credentials.
3. Start a KVM session → {token, console_url}; open the console URL → the BMC console proxies under the platform origin; the browser never receives BMC credentials.

## Scenario 6 — DNS config, stats, backup (US6)
1. Set the tenant DNS config and test it (live reverse lookup of a test IP → hostname + latency).
2. Read statistics → subnet/address/vlan/device/location totals, overall utilization, devices-by-type.
3. Export the tenant; import into a clean tenant (skip vs overwrite); confirm subnets/addresses/devices/VLANs/locations/groups round-trip with no secrets and no cross-tenant leakage.

## Security checks (cross-cutting)
- SNMP/BMC credentials never appear in any response, log, audit or backup.
- An active op targeting a host outside the tenant's own subnets/devices is refused; a scan never exceeds the host bound.
- Power/KVM/IPMI refused for non-platform-admins; every active op is in the audit trail.
- Cross-tenant reads return nothing; RLS isolates all data.
