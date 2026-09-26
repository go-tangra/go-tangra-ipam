# Contract: service policy and gateway registration (020)

Freya policies are **inbound**: the rule allowing a caller lives in the
callee's `deploy/policy.yaml` (asset → inventory is `asset-sync` in
inventory's file).

## go-tangra-inventory-v4 `deploy/policy.yaml`

```yaml
  - id: ipam-hostsync
    # ipam pulls host reports (projection of the latest snapshot) to keep
    # devices, interfaces and addresses current (ipam feature 020, D4/D5).
    from: ["spiffe://example.org/svc/ipam"]
    to: ["inventory"]
    operations: ["/inventory.v1.HostReportService/ListReportTenants",
                 "/inventory.v1.HostReportService/ListHostReports",
                 "/inventory.v1.HostReportService/GetHostReport",
                 "/grpc.health.v1.Health/Check"]
    effect: allow
```

Defence in depth: `ListReportTenants` also checks the caller's service
name against `host_reports.consumers` (default `["ipam"]`), because the
existing `gateway-forwards: "*"` rule would otherwise admit the gateway.

## go-tangra-ipam-v4 `deploy/policy.yaml`

No rule change (nothing new calls IPAM). Header comment: "ipam calls
warden (secret refs), auth, lcm and inventory (host reports) as a client".

## go-tangra-docker (production — not edited by this feature)

- `policies/inventory.yaml` (copied by `prod-init.sh` into `prod/policies/`
  with the real trust domain): add the `ipam-hostsync` rule above when
  pinning inventory ≥ 4.3.0. Without it IPAM reports `degraded`
  (`inventory_unavailable`/`permission_denied`) and writes nothing.
- `policies/ipam.yaml`: header comment only.
- IPAM config: `host_sync:` section (defaults are usable; set
  `enabled: false` to keep the feature off at rollout).
- Hosts with BMCs: load `ipmi_devintf` and `ipmi_si` for BMC collection.

## Gateway (IPAM manifest)

- Permission `hostsync:manage` registered with auth through
  `ipammanifest.Registration()` (module-scoped).
- Routes from the OpenAPI extensions (contracts/ipam-http.md).
- No gRPC `Methods` (unchanged).
