//go:build integration

package repodb_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/portlink"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestInterfaceLinksUniqueFix is the regression test for the link uniqueness
// defect (T105): before 0005 a switch port that learned two MACs cannot be
// stored; after 0005 it can, and an SNMP scan persists every interface.
func TestInterfaceLinksUniqueFix(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, adminDSN, 4); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	var uniques int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conrelid='ipam_device_interface_links'::regclass AND contype='u'`).Scan(&uniques); err != nil || uniques != 1 {
		t.Fatalf("exactly one unique constraint before 0005: %d %v", uniques, err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := repodb.New(st)
	sw := store.Device{ID: store.NewID(), TenantID: tenantA, Name: "sw1", DeviceType: store.DevSwitch}
	if err := db.CreateDevice(ctx, sw); err != nil {
		t.Fatal(err)
	}
	port := store.DeviceInterface{ID: store.NewID(), TenantID: tenantA, DeviceID: sw.ID, Name: "Gi0/1"}
	if err := db.CreateInterface(ctx, port); err != nil {
		t.Fatal(err)
	}
	two := []store.DeviceInterfaceLink{
		{RemotePortName: "aa:bb:cc:00:00:01", LinkSource: store.LinkSNMPFDB, LinkVlan: 10},
		{RemotePortName: "aa:bb:cc:00:00:02", LinkSource: store.LinkSNMPFDB, LinkVlan: 10},
	}
	if err := db.ReplaceInterfaceLinks(ctx, tenantA, port.ID, two); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("documented defect: two FDB MACs on one port fail before 0005, got %v", err)
	}
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceInterfaceLinks(ctx, tenantA, port.ID, two); err != nil {
		t.Fatalf("after 0005: %v", err)
	}
	if l, _ := db.ListInterfaceLinks(ctx, tenantA, port.ID); len(l) != 2 {
		t.Fatalf("links %d", len(l))
	}
	dup := append(two, two[0])
	if err := db.ReplaceInterfaceLinks(ctx, tenantA, port.ID, dup); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("an identical link is still unique: %v", err)
	}

	// An SNMP scan of a switch whose first port learned two MACs persists the
	// following interfaces too, and correlation links the reported host.
	sub := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "mgmt", CIDR: "10.0.0.0/29", IPVersion: 4, PrefixLength: 29}
	if err := db.CreateSubnet(ctx, sub); err != nil {
		t.Fatal(err)
	}
	env, _ := sealed.NewEnvelope(bytes.Repeat([]byte{4}, 32))
	blob, err := snmpcred.Seal(env, tenantA, sub.ID, snmpcred.Input{Version: 2, Community: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSubnetSNMP(ctx, store.SubnetSNMP{TenantID: tenantA, SubnetID: sub.ID, Version: 2, Sealed: blob, UpdatedBy: "u"},
		store.AuditRow{Action: "snmp_credentials_set", ActorKind: "user", ActorID: "u", SubjectKind: "subnet", SubjectID: sub.ID, Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	disc := snmp.NewFake()
	disc.Set("10.0.0.1", snmp.DiscoveredDevice{SysName: "sw2", DeviceType: store.DevSwitch,
		Interfaces: []snmp.Interface{{Name: "Gi0/1", IfIndex: 1}, {Name: "Gi0/2", IfIndex: 2}},
		Links: []snmp.Link{
			{RemotePort: "aa:bb:cc:00:00:03", Source: snmp.SourceSNMPFDB, VLAN: 30, IfIndex: 1},
			{RemotePort: "aa:bb:cc:00:00:04", Source: snmp.SourceSNMPFDB, VLAN: 30, IfIndex: 1},
			{RemotePort: "aa:bb:cc:00:00:05", Source: snmp.SourceSNMPFDB, VLAN: 30, IfIndex: 2},
		}})
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		h := store.Device{ID: store.NewID(), Name: "web", Status: "active", DeviceType: "server", Source: store.SrcHostReport, InventoryHostID: store.NewID()}
		if err := tx.InsertDevice(h); err != nil {
			return err
		}
		return tx.UpsertInterfaceReported(store.DeviceInterface{ID: store.NewID(), DeviceID: h.ID, Name: "eth0", MACAddress: "aa:bb:cc:00:00:05", Enabled: true, ReportState: store.RepReported}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := scan.New(db, icmp.NewFake("10.0.0.1"), icmp.NewFake(), disc, events.HubPublisher{},
		scan.Config{MaxHosts: 64, Concurrency: 4, TimeoutMs: 100, Workers: 1, MaxRetries: 0}, nil)
	svc.SetEnvelope(env)
	svc.SetLinker(portlink.New(db, 16, 14*24*time.Hour))
	if _, err := svc.StartScan(ctx, authz.Subjects{TenantID: tenantA, UserID: "u", Roles: []string{"admin"}, ActorKind: authz.ActorUser}, sub.ID, scan.Options{EnableSNMP: true, SkipReverseDNS: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunOnce(ctx, nil); err != nil {
		t.Fatal(err)
	}
	devs, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{Query: "sw2"})
	if len(devs) != 1 {
		t.Fatal("switch discovered")
	}
	ifs, _ := db.ListInterfaces(ctx, tenantA, devs[0].ID)
	if len(ifs) != 2 {
		t.Fatalf("persistDevice stopped after the first port: %d interfaces", len(ifs))
	}
	hosts, _ := db.HostDevices(ctx, tenantA)
	hifs, _ := db.ListInterfaces(ctx, tenantA, hosts[0].ID)
	if len(hifs) != 1 || hifs[0].RemoteDeviceName != "sw2" || hifs[0].RemotePortName != "Gi0/2" || hifs[0].LinkVlan != 30 {
		t.Fatalf("host interface link %+v", hifs)
	}
	for _, i := range ifs {
		if i.Name == "Gi0/2" && i.BehindDeviceName != "web" {
			t.Fatalf("device behind the switch port %+v", i)
		}
	}
}
