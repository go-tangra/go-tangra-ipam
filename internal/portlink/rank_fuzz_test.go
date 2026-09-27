package portlink

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// FuzzRank: arbitrary FDB/LLDP data never panics, links only known host
// interfaces to known ports, at most once per interface and switch, with
// exactly one primary link per linked interface.
func FuzzRank(f *testing.F) {
	f.Add("aa:bb:cc:00:00:01", "sw lldp", "web-01", 3, uint8(2))
	f.Add("zz", "", "", 0, uint8(0))
	f.Fuzz(func(t *testing.T, mac, lldp, hostName string, extra int, maxMACs uint8) {
		if extra < 0 || extra > 64 {
			extra = 3
		}
		p := port("s1", "p1", mac)
		for i := 0; i < extra; i++ {
			p.MACs[string(rune('A'+i))] = i
		}
		sys, prt := ParseLLDP(lldp)
		p.LLDP = [][2]string{{sys, prt}}
		i := Input{Ports: []Port{p, port("s2", "p2", mac)}, Hosts: []Host{{DeviceID: "h", DeviceName: hostName, IfaceID: "hi", IfaceName: "eth0", MAC: mac}},
			SwitchMACs: map[string]string{}, SwitchNames: map[string]bool{"sw": true}, MaxMACs: int(maxMACs)}
		seen, primaries := map[string]bool{}, 0
		links := Rank(i)
		for _, l := range links {
			key := l.HostIfaceID + "|" + l.SwitchID
			if l.HostIfaceID != "hi" || seen[key] || (l.PortID != "p1" && l.PortID != "p2") ||
				(l.Source != store.LinkLLDP && l.Count > i.MaxMACs) {
				t.Fatalf("bad link %+v", l)
			}
			seen[key] = true
			if l.Primary {
				primaries++
			}
		}
		if len(links) > 0 && (primaries != 1 || !links[0].Primary) {
			t.Fatalf("primary %+v", links)
		}
	})
}
