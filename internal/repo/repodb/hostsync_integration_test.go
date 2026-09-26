//go:build integration

package repodb_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestHostSyncMigration upgrades a database at 0003 holding data to 0004 and
// checks defaults, constraints, the partial unique index and RLS on the new
// tables (T023).
func TestHostSyncMigration(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, adminDSN, 3); err != nil {
		t.Fatalf("migrate to 0003: %v", err)
	}
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	sub, dev, adr := store.NewID(), store.NewID(), store.NewID()
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO ipam_subnets (id, tenant_id, name, cidr) VALUES ('%s','%s','lan','10.0.0.0/24')`, sub, tenantA),
		fmt.Sprintf(`INSERT INTO ipam_devices (id, tenant_id, name) VALUES ('%s','%s','web')`, dev, tenantA),
		fmt.Sprintf(`INSERT INTO ipam_ip_addresses (id, tenant_id, address, subnet_id, device_id) VALUES ('%s','%s','10.0.0.5','%s','%s')`, adr, tenantA, sub, dev),
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatalf("migrate 0004+: %v", err)
	}
	var source, upd, drs, ars, origin string
	if err := admin.QueryRow(ctx, "SELECT source, update_status, report_state FROM ipam_devices WHERE id=$1", dev).Scan(&source, &upd, &drs); err != nil {
		t.Fatal(err)
	}
	_ = admin.QueryRow(ctx, "SELECT report_state FROM ipam_ip_addresses WHERE id=$1", adr).Scan(&ars)
	_ = admin.QueryRow(ctx, "SELECT origin FROM ipam_subnets WHERE id=$1", sub).Scan(&origin)
	if source != "manual" || upd != "unknown" || drs != "" || ars != "" || origin != "manual" {
		t.Fatalf("defaults: %s %s %q %q %s", source, upd, drs, ars, origin)
	}
	bad := []string{
		fmt.Sprintf("UPDATE ipam_devices SET source='agent' WHERE id='%s'", dev),
		fmt.Sprintf("UPDATE ipam_devices SET report_state='gone' WHERE id='%s'", dev),
		fmt.Sprintf("UPDATE ipam_devices SET update_status='fine' WHERE id='%s'", dev),
		fmt.Sprintf("UPDATE ipam_devices SET hypervisor_device_id=id WHERE id='%s'", dev),
		fmt.Sprintf("UPDATE ipam_ip_addresses SET report_state='x' WHERE id='%s'", adr),
		fmt.Sprintf("INSERT INTO ipam_hostsync_settings (tenant_id, full_interval_minutes) VALUES ('%s', 5)", tenantA),
		fmt.Sprintf("UPDATE ipam_subnets SET origin='agent' WHERE id='%s'", sub),
	}
	for _, q := range bad {
		if _, err := admin.Exec(ctx, q); err == nil {
			t.Errorf("CHECK must reject: %s", q)
		}
	}
	hid := store.NewID()
	if _, err := admin.Exec(ctx, "UPDATE ipam_devices SET inventory_host_id=$1 WHERE id=$2", hid, dev); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "INSERT INTO ipam_devices (id, tenant_id, name, inventory_host_id) VALUES ($1,$2,'web2',$3)", store.NewID(), tenantA, hid); err == nil {
		t.Fatal("partial UNIQUE (tenant_id, inventory_host_id)")
	}
	if _, err := admin.Exec(ctx, "INSERT INTO ipam_devices (id, tenant_id, name, inventory_host_id) VALUES ($1,$2,'web3',$3)", store.NewID(), tenantB, hid); err != nil {
		t.Fatalf("same host id in another tenant is independent: %v", err)
	}
	// RLS on the new tables.
	for _, q := range []string{
		fmt.Sprintf("INSERT INTO ipam_hostsync_settings (tenant_id) VALUES ('%s'),('%s')", tenantA, tenantB),
		fmt.Sprintf("INSERT INTO ipam_hostsync_device_state (device_id, tenant_id, inventory_host_id) VALUES ('%s','%s','%s')", dev, tenantA, hid),
		fmt.Sprintf("INSERT INTO ipam_hypervisor_guests (id, tenant_id, host_device_id, guest_ref, kind) VALUES ('%s','%s','%s','101','vm')", store.NewID(), tenantA, dev),
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, appDSN, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	count := func(scope store.Scope, table string) (n int) {
		if err := st.Tx(ctx, scope, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, table := range []string{"ipam_hostsync_settings", "ipam_hostsync_device_state", "ipam_hypervisor_guests"} {
		a, b, sys := count(store.Scope{TenantID: tenantA}, table), count(store.Scope{TenantID: tenantB}, table), count(store.Scope{System: true}, table)
		if table == "ipam_hostsync_settings" && (a != 1 || b != 1 || sys != 2) {
			t.Errorf("%s: a=%d b=%d sys=%d", table, a, b, sys)
		}
		if table != "ipam_hostsync_settings" && (a != 1 || b != 0 || sys != 1) {
			t.Errorf("%s: a=%d b=%d sys=%d", table, a, b, sys)
		}
	}
	// Guest rows cascade with their host device.
	if _, err := admin.Exec(ctx, "DELETE FROM ipam_devices WHERE id=$1", dev); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = admin.QueryRow(ctx, "SELECT count(*) FROM ipam_hypervisor_guests").Scan(&n)
	if n != 0 {
		t.Fatal("guest rows cascade with the host device")
	}
}

func openDB(t *testing.T) *repodb.DB {
	t.Helper()
	return openRepo(t).(*repodb.DB)
}

func TestHostSyncStoreIntegration(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()

	s, err := db.EnsureHostSyncSettings(ctx, tenantA)
	if err != nil || !s.Enabled || s.FullIntervalMinutes != 60 || len(s.ExcludedInterfaces) != 17 || s.Status != "ok" {
		t.Fatalf("defaults %+v %v", s, err)
	}
	s.Enabled, s.FullIntervalMinutes, s.ExcludedInterfaces, s.UpdatedBy = false, 30, []string{"x*"}, "u1"
	if err := db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, ActorKind: "user", ActorID: "u1", Action: "hostsync_settings_updated", SubjectKind: "hostsync", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetHostSyncSettings(ctx, tenantA)
	if got.Enabled || got.Status != "disabled" || got.FullIntervalMinutes != 30 || got.ExcludedInterfaces[0] != "x*" {
		t.Fatalf("updated %+v", got)
	}
	if err := db.ApplyHostReport(ctx, tenantA, func(repo.HostTx) error { return nil }); !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	s.Enabled = true
	_ = db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated"})
	if got, _ = db.GetHostSyncSettings(ctx, tenantA); !got.ReconcileRequested || got.Status != "ok" {
		t.Fatalf("re-enable %+v", got)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.SaveHostSyncState(ctx, tenantA, store.HostSyncStatus{Status: "degraded", LastError: "inventory_unavailable",
		ChangedSince: &now, LastPollAt: &now, ClearReconcile: true, HostsReported: 2}); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.GetHostSyncSettings(ctx, tenantA); got.ReconcileRequested || got.ChangedSince == nil || got.Status != "degraded" {
		t.Fatalf("state %+v", got)
	}
	_ = db.RequestReconcile(ctx, tenantB, store.AuditRow{TenantID: tenantB, Action: "hostsync_resync_requested"})
	all, err := db.ListHostSyncSettings(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("system list %d %v", len(all), err)
	}

	// A failing apply rolls back everything, audit included.
	boom := errors.New("boom")
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		if e := tx.InsertDevice(store.Device{ID: store.NewID(), Name: "rolled", Status: "active", DeviceType: "server", Source: "host_report", InventoryHostID: store.NewID()}); e != nil {
			return e
		}
		if e := tx.AppendAudit(store.AuditRow{Action: "device_created", ActorKind: "system", ActorID: "hostsync"}); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if l, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{Query: "rolled"}); len(l) != 0 {
		t.Fatal("rollback")
	}

	// Full HostTx round trip.
	hv, g1, hostID := store.NewID(), store.NewID(), store.NewID()
	seen := now
	subID, addrID, rowID := store.NewID(), store.NewID(), store.NewID()
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		must := func(e error) {
			if e != nil {
				t.Fatalf("hosttx: %v", e)
			}
		}
		must(tx.InsertDevice(store.Device{ID: hv, Name: "hv", Status: "active", DeviceType: "server", Source: "host_report",
			InventoryHostID: hostID, SerialNumber: " SN1 ", ReportState: "reported", LastReportAt: &seen, ReportDigest: "d", Description: "x"}))
		must(tx.InsertDevice(store.Device{ID: g1, Name: "guest", Status: "active", DeviceType: "vm", Source: "host_report", InventoryHostID: store.NewID()}))
		d, ok, e := tx.DeviceByInventoryHost(hostID)
		if e != nil || !ok || d.ID != hv || d.Source != "host_report" || d.ReportDigest != "d" {
			t.Fatalf("by host %+v %v", d, e)
		}
		if _, ok, _ := tx.DeviceByInventoryHost(store.NewID()); ok {
			t.Fatal("missing host")
		}
		if l, _ := tx.DevicesBySerial("sn1"); len(l) != 1 {
			t.Fatal("serial")
		}
		if l, _ := tx.DevicesByNames([]string{"HV"}); len(l) != 1 {
			t.Fatal("names")
		}
		must(tx.UpsertInterfaceReported(store.DeviceInterface{ID: store.NewID(), DeviceID: g1, Name: "eth0", MACAddress: "bc:24:11:00:00:01", Enabled: true, ReportState: "reported"}, true))
		must(tx.CreateSubnetAuto(store.Subnet{ID: subID, Name: "10.0.0.0/24", CIDR: "10.0.0.0/24", Status: "active", IPVersion: 4, PrefixLength: 24, Origin: "host_sync", CreatedBy: "hostsync"}))
		must(tx.InsertAddressReported(store.IPAddress{ID: addrID, Address: "10.0.0.5", SubnetID: subID, DeviceID: g1, Status: "active", AddressType: "host", ReportState: "reported", LastSeen: &seen}))
		must(tx.UpdateAddressReported(store.IPAddress{ID: addrID, DeviceID: hv, PreviousDeviceID: g1, MovedAt: &seen, MoveCount: 1, MoveWindowStart: &seen, ReportState: "reported", Conflict: true}))
		pk := make([]store.DevicePackage, 5000)
		for i := range pk {
			pk[i] = store.DevicePackage{Name: fmt.Sprintf("pkg-%05d", i), CurrentVersion: "1", AvailableVersion: "2", NeedsUpdate: true, PackageManager: "apt"}
		}
		start := time.Now()
		must(tx.ReplacePendingPackages(hv, pk))
		if el := time.Since(start); el > time.Second {
			t.Errorf("5000-package replace took %s (budget 1 s)", el)
		}
		must(tx.ReplaceGuests(hv, []store.HypervisorGuest{{ID: rowID, GuestRef: "101", Name: "vm", Kind: "vm", Platform: "proxmox", MACs: []string{"bc:24:11:00:00:01"}, LastReportedAt: now}}))
		if l, _ := tx.GuestRowsByMAC([]string{"bc:24:11:00:00:01"}); len(l) != 1 || l[0].ID != rowID {
			t.Fatalf("GIN mac lookup %+v", l)
		}
		if l, _ := tx.DevicesByMAC([]string{"bc:24:11:00:00:01"}); len(l) != 1 || l[0].DeviceID != g1 {
			t.Fatalf("mac owners %+v", l)
		}
		must(tx.SetHypervisor(g1, hv))
		must(tx.SetGuestDevice(rowID, g1))
		if l, _ := tx.GuestDevicesOf(hv); len(l) != 1 {
			t.Fatal("guest devices")
		}
		if l, _ := tx.Guests(hv); len(l) != 1 || l[0].GuestDeviceID != g1 {
			t.Fatal("guests")
		}
		if l, _ := tx.Packages(hv); len(l) != 5000 {
			t.Fatal("packages")
		}
		if l, _ := tx.Interfaces(g1); len(l) != 1 {
			t.Fatal("interfaces")
		}
		if l, _ := tx.Subnets(); len(l) != 1 || l[0].Origin != "host_sync" {
			t.Fatal("subnets")
		}
		if l, _ := tx.AddressesByValue([]string{"10.0.0.5"}); len(l) != 1 || !l[0].Conflict || l[0].PreviousDeviceID != g1 {
			t.Fatalf("addresses %+v", l)
		}
		if l, _ := tx.AddressesOfDevice(hv); len(l) != 1 {
			t.Fatal("addresses of device")
		}
		d.Name, d.OSVersion, d.Description = "hv2", "Ubuntu", "ignored"
		must(tx.UpdateDeviceReported(d))
		must(tx.SaveDeviceState(store.HostSyncDeviceState{DeviceID: hv, InventoryHostID: hostID, Trigger: "poll", Changes: 3,
			Issues: []store.HostSyncIssue{{Field: "hostname", Reason: "too_long", Count: 1}}, AppliedAt: &seen}))
		must(tx.MarkDeviceNotReported(g1))
		return tx.AppendAudit(store.AuditRow{Action: "device_created", ActorKind: "system", ActorID: "hostsync", SubjectKind: "device", SubjectID: hv, Outcome: "ok"})
	})
	if err != nil {
		t.Fatal(err)
	}
	dv, _ := db.GetDevice(ctx, tenantA, hv)
	if dv.Name != "hv2" || dv.Description != "x" || dv.GuestCount != 1 || dv.PackageUpdateCount != 5000 {
		t.Fatalf("device after apply %+v", dv)
	}
	gd, _ := db.GetDevice(ctx, tenantA, g1)
	if gd.ReportState != "not_reported" || gd.HypervisorDeviceID != hv {
		t.Fatalf("guest %+v", gd)
	}
	if ds, err := db.GetHostSyncDeviceState(ctx, tenantA, hv); err != nil || ds.Changes != 3 || len(ds.Issues) != 1 {
		t.Fatalf("device state %+v %v", ds, err)
	}
	if _, err := db.GetHostSyncDeviceState(ctx, tenantB, hv); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant device state")
	}
	if gl, _ := db.ListGuests(ctx, tenantA, hv); len(gl) != 1 || gl[0].GuestDeviceName != "guest" {
		t.Fatalf("guests %+v", gl)
	}
	if l, _ := db.HostDevices(ctx, tenantA); len(l) != 2 {
		t.Fatal("host devices")
	}
	if nr, cf, _ := db.HostSyncCounts(ctx, tenantA); nr != 1 || cf != 1 {
		t.Fatalf("counts %d %d", nr, cf)
	}
	tr := true
	if l, _ := db.ListAddresses(ctx, tenantA, store.AddressFilter{Conflict: &tr, ReportState: "reported"}); len(l) != 1 {
		t.Fatal("address filters")
	}
	if l, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{Source: "host_report", ReportState: "not_reported"}); len(l) != 1 {
		t.Fatal("device filters")
	}
	if a, err := db.ClearAddressConflict(ctx, tenantA, addrID, store.AuditRow{TenantID: tenantA, Action: "address_conflict_cleared"}); err != nil || a.Conflict {
		t.Fatal("clear conflict")
	}
	if _, err := db.ClearAddressConflict(ctx, tenantB, addrID, store.AuditRow{TenantID: tenantB}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant clear")
	}
	// Admin update through the API path keeps server-owned columns.
	dv.Description = "admin"
	if err := db.UpdateDevice(ctx, dv); err != nil {
		t.Fatal(err)
	}
	if dv2, _ := db.GetDevice(ctx, tenantA, hv); dv2.Source != "host_report" || dv2.InventoryHostID != hostID {
		t.Fatal("server-owned fields survive the API update")
	}
	// D9: a scan never overwrites a host-reported device's fields.
	scan, err := db.UpsertDeviceByName(ctx, store.Device{TenantID: tenantA, Name: "hv2", DeviceType: "switch", ManagementIP: "10.0.0.1", OSVersion: "IOS", Model: "M", LastSeen: &seen})
	if err != nil || scan.DeviceType != "server" || scan.OSVersion != "Ubuntu" || scan.Model != "M" {
		t.Fatalf("scan guard %+v %v", scan, err)
	}
	if nd, _ := db.UpsertDeviceByName(ctx, store.Device{TenantID: tenantA, Name: "sw-new"}); nd.Source != "scan" {
		t.Fatalf("scan source %q", nd.Source)
	}
	// ... nor unlinks a host-reported address.
	if _, err := db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.0.0.5", SubnetID: subID, Hostname: "scan"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := db.FindAddress(ctx, tenantA, "10.0.0.5"); a.DeviceID != hv {
		t.Fatalf("scan unlinked a reported address: %+v", a)
	}
	// Cross-tenant: an apply for tenant B cannot see or change tenant A rows (RLS).
	_ = db.ApplyHostReport(ctx, tenantB, func(tx repo.HostTx) error {
		if _, ok, _ := tx.DeviceByInventoryHost(hostID); ok {
			t.Error("tenant B sees tenant A device")
		}
		if l, _ := tx.AddressesByValue([]string{"10.0.0.5"}); len(l) != 0 {
			t.Error("tenant B sees tenant A address")
		}
		if err := tx.UpdateDeviceReported(store.Device{ID: hv, Name: "stolen", Source: "host_report"}); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("tenant B wrote tenant A device: %v", err)
		}
		return nil
	})
}

// TestHostSyncDisableRace: an apply holding FOR SHARE blocks the disabling
// update; after it commits every apply is refused (SR-006, T069).
func TestHostSyncDisableRace(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	s, _ := db.EnsureHostSyncSettings(ctx, tenantA)
	inApply, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var applyErr error
	go func() {
		defer wg.Done()
		applyErr = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
			close(inApply)
			<-release
			return tx.InsertDevice(store.Device{ID: store.NewID(), Name: "before-disable", Status: "active", DeviceType: "server", Source: "host_report"})
		})
	}()
	<-inApply
	disabled := make(chan time.Time, 1)
	go func() {
		s.Enabled = false
		if err := db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated"}); err != nil {
			t.Error(err)
		}
		disabled <- time.Now()
	}()
	select {
	case <-disabled:
		t.Fatal("disable must wait for the running apply")
	case <-time.After(300 * time.Millisecond):
	}
	committed := time.Now()
	close(release)
	wg.Wait()
	if applyErr != nil {
		t.Fatal(applyErr)
	}
	if at := <-disabled; at.Before(committed) {
		t.Fatal("disable committed before the apply")
	}
	err := db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		return tx.InsertDevice(store.Device{ID: store.NewID(), Name: "after-disable", Status: "active", DeviceType: "server"})
	})
	if !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatalf("apply after disable: %v", err)
	}
	if l, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{Query: "after-disable"}); len(l) != 0 {
		t.Fatal("no row after the disable")
	}
	s.Enabled = true
	_ = db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated"})
	if got, _ := db.GetHostSyncSettings(ctx, tenantA); !got.ReconcileRequested {
		t.Fatal("re-enable requests a reconcile")
	}
}

// TestHostSyncApplySerialized: two applies of one tenant never interleave
// (advisory transaction lock; several IPAM replicas).
func TestHostSyncApplySerialized(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	var mu sync.Mutex
	var trace []string
	inFirst, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = db.ApplyHostReport(ctx, tenantA, func(repo.HostTx) error {
			close(inFirst)
			<-release
			mu.Lock()
			trace = append(trace, "first")
			mu.Unlock()
			return nil
		})
	}()
	<-inFirst
	go func() {
		defer wg.Done()
		_ = db.ApplyHostReport(ctx, tenantA, func(repo.HostTx) error {
			mu.Lock()
			trace = append(trace, "second")
			mu.Unlock()
			return nil
		})
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	if strings.Join(trace, ",") != "first,second" {
		t.Fatalf("trace %v", trace)
	}
}
