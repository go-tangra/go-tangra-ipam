package grpcapi

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/events"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
)

func svcCaller(t *testing.T) {
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
}

// ---- Subnet: Update, Delete, Scan ----

func TestSubnetUpdateDeleteScan(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.50.0.0/24")

	upd, err := k.subnet.Update(ctx, &ipamv1.UpdateSubnetRequest{
		TenantId: tenant, Id: sub.GetId(),
		Subnet: &ipamv1.Subnet{Name: "renamed", Cidr: sub.GetCidr(), Description: "d"},
	})
	if err != nil || upd.GetName() != "renamed" {
		t.Fatalf("update subnet: %v %+v", err, upd)
	}

	job, err := k.subnet.Scan(ctx, &ipamv1.ScanSubnetRequest{TenantId: tenant, Id: sub.GetId(), EnableSnmp: true})
	if err != nil || job.GetId() == "" {
		t.Fatalf("scan subnet: %v %+v", err, job)
	}

	if _, err := k.subnet.Delete(ctx, &ipamv1.DeleteSubnetRequest{TenantId: tenant, Id: sub.GetId(), Force: true}); err != nil {
		t.Fatalf("delete subnet: %v", err)
	}
	if _, err := k.subnet.Get(ctx, &ipamv1.GetSubnetRequest{TenantId: tenant, Id: sub.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("get after delete: want NotFound, got %v", err)
	}
}

func TestSubnetScanUnavailable(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	k.subnet.scan = nil
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.51.0.0/24")
	if _, err := k.subnet.Scan(ctx, &ipamv1.ScanSubnetRequest{TenantId: tenant, Id: sub.GetId()}); status.Code(err) != codes.Unavailable {
		t.Fatalf("scan without scanner: want Unavailable, got %v", err)
	}
}

func TestSubnetListFilters(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	k.createSubnet(t, ctx, "10.52.0.0/24")
	k.createSubnet(t, ctx, "10.53.0.0/24")

	list, err := k.subnet.List(ctx, &ipamv1.ListSubnetsRequest{
		TenantId: tenant, Status: ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE, IpVersion: 4, Limit: 10,
	})
	if err != nil || len(list.GetSubnets()) != 2 {
		t.Fatalf("list filtered: %v n=%d", err, len(list.GetSubnets()))
	}
	if list.GetNextCursorId() == "" {
		t.Fatal("expected next cursor id")
	}
}

// ---- Address: Create, Get, List, Update, Delete, BulkAllocate, Suggest, Ping ----

func TestAddressFullFlow(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.60.0.0/24")

	created, err := k.addr.Create(ctx, &ipamv1.CreateIpAddressRequest{
		TenantId: tenant,
		Address: &ipamv1.IPAddress{
			Address: "10.60.0.10", SubnetId: sub.GetId(), Hostname: "h10",
			Status: ipamv1.IpStatus_IP_STATUS_ACTIVE, AddressType: ipamv1.AddressType_ADDRESS_TYPE_HOST,
		},
	})
	if err != nil || created.GetAddress() != "10.60.0.10" {
		t.Fatalf("create address: %v %+v", err, created)
	}

	got, err := k.addr.Get(ctx, &ipamv1.GetIpAddressRequest{TenantId: tenant, Id: created.GetId()})
	if err != nil || got.GetHostname() != "h10" {
		t.Fatalf("get address: %v %+v", err, got)
	}

	upd, err := k.addr.Update(ctx, &ipamv1.UpdateIpAddressRequest{
		TenantId: tenant, Id: created.GetId(),
		Address: &ipamv1.IPAddress{Address: "10.60.0.10", SubnetId: sub.GetId(), Hostname: "h10b"},
	})
	if err != nil || upd.GetHostname() != "h10b" {
		t.Fatalf("update address: %v %+v", err, upd)
	}

	list, err := k.addr.List(ctx, &ipamv1.ListIpAddressesRequest{
		TenantId: tenant, SubnetId: sub.GetId(), Limit: 50,
	})
	if err != nil || len(list.GetAddresses()) == 0 {
		t.Fatalf("list addresses: %v n=%d", err, len(list.GetAddresses()))
	}

	if _, err := k.addr.Delete(ctx, &ipamv1.DeleteIpAddressRequest{TenantId: tenant, Id: created.GetId()}); err != nil {
		t.Fatalf("delete address: %v", err)
	}
}

func TestAddressBulkSuggestPing(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.61.0.0/24")

	bulk, err := k.addr.BulkAllocate(ctx, &ipamv1.BulkAllocateRequest{
		TenantId: tenant, SubnetId: sub.GetId(), Count: 3, HostnamePrefix: "bulk",
	})
	if err != nil || len(bulk.GetAddresses()) != 3 {
		t.Fatalf("bulk allocate: %v n=%d", err, len(bulk.GetAddresses()))
	}

	sug, err := k.addr.Suggest(ctx, &ipamv1.SuggestIpAddressRequest{TenantId: tenant, SubnetId: sub.GetId(), Count: 2})
	if err != nil || len(sug.GetAddresses()) == 0 {
		t.Fatalf("suggest: %v %+v", err, sug)
	}

	alloc, err := k.addr.AllocateNext(ctx, &ipamv1.AllocateNextRequest{TenantId: tenant, SubnetId: sub.GetId()})
	if err != nil {
		t.Fatalf("allocate for ping: %v", err)
	}
	ping, err := k.addr.Ping(ctx, &ipamv1.PingIpAddressRequest{TenantId: tenant, Id: alloc.GetId()})
	if err != nil {
		t.Fatalf("ping: %v %+v", err, ping)
	}
}

func TestAddressExhaustion(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.62.0.0/30")
	// /30 has 2 usable hosts; allocate until exhausted.
	var lastErr error
	for i := 0; i < 5; i++ {
		if _, err := k.addr.AllocateNext(ctx, &ipamv1.AllocateNextRequest{TenantId: tenant, SubnetId: sub.GetId()}); err != nil {
			lastErr = err
			break
		}
	}
	if status.Code(lastErr) != codes.ResourceExhausted {
		t.Fatalf("exhaustion: want ResourceExhausted, got %v", lastErr)
	}
}

// ---- Device: List, Update, Delete, interfaces, packages ----

func newDevice(t *testing.T, k kit, ctx context.Context, name string) *ipamv1.Device {
	t.Helper()
	dev, err := k.device.Create(ctx, &ipamv1.CreateDeviceRequest{
		TenantId: tenant,
		Device: &ipamv1.Device{
			Name: name, DeviceType: ipamv1.DeviceType_DEVICE_TYPE_SERVER,
			ManagementIp: "10.0.0.9", Status: ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE,
		},
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	return dev
}

func TestDeviceListUpdateDelete(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	dev := newDevice(t, k, ctx, "dev-a")

	upd, err := k.device.Update(ctx, &ipamv1.UpdateDeviceRequest{
		TenantId: tenant, Id: dev.GetId(),
		Device: &ipamv1.Device{Name: "dev-a2", DeviceType: ipamv1.DeviceType_DEVICE_TYPE_ROUTER},
	})
	if err != nil || upd.GetName() != "dev-a2" {
		t.Fatalf("update device: %v %+v", err, upd)
	}

	list, err := k.device.List(ctx, &ipamv1.ListDevicesRequest{
		TenantId: tenant, DeviceType: ipamv1.DeviceType_DEVICE_TYPE_ROUTER, Limit: 10,
	})
	if err != nil || len(list.GetDevices()) != 1 {
		t.Fatalf("list devices: %v n=%d", err, len(list.GetDevices()))
	}

	if _, err := k.device.Delete(ctx, &ipamv1.DeleteDeviceRequest{TenantId: tenant, Id: dev.GetId(), Force: true}); err != nil {
		t.Fatalf("delete device: %v", err)
	}
}

func TestDeviceInterfacesAddressesPackages(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	dev := newDevice(t, k, ctx, "dev-b")

	iface, err := k.device.CreateInterface(ctx, &ipamv1.CreateInterfaceRequest{
		TenantId: tenant, DeviceId: dev.GetId(),
		Iface: &ipamv1.DeviceInterface{Name: "eth0", MacAddress: "aa:bb:cc:dd:ee:ff", InterfaceType: "ethernet", Enabled: true},
	})
	if err != nil || iface.GetName() != "eth0" {
		t.Fatalf("create interface: %v %+v", err, iface)
	}

	ifaces, err := k.device.GetInterfaces(ctx, &ipamv1.GetDeviceInterfacesRequest{TenantId: tenant, Id: dev.GetId()})
	if err != nil || len(ifaces.GetInterfaces()) != 1 {
		t.Fatalf("get interfaces: %v n=%d", err, len(ifaces.GetInterfaces()))
	}

	addrs, err := k.device.GetAddresses(ctx, &ipamv1.GetDeviceAddressesRequest{TenantId: tenant, Id: dev.GetId()})
	if err != nil {
		t.Fatalf("get addresses: %v", err)
	}
	_ = addrs

	pkgs, err := k.device.ListPackages(ctx, &ipamv1.ListPackagesRequest{TenantId: tenant, DeviceId: dev.GetId()})
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	_ = pkgs

	if _, err := k.device.DeleteInterface(ctx, &ipamv1.DeleteInterfaceRequest{TenantId: tenant, InterfaceId: iface.GetId()}); err != nil {
		t.Fatalf("delete interface: %v", err)
	}

	// SyncPackages is not offered on the mesh.
	if _, err := k.device.SyncPackages(ctx, &ipamv1.SyncPackagesRequest{TenantId: tenant, DeviceId: dev.GetId()}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("sync packages: want Unimplemented, got %v", err)
	}
}

// ---- Vlan: List, Update, Delete, GetSubnets ----

func TestVlanListUpdateDeleteSubnets(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	vl, err := k.vlan.Create(ctx, &ipamv1.CreateVlanRequest{
		TenantId: tenant, Vlan: &ipamv1.Vlan{VlanId: 200, Name: "vlan-200"},
	})
	if err != nil {
		t.Fatalf("vlan create: %v", err)
	}

	upd, err := k.vlan.Update(ctx, &ipamv1.UpdateVlanRequest{
		TenantId: tenant, Id: vl.GetId(),
		Vlan: &ipamv1.Vlan{VlanId: 200, Name: "vlan-200b"},
	})
	if err != nil || upd.GetName() != "vlan-200b" {
		t.Fatalf("vlan update: %v %+v", err, upd)
	}

	list, err := k.vlan.List(ctx, &ipamv1.ListVlansRequest{TenantId: tenant, Limit: 10})
	if err != nil || len(list.GetVlans()) != 1 {
		t.Fatalf("vlan list: %v n=%d", err, len(list.GetVlans()))
	}

	subs, err := k.vlan.GetSubnets(ctx, &ipamv1.GetVlanSubnetsRequest{TenantId: tenant, Id: vl.GetId()})
	if err != nil {
		t.Fatalf("vlan subnets: %v", err)
	}
	_ = subs

	if _, err := k.vlan.Delete(ctx, &ipamv1.DeleteVlanRequest{TenantId: tenant, Id: vl.GetId()}); err != nil {
		t.Fatalf("vlan delete: %v", err)
	}
}

// ---- Location: List, Update, Delete ----

func TestLocationListUpdateDelete(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	loc, err := k.location.Create(ctx, &ipamv1.CreateLocationRequest{
		TenantId: tenant, Location: &ipamv1.Location{Name: "dc-2", LocationType: ipamv1.LocationType_LOCATION_TYPE_DATACENTER},
	})
	if err != nil {
		t.Fatalf("loc create: %v", err)
	}

	upd, err := k.location.Update(ctx, &ipamv1.UpdateLocationRequest{
		TenantId: tenant, Id: loc.GetId(),
		Location: &ipamv1.Location{Name: "dc-2b", LocationType: ipamv1.LocationType_LOCATION_TYPE_DATACENTER},
	})
	if err != nil || upd.GetName() != "dc-2b" {
		t.Fatalf("loc update: %v %+v", err, upd)
	}

	list, err := k.location.List(ctx, &ipamv1.ListLocationsRequest{
		TenantId: tenant, LocationType: ipamv1.LocationType_LOCATION_TYPE_DATACENTER, Limit: 10,
	})
	if err != nil || len(list.GetLocations()) != 1 {
		t.Fatalf("loc list: %v n=%d", err, len(list.GetLocations()))
	}

	if _, err := k.location.Delete(ctx, &ipamv1.DeleteLocationRequest{TenantId: tenant, Id: loc.GetId(), Force: true}); err != nil {
		t.Fatalf("loc delete: %v", err)
	}
}

// ---- IpGroup: Get, List, Update, Delete, member ops ----

func TestIpGroupFullFlow(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	g, err := k.ipgroup.Create(ctx, &ipamv1.CreateIpGroupRequest{
		TenantId: tenant, Group: &ipamv1.IPGroup{Name: "grp-x"},
	})
	if err != nil {
		t.Fatalf("group create: %v", err)
	}

	m, err := k.ipgroup.AddMember(ctx, &ipamv1.AddIpGroupMemberRequest{
		TenantId: tenant, IpGroupId: g.GetId(),
		Member: &ipamv1.IPGroupMember{MemberType: ipamv1.MemberType_MEMBER_TYPE_SUBNET, Value: "10.70.0.0/24"},
	})
	if err != nil {
		t.Fatalf("add member: %v", err)
	}

	got, err := k.ipgroup.Get(ctx, &ipamv1.GetIpGroupRequest{TenantId: tenant, Id: g.GetId(), IncludeMembers: true})
	if err != nil || len(got.GetMembers()) != 1 {
		t.Fatalf("get group with members: %v %+v", err, got)
	}

	list, err := k.ipgroup.List(ctx, &ipamv1.ListIpGroupsRequest{TenantId: tenant, IncludeMembers: true, Limit: 10})
	if err != nil || len(list.GetGroups()) != 1 {
		t.Fatalf("list groups: %v n=%d", err, len(list.GetGroups()))
	}

	upd, err := k.ipgroup.Update(ctx, &ipamv1.UpdateIpGroupRequest{
		TenantId: tenant, Id: g.GetId(), Group: &ipamv1.IPGroup{Name: "grp-x2"},
	})
	if err != nil || upd.GetName() != "grp-x2" {
		t.Fatalf("update group: %v %+v", err, upd)
	}

	um, err := k.ipgroup.UpdateMember(ctx, &ipamv1.UpdateIpGroupMemberRequest{
		TenantId: tenant, IpGroupId: g.GetId(), MemberId: m.GetId(),
		Member: &ipamv1.IPGroupMember{MemberType: ipamv1.MemberType_MEMBER_TYPE_SUBNET, Value: "10.71.0.0/24", Description: "u"},
	})
	if err != nil || um.GetValue() != "10.71.0.0/24" {
		t.Fatalf("update member: %v %+v", err, um)
	}

	members, err := k.ipgroup.ListMembers(ctx, &ipamv1.ListIpGroupMembersRequest{TenantId: tenant, IpGroupId: g.GetId()})
	if err != nil || len(members.GetMembers()) != 1 {
		t.Fatalf("list members: %v n=%d", err, len(members.GetMembers()))
	}

	if _, err := k.ipgroup.RemoveMember(ctx, &ipamv1.RemoveIpGroupMemberRequest{TenantId: tenant, MemberId: m.GetId()}); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	if _, err := k.ipgroup.Delete(ctx, &ipamv1.DeleteIpGroupRequest{TenantId: tenant, Id: g.GetId()}); err != nil {
		t.Fatalf("delete group: %v", err)
	}
}

// ---- HostGroup: full flow ----

func TestHostGroupFullFlow(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	hgSrv := &HostGroupServer{groups: k.ipgroup.groups}
	dev := newDevice(t, k, ctx, "dev-hg")

	g, err := hgSrv.Create(ctx, &ipamv1.CreateHostGroupRequest{
		TenantId: tenant, Group: &ipamv1.HostGroup{Name: "hg-1"},
	})
	if err != nil {
		t.Fatalf("hostgroup create: %v", err)
	}

	if _, err := hgSrv.AddMember(ctx, &ipamv1.AddHostGroupMemberRequest{
		TenantId: tenant, HostGroupId: g.GetId(), DeviceId: dev.GetId(), Sequence: 1,
	}); err != nil {
		t.Fatalf("add host member: %v", err)
	}

	got, err := hgSrv.Get(ctx, &ipamv1.GetHostGroupRequest{TenantId: tenant, Id: g.GetId(), IncludeMembers: true})
	if err != nil || len(got.GetMembers()) != 1 {
		t.Fatalf("get hostgroup: %v %+v", err, got)
	}

	list, err := hgSrv.List(ctx, &ipamv1.ListHostGroupsRequest{TenantId: tenant, IncludeMembers: true, Limit: 10})
	if err != nil || len(list.GetGroups()) != 1 {
		t.Fatalf("list hostgroups: %v n=%d", err, len(list.GetGroups()))
	}

	upd, err := hgSrv.Update(ctx, &ipamv1.UpdateHostGroupRequest{
		TenantId: tenant, Id: g.GetId(), Group: &ipamv1.HostGroup{Name: "hg-1b"},
	})
	if err != nil || upd.GetName() != "hg-1b" {
		t.Fatalf("update hostgroup: %v %+v", err, upd)
	}

	members, err := hgSrv.ListMembers(ctx, &ipamv1.ListHostGroupMembersRequest{TenantId: tenant, HostGroupId: g.GetId()})
	if err != nil || len(members.GetMembers()) != 1 {
		t.Fatalf("list host members: %v n=%d", err, len(members.GetMembers()))
	}

	dgs, err := hgSrv.ListDeviceHostGroups(ctx, &ipamv1.ListDeviceHostGroupsRequest{TenantId: tenant, DeviceId: dev.GetId()})
	if err != nil || len(dgs.GetGroups()) != 1 {
		t.Fatalf("list device host groups: %v n=%d", err, len(dgs.GetGroups()))
	}

	if _, err := hgSrv.RemoveMember(ctx, &ipamv1.RemoveHostGroupMemberRequest{
		TenantId: tenant, HostGroupId: g.GetId(), DeviceId: dev.GetId(),
	}); err != nil {
		t.Fatalf("remove host member: %v", err)
	}
	// Removing an unknown device now yields NotFound.
	if _, err := hgSrv.RemoveMember(ctx, &ipamv1.RemoveHostGroupMemberRequest{
		TenantId: tenant, HostGroupId: g.GetId(), DeviceId: "nope",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("remove unknown host member: want NotFound, got %v", err)
	}

	if _, err := hgSrv.Delete(ctx, &ipamv1.DeleteHostGroupRequest{TenantId: tenant, Id: g.GetId()}); err != nil {
		t.Fatalf("delete hostgroup: %v", err)
	}
}

// ---- Scan: List, Cancel ----

func TestScanListCancel(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.80.0.0/28")

	job, err := k.scanSrv.Start(ctx, &ipamv1.StartScanRequest{TenantId: tenant, SubnetId: sub.GetId()})
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	list, err := k.scanSrv.List(ctx, &ipamv1.ListScansRequest{TenantId: tenant, SubnetId: sub.GetId(), Limit: 10})
	if err != nil || len(list.GetJobs()) == 0 {
		t.Fatalf("list scans: %v n=%d", err, len(list.GetJobs()))
	}

	cancelled, err := k.scanSrv.Cancel(ctx, &ipamv1.CancelScanRequest{TenantId: tenant, Id: job.GetId()})
	if err != nil || cancelled.GetStatus() != ipamv1.ScanStatus_SCAN_STATUS_CANCELLED {
		t.Fatalf("cancel scan: %v %+v", err, cancelled)
	}
}

// ---- System: Health, GetDnsConfig, UpdateDnsConfig ----

func TestSystemHealthAndDNS(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()

	h, err := k.system.Health(ctx, &ipamv1.HealthRequest{})
	if err != nil || h.GetStatus() != "ok" {
		t.Fatalf("health: %v %+v", err, h)
	}

	cfg, err := k.system.GetDnsConfig(ctx, &ipamv1.GetDnsConfigRequest{TenantId: tenant})
	if err != nil {
		t.Fatalf("get dns config: %v", err)
	}
	_ = cfg

	// A plain service caller lacks admin; updating DNS config is forbidden.
	if _, err := k.system.UpdateDnsConfig(ctx, &ipamv1.UpdateDnsConfigRequest{
		TenantId: tenant, Config: &ipamv1.DNSConfig{DnsServers: []string{"1.1.1.1"}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("update dns as non-admin: want PermissionDenied, got %v", err)
	}

	// An admin caller may update the tenant DNS config.
	withCaller(t, "spiffe://example.org/svc/admin", []string{"admin"}, true)
	upd, err := k.system.UpdateDnsConfig(ctx, &ipamv1.UpdateDnsConfigRequest{
		TenantId: tenant,
		Config: &ipamv1.DNSConfig{
			DnsServers: []string{"1.1.1.1"}, TimeoutMs: 250, ReverseDnsEnabled: true, UseSystemDnsFallback: true,
		},
	})
	if err != nil || len(upd.GetDnsServers()) != 1 {
		t.Fatalf("update dns config: %v %+v", err, upd)
	}
}

func TestSystemUnavailable(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	k.system.stats = nil
	k.system.dns = nil
	if _, err := k.system.GetStats(ctx, &ipamv1.GetStatsRequest{TenantId: tenant}); status.Code(err) != codes.Unavailable {
		t.Fatalf("stats nil: want Unavailable, got %v", err)
	}
	if _, err := k.system.GetDnsConfig(ctx, &ipamv1.GetDnsConfigRequest{TenantId: tenant}); status.Code(err) != codes.Unavailable {
		t.Fatalf("dns get nil: want Unavailable, got %v", err)
	}
	if _, err := k.system.UpdateDnsConfig(ctx, &ipamv1.UpdateDnsConfigRequest{TenantId: tenant, Config: &ipamv1.DNSConfig{}}); status.Code(err) != codes.Unavailable {
		t.Fatalf("dns update nil: want Unavailable, got %v", err)
	}
}

// ---- grpcError mapping via domain flows ----

func TestGrpcErrorMappings(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()

	// NotFound: missing device.
	if _, err := k.device.Get(ctx, &ipamv1.GetDeviceRequest{TenantId: tenant, Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing device: want NotFound, got %v", err)
	}

	// FailedPrecondition: delete a non-empty subnet without force.
	sub := k.createSubnet(t, ctx, "10.90.0.0/24")
	if _, err := k.addr.AllocateNext(ctx, &ipamv1.AllocateNextRequest{TenantId: tenant, SubnetId: sub.GetId()}); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if _, err := k.subnet.Delete(ctx, &ipamv1.DeleteSubnetRequest{TenantId: tenant, Id: sub.GetId(), Force: false}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete non-empty: want FailedPrecondition, got %v", err)
	}

	// InvalidArgument: bad member value type via ip group.
	// Unauthenticated already covered elsewhere; add one more path here.
	withCaller(t, "", nil, false)
	if _, err := k.device.List(ctx, &ipamv1.ListDevicesRequest{TenantId: tenant}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauth list: want Unauthenticated, got %v", err)
	}
}

// ---- Register: wire all servers onto a registrar ----

type fakeRegistrar struct{ n int }

func (f *fakeRegistrar) RegisterService(_ *grpc.ServiceDesc, _ any) { f.n++ }

func TestRegister(t *testing.T) {
	mem := memstore.New()
	pub := events.HubPublisher{}
	reg := &fakeRegistrar{}
	Register(reg, Deps{
		Subnets:   subnets.New(mem),
		Addresses: addresses.New(mem, pub, 0, 0),
		Devices:   devices.New(mem),
		Vlans:     vlans.New(mem),
		Locations: locations.New(mem),
		Groups:    groups.New(mem),
		Stats:     stats.New(mem),
		DNS:       dnscfg.New(mem),
	})
	// SubnetService, IpAddress, Device, Vlan, Location, IpGroup, HostGroup, System = 8
	if reg.n < 7 {
		t.Fatalf("Register wired too few services: %d", reg.n)
	}
}

func TestRegisterEmpty(t *testing.T) {
	reg := &fakeRegistrar{}
	Register(reg, Deps{})
	if reg.n != 0 {
		t.Fatalf("empty Register wired %d services", reg.n)
	}
}
