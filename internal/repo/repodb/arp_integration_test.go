//go:build integration

package repodb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestARPStore (022 T038): 0007 -> 0008 upgrade with the MAC provenance
// backfill, RLS on ipam_arp_settings, the guarded ApplyARP transaction (and
// its rollback), address links, the MAC search, host-sync provenance, the
// scan job ARP columns and the scan upsert keeping a MAC.
func TestARPStore(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, adminDSN, 7); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	subA, subB := store.NewID(), store.NewID()
	aAgent, aManual, aEmpty := store.NewID(), store.NewID(), store.NewID()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO ipam_subnets (id, tenant_id, name, cidr, ip_version) VALUES ($1,$2,'a','10.30.0.0/24',4)`, []any{subA, tenantA}},
		{`INSERT INTO ipam_subnets (id, tenant_id, name, cidr, ip_version) VALUES ($1,$2,'b','10.30.0.0/24',4)`, []any{subB, tenantB}},
		{`INSERT INTO ipam_ip_addresses (id, tenant_id, address, subnet_id, mac_address, report_state) VALUES ($1,$2,'10.30.0.4',$3,'52:54:00:00:00:04','reported')`, []any{aAgent, tenantA, subA}},
		{`INSERT INTO ipam_ip_addresses (id, tenant_id, address, subnet_id, mac_address) VALUES ($1,$2,'10.30.0.3',$3,'00:11:22:33:44:03')`, []any{aManual, tenantA, subA}},
		{`INSERT INTO ipam_ip_addresses (id, tenant_id, address, subnet_id) VALUES ($1,$2,'10.30.0.1',$3)`, []any{aEmpty, tenantA, subA}},
	} {
		if _, err := admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed at 0007: %v", err)
		}
	}
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatalf("upgrade to 0008: %v", err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := repodb.New(st)
	get := func(tid, id string) store.IPAddress {
		t.Helper()
		a, err := db.GetAddress(ctx, tid, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return a
	}
	if a := get(tenantA, aAgent); a.MACSource != store.MACSourceAgent {
		t.Fatalf("backfill agent %+v", a)
	}
	if a := get(tenantA, aManual); a.MACSource != store.MACSourceManual {
		t.Fatalf("backfill manual %+v", a)
	}
	if a := get(tenantA, aEmpty); a.MACSource != "" || a.Link != nil || a.Origin != "" {
		t.Fatalf("backfill empty %+v", a)
	}

	// ARP settings: defaults, write + audit in one transaction, RLS, CHECKs.
	s, err := db.GetARPSettings(ctx, tenantA)
	if err != nil || !s.Enabled || s.ProxyThreshold != 8 || len(s.ExcludedDevices) != 0 {
		t.Fatalf("defaults %+v %v", s, err)
	}
	router := store.Device{ID: store.NewID(), TenantID: tenantA, Name: "mikrotik", DeviceType: store.DevRouter}
	sw := store.Device{ID: store.NewID(), TenantID: tenantA, Name: "msw-rack2", DeviceType: store.DevSwitch}
	srv := store.Device{ID: store.NewID(), TenantID: tenantA, Name: "web", DeviceType: store.DevServer}
	for _, d := range []store.Device{router, sw, srv} {
		if err := db.CreateDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	row := store.AuditRow{Action: "arp_settings_updated", ActorKind: "user", ActorID: "u1", SubjectKind: "tenant", SubjectID: tenantA, Outcome: "ok"}
	if err := db.PutARPSettings(ctx, store.ARPSettings{TenantID: tenantA, Enabled: false, ExcludedDevices: []string{router.ID}, ProxyThreshold: 12, UpdatedBy: "u1"}, row); err != nil {
		t.Fatal(err)
	}
	if s, _ = db.GetARPSettings(ctx, tenantA); s.Enabled || s.ProxyThreshold != 12 || len(s.ExcludedDevices) != 1 || s.ExcludedDevices[0] != router.ID || s.UpdatedAt.IsZero() {
		t.Fatalf("stored %+v", s)
	}
	if s, _ = db.GetARPSettings(ctx, tenantB); !s.Enabled || s.ProxyThreshold != 8 {
		t.Fatalf("tenant B sees tenant A settings %+v", s)
	}
	if err := db.PutARPSettings(ctx, store.ARPSettings{TenantID: tenantA, Enabled: true, ProxyThreshold: 1}, row); err == nil {
		t.Fatal("threshold CHECK")
	}
	var n int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE action='arp_settings_updated'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("settings audit %d %v", n, err)
	}

	// Network-device MACs.
	for _, i := range []store.DeviceInterface{
		{ID: store.NewID(), TenantID: tenantA, DeviceID: router.ID, Name: "ether1", MACAddress: "4C:5E:0C:00:00:01"},
		{ID: store.NewID(), TenantID: tenantA, DeviceID: srv.ID, Name: "eth0", MACAddress: "52:54:00:00:00:99"},
	} {
		if err := db.CreateInterface(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	port := store.DeviceInterface{ID: store.NewID(), TenantID: tenantA, DeviceID: sw.ID, Name: "14", MACAddress: "00:04:96:00:00:14"}
	if err := db.CreateInterface(ctx, port); err != nil {
		t.Fatal(err)
	}
	macs, err := db.NetworkMACs(ctx, tenantA)
	if err != nil || len(macs) != 2 || !macs["4c:5e:0c:00:00:01"] || !macs["00:04:96:00:00:14"] {
		t.Fatalf("network macs %v %v", macs, err)
	}

	// ApplyARP: applied ops write their audit rows, guarded ones do not.
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	audit := func(action, id string) []store.AuditRow {
		return []store.AuditRow{{Action: action, ActorKind: "system", ActorID: "scan", SubjectKind: "address", SubjectID: id, Outcome: "ok", At: at}}
	}
	aNew, aForeign := store.NewID(), store.NewID()
	ops := []store.ARPOp{
		{Kind: store.ARPFill, AddressID: aEmpty, MAC: "0a:5c:d2:f1:00:01", SourceDeviceID: router.ID, At: at, Audit: audit("mac_learned", aEmpty)},
		{Kind: store.ARPConflict, AddressID: aManual, MAC: "0a:5c:d2:f1:00:03", SourceDeviceID: router.ID, At: at, Audit: audit("mac_conflict", aManual)},
		{Kind: store.ARPFill, AddressID: aAgent, MAC: "0a:5c:d2:f1:00:04", SourceDeviceID: router.ID, At: at, Audit: audit("mac_learned", aAgent)},
		{Kind: store.ARPCreate, AddressID: aNew, Address: "10.30.0.9", SubnetID: subA, MAC: "0a:5c:d2:f1:00:09", SourceDeviceID: router.ID, At: at, Audit: audit("address_created", aNew)},
		{Kind: store.ARPCreate, AddressID: aForeign, Address: "10.30.0.10", SubnetID: subB, MAC: "0a:5c:d2:f1:00:0a", SourceDeviceID: router.ID, At: at, Audit: audit("address_created", aForeign)},
		{Kind: store.ARPCreate, AddressID: store.NewID(), Address: "10.30.0.1", SubnetID: subA, MAC: "0a:5c:d2:f1:00:0b", At: at, Audit: audit("address_created", "dup")},
		{Kind: store.ARPTouch, AddressID: aAgent, MAC: "52:54:00:00:00:04", At: at},
		{Kind: "bogus", AddressID: aEmpty},
	}
	summary := []store.AuditRow{{Action: "arp_run", ActorKind: "system", ActorID: "scan", SubjectKind: "scan", SubjectID: "j1", Outcome: "ok", Detail: map[string]any{"entries": 6}}}
	if err := db.ApplyARP(ctx, tenantA, ops, summary); err != nil {
		t.Fatal(err)
	}
	if a := get(tenantA, aEmpty); a.MACAddress != "0a:5c:d2:f1:00:01" || a.MACSource != store.MACSourceARP || a.MACSourceDeviceID != router.ID || a.MACSeenAt == nil {
		t.Fatalf("fill %+v", a)
	}
	if a := get(tenantA, aManual); a.MACAddress != "00:11:22:33:44:03" || a.MACConflict != "0a:5c:d2:f1:00:03" {
		t.Fatalf("conflict %+v", a)
	}
	if a := get(tenantA, aAgent); a.MACAddress != "52:54:00:00:00:04" || a.MACSource != store.MACSourceAgent || a.MACSeenAt == nil {
		t.Fatalf("SC-003: agent MAC overwritten %+v", a)
	}
	if a := get(tenantA, aNew); a.Origin != store.OriginARP || a.MACSource != store.MACSourceARP || a.Status != store.IPActive {
		t.Fatalf("create %+v", a)
	}
	if _, err := db.GetAddress(ctx, tenantA, aForeign); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("created in another tenant's subnet: %v", err)
	}
	for action, want := range map[string]int{"mac_learned": 1, "mac_conflict": 1, "address_created": 1, "arp_run": 1} {
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE action=$1", action).Scan(&n); err != nil || n != want {
			t.Errorf("%s audit rows %d (want %d) %v", action, n, want, err)
		}
	}

	// A failing op rolls the whole plan back (no MAC, no audit row).
	var before int
	_ = admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events").Scan(&before)
	err = db.ApplyARP(ctx, tenantA, []store.ARPOp{
		{Kind: store.ARPUpdate, AddressID: aEmpty, MAC: "0a:5c:d2:f1:00:77", SourceDeviceID: router.ID, At: at, Audit: audit("mac_changed", aEmpty)},
		{Kind: store.ARPUpdate, AddressID: aNew, MAC: "0a:5c:d2:f1:00:78", SourceDeviceID: store.NewID(), At: at, Audit: audit("mac_changed", aNew)}, // FK violation
	}, summary)
	if err == nil {
		t.Fatal("FK violation not reported")
	}
	var after int
	_ = admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events").Scan(&after)
	if a := get(tenantA, aEmpty); a.MACAddress != "0a:5c:d2:f1:00:01" || after != before {
		t.Fatalf("not rolled back: %+v audit %d->%d", a, before, after)
	}

	// Address links, the switch port's addresses and the correlation input.
	seen := at.Add(time.Hour)
	if err := db.SetAddressLinks(ctx, tenantA, []store.IPAddress{{ID: aEmpty, Link: &store.AddressLink{SwitchID: sw.ID, PortID: port.ID, PortName: "14",
		VLAN: 30, Source: store.LinkSNMPFDB, LastSeen: &seen}}}, audit("port_linked", aEmpty)); err != nil {
		t.Fatal(err)
	}
	if a := get(tenantA, aEmpty); a.Link == nil || a.Link.SwitchName != "msw-rack2" || a.Link.PortID != port.ID || a.Link.VLAN != 30 || !a.Link.LastSeen.Equal(seen) {
		t.Fatalf("link %+v", a.Link)
	}
	ifaces, err := db.ListInterfaces(ctx, tenantA, sw.ID)
	if err != nil || len(ifaces) != 1 || len(ifaces[0].BehindAddresses) != 1 || ifaces[0].BehindAddresses[0].Address != "10.30.0.1" {
		t.Fatalf("behind addresses %+v %v", ifaces, err)
	}
	pd, err := db.PortLinkData(ctx, tenantA)
	if err != nil || len(pd.Addresses) != 4 || !pd.NetworkMACs["4c:5e:0c:00:00:01"] {
		t.Fatalf("port link data %d addresses %v", len(pd.Addresses), err)
	}
	if err := db.SetAddressLinks(ctx, tenantB, []store.IPAddress{{ID: aEmpty}}, nil); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant link write: %v", err)
	}
	if err := db.SetAddressLinks(ctx, tenantA, []store.IPAddress{{ID: aEmpty}}, nil); err != nil {
		t.Fatal(err)
	}
	if a := get(tenantA, aEmpty); a.Link != nil {
		t.Fatalf("link not cleared %+v", a.Link)
	}

	// MAC search on the hex-only form.
	for q, want := range map[string]int{"0a5c": 2, "5cd2f10009": 1, "001122334403": 1, "ffff": 0} {
		got, err := db.ListAddresses(ctx, tenantA, store.AddressFilter{MAC: q})
		if err != nil || len(got) != want {
			t.Errorf("mac %s: %d rows (want %d) %v", q, len(got), want, err)
		}
	}
	if l, _ := db.ListAddresses(ctx, tenantB, store.AddressFilter{MAC: "0a5c"}); len(l) != 0 {
		t.Fatal("MAC search crosses tenants")
	}
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE indexname IN ('addresses_mac_hex','addresses_link_port')").Scan(&n); err != nil || n != 2 {
		t.Fatalf("indexes %d %v", n, err)
	}

	// Manual writes keep the provenance the service decided; a sweep keeps
	// the MAC.
	m := get(tenantA, aManual)
	m.MACAddress, m.MACSource, m.MACConflict = "00:11:22:33:44:33", store.MACSourceManual, ""
	if err := db.UpdateAddress(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.30.0.3", SubnetID: subA, Status: store.IPActive}); err != nil {
		t.Fatal(err)
	}
	if a := get(tenantA, aManual); a.MACAddress != "00:11:22:33:44:33" || a.MACSource != store.MACSourceManual || a.MACConflict != "" {
		t.Fatalf("manual after sweep %+v", a)
	}

	// Host sync writes agent provenance.
	hsNew := store.NewID()
	if err := db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		if err := tx.InsertAddressReported(store.IPAddress{ID: hsNew, Address: "10.30.0.20", SubnetID: subA, MACAddress: "52:54:00:00:00:20",
			Status: store.IPActive, AddressType: store.AddrHost, ReportState: store.RepReported}); err != nil {
			return err
		}
		return tx.UpdateAddressReported(store.IPAddress{ID: aNew, MACAddress: "52:54:00:00:00:09", ReportState: store.RepReported})
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hsNew, aNew} {
		if a := get(tenantA, id); a.MACSource != store.MACSourceAgent || a.MACSeenAt == nil || a.MACSourceDeviceID != "" {
			t.Fatalf("host sync provenance %+v", a)
		}
	}

	// Scan job ARP columns round-trip, including the ignored counters.
	job := store.IPScanJob{ID: store.NewID(), TenantID: tenantA, SubnetID: subA, Status: store.ScanPending, TriggeredBy: store.TriggerManual}
	if err := db.CreateScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.ARPStatus, job.ARPDevices, job.ARPPartial, job.ARPEntries, job.ARPApplied, job.ARPCreated, job.ARPConflicts =
		store.ARPRan, 3, 1, 120, 40, 5, 2
	job.ARPIgnored = map[string]int{store.ARPIgnoredProxy: 20, store.ARPIgnoredNetworkDevice: 4}
	if err := db.UpdateScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	j, err := db.GetScanJob(ctx, tenantA, job.ID)
	if err != nil || j.ARPStatus != store.ARPRan || j.ARPDevices != 3 || j.ARPPartial != 1 || j.ARPEntries != 120 || j.ARPApplied != 40 ||
		j.ARPCreated != 5 || j.ARPConflicts != 2 || j.ARPIgnored[store.ARPIgnoredProxy] != 20 || j.ARPIgnored[store.ARPIgnoredNetworkDevice] != 4 {
		t.Fatalf("scan job %+v %v", j, err)
	}
	job.ARPStatus = "bogus"
	if err := db.UpdateScanJob(ctx, job); err == nil {
		t.Fatal("arp_status CHECK")
	}
}
