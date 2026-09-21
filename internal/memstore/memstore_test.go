package memstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

func bg() context.Context { return context.Background() }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// --- FailNext seam ---

func TestFailNextInjectsError(t *testing.T) {
	ctx := bg()
	m := New()

	// A representative operation from each family should honor FailNext, and the
	// injection must be one-shot (disarmed after firing).
	ops := []struct {
		name string
		call func() error
	}{
		{"CreateSubnet", func() error { return m.CreateSubnet(ctx, store.Subnet{TenantID: "t1", Name: "n", CIDR: "10.0.0.0/24"}) }},
		{"CreateDevice", func() error { return m.CreateDevice(ctx, store.Device{TenantID: "t1", Name: "d"}) }},
		{"CreateVlan", func() error { return m.CreateVlan(ctx, store.Vlan{TenantID: "t1", Name: "v", VlanID: 10}) }},
		{"CreateLocation", func() error { return m.CreateLocation(ctx, store.Location{TenantID: "t1", Name: "l"}) }},
		{"CreateIPGroup", func() error { return m.CreateIPGroup(ctx, store.IPGroup{TenantID: "t1", Name: "g"}) }},
		{"CreateHostGroup", func() error { return m.CreateHostGroup(ctx, store.HostGroup{TenantID: "t1", Name: "h"}) }},
		{"CreateScanJob", func() error { return m.CreateScanJob(ctx, store.IPScanJob{TenantID: "t1", SubnetID: "s"}) }},
		{"AppendAudit", func() error { return m.AppendAudit(ctx, store.AuditRow{TenantID: "t1"}) }},
		{"ClaimDueScanJobs", func() error { _, e := m.ClaimDueScanJobs(ctx, time.Now(), 5); return e }},
		{"UpsertDNSConfig", func() error { return m.UpsertDNSConfig(ctx, store.DNSConfig{TenantID: "t1"}) }},
	}
	for _, op := range ops {
		m.FailNext(op.name)
		err := op.call()
		if err == nil {
			t.Fatalf("%s: expected injected failure", op.name)
		}
		if got := err.Error(); got == "" || !contains(got, op.name) {
			t.Fatalf("%s: error message %q should name the method", op.name, got)
		}
		// Second call must succeed (injection disarmed).
		if err := op.call(); err != nil {
			t.Fatalf("%s: injection not one-shot: %v", op.name, err)
		}
	}
}

// --- subnets ---

func TestSubnetCRUDAndFilters(t *testing.T) {
	ctx := bg()
	m := New()

	if err := m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "prod", CIDR: "10.0.0.0/24", LocationID: "loc1", VlanID: "vl1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Defaults applied.
	got, err := m.GetSubnet(ctx, "t1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != store.SubnetActive || got.IPVersion != 4 {
		t.Fatalf("defaults not applied: %+v", got)
	}
	if got.TotalAddresses != 256 {
		t.Fatalf("total = %d, want 256", got.TotalAddresses)
	}

	// Duplicate name -> conflict.
	if err := m.CreateSubnet(ctx, store.Subnet{TenantID: "t1", Name: "prod", CIDR: "10.1.0.0/24"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup name err = %v, want conflict", err)
	}
	// Cross-tenant get -> not found.
	if _, err := m.GetSubnet(ctx, "t2", "s1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant err = %v, want not found", err)
	}

	// Second subnet in same tenant for filters.
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s2", TenantID: "t1", Name: "dev", CIDR: "192.168.0.0/24", Status: store.SubnetReserved})

	all, _ := m.ListSubnets(ctx, "t1", store.SubnetFilter{})
	if len(all) != 2 {
		t.Fatalf("list all = %d, want 2", len(all))
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{Status: store.SubnetReserved})); len(got) != 1 || got[0].ID != "s2" {
		t.Fatalf("status filter = %+v", got)
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{LocationID: "loc1"})); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("location filter = %+v", got)
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{VlanID: "vl1"})); len(got) != 1 {
		t.Fatalf("vlan filter len = %d", len(got))
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{IPVersion: 4})); len(got) != 2 {
		t.Fatalf("ipversion filter len = %d", len(got))
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{Query: "prod"})); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("query filter = %+v", got)
	}
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{ParentID: "nope"})); len(got) != 0 {
		t.Fatalf("parent filter should be empty")
	}
	// Limit pagination.
	if got := mustList(m.ListSubnets(ctx, "t1", store.SubnetFilter{Limit: 1})); len(got) != 1 {
		t.Fatalf("limit len = %d", len(got))
	}

	// Update, conflict, not-found.
	got.Description = "updated"
	if err := m.UpdateSubnet(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	dup := got
	dup.Name = "dev"
	if err := m.UpdateSubnet(ctx, dup); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup err = %v, want conflict", err)
	}
	if err := m.UpdateSubnet(ctx, store.Subnet{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v, want not found", err)
	}

	// SubnetsForVlan / AllSubnetCIDRs helpers.
	if v, _ := m.SubnetsForVlan(ctx, "t1", "vl1"); len(v) != 1 {
		t.Fatalf("SubnetsForVlan = %d, want 1", len(v))
	}
	if c, _ := m.AllSubnetCIDRs(ctx, "t1"); len(c) != 2 {
		t.Fatalf("AllSubnetCIDRs = %d, want 2", len(c))
	}

	// Delete guard: add address then delete without force.
	_ = m.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.5", SubnetID: "s1"})
	if err := m.DeleteSubnet(ctx, "t1", "s1", false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete non-empty err = %v, want not empty", err)
	}
	if err := m.DeleteSubnet(ctx, "t1", "s1", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if err := m.DeleteSubnet(ctx, "t1", "ghost", true); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost err = %v, want not found", err)
	}
}

func TestSubnetAddressCounts(t *testing.T) {
	ctx := bg()
	m := New()
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "n", CIDR: "10.0.0.0/24"})
	_ = m.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1"})
	_ = m.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.2", SubnetID: "s1"})

	if n, _ := m.CountAddressesInSubnet(ctx, "t1", "s1"); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	alloc, _ := m.ListAllocatedAddresses(ctx, "t1", "s1")
	if len(alloc) != 2 || alloc[0] != "10.0.0.1" {
		t.Fatalf("alloc = %v", alloc)
	}
	sub, _ := m.GetSubnet(ctx, "t1", "s1")
	if sub.UsedAddresses != 2 || sub.AvailableAddresses != 254 {
		t.Fatalf("used=%d avail=%d", sub.UsedAddresses, sub.AvailableAddresses)
	}
}

func TestSubnetTotalEdgeCases(t *testing.T) {
	ctx := bg()
	m := New()
	// /32 IPv4 -> hostBits 0 -> capacity 1.
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "p32", TenantID: "t1", Name: "p32", CIDR: "10.0.0.1/32"})
	if s, _ := m.GetSubnet(ctx, "t1", "p32"); s.TotalAddresses != 1 {
		t.Fatalf("/32 total = %d, want 1", s.TotalAddresses)
	}
	// Wide IPv6 (hostBits > 62) -> treated as unbounded (0).
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "v6", TenantID: "t1", Name: "v6", CIDR: "2001:db8::/48", IPVersion: 6})
	if s, _ := m.GetSubnet(ctx, "t1", "v6"); s.TotalAddresses != 0 {
		t.Fatalf("wide v6 total = %d, want 0 (unbounded)", s.TotalAddresses)
	}
	// Empty / unparseable CIDR -> 0.
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "bad", TenantID: "t1", Name: "bad", CIDR: ""})
	if s, _ := m.GetSubnet(ctx, "t1", "bad"); s.TotalAddresses != 0 {
		t.Fatalf("empty cidr total = %d, want 0", s.TotalAddresses)
	}
}

// --- addresses ---

func TestAddressCRUDAndFilters(t *testing.T) {
	ctx := bg()
	m := New()
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "n", CIDR: "10.0.0.0/24"})
	_ = m.CreateDevice(ctx, store.Device{ID: "d1", TenantID: "t1", Name: "dev"})

	if err := m.CreateAddress(ctx, store.IPAddress{ID: "a1", TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1", DeviceID: "d1", Hostname: "web01"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Duplicate IP -> conflict (allocation guard).
	if err := m.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup ip err = %v, want conflict", err)
	}
	_ = m.CreateAddress(ctx, store.IPAddress{ID: "a2", TenantID: "t1", Address: "10.0.0.2", SubnetID: "s1", Status: store.IPReserved, AddressType: store.AddrGateway})

	if a, err := m.GetAddress(ctx, "t1", "a1"); err != nil || a.Hostname != "web01" {
		t.Fatalf("get: %v %+v", err, a)
	}
	if _, err := m.GetAddress(ctx, "t2", "a1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant get err = %v", err)
	}
	if a, err := m.FindAddress(ctx, "t1", "10.0.0.2"); err != nil || a.ID != "a2" {
		t.Fatalf("find: %v %+v", err, a)
	}
	if _, err := m.FindAddress(ctx, "t1", "10.0.0.99"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("find missing err = %v", err)
	}

	// Filters.
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{SubnetID: "s1"})); len(got) != 2 {
		t.Fatalf("subnet filter = %d", len(got))
	}
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{DeviceID: "d1"})); len(got) != 1 {
		t.Fatalf("device filter = %d", len(got))
	}
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{Status: store.IPReserved})); len(got) != 1 {
		t.Fatalf("status filter = %d", len(got))
	}
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{AddressType: store.AddrGateway})); len(got) != 1 {
		t.Fatalf("type filter = %d", len(got))
	}
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{AddressPrefix: "10.0.0.1"})); len(got) != 1 {
		t.Fatalf("prefix filter = %d", len(got))
	}
	if got := mustListA(m.ListAddresses(ctx, "t1", store.AddressFilter{HostnamePattern: "web"})); len(got) != 1 {
		t.Fatalf("hostname filter = %d", len(got))
	}

	// AddressesForDevice.
	if got, _ := m.AddressesForDevice(ctx, "t1", "d1"); len(got) != 1 {
		t.Fatalf("AddressesForDevice = %d", len(got))
	}

	// Update: rename address, conflict on collide, not-found.
	a1, _ := m.GetAddress(ctx, "t1", "a1")
	a1.Address = "10.0.0.10"
	if err := m.UpdateAddress(ctx, a1); err != nil {
		t.Fatalf("update: %v", err)
	}
	a1.Address = "10.0.0.2" // collide with a2
	if err := m.UpdateAddress(ctx, a1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update collide err = %v, want conflict", err)
	}
	if err := m.UpdateAddress(ctx, store.IPAddress{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// Delete + not-found.
	if err := m.DeleteAddress(ctx, "t1", "a2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := m.DeleteAddress(ctx, "t1", "a2"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete twice err = %v", err)
	}
}

func TestUpsertAddressByAddress(t *testing.T) {
	ctx := bg()
	m := New()
	created, err := m.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1"})
	if err != nil || !created {
		t.Fatalf("first upsert created=%v err=%v", created, err)
	}
	// Second upsert of the same address updates (created=false) and preserves status/type.
	created, err = m.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1", Hostname: "h"})
	if err != nil || created {
		t.Fatalf("second upsert created=%v err=%v", created, err)
	}
	got, _ := m.FindAddress(ctx, "t1", "10.0.0.1")
	if got.Hostname != "h" || got.Status != store.IPActive {
		t.Fatalf("upsert merge = %+v", got)
	}
}

// --- devices / interfaces / links / packages ---

func TestDeviceInterfaceLinkPackageLifecycle(t *testing.T) {
	ctx := bg()
	m := New()

	if err := m.CreateDevice(ctx, store.Device{ID: "d1", TenantID: "t1", Name: "sw1", DeviceType: store.DevSwitch, LocationID: "loc1", Manufacturer: "acme", PrimaryIP: "10.0.0.1"}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := m.CreateDevice(ctx, store.Device{TenantID: "t1", Name: "sw1"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup device err = %v", err)
	}
	_ = m.CreateDevice(ctx, store.Device{ID: "d2", TenantID: "t1", Name: "srv1", DeviceType: store.DevServer})

	if d, err := m.GetDevice(ctx, "t1", "d1"); err != nil || d.DeviceType != store.DevSwitch {
		t.Fatalf("get device: %v %+v", err, d)
	}
	// Device filters.
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{DeviceType: store.DevSwitch})); len(got) != 1 {
		t.Fatalf("type filter = %d", len(got))
	}
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{LocationID: "loc1"})); len(got) != 1 {
		t.Fatalf("loc filter = %d", len(got))
	}
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{Manufacturer: "acme"})); len(got) != 1 {
		t.Fatalf("mfr filter = %d", len(got))
	}
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{Query: "srv"})); len(got) != 1 {
		t.Fatalf("query filter = %d", len(got))
	}
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{Status: store.DevStActive})); len(got) != 2 {
		t.Fatalf("status filter = %d", len(got))
	}
	if got := mustListD(m.ListDevices(ctx, "t1", store.DeviceFilter{RackID: "r9"})); len(got) != 0 {
		t.Fatalf("rack filter should be empty")
	}

	// Update + conflict + not-found.
	d1, _ := m.GetDevice(ctx, "t1", "d1")
	d1.Model = "x100"
	if err := m.UpdateDevice(ctx, d1); err != nil {
		t.Fatalf("update device: %v", err)
	}
	d1.Name = "srv1"
	if err := m.UpdateDevice(ctx, d1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup device err = %v", err)
	}
	if err := m.UpdateDevice(ctx, store.Device{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost device err = %v", err)
	}

	// UpsertDeviceByName: create then merge.
	up, err := m.UpsertDeviceByName(ctx, store.Device{TenantID: "t1", Name: "sw1", OSVersion: "1.2.3"})
	if err != nil || up.ID != "d1" || up.OSVersion != "1.2.3" {
		t.Fatalf("upsert merge = %+v err=%v", up, err)
	}
	up2, err := m.UpsertDeviceByName(ctx, store.Device{TenantID: "t1", Name: "brandnew"})
	if err != nil || up2.ID == "" {
		t.Fatalf("upsert create = %+v err=%v", up2, err)
	}

	// Interfaces.
	if err := m.CreateInterface(ctx, store.DeviceInterface{ID: "if1", TenantID: "t1", DeviceID: "d1", Name: "Gi0/1", IfIndex: 1}); err != nil {
		t.Fatalf("create iface: %v", err)
	}
	if err := m.CreateInterface(ctx, store.DeviceInterface{TenantID: "t1", DeviceID: "d1", Name: "Gi0/1"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup iface err = %v", err)
	}
	if i, err := m.GetInterface(ctx, "t1", "if1"); err != nil || i.Name != "Gi0/1" {
		t.Fatalf("get iface: %v %+v", err, i)
	}
	if got, _ := m.ListInterfaces(ctx, "t1", "d1"); len(got) != 1 {
		t.Fatalf("list ifaces = %d", len(got))
	}
	// Upsert interface (merge existing + create new).
	if _, err := m.UpsertInterfaceByName(ctx, store.DeviceInterface{TenantID: "t1", DeviceID: "d1", Name: "Gi0/1", SpeedMbps: 1000}); err != nil {
		t.Fatalf("upsert iface merge: %v", err)
	}
	si, err := m.UpsertInterfaceByName(ctx, store.DeviceInterface{TenantID: "t1", DeviceID: "d1", Name: "Gi0/2", IfIndex: 2})
	if err != nil {
		t.Fatalf("upsert iface create: %v", err)
	}

	// Links.
	links := []store.DeviceInterfaceLink{{TenantID: "t1", InterfaceID: "if1", RemotePortName: "aa", LinkSource: "lldp", LinkVlan: 5}}
	if err := m.ReplaceInterfaceLinks(ctx, "t1", "if1", links); err != nil {
		t.Fatalf("replace links: %v", err)
	}
	if err := m.ReplaceInterfaceLinks(ctx, "t1", "ghost-if", links); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("replace links ghost err = %v", err)
	}
	if got, _ := m.ListInterfaceLinks(ctx, "t1", "if1"); len(got) != 1 || got[0].LinkVlan != 5 {
		t.Fatalf("list links = %+v", got)
	}

	// Packages.
	pkgs := []store.DevicePackage{
		{TenantID: "t1", DeviceID: "d1", Name: "openssl", NeedsUpdate: true, IsSecurityUpdate: true, PackageManager: "apt"},
		{TenantID: "t1", DeviceID: "d1", Name: "vim", NeedsUpdate: false, PackageManager: "apt"},
	}
	if err := m.ReplaceDevicePackages(ctx, "t1", "d1", pkgs); err != nil {
		t.Fatalf("replace pkgs: %v", err)
	}
	// Duplicate package name -> conflict.
	if err := m.ReplaceDevicePackages(ctx, "t1", "d1", []store.DevicePackage{{Name: "x"}, {Name: "x"}}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup pkg err = %v", err)
	}
	_ = m.ReplaceDevicePackages(ctx, "t1", "d1", pkgs) // restore
	yes := true
	if got, _ := m.ListDevicePackages(ctx, "t1", "d1", &yes, nil, ""); len(got) != 1 {
		t.Fatalf("needsUpdate filter = %d", len(got))
	}
	if got, _ := m.ListDevicePackages(ctx, "t1", "d1", nil, &yes, ""); len(got) != 1 {
		t.Fatalf("security filter = %d", len(got))
	}
	if got, _ := m.ListDevicePackages(ctx, "t1", "d1", nil, nil, "apt"); len(got) != 2 {
		t.Fatalf("manager filter = %d", len(got))
	}
	if got, _ := m.ListDevicePackages(ctx, "t1", "d1", nil, nil, "yum"); len(got) != 0 {
		t.Fatalf("manager filter miss should be empty")
	}

	// fillDevice counts reflected on Get.
	gd, _ := m.GetDevice(ctx, "t1", "d1")
	if gd.InterfaceCount != 2 || gd.PackageUpdateCount != 1 || gd.SecurityUpdateCount != 1 {
		t.Fatalf("device counts = if:%d pu:%d su:%d", gd.InterfaceCount, gd.PackageUpdateCount, gd.SecurityUpdateCount)
	}

	if err := m.DeleteDevicePackages(ctx, "t1", "d1"); err != nil {
		t.Fatalf("delete pkgs: %v", err)
	}
	if err := m.DeleteInterface(ctx, "t1", si.ID); err != nil {
		t.Fatalf("delete iface: %v", err)
	}
	if err := m.DeleteInterface(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete iface ghost err = %v", err)
	}

	// Device delete guard (still has if1) then force.
	if err := m.DeleteDevice(ctx, "t1", "d1", false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete non-empty device err = %v", err)
	}
	if err := m.DeleteDevice(ctx, "t1", "d1", true); err != nil {
		t.Fatalf("force delete device: %v", err)
	}
	if err := m.DeleteDevice(ctx, "t1", "ghost", true); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost device err = %v", err)
	}
}

// --- vlans ---

func TestVlanCRUDAndFilters(t *testing.T) {
	ctx := bg()
	m := New()
	if err := m.CreateVlan(ctx, store.Vlan{ID: "v1", TenantID: "t1", VlanID: 10, Name: "vlan10", Domain: "corp", LocationID: "loc1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.CreateVlan(ctx, store.Vlan{TenantID: "t1", VlanID: 10, Name: "other"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup vlanid err = %v", err)
	}
	_ = m.CreateVlan(ctx, store.Vlan{ID: "v2", TenantID: "t1", VlanID: 20, Name: "vlan20", Status: store.VlanReserved})

	if v, err := m.GetVlan(ctx, "t1", "v1"); err != nil || v.Status != store.VlanActive {
		t.Fatalf("get: %v %+v", err, v)
	}
	if _, err := m.GetVlan(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get ghost err = %v", err)
	}
	if got := mustListV(m.ListVlans(ctx, "t1", store.VlanFilter{Domain: "corp"})); len(got) != 1 {
		t.Fatalf("domain filter = %d", len(got))
	}
	if got := mustListV(m.ListVlans(ctx, "t1", store.VlanFilter{LocationID: "loc1"})); len(got) != 1 {
		t.Fatalf("loc filter = %d", len(got))
	}
	if got := mustListV(m.ListVlans(ctx, "t1", store.VlanFilter{Status: store.VlanReserved})); len(got) != 1 {
		t.Fatalf("status filter = %d", len(got))
	}
	if got := mustListV(m.ListVlans(ctx, "t1", store.VlanFilter{VlanIDMin: 15, VlanIDMax: 25})); len(got) != 1 || got[0].ID != "v2" {
		t.Fatalf("range filter = %+v", got)
	}

	v1, _ := m.GetVlan(ctx, "t1", "v1")
	v1.Description = "u"
	if err := m.UpdateVlan(ctx, v1); err != nil {
		t.Fatalf("update: %v", err)
	}
	v1.VlanID = 20
	if err := m.UpdateVlan(ctx, v1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup err = %v", err)
	}
	if err := m.UpdateVlan(ctx, store.Vlan{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// Delete guard: a subnet references the vlan.
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "n", CIDR: "10.0.0.0/24", VlanID: "v1"})
	if err := m.DeleteVlan(ctx, "t1", "v1", false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete referenced err = %v", err)
	}
	if err := m.DeleteVlan(ctx, "t1", "v1", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if err := m.DeleteVlan(ctx, "t1", "ghost", true); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost err = %v", err)
	}
}

// --- locations ---

func TestLocationCRUDAndFilters(t *testing.T) {
	ctx := bg()
	m := New()
	if err := m.CreateLocation(ctx, store.Location{ID: "l1", TenantID: "t1", Name: "hq", Code: "HQ", LocationType: "site", Country: "US"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.CreateLocation(ctx, store.Location{TenantID: "t1", Name: "hq"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup name err = %v", err)
	}
	if err := m.CreateLocation(ctx, store.Location{TenantID: "t1", Name: "other", Code: "HQ"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup code err = %v", err)
	}
	_ = m.CreateLocation(ctx, store.Location{ID: "l2", TenantID: "t1", Name: "rack-a", LocationType: "rack", ParentID: "l1", Status: store.LocStPlanned})

	if l, err := m.GetLocation(ctx, "t1", "l1"); err != nil || l.ChildCount != 1 {
		t.Fatalf("get: %v %+v", err, l)
	}
	if got := mustListL(m.ListLocations(ctx, "t1", store.LocationFilter{ParentID: "l1"})); len(got) != 1 {
		t.Fatalf("parent filter = %d", len(got))
	}
	if got := mustListL(m.ListLocations(ctx, "t1", store.LocationFilter{LocationType: "rack"})); len(got) != 1 {
		t.Fatalf("type filter = %d", len(got))
	}
	if got := mustListL(m.ListLocations(ctx, "t1", store.LocationFilter{Country: "US"})); len(got) != 1 {
		t.Fatalf("country filter = %d", len(got))
	}
	if got := mustListL(m.ListLocations(ctx, "t1", store.LocationFilter{Status: store.LocStPlanned})); len(got) != 1 {
		t.Fatalf("status filter = %d", len(got))
	}

	l1, _ := m.GetLocation(ctx, "t1", "l1")
	l1.City = "NYC"
	if err := m.UpdateLocation(ctx, l1); err != nil {
		t.Fatalf("update: %v", err)
	}
	l1.Name = "rack-a"
	if err := m.UpdateLocation(ctx, l1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup err = %v", err)
	}
	if err := m.UpdateLocation(ctx, store.Location{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// Delete guard: l1 has a child l2.
	if err := m.DeleteLocation(ctx, "t1", "l1", false); !errors.Is(err, repo.ErrNotEmpty) {
		t.Fatalf("delete referenced err = %v", err)
	}
	if err := m.DeleteLocation(ctx, "t1", "l1", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	// l2's parent ref was nulled.
	if l2, _ := m.GetLocation(ctx, "t1", "l2"); l2.ParentID != "" {
		t.Fatalf("child parent not nulled: %+v", l2)
	}
	if err := m.DeleteLocation(ctx, "t1", "ghost", true); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost err = %v", err)
	}
}

// --- ip groups + members ---

func TestIPGroupsAndMembers(t *testing.T) {
	ctx := bg()
	m := New()
	if err := m.CreateIPGroup(ctx, store.IPGroup{ID: "g1", TenantID: "t1", Name: "web"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.CreateIPGroup(ctx, store.IPGroup{TenantID: "t1", Name: "web"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup err = %v", err)
	}
	_ = m.CreateIPGroup(ctx, store.IPGroup{ID: "g2", TenantID: "t1", Name: "db"})

	if g, err := m.GetIPGroup(ctx, "t1", "g1"); err != nil || g.Status != store.GroupActive {
		t.Fatalf("get: %v %+v", err, g)
	}
	if _, err := m.GetIPGroup(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get ghost err = %v", err)
	}
	if got, _ := m.ListIPGroups(ctx, "t1", 0, ""); len(got) != 2 {
		t.Fatalf("list = %d", len(got))
	}

	g1, _ := m.GetIPGroup(ctx, "t1", "g1")
	g1.Description = "u"
	if err := m.UpdateIPGroup(ctx, g1); err != nil {
		t.Fatalf("update: %v", err)
	}
	g1.Name = "db"
	if err := m.UpdateIPGroup(ctx, g1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup err = %v", err)
	}
	if err := m.UpdateIPGroup(ctx, store.IPGroup{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// Members.
	if err := m.AddIPGroupMember(ctx, store.IPGroupMember{ID: "m1", TenantID: "t1", IPGroupID: "g1", Value: "10.0.0.1", Sequence: 2}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := m.AddIPGroupMember(ctx, store.IPGroupMember{TenantID: "t1", IPGroupID: "g1", Value: "10.0.0.1"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup member err = %v", err)
	}
	if err := m.AddIPGroupMember(ctx, store.IPGroupMember{TenantID: "t1", IPGroupID: "ghost", Value: "x"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("member to ghost group err = %v", err)
	}
	_ = m.AddIPGroupMember(ctx, store.IPGroupMember{ID: "m2", TenantID: "t1", IPGroupID: "g1", Value: "10.0.0.2", Sequence: 1})

	if got, _ := m.ListIPGroupMembers(ctx, "t1", "g1"); len(got) != 2 || got[0].ID != "m2" {
		t.Fatalf("members sorted by sequence = %+v", got)
	}
	if g, _ := m.GetIPGroup(ctx, "t1", "g1"); g.MemberCount != 2 {
		t.Fatalf("member count = %d", g.MemberCount)
	}

	// Update member + conflict + not-found.
	mm, _ := func() (store.IPGroupMember, error) {
		list, err := m.ListIPGroupMembers(ctx, "t1", "g1")
		return list[0], err
	}()
	mm.Description = "primary"
	if err := m.UpdateIPGroupMember(ctx, mm); err != nil {
		t.Fatalf("update member: %v", err)
	}
	mm.Value = "10.0.0.1" // collide with m1
	if err := m.UpdateIPGroupMember(ctx, mm); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update member collide err = %v", err)
	}
	if err := m.UpdateIPGroupMember(ctx, store.IPGroupMember{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost member err = %v", err)
	}

	// AllIPGroupsWithMembers (all, then filtered).
	groups, members, err := m.AllIPGroupsWithMembers(ctx, "t1", nil)
	if err != nil || len(groups) != 2 {
		t.Fatalf("all groups = %d err=%v", len(groups), err)
	}
	if len(members["g1"]) != 2 {
		t.Fatalf("g1 members = %d", len(members["g1"]))
	}
	groups, _, _ = m.AllIPGroupsWithMembers(ctx, "t1", []string{"g1"})
	if len(groups) != 1 || groups[0].ID != "g1" {
		t.Fatalf("filtered groups = %+v", groups)
	}

	// Remove member + not-found.
	if err := m.RemoveIPGroupMember(ctx, "t1", "m1"); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := m.RemoveIPGroupMember(ctx, "t1", "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("remove twice err = %v", err)
	}

	// Delete group cascades members.
	if err := m.DeleteIPGroup(ctx, "t1", "g1"); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if err := m.DeleteIPGroup(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost group err = %v", err)
	}
}

// --- host groups + members + ListDeviceHostGroups ---

func TestHostGroupsAndMembers(t *testing.T) {
	ctx := bg()
	m := New()
	_ = m.CreateDevice(ctx, store.Device{ID: "d1", TenantID: "t1", Name: "srv1", DeviceType: store.DevServer, PrimaryIP: "10.0.0.1"})

	if err := m.CreateHostGroup(ctx, store.HostGroup{ID: "h1", TenantID: "t1", Name: "linux"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.CreateHostGroup(ctx, store.HostGroup{TenantID: "t1", Name: "linux"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup err = %v", err)
	}
	_ = m.CreateHostGroup(ctx, store.HostGroup{ID: "h2", TenantID: "t1", Name: "windows"})

	if g, err := m.GetHostGroup(ctx, "t1", "h1"); err != nil || g.Status != store.GroupActive {
		t.Fatalf("get: %v %+v", err, g)
	}
	if _, err := m.GetHostGroup(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get ghost err = %v", err)
	}
	if got, _ := m.ListHostGroups(ctx, "t1", 0, ""); len(got) != 2 {
		t.Fatalf("list = %d", len(got))
	}

	h1, _ := m.GetHostGroup(ctx, "t1", "h1")
	h1.Description = "u"
	if err := m.UpdateHostGroup(ctx, h1); err != nil {
		t.Fatalf("update: %v", err)
	}
	h1.Name = "windows"
	if err := m.UpdateHostGroup(ctx, h1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update dup err = %v", err)
	}
	if err := m.UpdateHostGroup(ctx, store.HostGroup{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// Members.
	if err := m.AddHostGroupMember(ctx, store.HostGroupMember{ID: "m1", TenantID: "t1", HostGroupID: "h1", DeviceID: "d1"}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := m.AddHostGroupMember(ctx, store.HostGroupMember{TenantID: "t1", HostGroupID: "h1", DeviceID: "d1"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup member err = %v", err)
	}
	if err := m.AddHostGroupMember(ctx, store.HostGroupMember{TenantID: "t1", HostGroupID: "ghost", DeviceID: "d1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("member to ghost group err = %v", err)
	}

	// ListHostGroupMembers enriches device fields.
	got, _ := m.ListHostGroupMembers(ctx, "t1", "h1")
	if len(got) != 1 || got[0].DeviceName != "srv1" || got[0].DevicePrimaryIP != "10.0.0.1" {
		t.Fatalf("member enrichment = %+v", got)
	}

	// ListDeviceHostGroups.
	if groups, _ := m.ListDeviceHostGroups(ctx, "t1", "d1"); len(groups) != 1 || groups[0].ID != "h1" {
		t.Fatalf("ListDeviceHostGroups = %+v", groups)
	}

	// Update member + not-found.
	mm := got[0]
	mm.Sequence = 3
	if err := m.UpdateHostGroupMember(ctx, mm); err != nil {
		t.Fatalf("update member: %v", err)
	}
	if err := m.UpdateHostGroupMember(ctx, store.HostGroupMember{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost member err = %v", err)
	}

	// Remove member + not-found.
	if err := m.RemoveHostGroupMember(ctx, "t1", "m1"); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := m.RemoveHostGroupMember(ctx, "t1", "m1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("remove twice err = %v", err)
	}

	if err := m.DeleteHostGroup(ctx, "t1", "h1"); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if err := m.DeleteHostGroup(ctx, "t1", "ghost"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete ghost group err = %v", err)
	}
}

// --- scan jobs + claim + active ---

func TestScanJobsClaimAndActive(t *testing.T) {
	ctx := bg()
	m := New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return base }

	if err := m.CreateScanJob(ctx, store.IPScanJob{ID: "j1", TenantID: "t1", SubnetID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Defaults.
	j1, err := m.GetScanJob(ctx, "t1", "j1")
	if err != nil || j1.Status != store.ScanPending || j1.MaxRetries != 3 || j1.TimeoutMs != 1000 || j1.Concurrency != 50 {
		t.Fatalf("defaults: %v %+v", err, j1)
	}
	if _, err := m.GetScanJob(ctx, "t2", "j1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant get err = %v", err)
	}
	_ = m.CreateScanJob(ctx, store.IPScanJob{ID: "j2", TenantID: "t1", SubnetID: "s2", Status: store.ScanCompleted})

	// List filters.
	if got := mustListJ(m.ListScanJobs(ctx, "t1", store.ScanFilter{SubnetID: "s1"})); len(got) != 1 {
		t.Fatalf("subnet filter = %d", len(got))
	}
	if got := mustListJ(m.ListScanJobs(ctx, "t1", store.ScanFilter{Status: store.ScanCompleted})); len(got) != 1 {
		t.Fatalf("status filter = %d", len(got))
	}

	// ActiveScanForSubnet: j1 pending -> active.
	if active, _ := m.ActiveScanForSubnet(ctx, "t1", "s1"); !active {
		t.Fatal("s1 should be active (pending job)")
	}
	if active, _ := m.ActiveScanForSubnet(ctx, "t1", "s2"); active {
		t.Fatal("s2 completed -> not active")
	}

	// Update + not-found.
	j1.Progress = 50
	if err := m.UpdateScanJob(ctx, j1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.UpdateScanJob(ctx, store.IPScanJob{ID: "ghost", TenantID: "t1"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update ghost err = %v", err)
	}

	// A future-retry pending job is not yet due.
	future := base.Add(time.Hour)
	_ = m.CreateScanJob(ctx, store.IPScanJob{ID: "j3", TenantID: "t1", SubnetID: "s3", Status: store.ScanPending, NextRetryAt: &future})
	// A past-retry pending job IS due; two due jobs force the dueKey sort path.
	past := base.Add(-time.Hour)
	_ = m.CreateScanJob(ctx, store.IPScanJob{ID: "j4", TenantID: "t1", SubnetID: "s4", Status: store.ScanPending, NextRetryAt: &past})

	// A limit smaller than the due set truncates.
	if one, _ := m.ClaimDueScanJobs(ctx, base, 1); len(one) != 1 {
		t.Fatalf("limited claim = %d, want 1", len(one))
	}

	// ClaimDueScanJobs claims remaining due jobs but not j3 (future) or j2 (completed).
	claimed, err := m.ClaimDueScanJobs(ctx, base, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Status != store.ScanScanning {
		t.Fatalf("claimed = %+v, want one remaining scanning", claimed)
	}
	if claimed[0].StartedAt == nil {
		t.Fatal("claimed job should carry StartedAt")
	}
	// Now j1 is scanning -> still active for s1.
	if active, _ := m.ActiveScanForSubnet(ctx, "t1", "s1"); !active {
		t.Fatal("scanning job should still be active")
	}
}

// --- dns config ---

func TestDNSConfig(t *testing.T) {
	ctx := bg()
	m := New()
	if _, err := m.GetDNSConfig(ctx, "t1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get missing err = %v", err)
	}
	if err := m.UpsertDNSConfig(ctx, store.DNSConfig{TenantID: "t1", ReverseDNSEnabled: true}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, _ := m.GetDNSConfig(ctx, "t1")
	if got.TimeoutMs != 5000 || got.ID == "" {
		t.Fatalf("insert defaults = %+v", got)
	}
	// Update path preserves ID/CreatedAt.
	firstID := got.ID
	if err := m.UpsertDNSConfig(ctx, store.DNSConfig{TenantID: "t1", TimeoutMs: 100}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := m.GetDNSConfig(ctx, "t1")
	if got2.ID != firstID || got2.TimeoutMs != 100 {
		t.Fatalf("update = %+v, want same id new timeout", got2)
	}
}

// --- stats / tenant ids / audit / close ---

func TestTenantStatsIDsAuditClose(t *testing.T) {
	ctx := bg()
	m := New()
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "n", CIDR: "10.0.0.0/24"})
	_ = m.CreateAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.1", SubnetID: "s1"})
	_ = m.CreateVlan(ctx, store.Vlan{TenantID: "t1", VlanID: 10, Name: "v"})
	_ = m.CreateDevice(ctx, store.Device{TenantID: "t1", Name: "d", DeviceType: store.DevRouter})
	_ = m.CreateLocation(ctx, store.Location{TenantID: "t1", Name: "l"})

	st, err := m.TenantStats(ctx, "t1")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.TotalSubnets != 1 || st.UsedAddresses != 1 || st.TotalVlans != 1 || st.TotalDevices != 1 || st.TotalLocations != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if st.TotalAddresses != 256 || st.AvailableAddresses != 255 {
		t.Fatalf("stats caps = %+v", st)
	}
	if st.DevicesByType[store.DevRouter] != 1 {
		t.Fatalf("devices by type = %+v", st.DevicesByType)
	}
	if st.OverallUtilization <= 0 {
		t.Fatalf("utilization = %v", st.OverallUtilization)
	}

	ids, _ := m.TenantIDs(ctx)
	if len(ids) != 1 || ids[0] != "t1" {
		t.Fatalf("tenant ids = %v", ids)
	}

	if err := m.AppendAudit(ctx, store.AuditRow{TenantID: "t1", Action: "create"}); err != nil {
		t.Fatalf("audit: %v", err)
	}

	m.Close() // no-op, must not panic
}

// --- list helpers (one per typed List signature); they panic on error, which
// fails the test with a clear message while keeping call sites terse. ---

func mustList(out []store.Subnet, err error) []store.Subnet {
	if err != nil {
		panic(err)
	}
	return out
}
func mustListA(out []store.IPAddress, err error) []store.IPAddress {
	if err != nil {
		panic(err)
	}
	return out
}
func mustListD(out []store.Device, err error) []store.Device {
	if err != nil {
		panic(err)
	}
	return out
}
func mustListV(out []store.Vlan, err error) []store.Vlan {
	if err != nil {
		panic(err)
	}
	return out
}
func mustListL(out []store.Location, err error) []store.Location {
	if err != nil {
		panic(err)
	}
	return out
}
func mustListJ(out []store.IPScanJob, err error) []store.IPScanJob {
	if err != nil {
		panic(err)
	}
	return out
}
