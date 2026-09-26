package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestHostSyncSettings(t *testing.T) {
	ctx := bg()
	m := New()
	if _, err := m.GetHostSyncSettings(ctx, "t1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("absent settings")
	}
	s, err := m.EnsureHostSyncSettings(ctx, "t1")
	if err != nil || !s.Enabled || s.FullIntervalMinutes != 60 || len(s.ExcludedInterfaces) != 17 || s.Status != store.HostSyncOK {
		t.Fatalf("defaults %+v %v", s, err)
	}
	s.Enabled, s.ExcludedInterfaces, s.UpdatedBy = false, []string{"x*"}, "u1"
	if err := m.UpdateHostSyncSettings(ctx, s, store.AuditRow{Action: "hostsync_settings_updated", TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetHostSyncSettings(ctx, "t1")
	if got.Enabled || got.Status != store.HostSyncDisabled || got.ExcludedInterfaces[0] != "x*" || got.UpdatedBy != "u1" {
		t.Fatalf("updated %+v", got)
	}
	if a := m.Audit(); len(a) != 1 || a[0].Action != "hostsync_settings_updated" {
		t.Fatal("audit row written with the settings")
	}
	// Disabled -> every apply aborts and writes nothing.
	err = m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error { return tx.InsertDevice(store.Device{ID: "d", Name: "d"}) })
	if !errors.Is(err, repo.ErrSyncDisabled) {
		t.Fatalf("disabled apply: %v", err)
	}
	if _, err := m.GetDevice(ctx, "t1", "d"); err == nil {
		t.Fatal("disabled apply wrote")
	}
	// Re-enable -> reconcile requested, status ok.
	s.Enabled = true
	_ = m.UpdateHostSyncSettings(ctx, s, store.AuditRow{})
	got, _ = m.GetHostSyncSettings(ctx, "t1")
	if !got.ReconcileRequested || got.Status != store.HostSyncOK {
		t.Fatalf("re-enable %+v", got)
	}
	now := time.Unix(100, 0)
	if err := m.SaveHostSyncState(ctx, "t1", store.HostSyncStatus{Status: store.HostSyncDegraded, LastError: "inventory_unavailable",
		ChangedSince: &now, LastPollAt: &now, LastReconcileAt: &now, ClearReconcile: true, HostsReported: 3, HostsFailed: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetHostSyncSettings(ctx, "t1")
	if got.Status != store.HostSyncDegraded || got.ReconcileRequested || !got.ChangedSince.Equal(now) || got.HostsFailed != 1 || got.LastError == "" {
		t.Fatalf("state %+v", got)
	}
	_ = m.SaveHostSyncState(ctx, "t1", store.HostSyncStatus{Status: store.HostSyncOK})
	if got, _ = m.GetHostSyncSettings(ctx, "t1"); got.ChangedSince == nil {
		t.Fatal("nil watermark keeps the current one")
	}
	_ = m.RequestReconcile(ctx, "t2", store.AuditRow{Action: "hostsync_resync_requested"})
	all, _ := m.ListHostSyncSettings(ctx)
	if len(all) != 2 || all[0].TenantID != "t1" || !all[1].ReconcileRequested {
		t.Fatalf("list %+v", all)
	}
	for _, method := range []string{"EnsureHostSyncSettings", "GetHostSyncSettings", "UpdateHostSyncSettings", "RequestReconcile",
		"ListHostSyncSettings", "SaveHostSyncState", "HostDevices", "HostSyncCounts", "ListGuests", "ClearAddressConflict", "ApplyHostReport"} {
		m.FailNext(method)
		var err error
		switch method {
		case "EnsureHostSyncSettings":
			_, err = m.EnsureHostSyncSettings(ctx, "t1")
		case "GetHostSyncSettings":
			_, err = m.GetHostSyncSettings(ctx, "t1")
		case "UpdateHostSyncSettings":
			err = m.UpdateHostSyncSettings(ctx, s, store.AuditRow{})
		case "RequestReconcile":
			err = m.RequestReconcile(ctx, "t1", store.AuditRow{})
		case "ListHostSyncSettings":
			_, err = m.ListHostSyncSettings(ctx)
		case "SaveHostSyncState":
			err = m.SaveHostSyncState(ctx, "t1", store.HostSyncStatus{})
		case "HostDevices":
			_, err = m.HostDevices(ctx, "t1")
		case "HostSyncCounts":
			_, _, err = m.HostSyncCounts(ctx, "t1")
		case "ListGuests":
			_, err = m.ListGuests(ctx, "t1", "d")
		case "ClearAddressConflict":
			_, err = m.ClearAddressConflict(ctx, "t1", "a", store.AuditRow{})
		case "ApplyHostReport":
			err = m.ApplyHostReport(ctx, "t1", func(repo.HostTx) error { return nil })
		}
		if err == nil {
			t.Errorf("%s: injected failure not returned", method)
		}
	}
}

func TestApplyHostReportRollsBack(t *testing.T) {
	ctx := bg()
	m := New()
	boom := errors.New("boom")
	err := m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error {
		_ = tx.InsertDevice(store.Device{ID: "d1", Name: "web", InventoryHostID: "h1"})
		_ = tx.CreateSubnetAuto(store.Subnet{ID: "s1", Name: "10.0.0.0/24", CIDR: "10.0.0.0/24"})
		_ = tx.InsertAddressReported(store.IPAddress{ID: "a1", Address: "10.0.0.5", SubnetID: "s1", DeviceID: "d1"})
		_ = tx.AppendAudit(store.AuditRow{Action: "device_created"})
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := m.GetDevice(ctx, "t1", "d1"); err == nil || len(m.Audit()) != 0 {
		t.Fatal("failed apply must roll back everything incl. audit")
	}
}

func TestHostTxOperations(t *testing.T) {
	ctx := bg()
	m := New()
	seen := time.Unix(50, 0)
	err := m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error {
		must := func(e error) {
			if e != nil {
				t.Fatal(e)
			}
		}
		must(tx.InsertDevice(store.Device{ID: "hv", Name: "hv", InventoryHostID: "h1", SerialNumber: " SN1 "}))
		must(tx.InsertDevice(store.Device{ID: "g1", Name: "guest", InventoryHostID: "h2"}))
		if err := tx.InsertDevice(store.Device{ID: "x", Name: "hv"}); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("name unique")
		}
		if err := tx.InsertDevice(store.Device{ID: "y", Name: "other", InventoryHostID: "h1"}); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("inventory host unique")
		}
		d, ok, _ := tx.DeviceByInventoryHost("H1")
		if !ok || d.ID != "hv" {
			t.Fatal("by host")
		}
		if _, ok, _ := tx.DeviceByInventoryHost("nope"); ok {
			t.Fatal("missing host")
		}
		if l, _ := tx.DevicesBySerial("sn1"); len(l) != 1 {
			t.Fatal("serial")
		}
		if l, _ := tx.DevicesBySerial(" "); len(l) != 0 {
			t.Fatal("blank serial")
		}
		if l, _ := tx.DevicesByNames([]string{"HV", "zzz"}); len(l) != 1 {
			t.Fatal("names")
		}
		must(tx.UpsertInterfaceReported(store.DeviceInterface{ID: "i1", DeviceID: "g1", Name: "eth0", MACAddress: "bc:24:11:00:00:01", ReportState: store.RepReported}, true))
		if err := tx.UpsertInterfaceReported(store.DeviceInterface{ID: "i9", DeviceID: "g1", Name: "eth0"}, true); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("iface unique")
		}
		must(tx.UpsertInterfaceReported(store.DeviceInterface{ID: "i1", SpeedMbps: 10, ReportState: store.RepReported, MACAddress: "bc:24:11:00:00:01"}, false))
		if err := tx.UpsertInterfaceReported(store.DeviceInterface{ID: "nope"}, false); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("iface missing")
		}
		ifs, _ := tx.Interfaces("g1")
		if len(ifs) != 1 || ifs[0].SpeedMbps != 10 || ifs[0].Name != "eth0" {
			t.Fatalf("iface %+v", ifs)
		}
		must(tx.CreateSubnetAuto(store.Subnet{ID: "s1", Name: "10.0.0.0/24", CIDR: "10.0.0.0/24", Origin: store.OriginHostSync}))
		if err := tx.CreateSubnetAuto(store.Subnet{ID: "s2", Name: "10.0.0.0/24"}); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("subnet name unique")
		}
		if l, _ := tx.Subnets(); len(l) != 1 || l[0].Origin != store.OriginHostSync {
			t.Fatal("subnets")
		}
		must(tx.InsertAddressReported(store.IPAddress{ID: "a1", Address: "10.0.0.5", SubnetID: "s1", DeviceID: "g1", ReportState: store.RepReported, Note: "n"}))
		if err := tx.InsertAddressReported(store.IPAddress{ID: "a2", Address: "10.0.0.5"}); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("address unique")
		}
		must(tx.UpdateAddressReported(store.IPAddress{ID: "a1", DeviceID: "hv", PreviousDeviceID: "g1", LastSeen: &seen, ReportState: store.RepReported, Conflict: true}))
		if err := tx.UpdateAddressReported(store.IPAddress{ID: "zz"}); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("addr missing")
		}
		if l, _ := tx.AddressesByValue([]string{"10.0.0.5"}); len(l) != 1 || l[0].Note != "n" || l[0].DeviceID != "hv" || !l[0].Conflict {
			t.Fatalf("addr %+v", l)
		}
		if l, _ := tx.AddressesOfDevice("hv"); len(l) != 1 {
			t.Fatal("addrs of device")
		}
		must(tx.ReplacePendingPackages("hv", []store.DevicePackage{{Name: "openssl", NeedsUpdate: true}}))
		if l, _ := tx.Packages("hv"); len(l) != 1 || l[0].ID == "" {
			t.Fatal("packages")
		}
		must(tx.ReplaceGuests("hv", []store.HypervisorGuest{{ID: "r1", GuestRef: "101", Kind: "vm", MACs: []string{"bc:24:11:00:00:01"}}}))
		if l, _ := tx.Guests("hv"); len(l) != 1 {
			t.Fatal("guests")
		}
		if l, _ := tx.GuestRowsByMAC([]string{"bc:24:11:00:00:01"}); len(l) != 1 {
			t.Fatal("guest rows by mac")
		}
		if l, _ := tx.DevicesByMAC([]string{"bc:24:11:00:00:01"}); len(l) != 1 || l[0].DeviceID != "g1" {
			t.Fatalf("mac owners %+v", l)
		}
		must(tx.SetHypervisor("g1", "hv"))
		if err := tx.SetHypervisor("g1", "g1"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("self hypervisor refused")
		}
		must(tx.SetGuestDevice("r1", "g1"))
		if err := tx.SetGuestDevice("zz", "g1"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("guest row missing")
		}
		if l, _ := tx.GuestDevicesOf("hv"); len(l) != 1 {
			t.Fatal("guest devices")
		}
		must(tx.UpdateDeviceReported(store.Device{ID: "hv", Name: "hv2", Source: store.SrcHostReport, ReportState: store.RepReported, InventoryHostID: "h1"}))
		if err := tx.UpdateDeviceReported(store.Device{ID: "hv", Name: "guest"}); !errors.Is(err, repo.ErrConflict) {
			t.Fatal("rename collision")
		}
		if err := tx.UpdateDeviceReported(store.Device{ID: "zz"}); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("update missing")
		}
		must(tx.SaveDeviceState(store.HostSyncDeviceState{DeviceID: "hv", InventoryHostID: "h1", Issues: []store.HostSyncIssue{{Field: "f"}}}))
		must(tx.AppendAudit(store.AuditRow{Action: "x"}))
		must(tx.MarkDeviceNotReported("g1"))
		if err := tx.MarkDeviceNotReported("zz"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatal("mark missing")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g, _ := m.GetDevice(ctx, "t1", "g1")
	hv, _ := m.GetDevice(ctx, "t1", "hv")
	if g.ReportState != store.RepNotReported || g.HypervisorDeviceID != "hv" || hv.Name != "hv2" || hv.GuestCount != 1 {
		t.Fatalf("devices %+v %+v", g, hv)
	}
	ifs, _ := m.ListInterfaces(ctx, "t1", "g1")
	if ifs[0].ReportState != store.RepNotReported {
		t.Fatal("interfaces marked")
	}
	if st, err := m.GetHostSyncDeviceState(ctx, "t1", "hv"); err != nil || st.InventoryHostID != "h1" || len(st.Issues) != 1 {
		t.Fatal("device state")
	}
	if _, err := m.GetHostSyncDeviceState(ctx, "t2", "hv"); err == nil {
		t.Fatal("cross-tenant device state")
	}
	gl, _ := m.ListGuests(ctx, "t1", "hv")
	if len(gl) != 1 || gl[0].GuestDeviceName != "guest" {
		t.Fatalf("guests %+v", gl)
	}
	if l, _ := m.HostDevices(ctx, "t1"); len(l) != 2 {
		t.Fatal("host devices")
	}
	nr, cf, _ := m.HostSyncCounts(ctx, "t1")
	if nr != 1 || cf != 1 {
		t.Fatalf("counts %d %d", nr, cf)
	}
	a, err := m.ClearAddressConflict(ctx, "t1", "a1", store.AuditRow{Action: "address_conflict_cleared"})
	if err != nil || a.Conflict {
		t.Fatal("clear conflict")
	}
	if _, err := m.ClearAddressConflict(ctx, "t2", "a1", store.AuditRow{}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("cross-tenant clear")
	}
	// Admin API update keeps server-owned fields; filters see them.
	hv.Description = "admin"
	hv.Source = ""
	_ = m.UpdateDevice(ctx, hv)
	hv2, _ := m.GetDevice(ctx, "t1", "hv")
	if hv2.Source != store.SrcHostReport || hv2.Description != "admin" {
		t.Fatalf("api update %+v", hv2)
	}
	if l, _ := m.ListDevices(ctx, "t1", store.DeviceFilter{Source: store.SrcHostReport, ReportState: store.RepReported}); len(l) != 1 {
		t.Fatal("device filters")
	}
	if l, _ := m.ListDevices(ctx, "t1", store.DeviceFilter{Source: store.SrcScan}); len(l) != 0 {
		t.Fatal("source filter")
	}
	tr := true
	if l, _ := m.ListAddresses(ctx, "t1", store.AddressFilter{ReportState: store.RepReported, Conflict: &tr}); len(l) != 0 {
		t.Fatal("conflict filter")
	}
	if l, _ := m.ListAddresses(ctx, "t1", store.AddressFilter{ReportState: store.RepNotReported}); len(l) != 0 {
		t.Fatal("report state filter")
	}
	// Deleting the hypervisor cascades its guests and unlinks the guest device.
	if err := m.DeleteDevice(ctx, "t1", "hv", true); err != nil {
		t.Fatal(err)
	}
	g, _ = m.GetDevice(ctx, "t1", "g1")
	if g.HypervisorDeviceID != "" || len(m.guests) != 0 {
		t.Fatal("cascade")
	}
	// Deleting a guest device nulls its reference in guest rows.
	m.guests["r"] = store.HypervisorGuest{ID: "r", TenantID: "t1", HostDeviceID: "zz", GuestDeviceID: "g1"}
	_ = m.DeleteDevice(ctx, "t1", "g1", true)
	if m.guests["r"].GuestDeviceID != "" {
		t.Fatal("guest device set null")
	}
}

func TestHostTxInjectedFailures(t *testing.T) {
	ctx := bg()
	m := New()
	for _, method := range []string{"DeviceByInventoryHost", "InsertDevice", "UpdateDeviceReported", "UpsertInterfaceReported",
		"CreateSubnetAuto", "InsertAddressReported", "UpdateAddressReported", "ReplacePendingPackages", "ReplaceGuests",
		"SetHypervisor", "SetGuestDevice", "MarkDeviceNotReported", "SaveDeviceState", "AppendAudit"} {
		m.FailNext("tx." + method)
		err := m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error {
			switch method {
			case "DeviceByInventoryHost":
				_, _, e := tx.DeviceByInventoryHost("h")
				return e
			case "InsertDevice":
				return tx.InsertDevice(store.Device{})
			case "UpdateDeviceReported":
				return tx.UpdateDeviceReported(store.Device{})
			case "UpsertInterfaceReported":
				return tx.UpsertInterfaceReported(store.DeviceInterface{}, true)
			case "CreateSubnetAuto":
				return tx.CreateSubnetAuto(store.Subnet{})
			case "InsertAddressReported":
				return tx.InsertAddressReported(store.IPAddress{})
			case "UpdateAddressReported":
				return tx.UpdateAddressReported(store.IPAddress{})
			case "ReplacePendingPackages":
				return tx.ReplacePendingPackages("d", nil)
			case "ReplaceGuests":
				return tx.ReplaceGuests("d", nil)
			case "SetHypervisor":
				return tx.SetHypervisor("d", "e")
			case "SetGuestDevice":
				return tx.SetGuestDevice("g", "d")
			case "MarkDeviceNotReported":
				return tx.MarkDeviceNotReported("d")
			case "SaveDeviceState":
				return tx.SaveDeviceState(store.HostSyncDeviceState{})
			default:
				return tx.AppendAudit(store.AuditRow{})
			}
		})
		if err == nil || !contains(err.Error(), "injected") {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestScanGuardHostReportedDevice(t *testing.T) {
	ctx := bg()
	m := New()
	_ = m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error {
		return tx.InsertDevice(store.Device{ID: "d1", Name: "web", DeviceType: store.DevServer, ManagementIP: "10.9.0.5",
			OSVersion: "Ubuntu", Source: store.SrcHostReport, InventoryHostID: "h1"})
	})
	seen := time.Unix(99, 0)
	out, err := m.UpsertDeviceByName(ctx, store.Device{TenantID: "t1", Name: "web", DeviceType: store.DevSwitch,
		ManagementIP: "10.0.0.5", OSVersion: "IOS", Model: "X1", LastSeen: &seen})
	if err != nil {
		t.Fatal(err)
	}
	if out.ManagementIP != "10.9.0.5" || out.DeviceType != store.DevServer || out.OSVersion != "Ubuntu" || out.Model != "X1" ||
		!out.LastSeen.Equal(seen) || out.Source != store.SrcHostReport {
		t.Fatalf("D9 guard: %+v", out)
	}
	nd, _ := m.UpsertDeviceByName(ctx, store.Device{TenantID: "t1", Name: "sw1"})
	if nd.Source != store.SrcScan {
		t.Fatal("scan-created device source")
	}
	// A scan never unlinks a host-reported address.
	_ = m.ApplyHostReport(ctx, "t1", func(tx repo.HostTx) error {
		return tx.InsertAddressReported(store.IPAddress{ID: "a1", Address: "10.0.0.7", DeviceID: "d1", InterfaceName: "eth0", ReportState: store.RepReported})
	})
	if _, err := m.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.7", Hostname: "dns"}); err != nil {
		t.Fatal(err)
	}
	a, _ := m.FindAddress(ctx, "t1", "10.0.0.7")
	if a.DeviceID != "d1" || a.InterfaceName != "eth0" || a.ReportState != store.RepReported {
		t.Fatalf("address guard %+v", a)
	}
}
