package hostplan

import (
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestCreateDevice(t *testing.T) {
	r := report()
	r.Updates = hostreport.Updates{Status: store.UpdAvailable, RebootRequired: hostreport.TriTrue, AutomaticUpdates: hostreport.TriFalse}
	p := Build(State{}, r, params())
	if p.Match != MatchCreated || len(ops(p, OpCreateDevice)) != 1 {
		t.Fatalf("create: %+v", p.Ops)
	}
	d := *ops(p, OpCreateDevice)[0].Device
	if d.Name != "web-01" || d.DeviceType != store.DevServer || d.OSType != "linux" || d.OSVersion != "Ubuntu 24.04" ||
		d.Manufacturer != "Dell" || d.Model != "R650" || d.SerialNumber != "SN-1" || d.PrimaryIP != "10.0.0.5" ||
		d.Source != store.SrcHostReport || d.ReportState != store.RepReported || d.InventoryHostID != hostID ||
		d.Status != store.DevStActive || !d.RebootRequired || d.UnattendedUpgrades || d.UpdateStatus != store.UpdAvailable ||
		d.ReportDigest != digest1 || d.LastSeen == nil || d.LastReportAt == nil || d.CreatedBy != "hostsync" {
		t.Fatalf("device %+v", d)
	}
	if p.DeviceID != d.ID || p.State.DeviceID != d.ID || p.State.Changes != len(p.Ops) || p.State.Trigger != "poll" {
		t.Fatalf("state %+v", p.State)
	}
	// Interfaces: eth0 only (lo is loopback, docker0 excluded by default).
	ci := ops(p, OpCreateInterface)
	if len(ci) != 1 || ci[0].Interface.Name != "eth0" || ci[0].Interface.MACAddress != "aa:bb:cc:00:00:01" ||
		ci[0].Interface.InterfaceType != hostreport.KindEthernet || ci[0].Interface.SpeedMbps != 1000 || !ci[0].Interface.Enabled {
		t.Fatalf("interfaces %+v", ci)
	}
	for _, e := range p.Events {
		if e.Payload["address"] == "172.17.0.1" || e.Payload["address"] == "127.0.0.1" {
			t.Fatal("excluded interface produced an address")
		}
	}
}

func TestDeviceTypeRules(t *testing.T) {
	cases := []struct {
		role, current string
		create        bool
		want          string
	}{
		{hostreport.RoleVM, store.DevServer, false, store.DevVM},
		{hostreport.RoleContainer, "", true, store.DevContainer},
		{hostreport.RolePhysical, "", true, store.DevServer},
		{hostreport.RolePhysical, store.DevVM, false, store.DevServer},
		{hostreport.RolePhysical, store.DevContainer, false, store.DevServer},
		{hostreport.RolePhysical, store.DevWorkstation, false, store.DevWorkstation},
		{hostreport.RoleUnknown, "", true, store.DevServer},
		{hostreport.RoleUnknown, store.DevStorage, false, store.DevStorage},
	}
	for _, c := range cases {
		if got := deviceType(c.role, c.current, c.create); got != c.want {
			t.Errorf("deviceType(%s,%s,%v)=%s want %s", c.role, c.current, c.create, got, c.want)
		}
	}
}

func TestVirtualizationKind(t *testing.T) {
	r := report()
	r.VirtRole, r.VirtKind = hostreport.RoleVM, "kvm"
	d := Build(State{}, r, params()).Ops[0].Device
	if d.DeviceType != store.DevVM || d.VirtualizationKind != "kvm" {
		t.Fatalf("%+v", d)
	}
	cur := store.Device{ID: "d", Name: "web-01", InventoryHostID: hostID, DeviceType: store.DevVM, VirtualizationKind: "kvm"}
	r.VirtRole, r.VirtKind = hostreport.RolePhysical, ""
	up := ops(Build(State{Candidates: Candidates{ByHost: &cur}}, r, params()), OpUpdateDevice)[0].Device
	if up.DeviceType != store.DevServer || up.VirtualizationKind != "" {
		t.Fatalf("physical over vm: %+v", up)
	}
	r.VirtRole = hostreport.RoleUnknown
	up2 := ops(Build(State{Candidates: Candidates{ByHost: &cur}}, r, params()), OpUpdateDevice)[0].Device
	if up2.DeviceType != store.DevVM || up2.VirtualizationKind != "kvm" {
		t.Fatalf("unknown keeps: %+v", up2)
	}
}

// adminDevice carries a value in every field the sync must never touch.
func adminDevice() store.Device {
	seen := t0.Add(-time.Hour)
	return store.Device{
		ID: "d1", TenantID: tenant, Name: "web-01", DeviceType: store.DevWorkstation, Description: "admin desc",
		AssetTag: "AT-9", LocationID: "loc-1", RackID: "rack-1", RackPosition: 12, DeviceHeightU: 2, Status: store.DevStStaged,
		Contact: "sealed-contact", IPMISecretRef: "warden-ref", FirmwareVersion: "fw-1", Tags: map[string]string{"k": "v"},
		CreatedBy: "alice", ManagementIP: "10.9.9.9", LastSeen: &seen,
	}
}

func assertAdminUntouched(t *testing.T, before store.Device, p Plan) {
	t.Helper()
	for _, o := range p.Ops {
		if o.Device == nil {
			continue
		}
		d := *o.Device
		if o.Kind == OpCreateDevice {
			if d.Description != "" || d.AssetTag != "" || d.LocationID != "" || d.RackID != "" || d.Contact != "" ||
				d.IPMISecretRef != "" || d.FirmwareVersion != "" || len(d.Tags) != 0 {
				t.Fatalf("create wrote an admin field: %+v", d)
			}
			continue
		}
		if d.Description != before.Description || d.AssetTag != before.AssetTag || d.LocationID != before.LocationID ||
			d.RackID != before.RackID || d.RackPosition != before.RackPosition || d.DeviceHeightU != before.DeviceHeightU ||
			d.Status != before.Status || d.Contact != before.Contact || d.IPMISecretRef != before.IPMISecretRef ||
			d.FirmwareVersion != before.FirmwareVersion || d.CreatedBy != before.CreatedBy || len(d.Tags) != len(before.Tags) {
			t.Fatalf("SC-004: admin field changed: %+v", d)
		}
	}
}

func TestAdminFieldsNeverWritten(t *testing.T) {
	before := adminDevice()
	for _, how := range []string{"host", "serial", "name"} {
		d := adminDevice()
		c := Candidates{}
		switch how {
		case "host":
			d.InventoryHostID = hostID
			c.ByHost = &d
		case "serial":
			d.SerialNumber = "SN-1"
			c.BySerial = []store.Device{d}
		case "name":
			c.ByName = []store.Device{d}
		}
		r := report()
		r.BMC = &hostreport.BMC{Address: addrOf("10.9.0.5"), Prefix: 24, Ports: []hostreport.BMCPort{{MAC: "aa:bb:cc:00:00:10"}}}
		p := Build(State{Candidates: c}, r, params())
		if p.Match == MatchCreated {
			t.Fatalf("%s: not matched", how)
		}
		assertAdminUntouched(t, before, p)
		up := ops(p, OpUpdateDevice)[0].Device
		if up.DeviceType != store.DevWorkstation || up.ManagementIP != "10.9.0.5" {
			t.Fatalf("%s: %+v", how, up)
		}
	}
}

func TestInterfacesUpdateAndNotReported(t *testing.T) {
	d := store.Device{ID: "d1", Name: "web-01", InventoryHostID: hostID}
	st := State{Candidates: Candidates{ByHost: &d}, Interfaces: []store.DeviceInterface{
		{ID: "i1", DeviceID: "d1", Name: "eth0", MACAddress: "aa:bb:cc:00:00:01", InterfaceType: "ethernet", SpeedMbps: 100, Enabled: true, ReportState: store.RepReported, Description: "uplink"},
		{ID: "i2", DeviceID: "d1", Name: "eth1", ReportState: store.RepReported},
		{ID: "i3", DeviceID: "d1", Name: "manual0"}, // admin-created: never marked
		{ID: "i4", DeviceID: "d1", Name: "bmc", InterfaceType: store.DevInterfaceKindManagement, ReportState: store.RepReported},
		{ID: "i5", DeviceID: "d1", Name: "eth2", ReportState: store.RepNotReported},
	}}
	r := report()
	r.Interfaces = append(r.Interfaces, hostreport.Interface{Name: "eth2", Kind: "", Up: true})
	p := Build(st, r, params())
	up := ops(p, OpUpdateInterface)
	got := map[string]store.DeviceInterface{}
	for _, o := range up {
		got[o.Interface.ID] = *o.Interface
	}
	if got["i1"].SpeedMbps != 1000 || got["i1"].Description != "uplink" {
		t.Fatalf("eth0 update %+v", got["i1"])
	}
	if got["i2"].ReportState != store.RepNotReported {
		t.Fatal("eth1 must be marked not reported")
	}
	if _, ok := got["i3"]; ok {
		t.Fatal("admin interface touched")
	}
	if _, ok := got["i4"]; ok {
		t.Fatal("BMC interface marked while the BMC is unreadable")
	}
	if got["i5"].ReportState != store.RepReported || got["i5"].InterfaceType != "" {
		t.Fatalf("re-appearing interface %+v", got["i5"])
	}
	if !hasAction(p, "interface_not_reported") || !hasAction(p, "interface_updated") {
		t.Fatal(actions(p))
	}
}

func TestTenantExclusionPattern(t *testing.T) {
	p := params()
	p.Exclusions = []string{"eth*", "dock?r*"}
	pl := Build(State{}, report(), p)
	if len(ops(pl, OpCreateInterface)) != 0 || len(ops(pl, OpCreateAddress)) != 0 {
		t.Fatal("excluded interface produced rows")
	}
	p.Exclusions = nil
	pl = Build(State{}, report(), p)
	if len(ops(pl, OpCreateInterface)) != 2 { // eth0 + docker0; lo stays excluded
		t.Fatalf("%+v", ops(pl, OpCreateInterface))
	}
}
