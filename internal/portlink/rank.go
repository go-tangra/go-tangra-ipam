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
	// Related are MACs that belong to the host itself: its other interfaces
	// and, for a hypervisor, its guests. A switch port full of them is the
	// host's own access port, not an uplink, so they never count against
	// MaxMACs when that port is judged for this host.
	Related map[string]bool
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

// Link is one inferred host interface (or address) ↔ switch port connection
// on one switch. A host learned on several switches (MLAG / LACP bond across
// a switch pair) has one Link per switch; exactly one of them is Primary.
type Link struct {
	HostIfaceID string
	AddressID   string
	SwitchID    string
	PortID      string
	PortName    string
	VLAN        int
	Source      string // snmp_fdb | lldp
	// Count is the number of MACs the port learned (the ranking key).
	Count   int
	Primary bool
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

// Rank links host interfaces to switch ports (research D17, D6), one link
// per switch: on each switch an LLDP neighbour naming the host (system name =
// device name, or port id = the interface MAC or name) wins; otherwise the
// non-uplink port with at most MaxMACs MACs that learned the interface MAC
// with the fewest MACs — a tie between two ports of the same switch links
// nothing on that switch. Of a host's per-switch links the primary is an LLDP
// link if any, else the one with the fewest MACs (daisy chains: the access
// switch), ties between switches broken by the lowest switch id, then port
// id — a host bonded across a switch pair is linked to both.
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
		for mac, vlan := range p.MACs {
			byMAC[mac] = append(byMAC[mac], cand{port: p, count: len(p.MACs), vlan: vlan})
		}
	}
	var out []Link
	for _, h := range in.Hosts {
		per := lldpLinks(h, in.Hosts, access)
		bySwitch := map[string][]cand{}
		for _, c := range byMAC[h.MAC] {
			if _, ok := per[c.port.SwitchID]; ok {
				continue
			}
			// Only foreign MACs count: the host's own and its guests' MACs
			// on the port make it the host's access port, not an uplink.
			c.count = foreignMACs(c.port, h)
			if c.count > in.MaxMACs {
				continue // many foreign MACs: never inferred from the forwarding table
			}
			bySwitch[c.port.SwitchID] = append(bySwitch[c.port.SwitchID], c)
		}
		for sw, cs := range bySwitch {
			sort.Slice(cs, func(i, j int) bool {
				if cs[i].count != cs[j].count {
					return cs[i].count < cs[j].count
				}
				return cs[i].port.PortID < cs[j].port.PortID
			})
			if len(cs) > 1 && cs[0].count == cs[1].count {
				continue // tie on this switch: no link here
			}
			per[sw] = Link{HostIfaceID: h.IfaceID, AddressID: h.AddressID, SwitchID: sw, PortID: cs[0].port.PortID,
				PortName: cs[0].port.Name, VLAN: cs[0].vlan, Source: store.LinkSNMPFDB, Count: cs[0].count}
		}
		if len(per) == 0 {
			continue
		}
		links := make([]Link, 0, len(per))
		for _, l := range per {
			links = append(links, l)
		}
		sort.Slice(links, func(i, j int) bool { return primaryBefore(links[i], links[j]) })
		links[0].Primary = true
		out = append(out, links...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.HostIfaceID != b.HostIfaceID {
			return a.HostIfaceID < b.HostIfaceID
		}
		if a.AddressID != b.AddressID {
			return a.AddressID < b.AddressID
		}
		if a.Primary != b.Primary {
			return a.Primary
		}
		return a.SwitchID < b.SwitchID
	})
	return out
}

// foreignMACs counts the MACs learned on p, leaving out those related to the
// host (its other interfaces, its guests); the host's own MAC still counts,
// so without related MACs this is simply the port's MAC count.
func foreignMACs(p Port, h Host) int {
	n := 0
	for mac := range p.MACs {
		if mac == h.MAC || !h.Related[mac] {
			n++
		}
	}
	return n
}

// primaryBefore orders a host's per-switch links for picking the primary:
// LLDP first, then the fewest MACs, the lowest switch id, the lowest port id.
func primaryBefore(a, b Link) bool {
	if (a.Source == store.LinkLLDP) != (b.Source == store.LinkLLDP) {
		return a.Source == store.LinkLLDP
	}
	if a.Count != b.Count {
		return a.Count < b.Count
	}
	if a.SwitchID != b.SwitchID {
		return a.SwitchID < b.SwitchID
	}
	return a.PortID < b.PortID
}

// lldpLinks finds, per switch, the first access port whose LLDP neighbour is
// this host interface: the neighbour port id is the interface MAC or name, or
// the neighbour system name is the host and the interface is the host's only
// reported interface or the one whose MAC the port learned.
func lldpLinks(h Host, hosts []Host, ports []Port) map[string]Link {
	n := 0
	for _, o := range hosts {
		if o.DeviceID == h.DeviceID {
			n++
		}
	}
	single := n == 1
	out := map[string]Link{}
	for _, p := range ports {
		if _, done := out[p.SwitchID]; done {
			continue
		}
		for _, nb := range p.LLDP {
			sys, port := nb[0], nb[1]
			pm, _ := hostreport.NormalizeMAC(port)
			byPort := (pm != "" && pm == h.MAC) || (port != "" && strings.EqualFold(port, h.IfaceName) && strings.EqualFold(sys, h.DeviceName))
			bySys := sys != "" && strings.EqualFold(sys, h.DeviceName)
			if byPort || (bySys && (single || hasMAC(p, h.MAC))) {
				out[p.SwitchID] = Link{HostIfaceID: h.IfaceID, AddressID: h.AddressID, SwitchID: p.SwitchID, PortID: p.PortID, PortName: p.Name,
					VLAN: p.MACs[h.MAC], Source: store.LinkLLDP, Count: len(p.MACs)}
				break
			}
		}
	}
	return out
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
