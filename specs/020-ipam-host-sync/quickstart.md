# Quickstart: validating the host sync (020)

## Automated

- **inventory** (`go-tangra-inventory-v4`): `make test` (agent facts with fake
  sysfs/procfs trees and recorded command output, IPMI fake asserting the
  parameter selectors, ingest validation, projection + digest,
  `HostReportService` handlers incl. non-consumer denial), `make cover`
  (gate incl. `internal/hostreport` 100 %), `make fuzz` targets
  (`FuzzProxmoxConf`, `FuzzAptSimulate`, `FuzzDnfOutput`,
  `FuzzApkPacmanOutput`, `FuzzNetlinkAddr`, `FuzzBmcLanParams`,
  `FuzzSubmitMapper`, `FuzzHostReport`), `make test-integration` (migration
  0005 on a 0004 database, digest bump on ingest, `ListHostReports` paging),
  `buf lint` + `buf breaking --against '.git#tag=sdk/v4.0.0,subdir=sdk'`,
  agent cross-compile matrix, `make vuln`.
- **ipam**: `make test`, `make cover` (100 % for `internal/authz`,
  `internal/sealed`, `internal/ipnet`, `internal/hostreport`,
  `internal/hostplan`), fuzz (`FuzzNormalizeReport`, `FuzzPlan`,
  `FuzzExclusionPattern`, `FuzzMostSpecific` + existing), `make
  test-integration` (0004/0005 on a 0003 database with data, RLS on new
  tables, apply/move/release/conflict against PostgreSQL, disable race,
  end-to-end with an in-process inventory `HostReportService` over the
  framework test mesh, 1000-host benchmark), `buf breaking`, `cd ui && npm
  run lint && npm run test:unit && npm run build`, `make vuln`.

## Manual (freya-stack)

Prerequisites: inventory ≥ 4.3.0 and ipam ≥ 4.3.0 deployed, the
`ipam-hostsync` rule in inventory's policy, an operator signed in with
IPAM administrator rights, a Linux test host (ideally a Proxmox node with a
BMC, two guests: one with the agent, one without) and a Windows host.

1. **US1** — Enroll the new agent on the Linux host
   (`inventory-agent -ingest <edge>:9977 -token <file> -daemon`). Within
   5 minutes: IPAM → Devices shows the host (type, OS, manufacturer, model,
   serial), its interfaces with MACs and kinds, each address in a subnet,
   one primary; the device view shows "Source: host report · inventory host
   … · last report …". An address in an unknown network created a subnet
   with origin "host sync". Audit lists `device_created`,
   `interface_created`, `subnet_created`, `address_created` with actor
   `hostsync`.
2. Add an address on the host (`ip addr add 10.99.0.10/24 dev eth0`) and
   trigger a report (Inventory → host → Refresh). Within a minute IPAM shows
   the address in `10.99.0.0/24`; remove it and refresh → the address shows
   "no longer reported", unlinked from the device, still listed.
3. Give the device a description, tag, location and host group in IPAM;
   refresh the host → those fields are unchanged; `docker0`/`veth*`
   addresses were not recorded.
4. Assign one of the host's addresses to another device by hand, refresh →
   the address moves back; audit `address_moved` names the previous device.
   Repeat three times → the address shows a conflict badge.
5. **US2** — On the BMC host (`modprobe ipmi_devintf ipmi_si`), refresh →
   management address = BMC address, interface `bmc` (kind management) with
   its MAC, the BMC address in its subnet. `strace -f -e ioctl` of one agent
   run shows only Get Channel Info and Get LAN Config parameters 3/4/5/6/12/20.
6. **US3** — On the Proxmox node: device view → Guests tab lists both
   guests; the guest with the agent links to its device ("runs on <node>"),
   the other shows name, VMID and MACs; enroll the agent on the second guest
   → it links automatically. A VM reports type VM with kind kvm.
7. **US4** — Hold back a package on the Debian host → Packages tab lists
   installed/available versions, security updates highlighted; `touch
   /run/reboot-required` → "Reboot required"; after updating and rebooting,
   the next report clears both. The Windows host shows update state
   "unknown".
8. **US6** — Host sync page: status ok, last poll; disable the sync; change
   an address on the host and refresh → IPAM unchanged; re-enable → the
   change is applied by the next reconcile. As an IPAM operator: settings
   are read-only, "Re-sync" on a device works; as a viewer: no re-sync
   button, API → 403. Stop inventory → status degraded, IPAM data intact;
   start it → ok.
9. **US5** — With SNMP configured on the switch subnet, run a scan with
   SNMP → the host interface shows "connected to <switch> <port> (VLAN n)";
   the switch uplink port is not linked to any host.
10. **SC-006** — (lab) import 1000 synthetic hosts with the inventory load
    generator (`go run ./tests/loadgen -hosts 1000`), enable the sync, time
    until status shows 1000 hosts reported (< 15 min) while browsing IPAM
    devices stays responsive.
