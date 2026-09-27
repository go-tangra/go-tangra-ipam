package portlink

import (
	"context"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Correlator links host interfaces to switch ports for one tenant at a time.
type Correlator struct {
	st       repo.PortLinkStore
	maxMACs  int
	staleAge time.Duration
	now      func() time.Time
}

// New builds a correlator (maxMACs per access port, links not re-confirmed
// for staleAge are cleared).
func New(st repo.PortLinkStore, maxMACs int, staleAge time.Duration) *Correlator {
	return &Correlator{st: st, maxMACs: maxMACs, staleAge: staleAge, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock injects the clock (tests).
func (c *Correlator) SetClock(now func() time.Time) { c.now = now }

// BuildInput turns the tenant's stored SNMP data and reported interfaces
// into the ranking input.
func BuildInput(d repo.PortLinkData, maxMACs int) Input {
	in := Input{SwitchMACs: map[string]string{}, SwitchNames: map[string]bool{}, MaxMACs: maxMACs}
	names := map[string]string{}
	for _, s := range d.Switches {
		in.SwitchNames[strings.ToLower(s.Name)] = true
	}
	ports := map[string]*Port{}
	var order []string
	for _, i := range d.SwitchIfaces {
		if m, ok := hostreport.NormalizeMAC(i.MACAddress); ok {
			in.SwitchMACs[m] = i.DeviceID
		}
		ports[i.ID] = &Port{SwitchID: i.DeviceID, PortID: i.ID, Name: i.Name, MACs: map[string]int{}}
		order = append(order, i.ID)
	}
	for _, l := range d.Links {
		p := ports[l.InterfaceID]
		if p == nil {
			continue
		}
		switch l.LinkSource {
		case store.LinkSNMPFDB:
			if m, ok := hostreport.NormalizeMAC(l.RemotePortName); ok {
				p.MACs[m] = l.LinkVlan
			}
		case store.LinkLLDP:
			sys, port := ParseLLDP(l.RemotePortName)
			p.LLDP = append(p.LLDP, [2]string{sys, port})
		}
	}
	for _, id := range order {
		in.Ports = append(in.Ports, *ports[id])
	}
	for _, h := range d.Hosts {
		names[h.ID] = h.Name
	}
	reported := map[string]bool{}
	for _, i := range d.HostIfaces {
		m, ok := hostreport.NormalizeMAC(i.MACAddress)
		if !ok {
			continue
		}
		reported[m] = true
		in.Hosts = append(in.Hosts, Host{DeviceID: i.DeviceID, DeviceName: names[i.DeviceID], IfaceID: i.ID, IfaceName: i.Name, MAC: m})
	}
	// 022: addresses with a MAC are hosts too, unless a reported interface
	// already carries the MAC (its link covers the address) or the MAC is a
	// network device's own.
	for _, a := range d.Addresses {
		m, ok := hostreport.NormalizeMAC(a.MACAddress)
		if !ok || reported[m] || d.NetworkMACs[m] || in.SwitchMACs[m] != "" {
			continue
		}
		in.Hosts = append(in.Hosts, Host{AddressID: a.ID, DeviceID: "address:" + a.ID, DeviceName: a.Hostname, MAC: m})
	}
	return in
}

func auditRow(tenantID string, t audit.EventType, ifaceID string, detail map[string]any, at time.Time) store.AuditRow {
	return subjectRow(tenantID, t, audit.SubjectInterface, ifaceID, detail, at)
}

func subjectRow(tenantID string, t audit.EventType, kind, id string, detail map[string]any, at time.Time) store.AuditRow {
	row, _ := audit.Row(audit.Event{TenantID: tenantID, EventType: t, ActorKind: audit.ActorSystem, ActorID: audit.HostSyncActor,
		SubjectKind: kind, SubjectID: id, Outcome: audit.OutcomeOK, Details: detail}, at)
	return row
}

// Correlate ranks and writes the tenant's host interface links: new or
// changed links (port_linked, superseded ones port_unlinked), re-confirmed
// links refreshed, links not re-confirmed within the stale age cleared
// (port_unlinked). Only host-reported devices are linked; everything runs in
// the tenant's scope.
func (c *Correlator) Correlate(ctx context.Context, tenantID string) error {
	d, err := c.st.PortLinkData(ctx, tenantID)
	if err != nil || len(d.Switches) == 0 {
		return err
	}
	links := Rank(BuildInput(d, c.maxMACs))
	byIface, byAddr := map[string]Link{}, map[string]Link{}
	for _, l := range links {
		if l.AddressID != "" {
			byAddr[l.AddressID] = l
		} else {
			byIface[l.HostIfaceID] = l
		}
	}
	now := c.now()
	var changed []store.DeviceInterface
	var rows []store.AuditRow
	for _, i := range d.HostIfaces {
		l, ok := byIface[i.ID]
		switch {
		case ok && i.RemoteInterfaceID == l.PortID && i.LinkSource == l.Source && i.LinkVlan == l.VLAN:
			i.LinkLastSeen = &now // re-confirmed
			changed = append(changed, i)
		case ok:
			if i.RemoteInterfaceID != "" {
				rows = append(rows, auditRow(tenantID, audit.PortUnlinked, i.ID, map[string]any{"reason": "superseded", "switch_interface_id": i.RemoteInterfaceID}, now))
			}
			i.RemoteDeviceID, i.RemoteInterfaceID, i.RemotePortName = l.SwitchID, l.PortID, l.PortName
			i.LinkSource, i.LinkVlan, i.LinkLastSeen = l.Source, l.VLAN, &now
			changed = append(changed, i)
			rows = append(rows, auditRow(tenantID, audit.PortLinked, i.ID, map[string]any{"switch_device_id": l.SwitchID,
				"switch_interface_id": l.PortID, "vlan": l.VLAN, "source": l.Source}, now))
		case i.RemoteInterfaceID != "" && (i.LinkSource == store.LinkSNMPFDB || i.LinkSource == store.LinkLLDP) &&
			(i.LinkLastSeen == nil || now.Sub(*i.LinkLastSeen) > c.staleAge):
			rows = append(rows, auditRow(tenantID, audit.PortUnlinked, i.ID, map[string]any{"reason": "stale", "switch_interface_id": i.RemoteInterfaceID}, now))
			i.RemoteDeviceID, i.RemoteInterfaceID, i.RemotePortName, i.LinkSource, i.LinkVlan, i.LinkLastSeen = "", "", "", "", 0, nil
			changed = append(changed, i)
		}
	}
	if len(changed) > 0 {
		if err := c.st.SetInterfaceLinks(ctx, tenantID, changed, rows); err != nil {
			return err
		}
	}
	return c.correlateAddresses(ctx, tenantID, d, byIface, byAddr, now)
}

// correlateAddresses writes the address links (feature 022) with the same
// re-confirm / supersede / stale rules as interfaces. An address whose MAC a
// reported interface carries shows that interface's link.
func (c *Correlator) correlateAddresses(ctx context.Context, tenantID string, d repo.PortLinkData, byIface, byAddr map[string]Link, now time.Time) error {
	byMAC := map[string]Link{}
	for _, i := range d.HostIfaces {
		if l, ok := byIface[i.ID]; ok {
			if m, ok := hostreport.NormalizeMAC(i.MACAddress); ok {
				byMAC[m] = l
			}
		}
	}
	var changed []store.IPAddress
	var rows []store.AuditRow
	for _, a := range d.Addresses {
		l, ok := byAddr[a.ID]
		if !ok {
			m, _ := hostreport.NormalizeMAC(a.MACAddress)
			l, ok = byMAC[m]
		}
		cur := a.Link
		switch {
		case ok && cur != nil && cur.PortID == l.PortID && cur.Source == l.Source && cur.VLAN == l.VLAN:
			nl := *cur
			nl.LastSeen = &now // re-confirmed
			a.Link = &nl
		case ok:
			if cur != nil {
				rows = append(rows, subjectRow(tenantID, audit.PortUnlinked, audit.SubjectAddress, a.ID, map[string]any{"reason": "superseded", "switch_interface_id": cur.PortID}, now))
			}
			a.Link = &store.AddressLink{SwitchID: l.SwitchID, PortID: l.PortID, PortName: l.PortName, VLAN: l.VLAN, Source: l.Source, LastSeen: &now}
			rows = append(rows, subjectRow(tenantID, audit.PortLinked, audit.SubjectAddress, a.ID, map[string]any{"switch_device_id": l.SwitchID,
				"switch_interface_id": l.PortID, "vlan": l.VLAN, "source": l.Source}, now))
		case cur != nil && (cur.LastSeen == nil || now.Sub(*cur.LastSeen) > c.staleAge):
			rows = append(rows, subjectRow(tenantID, audit.PortUnlinked, audit.SubjectAddress, a.ID, map[string]any{"reason": "stale", "switch_interface_id": cur.PortID}, now))
			a.Link = nil
		default:
			continue
		}
		changed = append(changed, a)
	}
	if len(changed) == 0 {
		return nil
	}
	return c.st.SetAddressLinks(ctx, tenantID, changed, rows)
}
