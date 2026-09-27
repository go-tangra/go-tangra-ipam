package memstore

import (
	"context"
	"sort"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// PortLinkData implements repo.PortLinkStore.
func (m *Mem) PortLinkData(_ context.Context, tenantID string) (repo.PortLinkData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("PortLinkData"); err != nil {
		return repo.PortLinkData{}, err
	}
	var out repo.PortLinkData
	switches, hosts := map[string]bool{}, map[string]bool{}
	for _, d := range m.devices {
		if d.TenantID != tenantID {
			continue
		}
		if d.DeviceType == store.DevSwitch {
			out.Switches = append(out.Switches, d)
			switches[d.ID] = true
		}
		if d.Source == store.SrcHostReport {
			out.Hosts = append(out.Hosts, d)
			hosts[d.ID] = true
		}
	}
	for _, i := range m.ifaces {
		if i.TenantID != tenantID {
			continue
		}
		if switches[i.DeviceID] {
			out.SwitchIfaces = append(out.SwitchIfaces, i)
			for _, l := range m.links[i.ID] {
				if l.TenantID == tenantID && (l.LinkSource == store.LinkSNMPFDB || l.LinkSource == store.LinkLLDP) {
					out.Links = append(out.Links, l)
				}
			}
		}
		if hosts[i.DeviceID] && i.ReportState == store.RepReported && i.MACAddress != "" {
			i.Links = m.hostLinksLocked(tenantID, store.HostKindInterface, i.ID, i.RemoteInterfaceID)
			out.HostIfaces = append(out.HostIfaces, i)
		}
	}
	for _, a := range m.addrs {
		if a.TenantID == tenantID && (a.MACAddress != "" || a.Link != nil) {
			out.Addresses = append(out.Addresses, m.decorateAddrLocked(a))
		}
	}
	sort.Slice(out.Addresses, func(i, j int) bool { return out.Addresses[i].ID < out.Addresses[j].ID })
	out.NetworkMACs = m.networkMACsLocked(tenantID)
	sort.Slice(out.Switches, func(i, j int) bool { return out.Switches[i].ID < out.Switches[j].ID })
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].ID < out.Hosts[j].ID })
	sort.Slice(out.SwitchIfaces, func(i, j int) bool { return out.SwitchIfaces[i].ID < out.SwitchIfaces[j].ID })
	sort.Slice(out.HostIfaces, func(i, j int) bool { return out.HostIfaces[i].ID < out.HostIfaces[j].ID })
	sort.Slice(out.Links, func(i, j int) bool { return out.Links[i].ID < out.Links[j].ID })
	return out, nil
}

// SetInterfaceLinks implements repo.PortLinkStore.
func (m *Mem) SetInterfaceLinks(_ context.Context, tenantID string, ifaces []store.DeviceInterface, audit []store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("SetInterfaceLinks"); err != nil {
		return err
	}
	for _, i := range ifaces {
		if cur, ok := m.ifaces[i.ID]; !ok || cur.TenantID != tenantID {
			return repo.ErrNotFound
		}
	}
	for _, i := range ifaces {
		m.setHostLinksLocked(tenantID, store.HostKindInterface, i.ID, i.Links)
		cur := m.ifaces[i.ID]
		cur.RemoteDeviceID, cur.RemoteInterfaceID, cur.RemotePortName = i.RemoteDeviceID, i.RemoteInterfaceID, i.RemotePortName
		cur.LinkSource, cur.LinkVlan, cur.LinkLastSeen = i.LinkSource, i.LinkVlan, i.LinkLastSeen
		cur.UpdatedAt = now(m)
		m.ifaces[i.ID] = cur
	}
	for _, row := range audit {
		row.TenantID = tenantID
		m.appendAuditLocked(row)
	}
	return nil
}

// hostKey identifies a host's per-switch link set.
type hostKey struct{ tenant, kind, id string }

// setHostLinksLocked replaces a host's per-switch link set (computed fields
// dropped).
func (m *Mem) setHostLinksLocked(tenantID, kind, id string, links []store.HostSwitchLink) {
	k := hostKey{tenantID, kind, id}
	if len(links) == 0 {
		delete(m.hostLinks, k)
		return
	}
	cp := make([]store.HostSwitchLink, len(links))
	for n, l := range links {
		l.SwitchName, l.Primary = "", false
		if l.LastSeen == nil {
			t := now(m)
			l.LastSeen = &t
		}
		cp[n] = l
	}
	m.hostLinks[k] = cp
}

// hostLinksLocked returns a host's per-switch links whose switch port still
// exists (the ON DELETE CASCADE of the table), with switch names, primary
// (the port of the flat link columns) first.
func (m *Mem) hostLinksLocked(tenantID, kind, id, primaryPort string) []store.HostSwitchLink {
	var out []store.HostSwitchLink
	for _, l := range m.hostLinks[hostKey{tenantID, kind, id}] {
		p, ok := m.ifaces[l.PortID]
		if !ok || p.TenantID != tenantID || p.DeviceID != l.SwitchID {
			continue
		}
		l.SwitchName = m.devices[l.SwitchID].Name
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil
	}
	return store.MarkPrimary(out, primaryPort)
}

// linkedToLocked reports whether a host has a per-switch link to a port.
func (m *Mem) linkedToLocked(tenantID, kind, id, portID string) bool {
	for _, l := range m.hostLinksLocked(tenantID, kind, id, "") {
		if l.PortID == portID {
			return true
		}
	}
	return false
}
