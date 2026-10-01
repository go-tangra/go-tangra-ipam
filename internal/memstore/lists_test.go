package memstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestInetKey(t *testing.T) {
	ordered := []string{"9.0.0.0/8", "10.0.0.0/16", "10.0.0.0/24", "10.0.0.2", "10.0.0.10", "192.168.1.1", "::1", "2001:db8::/64"}
	for i := 1; i < len(ordered); i++ {
		if a, b := inetKey(ordered[i-1]).(string), inetKey(ordered[i]).(string); a >= b {
			t.Fatalf("%s should sort before %s", ordered[i-1], ordered[i])
		}
	}
	if inetKey("bogus") != nil || optional("") != nil || optional("x") != "x" {
		t.Fatal("invalid values sort last")
	}
}

func TestPagedLists(t *testing.T) {
	m := New()
	ctx := context.Background()
	const ten = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	sub := store.Subnet{ID: store.NewID(), TenantID: ten, Name: "lan", CIDR: "10.0.0.0/24", IPVersion: 4}
	v6 := store.Subnet{ID: store.NewID(), TenantID: ten, Name: "v6", CIDR: "2001:db8::/64", IPVersion: 6}
	for _, s := range []store.Subnet{sub, v6} {
		if err := m.CreateSubnet(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for i, o := range []int{100, 9, 2, 10} {
		a := store.IPAddress{ID: store.NewID(), TenantID: ten, SubnetID: sub.ID, Address: fmt.Sprintf("10.0.0.%d", o), Hostname: fmt.Sprint("h", i), MACAddress: "aa", Status: "active", AddressType: "host", CreatedAt: now}
		if i%2 == 0 {
			a.LastSeen = &now
		}
		if err := m.CreateAddress(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	addrs, total, applied, err := m.PageAddresses(ctx, ten, store.AddressFilter{CursorID: "ignored", Limit: 1}, listquery.Request{PageSize: 3})
	if err != nil || total != 4 || applied.Page != 1 || len(addrs) != 3 || addrs[0].Address != "10.0.0.2" || addrs[2].Address != "10.0.0.10" {
		t.Fatalf("addresses = %+v total %d (%v)", addrs, total, err)
	}
	for _, f := range []string{"hostname", "mac", "status", "address_type", "last_seen", "created_at"} {
		if _, total, _, err := m.PageAddresses(ctx, ten, store.AddressFilter{}, listquery.Request{Sort: f}); err != nil || total != 4 {
			t.Fatalf("sort %s: %d (%v)", f, total, err)
		}
	}
	subs, total, _, err := m.PageSubnets(ctx, ten, store.SubnetFilter{}, listquery.Request{Order: listquery.Desc})
	if err != nil || total != 2 || subs[0].CIDR != "2001:db8::/64" {
		t.Fatalf("subnets desc = %+v (%v)", subs, err)
	}
	for _, f := range []string{"name", "vlan", "location", "status"} {
		if _, _, _, err := m.PageSubnets(ctx, ten, store.SubnetFilter{IPVersion: 4}, listquery.Request{Sort: f}); err != nil {
			t.Fatal(err)
		}
	}
	dev := store.Device{ID: store.NewID(), TenantID: ten, Name: "b"}
	_ = m.CreateDevice(ctx, store.Device{ID: store.NewID(), TenantID: ten, Name: "A"})
	_ = m.CreateDevice(ctx, dev)
	devs, _, _, err := m.PageDevices(ctx, ten, store.DeviceFilter{}, listquery.Request{})
	if err != nil || devs[0].Name != "A" {
		t.Fatalf("devices = %+v (%v)", devs, err)
	}
	for _, f := range []string{"device_type", "status", "manufacturer", "location", "created_at"} {
		if _, _, _, err := m.PageDevices(ctx, ten, store.DeviceFilter{}, listquery.Request{Sort: f}); err != nil {
			t.Fatal(err)
		}
	}
	_ = m.CreateVlan(ctx, store.Vlan{ID: store.NewID(), TenantID: ten, VlanID: 20, Name: "b"})
	_ = m.CreateVlan(ctx, store.Vlan{ID: store.NewID(), TenantID: ten, VlanID: 3, Name: "a"})
	for _, f := range []string{"vlan_id", "name", "domain", "status"} {
		vl, _, _, err := m.PageVlans(ctx, ten, store.VlanFilter{}, listquery.Request{Sort: f})
		if err != nil || len(vl) != 2 || (f == "vlan_id" && vl[0].VlanID != 3) {
			t.Fatalf("vlans by %s = %+v (%v)", f, vl, err)
		}
	}
	_ = m.CreateScanJob(ctx, store.IPScanJob{ID: store.NewID(), TenantID: ten, SubnetID: sub.ID, Status: "pending"})
	for _, f := range []string{"created_at", "status", "subnet"} {
		if _, total, _, err := m.PageScanJobs(ctx, ten, store.ScanFilter{}, listquery.Request{Sort: f}); err != nil || total != 1 {
			t.Fatalf("scans by %s: %d (%v)", f, total, err)
		}
	}
	_ = m.CreateInterface(ctx, store.DeviceInterface{ID: store.NewID(), TenantID: ten, DeviceID: dev.ID, Name: "eth0"})
	if _, total, _, err := m.PageInterfaces(ctx, ten, dev.ID, listquery.Request{}); err != nil || total != 1 {
		t.Fatalf("interfaces: %d (%v)", total, err)
	}
	_ = m.ReplaceDevicePackages(ctx, ten, dev.ID, []store.DevicePackage{{ID: store.NewID(), TenantID: ten, Name: "b", CurrentVersion: "1"}, {ID: store.NewID(), TenantID: ten, Name: "a", CurrentVersion: "2"}})
	if pk, _, _, err := m.PageDevicePackages(ctx, ten, dev.ID, nil, nil, "", listquery.Request{Sort: "version", Order: listquery.Desc}); err != nil || pk[0].Name != "a" {
		t.Fatalf("packages by version = %+v (%v)", pk, err)
	}
	g := store.IPGroup{ID: store.NewID(), TenantID: ten, Name: "g"}
	_ = m.CreateIPGroup(ctx, g)
	_ = m.AddIPGroupMember(ctx, store.IPGroupMember{ID: store.NewID(), TenantID: ten, IPGroupID: g.ID, Value: "b", Sequence: 1})
	_ = m.AddIPGroupMember(ctx, store.IPGroupMember{ID: store.NewID(), TenantID: ten, IPGroupID: g.ID, Value: "a", Sequence: 2})
	if ms, _, _, err := m.PageIPGroupMembers(ctx, ten, g.ID, listquery.Request{Sort: "name"}); err != nil || ms[0].Value != "a" {
		t.Fatalf("ip members by name = %+v (%v)", ms, err)
	}
	if ms, _, _, err := m.PageIPGroupMembers(ctx, ten, g.ID, listquery.Request{}); err != nil || ms[0].Value != "b" {
		t.Fatalf("ip members by sequence = %+v (%v)", ms, err)
	}
	hg := store.HostGroup{ID: store.NewID(), TenantID: ten, Name: "h"}
	_ = m.CreateHostGroup(ctx, hg)
	_ = m.AddHostGroupMember(ctx, store.HostGroupMember{ID: store.NewID(), TenantID: ten, HostGroupID: hg.ID, DeviceID: dev.ID})
	for _, f := range []string{"sequence", "name"} {
		if hm, _, _, err := m.PageHostGroupMembers(ctx, ten, hg.ID, listquery.Request{Sort: f}); err != nil || len(hm) != 1 || hm[0].DeviceName != "b" {
			t.Fatalf("host members by %s = %+v (%v)", f, hm, err)
		}
	}
	if _, total, _, err := m.PageGuests(ctx, ten, dev.ID, listquery.Request{}); err != nil || total != 0 {
		t.Fatalf("guests: %d (%v)", total, err)
	}
	// Injected failures.
	for name, call := range map[string]func() error{
		"PageSubnets": func() error {
			_, _, _, e := m.PageSubnets(ctx, ten, store.SubnetFilter{}, listquery.Request{})
			return e
		},
		"PageAddresses": func() error {
			_, _, _, e := m.PageAddresses(ctx, ten, store.AddressFilter{}, listquery.Request{})
			return e
		},
		"PageDevices": func() error {
			_, _, _, e := m.PageDevices(ctx, ten, store.DeviceFilter{}, listquery.Request{})
			return e
		},
		"PageVlans": func() error { _, _, _, e := m.PageVlans(ctx, ten, store.VlanFilter{}, listquery.Request{}); return e },
		"PageScanJobs": func() error {
			_, _, _, e := m.PageScanJobs(ctx, ten, store.ScanFilter{}, listquery.Request{})
			return e
		},
		"PageIPGroupMembers":   func() error { _, _, _, e := m.PageIPGroupMembers(ctx, ten, g.ID, listquery.Request{}); return e },
		"PageHostGroupMembers": func() error { _, _, _, e := m.PageHostGroupMembers(ctx, ten, hg.ID, listquery.Request{}); return e },
		"PageInterfaces":       func() error { _, _, _, e := m.PageInterfaces(ctx, ten, dev.ID, listquery.Request{}); return e },
		"PageDevicePackages": func() error {
			_, _, _, e := m.PageDevicePackages(ctx, ten, dev.ID, nil, nil, "", listquery.Request{})
			return e
		},
		"PageGuests": func() error { _, _, _, e := m.PageGuests(ctx, ten, dev.ID, listquery.Request{}); return e },
	} {
		m.FailNext(name)
		if call() == nil {
			t.Fatalf("%s: injected failure not returned", name)
		}
	}
}
