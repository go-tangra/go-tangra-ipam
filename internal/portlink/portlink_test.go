package portlink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// seed builds a switch with an access port that learned the host MAC and a
// host-reported device with that MAC, in tenant tid.
func seed(t *testing.T, m *memstore.Mem, tid, prefix string) (hostIface, swPort string) {
	t.Helper()
	ctx := context.Background()
	sw := store.Device{ID: prefix + "sw", TenantID: tid, Name: prefix + "switch", DeviceType: store.DevSwitch}
	if err := m.CreateDevice(ctx, sw); err != nil {
		t.Fatal(err)
	}
	p := store.DeviceInterface{ID: prefix + "port", TenantID: tid, DeviceID: sw.ID, Name: "Gi0/1"}
	_ = m.CreateInterface(ctx, p)
	if err := m.ReplaceInterfaceLinks(ctx, tid, p.ID, []store.DeviceInterfaceLink{
		{RemotePortName: "AA:BB:CC:00:00:01", LinkSource: store.LinkSNMPFDB, LinkVlan: 20},
		{RemotePortName: "02:00:00:00:00:09", LinkSource: store.LinkSNMPFDB, LinkVlan: 20}, // second MAC on the port (0005)
	}); err != nil {
		t.Fatal(err)
	}
	err := m.ApplyHostReport(ctx, tid, func(tx repo.HostTx) error {
		if err := tx.InsertDevice(store.Device{ID: prefix + "host", Name: prefix + "web", Source: store.SrcHostReport, InventoryHostID: prefix + "h"}); err != nil {
			return err
		}
		return tx.UpsertInterfaceReported(store.DeviceInterface{ID: prefix + "hi", DeviceID: prefix + "host", Name: "eth0",
			MACAddress: "aa:bb:cc:00:00:01", ReportState: store.RepReported}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	return prefix + "hi", p.ID
}

func auditCount(m *memstore.Mem, action string) int {
	n := 0
	for _, a := range m.Audit() {
		if a.Action == action {
			n++
		}
	}
	return n
}

func TestCorrelateLinksRefreshesAndClears(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	hi, sp := seed(t, m, tA, "a-")
	c := New(m, 16, 14*24*time.Hour)
	now := t0
	c.SetClock(func() time.Time { return now })
	if err := c.Correlate(ctx, tA); err != nil {
		t.Fatal(err)
	}
	i, _ := m.GetInterface(ctx, tA, hi)
	if i.RemoteInterfaceID != sp || i.RemoteDeviceID != "a-sw" || i.RemotePortName != "Gi0/1" || i.LinkVlan != 20 ||
		i.LinkSource != store.LinkSNMPFDB || !i.LinkLastSeen.Equal(t0) || auditCount(m, "port_linked") != 1 {
		t.Fatalf("linked %+v", i)
	}
	// Re-confirmation refreshes link_last_seen, no new audit.
	now = t0.Add(time.Hour)
	_ = c.Correlate(ctx, tA)
	if i, _ = m.GetInterface(ctx, tA, hi); !i.LinkLastSeen.Equal(now) || auditCount(m, "port_linked") != 1 {
		t.Fatal("refresh")
	}
	// The FDB forgets the MAC; after the stale age the link is cleared.
	_ = m.ReplaceInterfaceLinks(ctx, tA, sp, nil)
	now = t0.Add(20 * 24 * time.Hour)
	_ = c.Correlate(ctx, tA)
	if i, _ = m.GetInterface(ctx, tA, hi); i.RemoteInterfaceID != "" || auditCount(m, "port_unlinked") != 1 {
		t.Fatalf("stale %+v", i)
	}
	// Reverse lookup: nothing to do for a tenant without switches.
	if err := c.Correlate(ctx, tB); err != nil {
		t.Fatal(err)
	}
}

func TestCorrelateSupersedesAndOnlyHostReported(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	hi, _ := seed(t, m, tA, "a-")
	// A manual device with the same MAC is never linked.
	_ = m.CreateDevice(ctx, store.Device{ID: "man", TenantID: tA, Name: "manual"})
	_ = m.CreateInterface(ctx, store.DeviceInterface{ID: "mi", TenantID: tA, DeviceID: "man", Name: "eth0", MACAddress: "aa:bb:cc:00:00:01"})
	// Existing link to another port -> superseded.
	_ = m.SetInterfaceLinks(ctx, tA, []store.DeviceInterface{{ID: hi, RemoteInterfaceID: "old", RemoteDeviceID: "x", LinkSource: store.LinkLLDP}}, nil)
	c := New(m, 16, time.Hour)
	if err := c.Correlate(ctx, tA); err != nil {
		t.Fatal(err)
	}
	if auditCount(m, "port_unlinked") != 1 || auditCount(m, "port_linked") != 1 {
		t.Fatal("superseded link audited")
	}
	if mi, _ := m.GetInterface(ctx, tA, "mi"); mi.RemoteInterfaceID != "" {
		t.Fatal("manual device linked")
	}
}

func TestCorrelateTenantScoped(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	// Tenant B has the switch and the FDB entry; tenant A has only the host.
	_, _ = seed(t, m, tB, "b-")
	err := m.ApplyHostReport(ctx, tA, func(tx repo.HostTx) error {
		_ = tx.InsertDevice(store.Device{ID: "ah", Name: "a-web", Source: store.SrcHostReport, InventoryHostID: "x"})
		return tx.UpsertInterfaceReported(store.DeviceInterface{ID: "ahi", DeviceID: "ah", Name: "eth0", MACAddress: "aa:bb:cc:00:00:01", ReportState: store.RepReported}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	c := New(m, 16, time.Hour)
	_ = c.Correlate(ctx, tA)
	_ = c.Correlate(ctx, tB)
	if i, _ := m.GetInterface(ctx, tA, "ahi"); i.RemoteInterfaceID != "" {
		t.Fatal("tenant B's FDB linked a tenant A host")
	}
	if i, _ := m.GetInterface(ctx, tB, "b-hi"); i.RemoteInterfaceID == "" {
		t.Fatal("tenant B host linked in its own tenant")
	}
	m.FailNext("PortLinkData")
	if err := c.Correlate(ctx, tA); err == nil {
		t.Fatal("store error")
	}
	if err := m.SetInterfaceLinks(ctx, tA, []store.DeviceInterface{{ID: "b-hi"}}, nil); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant write refused")
	}
	m.FailNext("SetInterfaceLinks")
	if err := m.SetInterfaceLinks(ctx, tA, nil, nil); err == nil {
		t.Fatal("injected")
	}
}

func TestBuildInputSkipsJunk(t *testing.T) {
	in := BuildInput(repo.PortLinkData{
		Switches:     []store.Device{{ID: "s", Name: "SW"}},
		SwitchIfaces: []store.DeviceInterface{{ID: "p", DeviceID: "s", MACAddress: "de:ad:00:00:00:01"}},
		Links: []store.DeviceInterfaceLink{{InterfaceID: "p", LinkSource: store.LinkSNMPFDB, RemotePortName: "junk"},
			{InterfaceID: "nope", LinkSource: store.LinkSNMPFDB, RemotePortName: "aa:bb:cc:00:00:01"},
			{InterfaceID: "p", LinkSource: store.LinkLLDP, RemotePortName: "host eth0"},
			{InterfaceID: "p", LinkSource: "manual", RemotePortName: "x"}},
		HostIfaces: []store.DeviceInterface{{ID: "h", MACAddress: "bad"}},
	}, 16)
	if len(in.Ports) != 1 || len(in.Ports[0].MACs) != 0 || len(in.Ports[0].LLDP) != 1 || len(in.Hosts) != 0 ||
		!in.SwitchNames["sw"] || in.SwitchMACs["de:ad:00:00:00:01"] != "s" {
		t.Fatalf("%+v", in)
	}
}

func TestInterfaceReadsShowBothEnds(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	hi, sp := seed(t, m, tA, "a-")
	_ = New(m, 16, time.Hour).Correlate(ctx, tA)
	hl, _ := m.ListInterfaces(ctx, tA, "a-host")
	sl, _ := m.ListInterfaces(ctx, tA, "a-sw")
	if len(hl) != 1 || hl[0].ID != hi || hl[0].RemoteDeviceName != "a-switch" {
		t.Fatalf("host side %+v", hl)
	}
	if len(sl) != 1 || sl[0].ID != sp || sl[0].BehindDeviceID != "a-host" || sl[0].BehindDeviceName != "a-web" {
		t.Fatalf("switch side %+v", sl)
	}
}
