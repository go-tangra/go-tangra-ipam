package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/go-tangra/go-tangra-ipam/internal/biz"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
)

// linkSourceHypervisor marks a device-interface link that was discovered from a
// hypervisor host's reported guest list (Proxmox), as opposed to SNMP bridge-FDB
// correlation (linkSourceSNMPFDB). It means: the interface's device is a guest
// VM/container hosted on the remote (host) device.
const linkSourceHypervisor = "hypervisor"

// hostedVM mirrors one entry of the "hosted_vms" array a hypervisor host reports
// in its device metadata (written by tangra-client from /etc/pve/*/<vmid>.conf).
type hostedVM struct {
	VMID string   `json:"vmid"`
	Name string   `json:"name"`
	Type string   `json:"type"` // "qemu" or "lxc"
	MACs []string `json:"macs"`
}

type hostedVMsEnvelope struct {
	HostedVMs []hostedVM `json:"hosted_vms"`
}

// derefTenantID returns the tenant id value, defaulting to 0 (platform) when nil.
func derefTenantID(t *uint32) uint32 {
	if t == nil {
		return 0
	}
	return *t
}

// parseHostedVMs extracts the hosted-guest list from a host device's metadata
// JSON. Malformed JSON yields no entries (best-effort, free-form metadata).
func parseHostedVMs(metadataJSON string) []hostedVM {
	metadataJSON = strings.TrimSpace(metadataJSON)
	if metadataJSON == "" {
		return nil
	}
	var env hostedVMsEnvelope
	if err := json.Unmarshal([]byte(metadataJSON), &env); err != nil {
		return nil
	}
	return env.HostedVMs
}

// guestPortLabel builds the host-side "port" label for a hosted guest link,
// e.g. "VM 101 (web01)" or "CT 200". It identifies which guest slot on the host
// the link represents.
func guestPortLabel(vm hostedVM) string {
	kind := "VM"
	if vm.Type == "lxc" {
		kind = "CT"
	}
	label := kind
	if vm.VMID != "" {
		label = fmt.Sprintf("%s %s", kind, vm.VMID)
	}
	if vm.Name != "" {
		label = fmt.Sprintf("%s (%s)", label, vm.Name)
	}
	return label
}

// correlateHostedVMs links each guest reported by a hypervisor host to that host:
// for every guest NIC MAC declared in the host's Proxmox config, it finds the
// guest device's matching interface and sets its remote link to the host, so the
// guest's "Connected To" shows the Proxmox server hosting it.
//
// It is best-effort and idempotent: a guest whose device/interface is not (yet)
// known in IPAM is skipped and will be linked on the host's next sync. Returns
// the number of guest interfaces linked.
func correlateHostedVMs(ctx context.Context, repo *data.DeviceInterfaceRepo, logger *log.Helper, hostDeviceID string, tenantID uint32, metadataJSON string) int {
	vms := parseHostedVMs(metadataJSON)
	if len(vms) == 0 {
		return 0
	}

	linked := 0
	// Interfaces this host legitimately hosts right now. Used to prune links to
	// guests this host no longer reports (migrated away) so their "Connected To"
	// stops pointing here.
	var keepInterfaceIDs []string
	for _, vm := range vms {
		portName := guestPortLabel(vm)
		for _, rawMAC := range vm.MACs {
			mac := biz.NormalizeMAC(rawMAC)
			if mac == "" {
				continue
			}
			guestIface, err := repo.FindGuestInterfaceByMAC(ctx, tenantID, mac)
			if err != nil {
				logger.Warnf("hosted-vm correlation: lookup MAC %s failed: %v", mac, err)
				continue
			}
			if guestIface == nil || guestIface.DeviceID == hostDeviceID {
				continue // guest device unknown, or the MAC is the host's own
			}
			keepInterfaceIDs = append(keepInterfaceIDs, guestIface.ID)

			// When the link already points at this host unchanged, still refresh
			// link_last_seen so it stays a true heartbeat (the time-based reaper
			// relies on it). Skip the heavier device/port rewrite in that case.
			alreadyLinked := guestIface.LinkSource == linkSourceHypervisor &&
				guestIface.RemoteDeviceID == hostDeviceID &&
				guestIface.RemotePortName == portName
			updates := map[string]any{"link_last_seen": time.Now()}
			if !alreadyLinked {
				updates["remote_device_id"] = hostDeviceID
				updates["remote_port_name"] = portName
				updates["link_source"] = linkSourceHypervisor
			}
			if _, err := repo.Update(ctx, guestIface.ID, updates); err != nil {
				logger.Warnf("hosted-vm correlation: failed to set link on interface %s: %v", guestIface.ID, err)
				continue
			}
			if !alreadyLinked {
				linked++
				logger.Infof("Hosted-VM link discovered: guest interface %s (MAC %s) -> host %s as %s",
					guestIface.Name, mac, hostDeviceID, portName)
			}
		}
	}

	// Authoritative prune: drop any hypervisor link still pointing at this host
	// for a guest it no longer reports (VM migrated or was removed).
	if cleared, err := repo.ClearFlatLinksForRemoteExcept(ctx, linkSourceHypervisor, hostDeviceID, keepInterfaceIDs); err != nil {
		logger.Warnf("hosted-vm correlation: failed to prune stale links for host %s: %v", hostDeviceID, err)
	} else if cleared > 0 {
		logger.Infof("Hosted-VM correlation: cleared %d stale guest link(s) no longer hosted on %s", cleared, hostDeviceID)
	}

	if linked > 0 {
		logger.Infof("Hosted-VM correlation: linked %d guest interfaces to host %s", linked, hostDeviceID)
	}
	return linked
}
