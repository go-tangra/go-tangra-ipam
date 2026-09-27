//go:build integration

package repodb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/portlink"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestHostSwitchLinks (022 per-switch links): correlation of a host bonded
// across a switch pair writes one link per switch for its interface and its
// address, both switches' ports show it behind them, the table is
// tenant-isolated by RLS with CHECKs, and deleting the host, a port or a
// switch removes the links.
func TestHostSwitchLinks(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	db := repodb.New(st)

	const mac = "d2:f1:15:7f:0a:5c"
	var switches, ports []string
	for _, name := range []string{"cs1", "cs2"} {
		sw := store.Device{ID: store.NewID(), TenantID: tenantA, Name: name, DeviceType: store.DevSwitch}
		if err := db.CreateDevice(ctx, sw); err != nil {
			t.Fatal(err)
		}
		port := store.DeviceInterface{ID: store.NewID(), TenantID: tenantA, DeviceID: sw.ID, Name: "Port 17", IfIndex: 17}
		if err := db.CreateInterface(ctx, port); err != nil {
			t.Fatal(err)
		}
		fdb := []store.DeviceInterfaceLink{{RemotePortName: mac, LinkSource: store.LinkSNMPFDB, LinkVlan: 30}}
		for _, filler := range []string{"02:00:00:00:00:01", "02:00:00:00:00:02", "02:00:00:00:00:03"} {
			fdb = append(fdb, store.DeviceInterfaceLink{RemotePortName: filler, LinkSource: store.LinkSNMPFDB, LinkVlan: 30})
		}
		if err := db.ReplaceInterfaceLinks(ctx, tenantA, port.ID, fdb); err != nil {
			t.Fatal(err)
		}
		switches, ports = append(switches, sw.ID), append(ports, port.ID)
	}
	host, iface := store.NewID(), store.NewID()
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		if err := tx.InsertDevice(store.Device{ID: host, Name: "ns1", Status: "active", DeviceType: "server", Source: store.SrcHostReport, InventoryHostID: store.NewID()}); err != nil {
			return err
		}
		return tx.UpsertInterfaceReported(store.DeviceInterface{ID: iface, DeviceID: host, Name: "bond0", MACAddress: mac, Enabled: true, ReportState: store.RepReported}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "dns", CIDR: "10.40.0.0/24", IPVersion: 4, PrefixLength: 24}
	if err := db.CreateSubnet(ctx, sub); err != nil {
		t.Fatal(err)
	}
	addr := store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: sub.ID, Address: "10.40.0.53", Hostname: "ns1", MACAddress: mac, MACSource: store.MACSourceARP}
	if err := db.CreateAddress(ctx, addr); err != nil {
		t.Fatal(err)
	}

	// Correlation: equal MAC counts on both switches -> two links each, cs1
	// (lowest id) primary... unless cs2's id sorts first.
	primary, secondary := 0, 1
	if switches[1] < switches[0] {
		primary, secondary = 1, 0
	}
	c := portlink.New(db, 16, 24*time.Hour)
	for run := 0; run < 2; run++ { // the second run re-confirms without audit
		if err := c.Correlate(ctx, tenantA); err != nil {
			t.Fatal(err)
		}
	}
	hl, err := db.ListInterfaces(ctx, tenantA, host)
	if err != nil || len(hl) != 1 || hl[0].RemoteInterfaceID != ports[primary] || len(hl[0].Links) != 2 ||
		!hl[0].Links[0].Primary || hl[0].Links[0].PortID != ports[primary] || hl[0].Links[1].Primary ||
		hl[0].Links[1].PortID != ports[secondary] || hl[0].Links[1].SwitchName == "" || hl[0].Links[1].VLAN != 30 ||
		hl[0].Links[1].Source != store.LinkSNMPFDB || hl[0].Links[1].LastSeen == nil || hl[0].Links[1].PortName != "Port 17" {
		t.Fatalf("interface links %+v %v", hl, err)
	}
	a, err := db.GetAddress(ctx, tenantA, addr.ID)
	if err != nil || a.Link == nil || a.Link.PortID != ports[primary] || len(a.Links) != 2 || !a.Links[0].Primary ||
		a.Links[1].PortID != ports[secondary] || a.Links[1].SwitchName == "" || a.Links[1].LastSeen == nil {
		t.Fatalf("address links %+v %v", a, err)
	}
	for _, sw := range switches {
		pl, err := db.ListInterfaces(ctx, tenantA, sw)
		if err != nil || len(pl) != 1 || pl[0].BehindDeviceName != "ns1" || len(pl[0].BehindAddresses) != 1 || pl[0].BehindAddresses[0].Address != "10.40.0.53" {
			t.Fatalf("behind %s %+v %v", sw, pl, err)
		}
	}
	var linked, secondaryLinked int
	_ = admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE detail->>'secondary' = 'true') FROM ipam_audit_events
		WHERE action='port_linked'`).Scan(&linked, &secondaryLinked)
	if linked != 4 || secondaryLinked != 2 {
		t.Fatalf("audit port_linked %d secondary %d", linked, secondaryLinked)
	}

	// RLS: tenant B sees and changes nothing; system scope sees all rows.
	count := func(scope store.Scope) (n int) {
		if err := st.Tx(ctx, scope, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_host_switch_links").Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if a, b, sys := count(store.Scope{TenantID: tenantA}), count(store.Scope{TenantID: tenantB}), count(store.Scope{System: true}); a != 4 || b != 0 || sys != 4 {
		t.Fatalf("rls a=%d b=%d sys=%d", a, b, sys)
	}
	err = st.Tx(ctx, store.Scope{TenantID: tenantB}, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_host_switch_links (tenant_id, host_kind, host_id, switch_id, port_id, source, last_seen)
			VALUES ($1,'interface',$2,$3,$4,'lldp',now())`, tenantA, iface, switches[0], ports[0])
		return e
	})
	if err == nil {
		t.Fatal("tenant B wrote a tenant A link")
	}
	if err := db.SetInterfaceLinks(ctx, tenantB, []store.DeviceInterface{{ID: iface}}, nil); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant set: %v", err)
	}
	if n := count(store.Scope{TenantID: tenantA}); n != 4 {
		t.Fatalf("cross-tenant write changed rows: %d", n)
	}
	for name, q := range map[string]string{
		"host kind": `INSERT INTO ipam_host_switch_links (tenant_id, host_kind, host_id, switch_id, port_id, source, last_seen) VALUES ($1,'device',$2,$3,$4,'lldp',now())`,
		"source":    `INSERT INTO ipam_host_switch_links (tenant_id, host_kind, host_id, switch_id, port_id, source, last_seen) VALUES ($1,'address',$2,$3,$4,'manual',now())`,
	} {
		if _, err := admin.Exec(ctx, q, tenantA, store.NewID(), switches[0], ports[0]); err == nil {
			t.Errorf("CHECK %s accepted", name)
		}
	}

	// A set write replaces the host's rows (a port that vanished meanwhile is
	// skipped, not an error).
	seen := time.Now().UTC()
	if err := db.SetInterfaceLinks(ctx, tenantA, []store.DeviceInterface{{ID: iface, RemoteDeviceID: switches[primary], RemoteInterfaceID: ports[primary],
		RemotePortName: "Port 17", LinkSource: store.LinkSNMPFDB, LinkVlan: 30, LinkLastSeen: &seen,
		Links: []store.HostSwitchLink{{SwitchID: switches[primary], PortID: ports[primary], PortName: "Port 17", VLAN: 30, Source: store.LinkSNMPFDB, LastSeen: &seen},
			{SwitchID: switches[secondary], PortID: store.NewID(), Source: store.LinkSNMPFDB, LastSeen: &seen}}}}, nil); err != nil {
		t.Fatal(err)
	}
	if hl, _ = db.ListInterfaces(ctx, tenantA, host); len(hl[0].Links) != 1 || hl[0].Links[0].PortID != ports[primary] {
		t.Fatalf("replaced set %+v", hl[0].Links)
	}
	if n := count(store.Scope{TenantID: tenantA}); n != 3 {
		t.Fatalf("rows after replace %d", n)
	}

	// Cascades: a switch port (FK), the host device's interface (trigger),
	// the address (trigger), the switch (FK).
	_ = c.Correlate(ctx, tenantA) // back to two links each
	if n := count(store.Scope{TenantID: tenantA}); n != 4 {
		t.Fatalf("re-correlated rows %d", n)
	}
	if err := db.DeleteInterface(ctx, tenantA, ports[secondary]); err != nil {
		t.Fatal(err)
	}
	if n := count(store.Scope{TenantID: tenantA}); n != 2 {
		t.Fatalf("port delete left %d rows", n)
	}
	if err := db.DeleteDevice(ctx, tenantA, host, true); err != nil {
		t.Fatal(err)
	}
	if n := count(store.Scope{TenantID: tenantA}); n != 1 {
		t.Fatalf("host delete left %d rows", n)
	}
	if err := db.DeleteAddress(ctx, tenantA, addr.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(store.Scope{TenantID: tenantA}); n != 0 {
		t.Fatalf("address delete left %d rows", n)
	}
	addr2 := store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: sub.ID, Address: "10.40.0.54", MACAddress: mac}
	if err := db.CreateAddress(ctx, addr2); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAddressLinks(ctx, tenantA, []store.IPAddress{{ID: addr2.ID,
		Links: []store.HostSwitchLink{{SwitchID: switches[primary], PortID: ports[primary], Source: store.LinkLLDP}}}}, nil); err != nil {
		t.Fatal(err)
	}
	if a, _ := db.GetAddress(ctx, tenantA, addr2.ID); len(a.Links) != 1 || a.Links[0].Primary || a.Links[0].LastSeen == nil {
		t.Fatalf("secondary-only link %+v", a.Links)
	}
	if err := db.DeleteDevice(ctx, tenantA, switches[primary], true); err != nil {
		t.Fatal(err)
	}
	if n := count(store.Scope{System: true}); n != 0 {
		t.Fatalf("switch delete left %d rows", n)
	}
}
