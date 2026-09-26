# Contract: inventory agent collection (020)

Repository go-tangra-inventory-v4. What the agent reads, per platform, with
bounds. Pure parsing/rules live in `internal/agentfacts` (unit + fuzz
tested, in the coverage gate); OS access lives in `internal/collector`
(`*_linux.go`, `*_windows.go`, `*_other.go`). Every collector is best effort
(error → field absent), context-bounded, and never blocks the snapshot.

## Never collected (SR-005)

BMC users, passwords, cipher suites, authentication type settings, the LAN
community string (IPMI LAN parameter 16), SNMP settings, any file under
`/etc/pve/priv`, any secret, token or key. The agent never loads kernel
modules and never changes system state.

## Network (FR-001, FR-002)

| Item | Linux | Windows |
|---|---|---|
| interfaces | `/sys/class/net/*` (name, `address`, `operstate`) | `GetAdaptersAddresses` |
| kind | `type` (772 loopback), `bonding/` bond, `bridge/` bridge, `wireless/` or `phy80211` wireless, `/proc/net/vlan/config` vlan, no `device` link → virtual, else ethernet | `IfType` 6 ethernet, 71 wireless, 24 loopback, 53/131 virtual; description "Multiplexor" → bond; `vEthernet` → virtual |
| speed | `speed` (Mbps; -1/absent → 0) | `TransmitLinkSpeed` |
| master / vlan id | `master` symlink; vlan config | — |
| addresses + prefix + flags | `syscall.NetlinkRIB(RTM_GETADDR)`: `IFA_ADDRESS/IFA_LOCAL`, prefixlen, `IFA_FLAGS`/`ifa_flags` (`PERMANENT` absent → dhcp, `TEMPORARY`, `DEPRECATED`), scope | unicast list: `OnLinkPrefixLength`, `PrefixOrigin/SuffixOrigin` (dhcp, random → temporary), `DadState` deprecated |
| default gateway | `syscall.NetlinkRIB(RTM_GETROUTE)` dst len 0: `RTA_GATEWAY`, `RTA_OIF`, `RTA_PRIORITY` | `FirstGatewayAddress`, `Ipv4Metric`/`Ipv6Metric` |
| primary_ipv4/6 | first global, non-temporary, non-deprecated address of the lowest-metric default-route interface | same |

Bounds: 256 interfaces, 64 addresses per interface; 5 s budget.

## Virtualization (FR-003)

`agentfacts.DetectVirtualization(Facts)` — first rule that matches:
1. container: `/.dockerenv` → docker; `/run/.containerenv` → podman;
   `/proc/1/environ` `container=<x>` → x (lxc, systemd-nspawn, podman,
   docker); `/proc/1/cgroup` contains `docker`/`lxc`/`kubepods` → that kind.
2. WSL: `/proc/version` contains `microsoft` → vm/wsl.
3. `/sys/hypervisor/type` = `xen` (and not dom0: `/proc/xen/capabilities`
   lacks `control_d`) → vm/xen.
4. SMBIOS (already collected, both platforms): manufacturer/product/BIOS
   vendor map (QEMU/KVM/Bochs/SeaBIOS → kvm, VMware → vmware, Microsoft
   "Virtual Machine" → hyperv, Xen → xen, innotek/VirtualBox → virtualbox,
   Amazon EC2 → aws, Google → gce, Parallels → parallels, DigitalOcean → kvm).
5. cpuinfo `hypervisor` flag → vm/unknown-kind (`kind = ""`, `source =
   cpuinfo`).
6. else physical when SMBIOS was readable, unknown otherwise.

## BMC (FR-004, Linux only)

go-ipmi v0.8.1 `NewOpenClient()` (OpenIPMI `/dev/ipmi0`), 10 s budget.
Channels 1–11: `GetChannelInfo`; for `ChannelMediumLAN` only:
`GetLanConfigParamFor` with parameters **3 IP, 4 IP source, 5 MAC,
6 subnet mask, 12 default gateway IP, 20 VLAN id** — nothing else. First
channel with a non-zero IP → `address/prefix_length/gateway/ip_source/vlan_id`;
every non-zero MAC → `ports` (≤ 8). Device absent, permission denied or
timeout → `bmc` absent. A test fake of the IPMI client records every
requested selector; the allowed set is asserted.

## Proxmox guests (FR-005, Linux only)

Only when `/etc/pve/qemu-server` or `/etc/pve/lxc` exists. `*.conf` files
(≤ 1000, each ≤ 64 KiB, VMID = file name `^[0-9]{1,9}$`): name from `name:`
(qemu) / `hostname:` (lxc); MACs from `net<N>:` values (`virtio=`, `e1000=`,
`vmxnet3=`, `rtl8139=`, `hwaddr=`), ≤ 32 per guest; parsing stops at the
first line starting with `[` (snapshot sections). Guests without MACs are
kept (name + id).

## Update state (FR-006, Linux only)

| Manager | Pending updates | Security | Notes |
|---|---|---|---|
| apt | `apt-get -s -o Debug::NoLocking=1 dist-upgrade` (`LANG=C`), `Inst <name> [<cur>] (<avail> <origin>…)` | origin contains `-security` | no `apt update` unless `refresh_package_lists` (then `apt-get update -q`, at most once per 24 h) |
| dnf / yum | `<pm> -q -C check-update` (exit 100 = updates) | `<pm> -q -C updateinfo list --updates security` | `-C` = cache only |
| apk | `apk -u list` | not classified | cached index |
| pacman | `checkupdates` | not classified | pacman-contrib; absent → `unsupported` |

- `status`: `up_to_date` (0 pending), `updates_available`, `unsupported`
  (no known manager), `error` (command failed/timed out), `unknown` (not
  collected).
- `reboot_required`: `/run/reboot-required` or `/var/run/reboot-required` →
  true; RHEL family `needs-restarting -r` exit 1 → true / 0 → false;
  fallback `needrestart -b -k` `NEEDRESTART-KSTA: N` (N ≥ 2 → true);
  otherwise `unknown`.
- `automatic_updates`: apt `apt-config dump APT::Periodic::Unattended-Upgrade`
  = `"1"` and `systemctl is-enabled apt-daily-upgrade.timer` = `enabled`;
  `dnf-automatic.timer`/`dnf-automatic-install.timer` enabled; `yum-cron`
  enabled → true; manager known but none → false; else unknown.
- `Program.available_version`/`security_update` are set on the matching
  installed program (name match; if the installed list lacks it, a program
  entry is added with its installed version from the manager output).
- Bounds: 60 s per command (`exec.CommandContext`), 120 s total
  (`update_timeout_seconds`), ≤ 5000 pending entries, 1 MiB per output line,
  `LANG=C LC_ALL=C`, no shell.

## Windows

Network, identity and virtualization as above; `bmc`, guests and update
state are not collected (`update_state.status = "unknown"`).

## Agent configuration (`AgentConfig`)

```yaml
collect_bmc: true              # read-only BMC LAN parameters
collect_updates: true
refresh_package_lists: false   # never refresh unless explicitly enabled
update_timeout_seconds: 120    # 30–600
```
