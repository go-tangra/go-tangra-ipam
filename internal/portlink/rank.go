package portlink

import (
	"sort"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Port is a switch port with what it learned.
type Port struct {
	SwitchID string
	PortID   string // switch interface id
	Name     string // switch interface name
	// MACs learned on the port (bridge FDB) with their VLAN.
	MACs map[string]int
	// LLDP neighbours as (system name, port id) pairs.
	LLDP [][2]string
}

// Host is a host-reported interface, or (AddressID set, feature 022) an
// address with a MAC: DeviceID is then unique per address and DeviceName the
// address's host name.
type Host struct {
	DeviceID   string
	DeviceName string
	IfaceID    string
	IfaceName  string
	MAC        string
	AddressID  string
}

// Input is everything the ranking needs for one tenant.
type Input struct {
	Ports []Port
	Hosts []Host
	// SwitchMACs are the MACs of switch interfaces (by switch id) and
	// SwitchNames the switches' names (lower-case) — both identify uplinks.
	SwitchMACs  map[string]string // mac -> switch id
	SwitchNames map[string]bool
	MaxMACs     int
}

// Link is one inferred host interface (or address) ↔ switch port connection.
type Link struct {
	HostIfaceID string
	AddressID   string
	SwitchID    string
	PortID      string
	PortName    string
	VLAN        int
	Source      string // snmp_fdb | lldp
}

// facesSwitch reports whether a port is an uplink: it learned another
// switch's MAC or its LLDP neighbour is a switch.
func facesSwitch(p Port, in Input) bool {
	for mac := range p.MACs {
		if sw, ok := in.SwitchMACs[mac]; ok && sw != p.SwitchID {
			return true
		}
	}
	for _, n := range p.LLDP {
		if in.SwitchNames[strings.ToLower(n[0])] {
			return true
		}
	}
	return false
}

// Rank links host interfaces to switch ports (research D17): an LLDP
// neighbour naming the host (system name = device name, or port id = the
// interface MAC or name) wins; otherwise the non-uplink port that learned the
// interface MAC with the fewest MACs overall (per switch, then across
// switches for daisy chains); a tie links nothing.
func Rank(in Input) []Link {
	type cand struct {
		port  Port
		count int
		vlan  int
	}
	byMAC := map[string][]cand{}
	var access []Port
	for _, p := range in.Ports {
		if facesSwitch(p, in) {
			continue
		}
		access = append(access, p) // LLDP may name a host on a busy (hypervisor) port
		if len(p.MACs) > in.MaxMACs {
			continue // many MACs: never inferred from the forwarding table
		}
		for mac, vlan := range p.MACs {
			byMAC[mac] = append(byMAC[mac], cand{port: p, count: len(p.MACs), vlan: vlan})
		}
	}
	var out []Link
	for _, h := range in.Hosts {
		if l, ok := lldpLink(h, in.Hosts, access); ok {
			out = append(out, l)
			continue
		}
		cs := byMAC[h.MAC]
		if len(cs) == 0 {
			continue
		}
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].count != cs[j].count {
				return cs[i].count < cs[j].count
			}
			return cs[i].port.PortID < cs[j].port.PortID
		})
		if len(cs) > 1 && cs[0].count == cs[1].count {
			continue // tie: no link
		}
		out = append(out, Link{HostIfaceID: h.IfaceID, AddressID: h.AddressID, SwitchID: cs[0].port.SwitchID, PortID: cs[0].port.PortID,
			PortName: cs[0].port.Name, VLAN: cs[0].vlan, Source: store.LinkSNMPFDB})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostIfaceID != out[j].HostIfaceID {
			return out[i].HostIfaceID < out[j].HostIfaceID
		}
		return out[i].AddressID < out[j].AddressID
	})
	return out
}

// lldpLink finds an access port whose LLDP neighbour is this host interface:
// the neighbour port id is the interface MAC or name, or the neighbour system
// name is the host and the interface is the host's only reported interface
// or the one whose MAC the port learned.
func lldpLink(h Host, hosts []Host, ports []Port) (Link, bool) {
	var single bool
	n := 0
	for _, o := range hosts {
		if o.DeviceID == h.DeviceID {
			n++
		}
	}
	single = n == 1
	for _, p := range ports {
		for _, nb := range p.LLDP {
			sys, port := nb[0], nb[1]
			pm, _ := hostreport.NormalizeMAC(port)
			byPort := (pm != "" && pm == h.MAC) || (port != "" && strings.EqualFold(port, h.IfaceName) && strings.EqualFold(sys, h.DeviceName))
			bySys := sys != "" && strings.EqualFold(sys, h.DeviceName)
			if byPort || (bySys && (single || hasMAC(p, h.MAC))) {
				return Link{HostIfaceID: h.IfaceID, AddressID: h.AddressID, SwitchID: p.SwitchID, PortID: p.PortID, PortName: p.Name,
					VLAN: p.MACs[h.MAC], Source: store.LinkLLDP}, true
			}
		}
	}
	return Link{}, false
}

func hasMAC(p Port, mac string) bool {
	_, ok := p.MACs[mac]
	return ok
}

// ParseLLDP splits a stored LLDP neighbour ("<sysName> <portId>", or just the
// port id when the neighbour sent no system name).
func ParseLLDP(s string) (sys, port string) {
	s = strings.TrimSpace(s)
	if a, b, ok := strings.Cut(s, " "); ok {
		return a, strings.TrimSpace(b)
	}
	return "", s
}
