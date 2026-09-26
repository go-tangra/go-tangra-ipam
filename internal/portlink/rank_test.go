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
	// Access switch s2 port (1 MAC) behind s1's port (3 MACs): globally fewest wins.
	l := Rank(in(port("s1", "p1", hMAC, "02:00:00:00:00:01", "02:00:00:00:00:02"), port("s2", "p2", hMAC)))
	if len(l) != 1 || l[0].PortID != "p2" {
		t.Fatalf("daisy chain %+v", l)
	}
	if l := Rank(in(port("s1", "p1", hMAC), port("s2", "p2", hMAC))); len(l) != 0 {
		t.Fatal("tie must not link")
	}
	if l := Rank(in(port("s1", "p1", h2MAC))); len(l) != 0 {
		t.Fatal("unknown MAC")
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
	if l := Rank(in(fdb, byMAC)); l[0].PortID != "p3" {
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
