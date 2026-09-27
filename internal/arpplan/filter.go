package arpplan

import (
	"net"
	"net/netip"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// classifyMAC normalises a MAC and returns the reason it must be ignored
// ("" when usable): not a 6-byte MAC or all zero (invalid), the group bit set
// (multicast, which includes broadcast), a VRRP/HSRP virtual-router MAC, or
// the MAC of a network device's own interface.
func classifyMAC(s string, network map[string]bool) (string, string) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", store.ARPIgnoredInvalid
	}
	if hw[0]|hw[1]|hw[2]|hw[3]|hw[4]|hw[5] == 0 {
		return "", store.ARPIgnoredInvalid
	}
	if hw[0]&1 == 1 {
		return "", store.ARPIgnoredMulticast
	}
	if virtualRouter(hw) {
		return "", store.ARPIgnoredVirtualRouter
	}
	mac := hw.String()
	if network[mac] {
		return "", store.ARPIgnoredNetworkDevice
	}
	return mac, ""
}

// virtualRouter reports the IANA VRRP ranges (00:00:5e:00:01:xx IPv4,
// 00:00:5e:00:02:xx IPv6) and Cisco HSRP v1 (00:00:0c:07:ac:xx) and v2
// (00:00:0c:9f:f0:00-00:00:0c:9f:ff:ff).
func virtualRouter(hw net.HardwareAddr) bool {
	switch {
	case hw[0] == 0x00 && hw[1] == 0x00 && hw[2] == 0x5e && hw[3] == 0x00:
		return hw[4] == 0x01 || hw[4] == 0x02
	case hw[0] == 0x00 && hw[1] == 0x00 && hw[2] == 0x0c:
		return (hw[3] == 0x07 && hw[4] == 0xac) || (hw[3] == 0x9f && hw[4]&0xf0 == 0xf0)
	}
	return false
}

// candidate is an entry that passed the per-entry filters.
type candidate struct {
	ip     netip.Addr
	mac    string
	device string
}

// filterProxy drops the candidates of every MAC answering for more than
// threshold distinct IPs (proxy ARP, a router MAC for a whole range).
func filterProxy(cands []candidate, threshold int, ignored map[string]int) []candidate {
	ips := map[string]map[netip.Addr]bool{}
	for _, c := range cands {
		if ips[c.mac] == nil {
			ips[c.mac] = map[netip.Addr]bool{}
		}
		ips[c.mac][c.ip] = true
	}
	out := cands[:0:0]
	for _, c := range cands {
		if len(ips[c.mac]) > threshold {
			ignored[store.ARPIgnoredProxy]++
			continue
		}
		out = append(out, c)
	}
	return out
}

// subnetOf places an IP in the tenant's most specific subnet.
func subnetOf(subnets []ipnet.Candidate, ip netip.Addr) (string, bool) {
	c, ok := ipnet.MostSpecific(subnets, ip)
	return c.ID, ok
}
