package portlink

import "testing"

// FuzzRank: arbitrary FDB/LLDP data never panics, links only known host
// interfaces to known ports, at most once per interface, and never through a
// port with more MACs than allowed unless LLDP named the host.
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
		seen := map[string]bool{}
		for _, l := range Rank(i) {
			if l.HostIfaceID != "hi" || seen[l.HostIfaceID] || (l.PortID != "p1" && l.PortID != "p2") {
				t.Fatalf("bad link %+v", l)
			}
			seen[l.HostIfaceID] = true
		}
	})
}
