package portlink

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Feature 022 US2: addresses with a MAC (any source) are linked to switch
// ports with the same rules as host-reported interfaces.

func TestBuildInputAddressHosts(t *testing.T) {
	d := repo.PortLinkData{
		Switches:     []store.Device{{ID: "sw", Name: "MSW-RACK2"}},
		SwitchIfaces: []store.DeviceInterface{{ID: "p1", DeviceID: "sw", Name: "14", MACAddress: "00:04:96:00:00:01"}},
		Hosts:        []store.Device{{ID: "h", Name: "ns1"}},
		HostIfaces:   []store.DeviceInterface{{ID: "hi", DeviceID: "h", Name: "eth0", MACAddress: "52:54:00:00:00:08"}},
		Addresses: []store.IPAddress{
			{ID: "a-printer", Hostname: "printer", MACAddress: "0A-5C-D2-F1-00-05"},
			{ID: "a-ns1", MACAddress: "52:54:00:00:00:08"},    // covered by the reported interface
			{ID: "a-router", MACAddress: "4c:5e:0c:00:00:01"}, // a router's own MAC
			{ID: "a-switch", MACAddress: "00:04:96:00:00:01"}, // a switch's own MAC
			{ID: "a-junk", MACAddress: "not-a-mac"},
			{ID: "a-none", Link: &store.AddressLink{PortID: "p1"}}, // no MAC: not a host
		},
		NetworkMACs: map[string]bool{"4c:5e:0c:00:00:01": true},
	}
	in := BuildInput(d, 16)
	var addrHosts []Host
	for _, h := range in.Hosts {
		if h.AddressID != "" {
			addrHosts = append(addrHosts, h)
		}
	}
	if len(addrHosts) != 1 || addrHosts[0].AddressID != "a-printer" || addrHosts[0].MAC != "0a:5c:d2:f1:00:05" ||
		addrHosts[0].DeviceName != "printer" || addrHosts[0].IfaceID != "" {
		t.Fatalf("address hosts %+v", addrHosts)
	}
}

func TestRankAddressHost(t *testing.T) {
	a := Host{AddressID: "a1", DeviceID: "address:a1", DeviceName: "printer", MAC: h2MAC}
	i := in(port("s1", "p1", hMAC), port("s1", "p2", h2MAC), port("s1", "trunk", h2MAC, hMAC, "02:00:00:00:00:01"))
	i.Hosts = append(i.Hosts, a)
	l := Rank(i)
	if len(l) != 2 || l[0].HostIfaceID != "" || l[0].AddressID != "a1" || l[0].PortID != "p2" || l[1].HostIfaceID != "hi" {
		t.Fatalf("links %+v", l)
	}
	// Only on a busy trunk: no link.
	i = in(port("s1", "trunk", h2MAC, "02:00:00:00:00:01", "02:00:00:00:00:02"))
	i.Hosts, i.MaxMACs = []Host{a}, 2
	if l := Rank(i); len(l) != 0 {
		t.Fatalf("trunk link %+v", l)
	}
	// LLDP naming the address's host name links it even on a busy port.
	p := port("s1", "ap", h2MAC, "02:00:00:00:00:01", "02:00:00:00:00:02")
	p.LLDP = [][2]string{{"printer", "eth0"}}
	i = in(p)
	i.Hosts, i.MaxMACs = []Host{a}, 2
	if l := Rank(i); len(l) != 1 || l[0].AddressID != "a1" || l[0].Source != store.LinkLLDP {
		t.Fatalf("lldp link %+v", l)
	}
}

func seedAddress(t *testing.T, m *memstore.Mem, tid, id, ip, mac string) {
	t.Helper()
	ctx := context.Background()
	if _, err := m.GetSubnet(ctx, tid, "net-"+tid); err != nil {
		if err := m.CreateSubnet(ctx, store.Subnet{ID: "net-" + tid, TenantID: tid, Name: "n", CIDR: "10.9.0.0/16"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.CreateAddress(ctx, store.IPAddress{ID: id, TenantID: tid, SubnetID: "net-" + tid, Address: ip, Hostname: "printer",
		MACAddress: mac, MACSource: store.MACSourceARP}); err != nil {
		t.Fatal(err)
	}
}

func TestCorrelateAddressLinks(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	hi, sp := seed(t, m, tA, "a-")
	// An agentless printer whose MAC the access port learned (the port
	// carries 02:00:00:00:00:09 already, see seed).
	seedAddress(t, m, tA, "printer", "10.9.0.5", "02:00:00:00:00:09")
	// A host-synced address with the interface's MAC shows the interface link.
	seedAddress(t, m, tA, "ns1", "10.9.0.8", "aa:bb:cc:00:00:01")
	c := New(m, 16, 14*24*time.Hour)
	now := t0
	c.SetClock(func() time.Time { return now })
	if err := c.Correlate(ctx, tA); err != nil {
		t.Fatal(err)
	}
	a, _ := m.GetAddress(ctx, tA, "printer")
	if a.Link == nil || a.Link.PortID != sp || a.Link.SwitchID != "a-sw" || a.Link.SwitchName != "a-switch" || a.Link.PortName != "Gi0/1" ||
		a.Link.VLAN != 20 || a.Link.Source != store.LinkSNMPFDB || !a.Link.LastSeen.Equal(t0) {
		t.Fatalf("printer link %+v", a.Link)
	}
	b, _ := m.GetAddress(ctx, tA, "ns1")
	i, _ := m.GetInterface(ctx, tA, hi)
	if b.Link == nil || b.Link.PortID != i.RemoteInterfaceID {
		t.Fatalf("bound address shows the interface link: %+v / %+v", b.Link, i)
	}
	linked := 0
	for _, r := range m.Audit() {
		if r.Action == "port_linked" && r.SubjectKind == "address" {
			linked++
			if r.Detail["switch_interface_id"] != sp {
				t.Fatalf("audit %+v", r)
			}
		}
	}
	if linked != 2 {
		t.Fatalf("address port_linked rows %d", linked)
	}
	// The switch port lists the addresses behind it.
	ports, _ := m.ListInterfaces(ctx, tA, "a-sw")
	if len(ports) != 1 || len(ports[0].BehindAddresses) != 2 || ports[0].BehindAddresses[0].Address != "10.9.0.5" {
		t.Fatalf("behind addresses %+v", ports)
	}
	// Re-confirmed: last seen refreshed, no new audit.
	now = t0.Add(time.Hour)
	_ = c.Correlate(ctx, tA)
	if a, _ = m.GetAddress(ctx, tA, "printer"); !a.Link.LastSeen.Equal(now) || auditCount(m, "port_linked") != 3 {
		t.Fatalf("refresh %+v %d", a.Link, auditCount(m, "port_linked"))
	}
	// Moved to another port: superseded.
	_ = m.CreateInterface(ctx, store.DeviceInterface{ID: "a-port2", TenantID: tA, DeviceID: "a-sw", Name: "Gi0/2"})
	_ = m.ReplaceInterfaceLinks(ctx, tA, sp, []store.DeviceInterfaceLink{{RemotePortName: "aa:bb:cc:00:00:01", LinkSource: store.LinkSNMPFDB, LinkVlan: 20}})
	_ = m.ReplaceInterfaceLinks(ctx, tA, "a-port2", []store.DeviceInterfaceLink{{RemotePortName: "02:00:00:00:00:09", LinkSource: store.LinkSNMPFDB, LinkVlan: 30}})
	_ = c.Correlate(ctx, tA)
	if a, _ = m.GetAddress(ctx, tA, "printer"); a.Link.PortID != "a-port2" || a.Link.VLAN != 30 {
		t.Fatalf("moved %+v", a.Link)
	}
	// Forgotten by every FDB: cleared after the stale age.
	_ = m.ReplaceInterfaceLinks(ctx, tA, "a-port2", nil)
	now = t0.Add(20 * 24 * time.Hour)
	_ = c.Correlate(ctx, tA)
	if a, _ = m.GetAddress(ctx, tA, "printer"); a.Link != nil {
		t.Fatalf("stale link kept %+v", a.Link)
	}
	unlinked := 0
	for _, r := range m.Audit() {
		if r.Action == "port_unlinked" && r.SubjectKind == "address" && r.SubjectID == "printer" {
			unlinked++
		}
	}
	if unlinked != 2 { // superseded + stale
		t.Fatalf("address port_unlinked rows %d", unlinked)
	}
}

func TestCorrelateAddressLinkWriteFails(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	seed(t, m, tA, "a-")
	seedAddress(t, m, tA, "printer", "10.9.0.5", "02:00:00:00:00:09")
	m.FailNext("SetAddressLinks")
	if err := New(m, 16, time.Hour).Correlate(ctx, tA); err == nil {
		t.Fatal("address link write failure not returned")
	}
}

func TestCorrelateAddressWithoutMACClearsStaleLink(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	seed(t, m, tA, "a-")
	seedAddress(t, m, tA, "old", "10.9.0.6", "")
	old := t0.Add(-30 * 24 * time.Hour)
	if err := m.SetAddressLinks(ctx, tA, []store.IPAddress{{ID: "old", Link: &store.AddressLink{SwitchID: "a-sw", PortID: "a-port", Source: store.LinkSNMPFDB, LastSeen: &old}}}, nil); err != nil {
		t.Fatal(err)
	}
	c := New(m, 16, 14*24*time.Hour)
	c.SetClock(func() time.Time { return t0 })
	_ = c.Correlate(ctx, tA)
	if a, _ := m.GetAddress(ctx, tA, "old"); a.Link != nil {
		t.Fatalf("stale link of an address without MAC kept %+v", a.Link)
	}
}
