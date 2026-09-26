package hostplan

import (
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func linked() (store.Device, State) {
	d := store.Device{ID: "d1", Name: "web-01", InventoryHostID: hostID, Source: store.SrcHostReport, ReportState: store.RepReported}
	return d, State{Candidates: Candidates{ByHost: &d}}
}

func createdAddrs(p Plan) map[string]store.IPAddress {
	out := map[string]store.IPAddress{}
	for _, o := range ops(p, OpCreateAddress) {
		out[o.Address.Address] = *o.Address
	}
	return out
}

func TestAddressPlacementMostSpecific(t *testing.T) {
	_, st := linked()
	st.Subnets = []store.Subnet{{ID: "s8", Name: "ten", CIDR: "10.0.0.0/8"}, {ID: "s24", Name: "lan", CIDR: "10.0.0.0/24"}, {ID: "v6", Name: "v6", CIDR: "2001:db8::/48"}}
	p := Build(st, report(), params())
	a := createdAddrs(p)
	if a["10.0.0.5"].SubnetID != "s24" || !a["10.0.0.5"].IsPrimary || a["10.0.0.5"].InterfaceName != "eth0" ||
		a["10.0.0.5"].MACAddress != "aa:bb:cc:00:00:01" || a["10.0.0.5"].Hostname != "web-01" || a["10.0.0.5"].Status != store.IPActive ||
		a["10.0.0.5"].AddressType != store.AddrHost || a["10.0.0.5"].ReportState != store.RepReported {
		t.Fatalf("v4 %+v", a["10.0.0.5"])
	}
	if a["2001:db8::5"].SubnetID != "v6" || a["2001:db8::5"].IsPrimary {
		t.Fatalf("v6 %+v", a["2001:db8::5"])
	}
	if _, ok := a["fe80::1"]; ok {
		t.Fatal("link-local recorded")
	}
	if len(ops(p, OpCreateSubnet)) != 0 {
		t.Fatal("no subnet should be created")
	}
}

func TestAutoSubnet(t *testing.T) {
	_, st := linked()
	st.Subnets = []store.Subnet{{ID: "x", Name: "10.0.0.0/24", CIDR: "192.168.0.0/24"}} // name collision only
	r := report()
	r.Interfaces = []hostreport.Interface{{Name: "eth0", Up: true, Addresses: []hostreport.Address{
		addr("10.0.0.5", 24), addr("10.0.0.6", 24), addr("2001:db8::9", 128), addr("192.0.2.7", 32),
	}}}
	p := Build(st, r, params())
	subs := ops(p, OpCreateSubnet)
	if len(subs) != 3 {
		t.Fatalf("subnets %+v", subs)
	}
	byCIDR := map[string]store.Subnet{}
	for _, o := range subs {
		byCIDR[o.Subnet.CIDR] = *o.Subnet
	}
	s := byCIDR["10.0.0.0/24"]
	if s.Name != "10.0.0.0/24 (auto)" || s.Origin != store.OriginHostSync || s.CreatedBy != "hostsync" || s.ParentID != "" ||
		s.IPVersion != 4 || s.PrefixLength != 24 || s.NetworkAddress != "10.0.0.0" || s.Status != store.SubnetActive {
		t.Fatalf("auto subnet %+v", s)
	}
	if _, ok := byCIDR["2001:db8::/64"]; !ok {
		t.Fatal("/128 must become a /64 subnet")
	}
	if _, ok := byCIDR["192.0.2.7/32"]; !ok {
		t.Fatal("/32 stays /32")
	}
	a := createdAddrs(p)
	if a["10.0.0.5"].SubnetID != s.ID || a["10.0.0.6"].SubnetID != s.ID {
		t.Fatal("both addresses share the planned subnet")
	}
	if !hasAction(p, "subnet_created") {
		t.Fatal("audit")
	}
}

func TestAddressSkips(t *testing.T) {
	_, st := linked()
	r := report()
	tmp := addr("2001:db8::77", 64)
	tmp.Temporary = true
	dep := addr("2001:db8::78", 64)
	dep.Deprecated = true
	r.Interfaces = []hostreport.Interface{
		{Name: "eth0", Addresses: []hostreport.Address{tmp, dep, addr("169.254.0.1", 16), addr("224.0.0.1", 4), addr("10.1.1.1", 24)}},
		{Name: "eth1", Addresses: []hostreport.Address{addr("10.1.1.1", 24)}}, // same address on a second interface
	}
	a := createdAddrs(Build(st, r, params()))
	if len(a) != 1 || a["10.1.1.1"].InterfaceName != "eth0" {
		t.Fatalf("only the global address once: %+v", a)
	}
}

func TestAddressMoveClaimAndConflict(t *testing.T) {
	_, st := linked()
	st.Subnets = []store.Subnet{{ID: "s", Name: "lan", CIDR: "10.0.0.0/24"}}
	prevStart := t0.Add(-time.Hour)
	st.Addresses = map[string]store.IPAddress{
		"10.0.0.5": {ID: "a1", Address: "10.0.0.5", SubnetID: "s", DeviceID: "other", ReportState: store.RepReported,
			Description: "desc", Note: "note", Tags: map[string]string{"x": "y"}, DNSName: "dns", PTRRecord: "ptr", Status: store.IPReserved,
			MoveCount: 2, MoveWindowStart: &prevStart},
	}
	r := report()
	r.Interfaces[0].Addresses = []hostreport.Address{addr("10.0.0.5", 24)}
	p := Build(st, r, params())
	up := ops(p, OpUpdateAddress)
	if len(up) != 1 {
		t.Fatalf("%+v", p.Ops)
	}
	a := *up[0].Address
	if a.DeviceID != "d1" || a.PreviousDeviceID != "other" || a.MovedAt == nil || a.MoveCount != 3 || !a.Conflict {
		t.Fatalf("move %+v", a)
	}
	if a.Description != "desc" || a.Note != "note" || a.DNSName != "dns" || a.PTRRecord != "ptr" || a.Status != store.IPReserved || a.Tags["x"] != "y" || a.SubnetID != "s" {
		t.Fatal("admin address fields changed")
	}
	if !hasAction(p, "address_moved") || !hasAction(p, "address_conflict") || hasAction(p, "address_updated") {
		t.Fatal(actions(p))
	}
	if p.Events[0].Payload["action"] != events.ActionMoved {
		t.Fatalf("event %+v", p.Events)
	}
	// Window expired: counting restarts, no conflict.
	old := t0.Add(-48 * time.Hour)
	row := st.Addresses["10.0.0.5"]
	row.MoveWindowStart = &old
	st.Addresses["10.0.0.5"] = row
	a = *ops(Build(st, r, params()), OpUpdateAddress)[0].Address
	if a.MoveCount != 1 || a.Conflict || !a.MoveWindowStart.Equal(t0) {
		t.Fatalf("window restart %+v", a)
	}
	// Unowned row -> claim, not move.
	row.DeviceID = ""
	st.Addresses["10.0.0.5"] = row
	p = Build(st, r, params())
	a = *ops(p, OpUpdateAddress)[0].Address
	if a.DeviceID != "d1" || a.PreviousDeviceID != "" || a.MovedAt != nil || !hasAction(p, "address_updated") || hasAction(p, "address_moved") {
		t.Fatalf("claim %+v %v", a, actions(p))
	}
	if p.Events[0].Payload["action"] != events.ActionClaimed {
		t.Fatal("claim event")
	}
}

func TestConflictClearedAfterWindow(t *testing.T) {
	_, st := linked()
	moved := t0.Add(-25 * time.Hour)
	st.Addresses = map[string]store.IPAddress{"10.0.0.5": {ID: "a1", Address: "10.0.0.5", DeviceID: "d1", InterfaceName: "eth0",
		MACAddress: "aa:bb:cc:00:00:01", Hostname: "web-01", IsPrimary: true, ReportState: store.RepReported,
		Conflict: true, MovedAt: &moved, MoveCount: 3, MoveWindowStart: &moved}}
	r := report()
	r.Interfaces[0].Addresses = []hostreport.Address{addr("10.0.0.5", 24)}
	p := Build(st, r, params())
	if !hasAction(p, "address_conflict_cleared") || ops(p, OpUpdateAddress)[0].Address.Conflict {
		t.Fatal(actions(p))
	}
	// Inside the window: stays.
	recent := t0.Add(-time.Hour)
	row := st.Addresses["10.0.0.5"]
	row.MovedAt = &recent
	st.Addresses["10.0.0.5"] = row
	if p := Build(st, r, params()); len(ops(p, OpUpdateAddress)) != 0 {
		t.Fatalf("no change expected: %v", actions(p))
	}
}

func TestAddressRelease(t *testing.T) {
	_, st := linked()
	st.Addresses = map[string]store.IPAddress{
		"10.0.0.99": {ID: "gone", Address: "10.0.0.99", DeviceID: "d1", InterfaceName: "eth0", IsPrimary: true, ReportState: store.RepReported, Note: "keep"},
		"10.0.0.98": {ID: "manual", Address: "10.0.0.98", DeviceID: "d1"}, // assigned by hand: never released
		"10.0.0.97": {ID: "bmcaddr", Address: "10.0.0.97", DeviceID: "d1", InterfaceName: "bmc", ReportState: store.RepReported},
		"10.0.0.96": {ID: "otherdev", Address: "10.0.0.96", DeviceID: "d2", ReportState: store.RepReported},
	}
	p := Build(st, report(), params())
	var rel []store.IPAddress
	for _, o := range ops(p, OpUpdateAddress) {
		rel = append(rel, *o.Address)
	}
	if len(rel) != 1 || rel[0].ID != "gone" || rel[0].DeviceID != "" || rel[0].PreviousDeviceID != "d1" ||
		rel[0].ReportState != store.RepNotReported || rel[0].IsPrimary || rel[0].InterfaceName != "" || rel[0].Note != "keep" {
		t.Fatalf("release %+v", rel)
	}
	if !hasAction(p, "address_released") {
		t.Fatal(actions(p))
	}
	found := false
	for _, e := range p.Events {
		found = found || e.Payload["action"] == events.ActionReleased
	}
	if !found {
		t.Fatal("release event")
	}
	// With a BMC reported, its missing address is released as well.
	r := report()
	r.BMC = &hostreport.BMC{Ports: []hostreport.BMCPort{{MAC: "aa:bb:cc:00:00:10"}}}
	n := 0
	for _, o := range ops(Build(st, r, params()), OpUpdateAddress) {
		if o.Address.ID == "bmcaddr" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("BMC address release with a BMC reported")
	}
}

func TestOwnAddressRefresh(t *testing.T) {
	_, st := linked()
	st.Addresses = map[string]store.IPAddress{"10.0.0.5": {ID: "a1", Address: "10.0.0.5", DeviceID: "d1", InterfaceName: "eth9",
		MACAddress: "aa:bb:cc:00:00:01", Hostname: "web-01", IsPrimary: true, ReportState: store.RepReported}}
	r := report()
	r.Interfaces[0].Addresses = []hostreport.Address{addr("10.0.0.5", 24)}
	p := Build(st, r, params())
	up := ops(p, OpUpdateAddress)
	if len(up) != 1 || up[0].Address.InterfaceName != "eth0" || up[0].Address.LastSeen == nil || p.Events[0].Payload["action"] != events.ActionUpdated {
		t.Fatalf("%+v", p.Ops)
	}
}

func TestAddressHostnameIsDNSSafe(t *testing.T) {
	for in, want := range map[string]string{"web-01": "web-01", "web-01.example.org": "web-01.example.org", "Web 01": "", "-x": "", "": "", "a_b": ""} {
		if got := DNSHostname(in); got != want {
			t.Errorf("DNSHostname(%q) = %q", in, got)
		}
	}
	_, st := linked()
	r := report()
	r.Hostname = "Kevin's Laptop"
	a := createdAddrs(Build(st, r, params()))["10.0.0.5"]
	if a.Hostname != "" {
		t.Fatalf("address hostname %q", a.Hostname)
	}
}
