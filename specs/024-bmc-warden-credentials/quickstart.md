# Quickstart: BMC Credentials from Warden (024)

## Automated checks

```bash
cd go-tangra-ipam-v4
unset GOROOT
go vet ./... && go test -race -count=1 ./...
sg docker -c 'unset GOROOT; cd '"$PWD"' && make test-integration'
make cover     # internal/bmc joins the 100 % list
make vuln
cd ui && export NODE_AUTH_TOKEN=$(gh auth token) && npm run lint && npm run test:unit && npm run build

cd ../go-tangra-warden-v4 && go test -race ./internal/app/ -run Policy
```

Key tests: `internal/warden` (token forwarding, code mapping, redaction),
`internal/bmc` (address, reasons, set/clear, no BMC contact on refusal),
`internal/httpapi/bmc_test.go` (routes, reasons, audit),
`internal/httpapi/bmc_leak_test.go` (SR-002), `internal/grpcapi` (power
RPCs with forwarded metadata), UI `DeviceBmcCard`, `WardenSecretPicker`,
`ipmi-kvm` states.

## Stack setup (freya-stack / go-tangra-docker dev)

1. Warden policy contains rule `ipam-bmc-secrets` (policies/warden.yaml,
   or the image default from warden ≥ 4.4.2); restart warden after editing.
2. `configs/ipam.yaml` lists `warden: ["warden:9843"]` (already present).
3. Start ipam with the new image.

## Scenario 1 — attach a secret (US1)

1. In Warden, create secret "zax-5 IPMI" (username `ADMIN`, password = BMC
   password) and grant read access to the admins who may use it.
2. IPAM → Devices → node-1 → Overview → **BMC credentials → Attach**.
3. Search "zax" → pick "zax-5 IPMI" → Save.
4. Expect: card shows "zax-5 IPMI · ADMIN · /…", BMC address
   `10.1.112.14 (reported by agent)`; device list row "BMC: zax-5 IPMI (Warden)".
5. Audit: `bmc_reference_set` row for node-1 without any password.

Negative: `curl -X PUT …/devices/<id>/bmc -d '{"reference":"<secret the user cannot read>"}'`
→ `403 {"reason":"bmc_secret_forbidden"}`; the device keeps its old reference.

## Scenario 2 — power, sensors, KVM (US2)

1. Open the **Power / KVM** tab: power state and sensor table load.
2. **Power cycle** → confirm → accepted; audit `power_action` (action
   `cycle`, outcome `ok`); Warden audit shows `secret_password_read` by the
   same user.
3. **Start session** opens the console.
4. As a platform admin without Warden access to the secret: the tab says
   "No access to the BMC credentials" and no IPMI packet is sent.

## Scenario 3 — explained states (US3)

| Setup | Tab shows |
|---|---|
| no reference | "No BMC credentials configured — attach a Warden secret" (+ Attach for managers) |
| no management IP and no agent-reported BMC | "No BMC address — set the management IP or let the agent report the BMC" |
| secret deleted in Warden | "The Warden secret no longer exists — attach another one" |
| warden stopped | "Warden is unavailable — try again later" |
| BMC address unreachable | "BMC 10.1.112.14 did not answer" |
| wrong password in the secret | "BMC 10.1.112.14 rejected the credentials — check the Warden secret" |

The browser console shows no unexplained 422.
