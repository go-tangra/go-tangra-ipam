package portlink

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	hMAC  = "aa:bb:cc:00:00:01"
	h2MAC = "aa:bb:cc:00:00:02"
)

func host() Host {
	return Host{DeviceID: "h", DeviceName: "web-01", IfaceID: "hi", IfaceName: "eth0", MAC: hMAC}
}

func port(sw, id string, macs ...string) Port {
	p := Port{SwitchID: sw, PortID: id, Name: "Gi" + id, MACs: map[string]int{}}
	for _, m := range macs {
		p.MACs[m] = 10
	}
	return p
}

func in(ports ...Port) Input {
	return Input{Ports: ports, Hosts: []Host{host()}, SwitchMACs: map[string]string{}, SwitchNames: map[string]bool{}, MaxMACs: 16}
}

func TestRankFewestMACsPort(t *testing.T) {
	l := Rank(in(port("s1", "p1", hMAC), port("s1", "p2", hMAC, "02:00:00:00:00:01", "02:00:00:00:00:02")))
	if len(l) != 1 || l[0].PortID != "p1" || l[0].VLAN != 10 || l[0].Source != store.LinkSNMPFDB || l[0].SwitchID != "s1" || l[0].PortName != "Gip1" {
		t.Fatalf("%+v", l)
	}
}

func TestRankIgnoresBusyPorts(t *testing.T) {
	busy := port("s1", "p1", hMAC)
	for i := 0; i < 16; i++ {
		busy.MACs[string(rune('a'+i))+"2:00:00:00:00:00"] = 1
	}
	if l := Rank(in(busy)); len(l) != 0 {
		t.Fatalf("port over max_macs_per_port linked: %+v", l)
	}
}

func TestRankUplinks(t *testing.T) {
	up := port("s1", "p1", hMAC, "de:ad:00:00:00:01")
	i := in(up, port("s2", "p9", hMAC, "02:00:00:00:00:05"))
	i.SwitchMACs["de:ad:00:00:00:01"] = "s2" // p1 learned switch s2's MAC
	l := Rank(i)
	if len(l) != 1 || l[0].PortID != "p9" {
		t.Fatalf("uplink by switch MAC: %+v", l)
	}
	own := in(port("s1", "p1", hMAC, "de:ad:00:00:00:02"))
	own.SwitchMACs["de:ad:00:00:00:02"] = "s1" // the switch's own MAC is not an uplink sign
	if l := Rank(own); len(l) != 1 {
		t.Fatal("own switch MAC")
	}
	lldp := port("s1", "p1", hMAC)
	lldp.LLDP = [][2]string{{"core-sw", "Gi0/48"}}
	j := in(lldp)
	j.SwitchNames["core-sw"] = true
	if l := Rank(j); len(l) != 0 {
		t.Fatal("port with a switch LLDP neighbour is an uplink")
	}
}

func TestRankDaisyChainAndTie(t *testing.T) {
	// Access switch s2 port (1 MAC) behind s1's port (3 MACs): the globally
	// fewest is primary, s1 keeps its most direct port as a secondary link.
	l := Rank(in(port("s1", "p1", hMAC, "02:00:00:00:00:01", "02:00:00:00:00:02"), port("s2", "p2", hMAC)))
	if len(l) != 2 || l[0].PortID != "p2" || !l[0].Primary || l[1].PortID != "p1" || l[1].Primary || l[1].Count != 3 {
		t.Fatalf("daisy chain %+v", l)
	}
	if l := Rank(in(port("s1", "p1", h2MAC))); len(l) != 0 {
		t.Fatal("unknown MAC")
	}
}

// TestRankMLAG: a host bonded across a switch pair is learned on a port of
// each switch with the same MAC count — one link per switch, the lowest
// switch id primary (never "no link" because two switches tie).
func TestRankMLAG(t *testing.T) {
	busy := func(sw, id string) Port {
		p := port(sw, id, hMAC)
		for n := 0; n < 5; n++ {
			p.MACs["02:00:00:00:01:0"+string(rune('0'+n))] = 30
		}
		return p
	}
	l := Rank(in(busy("cs2", "p17b"), busy("cs1", "p17a"), busy("cs1", "p3")))
	// cs1 has two ports with 6 MACs each (same-switch tie): no link on cs1.
	if len(l) != 1 || l[0].SwitchID != "cs2" || !l[0].Primary {
		t.Fatalf("same-switch tie %+v", l)
	}
	l = Rank(in(busy("cs2", "p17b"), busy("cs1", "p17a")))
	if len(l) != 2 || l[0].SwitchID != "cs1" || !l[0].Primary || l[1].SwitchID != "cs2" || l[1].Primary ||
		l[0].PortID != "p17a" || l[1].PortID != "p17b" || l[0].VLAN != 10 {
		t.Fatalf("mlag pair %+v", l)
	}
	// Per switch the fewest-MAC port wins; the primary is the fewest overall.
	l = Rank(in(busy("cs1", "p17a"), port("cs1", "p9", hMAC, "02:00:00:00:00:09"), busy("cs2", "p17b")))
	if len(l) != 2 || l[0].PortID != "p9" || !l[0].Primary || l[1].PortID != "p17b" {
		t.Fatalf("per-switch fewest %+v", l)
	}
	// Equal counts on both switches and equal switch ids are impossible; a
	// port id breaks the order when the switch ids tie in primaryBefore.
	if !primaryBefore(Link{SwitchID: "s", PortID: "a"}, Link{SwitchID: "s", PortID: "b"}) {
		t.Fatal("port id tie-break")
	}
	// LLDP on one switch wins that switch (and the primary), FDB on the other.
	named := port("cs2", "p1", "02:00:00:00:00:01", "02:00:00:00:00:02")
	named.LLDP = [][2]string{{"web-01", "enp1s0"}}
	l = Rank(in(port("cs1", "p5", hMAC), named))
	if len(l) != 2 || l[0].SwitchID != "cs2" || l[0].Source != store.LinkLLDP || !l[0].Primary || l[1].PortID != "p5" {
		t.Fatalf("lldp + fdb %+v", l)
	}
}

func TestRankLLDPOverridesFDB(t *testing.T) {
	fdb := port("s1", "p1", hMAC)
	named := port("s1", "p2", "02:00:00:00:00:01", "02:00:00:00:00:02")
	named.LLDP = [][2]string{{"WEB-01", "enp1s0"}}
	l := Rank(in(fdb, named))
	if len(l) != 1 || l[0].PortID != "p2" || l[0].Source != store.LinkLLDP {
		t.Fatalf("lldp by system name: %+v", l)
	}
	// Port id = the interface MAC.
	byMAC := port("s1", "p3")
	byMAC.LLDP = [][2]string{{"", "AA-BB-CC-00-00-01"}}
	if l := Rank(in(fdb, byMAC)); len(l) != 1 || l[0].PortID != "p3" {
		t.Fatalf("lldp by MAC %+v", l)
	}
	// Port id = interface name with the host's system name.
	byName := port("s1", "p4")
	byName.LLDP = [][2]string{{"web-01", "eth0"}}
	i := in(fdb, byName)
	i.Hosts = append(i.Hosts, Host{DeviceID: "h", DeviceName: "web-01", IfaceID: "hj", IfaceName: "eth1", MAC: h2MAC})
	l = Rank(i)
	if len(l) != 1 || l[0].HostIfaceID != "hi" || l[0].PortID != "p4" {
		t.Fatalf("lldp by name %+v", l)
	}
	// System name only on a multi-NIC host: the port must have learned the MAC.
	sysOnly := port("s1", "p5")
	sysOnly.LLDP = [][2]string{{"web-01", "x"}}
	i = in(fdb, sysOnly)
	i.Hosts = append(i.Hosts, Host{DeviceID: "h", DeviceName: "web-01", IfaceID: "hj", IfaceName: "eth1", MAC: h2MAC})
	for _, ln := range Rank(i) {
		if ln.PortID == "p5" {
			t.Fatal("ambiguous system-name LLDP linked")
		}
	}
	// LLDP names the host on a busy hypervisor port.
	busy := port("s1", "p6", hMAC)
	for n := 0; n < 20; n++ {
		busy.MACs[string(rune('a'+n))+"2:00:00:00:00:00"] = 1
	}
	busy.LLDP = [][2]string{{"web-01", "enp1s0"}}
	if l := Rank(in(busy)); len(l) != 1 || l[0].Source != store.LinkLLDP {
		t.Fatalf("lldp on a busy port %+v", l)
	}
}

func TestParseLLDP(t *testing.T) {
	if s, p := ParseLLDP(" sw1 Gi0/1 "); s != "sw1" || p != "Gi0/1" {
		t.Fatal(s, p)
	}
	if s, p := ParseLLDP("aa:bb:cc:dd:ee:ff"); s != "" || p != "aa:bb:cc:dd:ee:ff" {
		t.Fatal(s, p)
	}
}
