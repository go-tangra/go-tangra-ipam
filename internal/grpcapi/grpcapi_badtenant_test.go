package grpcapi

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
)

// TestBadTenantAcrossRPCs drives every RPC with a malformed tenant id so the
// caller() guard rejects it with InvalidArgument. This exercises the early
// caller-error return branch in each server method uniformly.
func TestBadTenantAcrossRPCs(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	hg := &HostGroupServer{groups: k.ipgroup.groups}
	ctx := context.Background()
	const bad = "not-a-uuid"

	check := func(name string, err error) {
		t.Helper()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	// Subnet
	_, e := k.subnet.Create(ctx, &ipamv1.CreateSubnetRequest{TenantId: bad, Subnet: &ipamv1.Subnet{}})
	check("subnet.Create", e)
	_, e = k.subnet.Get(ctx, &ipamv1.GetSubnetRequest{TenantId: bad})
	check("subnet.Get", e)
	_, e = k.subnet.List(ctx, &ipamv1.ListSubnetsRequest{TenantId: bad})
	check("subnet.List", e)
	_, e = k.subnet.Update(ctx, &ipamv1.UpdateSubnetRequest{TenantId: bad, Subnet: &ipamv1.Subnet{}})
	check("subnet.Update", e)
	_, e = k.subnet.Delete(ctx, &ipamv1.DeleteSubnetRequest{TenantId: bad})
	check("subnet.Delete", e)
	_, e = k.subnet.GetTree(ctx, &ipamv1.GetSubnetTreeRequest{TenantId: bad})
	check("subnet.GetTree", e)
	_, e = k.subnet.GetStats(ctx, &ipamv1.GetSubnetStatsRequest{TenantId: bad})
	check("subnet.GetStats", e)
	_, e = k.subnet.Scan(ctx, &ipamv1.ScanSubnetRequest{TenantId: bad})
	check("subnet.Scan", e)

	// Address
	_, e = k.addr.Create(ctx, &ipamv1.CreateIpAddressRequest{TenantId: bad, Address: &ipamv1.IPAddress{}})
	check("addr.Create", e)
	_, e = k.addr.Get(ctx, &ipamv1.GetIpAddressRequest{TenantId: bad})
	check("addr.Get", e)
	_, e = k.addr.List(ctx, &ipamv1.ListIpAddressesRequest{TenantId: bad})
	check("addr.List", e)
	_, e = k.addr.Update(ctx, &ipamv1.UpdateIpAddressRequest{TenantId: bad, Address: &ipamv1.IPAddress{}})
	check("addr.Update", e)
	_, e = k.addr.Delete(ctx, &ipamv1.DeleteIpAddressRequest{TenantId: bad})
	check("addr.Delete", e)
	_, e = k.addr.AllocateNext(ctx, &ipamv1.AllocateNextRequest{TenantId: bad})
	check("addr.AllocateNext", e)
	_, e = k.addr.BulkAllocate(ctx, &ipamv1.BulkAllocateRequest{TenantId: bad})
	check("addr.BulkAllocate", e)
	_, e = k.addr.Find(ctx, &ipamv1.FindIpAddressRequest{TenantId: bad})
	check("addr.Find", e)
	_, e = k.addr.Suggest(ctx, &ipamv1.SuggestIpAddressRequest{TenantId: bad})
	check("addr.Suggest", e)
	_, e = k.addr.Ping(ctx, &ipamv1.PingIpAddressRequest{TenantId: bad})
	check("addr.Ping", e)

	// Device (power/kvm paths go through caller first via bmcContext)
	_, e = k.device.Create(ctx, &ipamv1.CreateDeviceRequest{TenantId: bad, Device: &ipamv1.Device{}})
	check("device.Create", e)
	_, e = k.device.Get(ctx, &ipamv1.GetDeviceRequest{TenantId: bad})
	check("device.Get", e)
	_, e = k.device.List(ctx, &ipamv1.ListDevicesRequest{TenantId: bad})
	check("device.List", e)
	_, e = k.device.Update(ctx, &ipamv1.UpdateDeviceRequest{TenantId: bad, Device: &ipamv1.Device{}})
	check("device.Update", e)
	_, e = k.device.Delete(ctx, &ipamv1.DeleteDeviceRequest{TenantId: bad})
	check("device.Delete", e)
	_, e = k.device.GetAddresses(ctx, &ipamv1.GetDeviceAddressesRequest{TenantId: bad})
	check("device.GetAddresses", e)
	_, e = k.device.GetInterfaces(ctx, &ipamv1.GetDeviceInterfacesRequest{TenantId: bad})
	check("device.GetInterfaces", e)
	_, e = k.device.CreateInterface(ctx, &ipamv1.CreateInterfaceRequest{TenantId: bad, Iface: &ipamv1.DeviceInterface{}})
	check("device.CreateInterface", e)
	_, e = k.device.DeleteInterface(ctx, &ipamv1.DeleteInterfaceRequest{TenantId: bad})
	check("device.DeleteInterface", e)
	_, e = k.device.ListPackages(ctx, &ipamv1.ListPackagesRequest{TenantId: bad})
	check("device.ListPackages", e)

	// Vlan
	_, e = k.vlan.Create(ctx, &ipamv1.CreateVlanRequest{TenantId: bad, Vlan: &ipamv1.Vlan{}})
	check("vlan.Create", e)
	_, e = k.vlan.Get(ctx, &ipamv1.GetVlanRequest{TenantId: bad})
	check("vlan.Get", e)
	_, e = k.vlan.List(ctx, &ipamv1.ListVlansRequest{TenantId: bad})
	check("vlan.List", e)
	_, e = k.vlan.Update(ctx, &ipamv1.UpdateVlanRequest{TenantId: bad, Vlan: &ipamv1.Vlan{}})
	check("vlan.Update", e)
	_, e = k.vlan.Delete(ctx, &ipamv1.DeleteVlanRequest{TenantId: bad})
	check("vlan.Delete", e)
	_, e = k.vlan.GetSubnets(ctx, &ipamv1.GetVlanSubnetsRequest{TenantId: bad})
	check("vlan.GetSubnets", e)

	// Location
	_, e = k.location.Create(ctx, &ipamv1.CreateLocationRequest{TenantId: bad, Location: &ipamv1.Location{}})
	check("location.Create", e)
	_, e = k.location.Get(ctx, &ipamv1.GetLocationRequest{TenantId: bad})
	check("location.Get", e)
	_, e = k.location.List(ctx, &ipamv1.ListLocationsRequest{TenantId: bad})
	check("location.List", e)
	_, e = k.location.Update(ctx, &ipamv1.UpdateLocationRequest{TenantId: bad, Location: &ipamv1.Location{}})
	check("location.Update", e)
	_, e = k.location.Delete(ctx, &ipamv1.DeleteLocationRequest{TenantId: bad})
	check("location.Delete", e)
	_, e = k.location.GetTree(ctx, &ipamv1.GetLocationTreeRequest{TenantId: bad})
	check("location.GetTree", e)

	// IpGroup
	_, e = k.ipgroup.Create(ctx, &ipamv1.CreateIpGroupRequest{TenantId: bad, Group: &ipamv1.IPGroup{}})
	check("ipgroup.Create", e)
	_, e = k.ipgroup.Get(ctx, &ipamv1.GetIpGroupRequest{TenantId: bad})
	check("ipgroup.Get", e)
	_, e = k.ipgroup.List(ctx, &ipamv1.ListIpGroupsRequest{TenantId: bad})
	check("ipgroup.List", e)
	_, e = k.ipgroup.Update(ctx, &ipamv1.UpdateIpGroupRequest{TenantId: bad, Group: &ipamv1.IPGroup{}})
	check("ipgroup.Update", e)
	_, e = k.ipgroup.Delete(ctx, &ipamv1.DeleteIpGroupRequest{TenantId: bad})
	check("ipgroup.Delete", e)
	_, e = k.ipgroup.AddMember(ctx, &ipamv1.AddIpGroupMemberRequest{TenantId: bad, Member: &ipamv1.IPGroupMember{}})
	check("ipgroup.AddMember", e)
	_, e = k.ipgroup.UpdateMember(ctx, &ipamv1.UpdateIpGroupMemberRequest{TenantId: bad, Member: &ipamv1.IPGroupMember{}})
	check("ipgroup.UpdateMember", e)
	_, e = k.ipgroup.RemoveMember(ctx, &ipamv1.RemoveIpGroupMemberRequest{TenantId: bad})
	check("ipgroup.RemoveMember", e)
	_, e = k.ipgroup.ListMembers(ctx, &ipamv1.ListIpGroupMembersRequest{TenantId: bad})
	check("ipgroup.ListMembers", e)
	_, e = k.ipgroup.CheckIpInGroup(ctx, &ipamv1.CheckIpInGroupRequest{TenantId: bad})
	check("ipgroup.CheckIpInGroup", e)

	// HostGroup
	_, e = hg.Create(ctx, &ipamv1.CreateHostGroupRequest{TenantId: bad, Group: &ipamv1.HostGroup{}})
	check("hostgroup.Create", e)
	_, e = hg.Get(ctx, &ipamv1.GetHostGroupRequest{TenantId: bad})
	check("hostgroup.Get", e)
	_, e = hg.List(ctx, &ipamv1.ListHostGroupsRequest{TenantId: bad})
	check("hostgroup.List", e)
	_, e = hg.Update(ctx, &ipamv1.UpdateHostGroupRequest{TenantId: bad, Group: &ipamv1.HostGroup{}})
	check("hostgroup.Update", e)
	_, e = hg.Delete(ctx, &ipamv1.DeleteHostGroupRequest{TenantId: bad})
	check("hostgroup.Delete", e)
	_, e = hg.AddMember(ctx, &ipamv1.AddHostGroupMemberRequest{TenantId: bad})
	check("hostgroup.AddMember", e)
	_, e = hg.RemoveMember(ctx, &ipamv1.RemoveHostGroupMemberRequest{TenantId: bad})
	check("hostgroup.RemoveMember", e)
	_, e = hg.ListMembers(ctx, &ipamv1.ListHostGroupMembersRequest{TenantId: bad})
	check("hostgroup.ListMembers", e)
	_, e = hg.ListDeviceHostGroups(ctx, &ipamv1.ListDeviceHostGroupsRequest{TenantId: bad})
	check("hostgroup.ListDeviceHostGroups", e)

	// Scan
	_, e = k.scanSrv.Start(ctx, &ipamv1.StartScanRequest{TenantId: bad})
	check("scan.Start", e)
	_, e = k.scanSrv.Get(ctx, &ipamv1.GetScanRequest{TenantId: bad})
	check("scan.Get", e)
	_, e = k.scanSrv.List(ctx, &ipamv1.ListScansRequest{TenantId: bad})
	check("scan.List", e)
	_, e = k.scanSrv.Cancel(ctx, &ipamv1.CancelScanRequest{TenantId: bad})
	check("scan.Cancel", e)

	// System
	_, e = k.system.GetStats(ctx, &ipamv1.GetStatsRequest{TenantId: bad})
	check("system.GetStats", e)
	_, e = k.system.GetDnsConfig(ctx, &ipamv1.GetDnsConfigRequest{TenantId: bad})
	check("system.GetDnsConfig", e)
	_, e = k.system.UpdateDnsConfig(ctx, &ipamv1.UpdateDnsConfigRequest{TenantId: bad, Config: &ipamv1.DNSConfig{}})
	check("system.UpdateDnsConfig", e)
}

// TestPowerBadTenant covers the caller-error path inside bmcContext for the
// privileged power/kvm RPCs (they resolve the caller before the admin check).
func TestPowerBadTenant(t *testing.T) {
	k := newKit(t)
	svcCaller(t)
	ctx := context.Background()
	const bad = "not-a-uuid"
	if _, err := k.device.PowerStatus(ctx, &ipamv1.PowerStatusRequest{TenantId: bad}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PowerStatus bad tenant: %v", err)
	}
	if _, err := k.device.Power(ctx, &ipamv1.PowerRequest{TenantId: bad, Action: ipamv1.PowerAction_POWER_ACTION_ON}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Power bad tenant: %v", err)
	}
	if _, err := k.device.StartKvmSession(ctx, &ipamv1.StartKvmSessionRequest{TenantId: bad}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StartKvmSession bad tenant: %v", err)
	}
}
