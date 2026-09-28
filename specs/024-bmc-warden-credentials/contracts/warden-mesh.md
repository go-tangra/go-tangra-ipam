# Contract: IPAM → Warden over the mesh (024)

No Warden code or proto change. IPAM uses the published Warden SDK
`github.com/go-tangra/go-tangra-warden/sdk/v4 v4.0.0`.

## RPCs used

| RPC | Purpose | Called when |
|---|---|---|
| `/warden.v1.Secrets/Get` (`GetRequest{id}` → `GetResponse{Secret}`) | metadata (name, username, folder_path, host_url) | `GET /devices/{id}/bmc`, `PUT /devices/{id}/bmc` (validation), before every BMC action |
| `/warden.v1.Secrets/GetPassword` (`GetPasswordRequest{id}` → `{password, version}`) | the password (Warden audits `secret_password_read`) | every power status/action, sensors, SEL, KVM session |

`Check` is not used.

## Call requirements

- Transport: Freya SPIFFE mTLS client connection `a.Freya.Client(ctx, "warden")`
  (discovery `warden: ["warden:9843"]`).
- Metadata: `authorization: Bearer <platform token of the signed-in user>`,
  taken from the incoming IPAM request (HTTP `Authorization` header, or
  gRPC incoming `authorization` metadata). No token → no call
  (`ErrNoUserToken`).
- Timeout: 5 s per call.
- Status mapping in IPAM: `NotFound`, `InvalidArgument` → not found;
  `PermissionDenied` "forbidden" (Warden's per-secret refusal),
  `Unauthenticated` → forbidden; `PermissionDenied` otherwise (mesh policy
  refused `svc/ipam`) and everything else → unavailable. Warden's message text is discarded.

## Policy (Warden inbound)

```yaml
  # ipam fetches device BMC credentials on behalf of the signed-in user
  # (feature 024). The call carries the user's platform token and passes
  # warden's per-secret check and audit.
  - id: ipam-bmc-secrets
    from: ["spiffe://example.org/svc/ipam"]
    to: ["warden"]
    operations: ["/warden.v1.Secrets/Get", "/warden.v1.Secrets/GetPassword"]
    effect: allow
```

Files: go-tangra-warden-v4 `deploy/policy.yaml` (image default),
go-tangra-docker `policies/warden.yaml` (dev copy; production renders it
into `prod/policies/warden.yaml` with its trust domain — existing servers
edit that file and restart warden).
