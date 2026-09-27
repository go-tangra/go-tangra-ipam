package portlink

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Per-switch links (022 research D6): a host bonded across a switch pair
// (MLAG / LACP) is learned on a port of each switch with the same MAC count.

const bondMAC = "d2:f1:15:7f:0a:5c"

// seedSwitch adds a switch with one port that learned mac plus filler MACs.
func seedSwitch(t *testing.T, m *memstore.Mem, tid, id, name, port string, mac string, filler int) string {
	t.Helper()
	ctx := context.Background()
	if _, err := m.GetDevice(ctx, tid, id); err != nil {
		if err := m.CreateDevice(ctx, store.Device{ID: id, TenantID: tid, Name: name, DeviceType: store.DevSwitch}); err != nil {
			t.Fatal(err)
		}
	}
	pid := id + "-" + port
	if err := m.CreateInterface(ctx, store.DeviceInterface{ID: pid, TenantID: tid, DeviceID: id, Name: port}); err != nil {
		t.Fatal(err)
	}
	links := []store.DeviceInterfaceLink{{RemotePortName: mac, LinkSource: store.LinkSNMPFDB, LinkVlan: 30}}
	for n := 0; n < filler; n++ {
		links = append(links, store.DeviceInterfaceLink{RemotePortName: "02:00:00:00:00:" + string(rune('a'+n)) + "0", LinkSource: store.LinkSNMPFDB, LinkVlan: 30})
	}
	if err := m.ReplaceInterfaceLinks(ctx, tid, pid, links); err != nil {
		t.Fatal(err)
	}
	return pid
}

func secondaryCount(m *memstore.Mem, action string) int {
	n := 0
	for _, a := range m.Audit() {
		if a.Action == action && a.Detail["secondary"] == true {
			n++
		}
	}
	return n
}

func TestCorrelateBondedHost(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	p1 := seedSwitch(t, m, tA, "cs1", "cs1", "17", bondMAC, 5)
	p2 := seedSwitch(t, m, tA, "cs2", "cs2", "17", bondMAC, 5)
	err := m.ApplyHostReport(ctx, tA, func(tx repo.HostTx) error {
		if err := tx.InsertDevice(store.Device{ID: "ns1", Name: "ns1", Source: store.SrcHostReport, InventoryHostID: "inv"}); err != nil {
			return err
		}
		return tx.UpsertInterfaceReported(store.DeviceInterface{ID: "ns1-bond0", DeviceID: "ns1", Name: "bond0", MACAddress: bondMAC, ReportState: store.RepReported}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	seedAddress(t, m, tA, "ns1-ip", "10.9.0.53", bondMAC) // carried by the reported interface
	c := New(m, 16, 24*time.Hour)
	now := t0
	c.SetClock(func() time.Time { return now })
	if err := c.Correlate(ctx, tA); err != nil {
		t.Fatal(err)
	}
	hl, _ := m.ListInterfaces(ctx, tA, "ns1")
	if len(hl) != 1 || hl[0].RemoteInterfaceID != p1 || len(hl[0].Links) != 2 || !hl[0].Links[0].Primary ||
		hl[0].Links[0].PortID != p1 || hl[0].Links[1].PortID != p2 || hl[0].Links[1].Primary ||
		hl[0].Links[1].SwitchName != "cs2" || hl[0].Links[1].VLAN != 30 || !hl[0].Links[1].LastSeen.Equal(t0) {
		t.Fatalf("interface links %+v", hl)
	}
	a, _ := m.GetAddress(ctx, tA, "ns1-ip")
	if a.Link == nil || a.Link.PortID != p1 || len(a.Links) != 2 || a.Links[1].PortID != p2 {
		t.Fatalf("address links %+v", a)
	}
	// Both switches' ports show the host and the address behind them.
	for _, sw := range []string{"cs1", "cs2"} {
		ports, _ := m.ListInterfaces(ctx, tA, sw)
		if len(ports) != 1 || ports[0].BehindDeviceName != "ns1" || len(ports[0].BehindAddresses) != 1 {
			t.Fatalf("behind %s %+v", sw, ports)
		}
	}
	// Primary audited as before, the secondary with "secondary": true.
	if auditCount(m, "port_linked") != 4 || secondaryCount(m, "port_linked") != 2 {
		t.Fatalf("audit %d/%d", auditCount(m, "port_linked"), secondaryCount(m, "port_linked"))
	}
	// Re-confirmation refreshes last_seen without audit.
	now = t0.Add(time.Hour)
	_ = c.Correlate(ctx, tA)
	hl, _ = m.ListInterfaces(ctx, tA, "ns1")
	if !hl[0].Links[1].LastSeen.Equal(now) || auditCount(m, "port_linked") != 4 {
		t.Fatalf("refresh %+v", hl[0].Links)
	}
	// cs2 moves the host to another port: the old secondary is superseded.
	p3 := seedSwitch(t, m, tA, "cs2", "cs2", "18", bondMAC, 5)
	_ = m.ReplaceInterfaceLinks(ctx, tA, p2, nil)
	now = t0.Add(2 * time.Hour)
	_ = c.Correlate(ctx, tA)
	hl, _ = m.ListInterfaces(ctx, tA, "ns1")
	if len(hl[0].Links) != 2 || hl[0].Links[1].PortID != p3 || secondaryCount(m, "port_unlinked") != 2 || secondaryCount(m, "port_linked") != 4 {
		t.Fatalf("superseded %+v %d", hl[0].Links, secondaryCount(m, "port_unlinked"))
	}
	// cs2 forgets the MAC: the secondary stays until the stale age, then goes.
	_ = m.ReplaceInterfaceLinks(ctx, tA, p3, nil)
	now = t0.Add(3 * time.Hour)
	_ = c.Correlate(ctx, tA)
	if hl, _ = m.ListInterfaces(ctx, tA, "ns1"); len(hl[0].Links) != 2 {
		t.Fatalf("kept until stale %+v", hl[0].Links)
	}
	now = t0.Add(30 * time.Hour)
	_ = c.Correlate(ctx, tA)
	hl, _ = m.ListInterfaces(ctx, tA, "ns1")
	if len(hl[0].Links) != 1 || hl[0].Links[0].PortID != p1 || !hl[0].Links[0].Primary || secondaryCount(m, "port_unlinked") != 4 {
		t.Fatalf("stale secondary %+v", hl[0].Links)
	}
	if a, _ = m.GetAddress(ctx, tA, "ns1-ip"); len(a.Links) != 1 {
		t.Fatalf("address stale secondary %+v", a.Links)
	}
	// Deleting the switch removes its links (cascade).
	if err := m.DeleteDevice(ctx, tA, "cs1", true); err != nil {
		t.Fatal(err)
	}
	if hl, _ = m.ListInterfaces(ctx, tA, "ns1"); len(hl[0].Links) != 0 {
		t.Fatalf("cascade %+v", hl[0].Links)
	}
}

// TestCorrelateBondedAddress: an agentless address learned on two switches
// gets a link on each; the primary moves when its switch loses the MAC.
func TestCorrelateBondedAddress(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	p1 := seedSwitch(t, m, tA, "cs1", "cs1", "5", bondMAC, 3)
	p2 := seedSwitch(t, m, tA, "cs2", "cs2", "5", bondMAC, 3)
	seedAddress(t, m, tA, "srv", "10.9.0.7", bondMAC)
	c := New(m, 16, time.Hour)
	now := t0
	c.SetClock(func() time.Time { return now })
	if err := c.Correlate(ctx, tA); err != nil {
		t.Fatal(err)
	}
	a, _ := m.GetAddress(ctx, tA, "srv")
	if a.Link == nil || a.Link.PortID != p1 || len(a.Links) != 2 || !a.Links[0].Primary || a.Links[1].PortID != p2 {
		t.Fatalf("links %+v", a)
	}
	// cs1 forgets the MAC: cs2 becomes primary, cs1 stays a secondary link
	// until stale.
	_ = m.ReplaceInterfaceLinks(ctx, tA, p1, nil)
	now = t0.Add(time.Minute)
	_ = c.Correlate(ctx, tA)
	a, _ = m.GetAddress(ctx, tA, "srv")
	if a.Link.PortID != p2 || len(a.Links) != 2 || a.Links[0].PortID != p2 || !a.Links[0].Primary || a.Links[1].Primary {
		t.Fatalf("primary moved %+v", a.Links)
	}
	now = t0.Add(2 * time.Hour)
	_ = c.Correlate(ctx, tA)
	if a, _ = m.GetAddress(ctx, tA, "srv"); len(a.Links) != 1 || a.Links[0].PortID != p2 {
		t.Fatalf("stale %+v", a.Links)
	}
	// Deleting the address drops its set.
	if err := m.DeleteAddress(ctx, tA, "srv"); err != nil {
		t.Fatal(err)
	}
	ports, _ := m.ListInterfaces(ctx, tA, "cs2")
	if len(ports) != 1 || len(ports[0].BehindAddresses) != 0 {
		t.Fatalf("deleted address behind %+v", ports)
	}
}
