# v3 client parity (SC-007)

Every capability of the v3 `go-tangra-client` + v3 ipam listed in spec.md
§Context, with the task and the test that prove it in v4. IPAM side in this
repository; agent collection in go-tangra-inventory-v4 (`inventory:` prefix).

| # | v3 capability | v4 tasks | Proof (tests) |
|---|---|---|---|
| 1 | Host created as a device (server / VM / container, OS, serial, primary address) | T013–T021, T054–T055 (inventory); T045–T046, T058–T060 | `internal/hostplan/device_test.go` (TestCreateDevice, TestDeviceTypeRules, TestVirtualizationKind), `internal/hostplan/match_test.go`, `internal/hostsync/e2e_integration_test.go` (TestHostSyncEndToEnd) |
| 2 | Missing subnets created | T047, T058 | `internal/hostplan/address_test.go` (TestAutoSubnet), e2e (auto subnet with origin `host_sync`) |
| 3 | Every address recorded with its MAC and interface | T042–T043 (inventory); T047, T059 | `internal/hostplan/address_test.go` (TestAddressPlacementMostSpecific, TestAddressSkips, TestAddressMoveClaimAndConflict, TestAddressRelease), `internal/hostsync/runner_test.go` (TestPollAppliesNewTenantReports) |
| 4 | BMC/IPMI address recorded | T077–T083 (inventory); T080, T084–T085 | `internal/hostplan/bmc_test.go` (TestBMCRecorded, TestBMCAbsentLeavesManagementUntouched), inventory `internal/collector/bmc_linux_test.go` (credential-free parameter set) |
| 5 | Proxmox guests linked to their hypervisor | T086–T092 (inventory); T088–T095 | `internal/hostplan/virt_test.go`, `internal/hostsync/resync_test.go` (TestAdminGuestsAndConflicts: guest's own report links it), `ui/tests/unit/hostsync.spec.ts` (Guests tab) |
| 6 | Pending updates and reboot required | T096–T102 (inventory); T098–T104 | `internal/hostplan/updates_test.go`, `internal/repo/repodb/hostsync_integration_test.go` (5000-row replace), `ui/tests/unit/hostsync.spec.ts` (update chip, security filter) |
| 7 | Switch port a host is connected to (MAC tables) | T105–T112 | `internal/portlink/rank_test.go`, `internal/portlink/portlink_test.go`, `internal/repo/repodb/links_integration_test.go` (0005 fix + scan → link) |

Improvements over v3 (not parity items): report validation and bounds
(`internal/hostreport`), transactional audit of every change, conflict
detection for flapping addresses, release instead of overwrite, LLDP
precedence for port links, no package-list refresh by the agent, credential-free
BMC reads, per-tenant enable/disable and exclusions.
