package grpcapi

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/events"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/ipmi"
	"github.com/go-freya/freya/services/ipam/internal/kvm"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

const tenant = "11111111-1111-1111-1111-111111111111"

type kit struct {
	subnet   *SubnetServer
	addr     *IpAddressServer
	device   *DeviceServer
	vlan     *VlanServer
	location *LocationServer
	ipgroup  *IpGroupServer
	scanSrv  *IpScanServer
	system   *SystemServer
	bmc      *ipmi.Fake
	warden   *warden.Fake
}

func newKit(t *testing.T) kit {
	t.Helper()
	mem := memstore.New()
	pub := events.HubPublisher{}
	scanSvc := scan.New(mem, nil, nil, nil, warden.NewFake(), pub, scan.Config{
		MaxHosts: 100000, Concurrency: 1, TimeoutMs: 1000, Workers: 1, MaxRetries: 0,
	}, nil)
	bmc := ipmi.NewFake()
	wf := warden.NewFake()
	return kit{
		subnet:   &SubnetServer{subnets: subnets.New(mem), scan: scanSvc},
		addr:     &IpAddressServer{addresses: addresses.New(mem, pub, 0, 0)},
		device:   &DeviceServer{devices: devices.New(mem), bmc: bmc, kvm: kvm.NewManager(nil, 0), warden: wf},
		vlan:     &VlanServer{vlans: vlans.New(mem)},
		location: &LocationServer{locations: locations.New(mem)},
		ipgroup:  &IpGroupServer{groups: groups.New(mem)},
		scanSrv:  &IpScanServer{scan: scanSvc},
		system:   &SystemServer{stats: stats.New(mem), dns: dnscfg.New(mem)},
		bmc:      bmc,
		warden:   wf,
	}
}

// withCaller overrides the SPIFFE resolver seam for the test and restores it.
func withCaller(t *testing.T, id string, roles []string, ok bool) {
	t.Helper()
	prev := callerFunc
	callerFunc = func(context.Context) (string, []string, bool) { return id, roles, ok }
	t.Cleanup(func() { callerFunc = prev })
}

func (k kit) createSubnet(t *testing.T, ctx context.Context, cidr string) *ipamv1.Subnet {
	t.Helper()
	sub, err := k.subnet.Create(ctx, &ipamv1.CreateSubnetRequest{
		TenantId: tenant,
		Subnet:   &ipamv1.Subnet{Name: "net-" + cidr, Cidr: cidr, Gateway: ""},
	})
	if err != nil {
		t.Fatalf("create subnet: %v", err)
	}
	return sub
}

func TestSubnetRPCs(t *testing.T) {
	k := newKit(t)
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	ctx := context.Background()

	sub := k.createSubnet(t, ctx, "10.10.0.0/24")
	if sub.GetIpVersion() != 4 || sub.GetStatus() != ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE {
		t.Fatalf("derived fields: %+v", sub)
	}

	got, err := k.subnet.Get(ctx, &ipamv1.GetSubnetRequest{TenantId: tenant, Id: sub.GetId()})
	if err != nil || got.GetCidr() != "10.10.0.0/24" {
		t.Fatalf("get: %v %+v", err, got)
	}

	list, err := k.subnet.List(ctx, &ipamv1.ListSubnetsRequest{TenantId: tenant})
	if err != nil || len(list.GetSubnets()) != 1 {
		t.Fatalf("list: %v n=%d", err, len(list.GetSubnets()))
	}

	tree, err := k.subnet.GetTree(ctx, &ipamv1.GetSubnetTreeRequest{TenantId: tenant})
	if err != nil || len(tree.GetRoots()) != 1 {
		t.Fatalf("tree: %v %+v", err, tree)
	}

	st, err := k.subnet.GetStats(ctx, &ipamv1.GetSubnetStatsRequest{TenantId: tenant, Id: sub.GetId()})
	if err != nil || st.GetTotalAddresses() == 0 {
		t.Fatalf("stats: %v %+v", err, st)
	}
}

func TestAddressRPCs(t *testing.T) {
	k := newKit(t)
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.20.0.0/24")

	a, err := k.addr.AllocateNext(ctx, &ipamv1.AllocateNextRequest{
		TenantId: tenant, SubnetId: sub.GetId(), Hostname: "host-1",
	})
	if err != nil || a.GetAddress() == "" {
		t.Fatalf("allocate: %v %+v", err, a)
	}
	if a.GetHostname() != "host-1" {
		t.Fatalf("hostname not stamped: %+v", a)
	}

	found, err := k.addr.Find(ctx, &ipamv1.FindIpAddressRequest{TenantId: tenant, Address: a.GetAddress()})
	if err != nil || found.GetId() != a.GetId() {
		t.Fatalf("find: %v %+v", err, found)
	}
}

func TestDevicePowerAuthz(t *testing.T) {
	k := newKit(t)
	ctx := context.Background()
	k.warden.Put("ipmi-ref", map[string]string{
		"username": "admin", "password": "pw", "protocol": "2.0", "port": "623",
	}, warden.SecretMeta{Name: "bmc"})

	// Create the device as a plain service caller.
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	dev, err := k.device.Create(ctx, &ipamv1.CreateDeviceRequest{
		TenantId: tenant,
		Device: &ipamv1.Device{
			Name: "srv-1", DeviceType: ipamv1.DeviceType_DEVICE_TYPE_SERVER,
			ManagementIp: "10.0.0.9", IpmiSecretRef: "ipmi-ref",
		},
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	got, err := k.device.Get(ctx, &ipamv1.GetDeviceRequest{TenantId: tenant, Id: dev.GetId()})
	if err != nil || got.GetName() != "srv-1" {
		t.Fatalf("get device: %v %+v", err, got)
	}

	// Plain service (no platform-admin role) must be refused power control.
	if _, err := k.device.PowerStatus(ctx, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("plain service power: want PermissionDenied, got %v", err)
	}

	// A platform-admin caller may drive power; creds are fetched at use time.
	withCaller(t, "spiffe://example.org/svc/console", []string{"platform-admin"}, true)
	ps, err := k.device.PowerStatus(ctx, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()})
	if err != nil || ps.GetState() != "on" {
		t.Fatalf("power status: %v %+v", err, ps)
	}
	if k.bmc.LastCreds.Username != "admin" || k.bmc.LastHost != "10.0.0.9" {
		t.Fatalf("creds not fetched from warden at use time: host=%q user=%q", k.bmc.LastHost, k.bmc.LastCreds.Username)
	}

	pr, err := k.device.Power(ctx, &ipamv1.PowerRequest{TenantId: tenant, Id: dev.GetId(), Action: ipamv1.PowerAction_POWER_ACTION_OFF})
	if err != nil || pr.GetState() != "off" {
		t.Fatalf("power off: %v %+v", err, pr)
	}
	if len(k.bmc.Actions) != 1 || k.bmc.Actions[0] != "off" {
		t.Fatalf("power action not applied: %+v", k.bmc.Actions)
	}

	// KVM session requires platform-admin too.
	sess, err := k.device.StartKvmSession(ctx, &ipamv1.StartKvmSessionRequest{TenantId: tenant, Id: dev.GetId()})
	if err != nil || sess.GetToken() == "" || sess.GetConsoleUrl() == "" {
		t.Fatalf("kvm session: %v %+v", err, sess)
	}
}

func TestVlanLocationGroup(t *testing.T) {
	k := newKit(t)
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	ctx := context.Background()

	vl, err := k.vlan.Create(ctx, &ipamv1.CreateVlanRequest{
		TenantId: tenant, Vlan: &ipamv1.Vlan{VlanId: 100, Name: "vlan-100"},
	})
	if err != nil || vl.GetVlanId() != 100 {
		t.Fatalf("vlan create: %v %+v", err, vl)
	}
	if _, err := k.vlan.Get(ctx, &ipamv1.GetVlanRequest{TenantId: tenant, Id: vl.GetId()}); err != nil {
		t.Fatalf("vlan get: %v", err)
	}

	loc, err := k.location.Create(ctx, &ipamv1.CreateLocationRequest{
		TenantId: tenant, Location: &ipamv1.Location{Name: "dc-1", LocationType: ipamv1.LocationType_LOCATION_TYPE_DATACENTER},
	})
	if err != nil || loc.GetName() != "dc-1" {
		t.Fatalf("location create: %v %+v", err, loc)
	}
	ltree, err := k.location.GetTree(ctx, &ipamv1.GetLocationTreeRequest{TenantId: tenant})
	if err != nil || len(ltree.GetRoots()) != 1 {
		t.Fatalf("location tree: %v %+v", err, ltree)
	}

	g, err := k.ipgroup.Create(ctx, &ipamv1.CreateIpGroupRequest{
		TenantId: tenant, Group: &ipamv1.IPGroup{Name: "grp-1"},
	})
	if err != nil {
		t.Fatalf("group create: %v", err)
	}
	if _, err := k.ipgroup.AddMember(ctx, &ipamv1.AddIpGroupMemberRequest{
		TenantId: tenant, IpGroupId: g.GetId(),
		Member: &ipamv1.IPGroupMember{MemberType: ipamv1.MemberType_MEMBER_TYPE_SUBNET, Value: "10.30.0.0/24"},
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}

	check, err := k.ipgroup.CheckIpInGroup(ctx, &ipamv1.CheckIpInGroupRequest{TenantId: tenant, Ip: "10.30.0.5"})
	if err != nil || len(check.GetGroups()) != 1 || check.GetGroups()[0].GetId() != g.GetId() {
		t.Fatalf("check ip in group: %v %+v", err, check)
	}
	// An address outside the member subnet matches nothing.
	miss, err := k.ipgroup.CheckIpInGroup(ctx, &ipamv1.CheckIpInGroupRequest{TenantId: tenant, Ip: "10.99.0.5"})
	if err != nil || len(miss.GetGroups()) != 0 {
		t.Fatalf("check ip miss: %v %+v", err, miss)
	}
}

func TestScanStartAndSystemStats(t *testing.T) {
	k := newKit(t)
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	ctx := context.Background()
	sub := k.createSubnet(t, ctx, "10.40.0.0/28")

	job, err := k.scanSrv.Start(ctx, &ipamv1.StartScanRequest{TenantId: tenant, SubnetId: sub.GetId()})
	if err != nil || job.GetStatus() != ipamv1.ScanStatus_SCAN_STATUS_PENDING {
		t.Fatalf("start scan: %v %+v", err, job)
	}
	got, err := k.scanSrv.Get(ctx, &ipamv1.GetScanRequest{TenantId: tenant, Id: job.GetId()})
	if err != nil || got.GetId() != job.GetId() {
		t.Fatalf("get scan: %v %+v", err, got)
	}

	st, err := k.system.GetStats(ctx, &ipamv1.GetStatsRequest{TenantId: tenant})
	if err != nil || st.GetSubnetsTotal() != 1 {
		t.Fatalf("system stats: %v %+v", err, st)
	}
}

func TestUnauthenticated(t *testing.T) {
	k := newKit(t)
	withCaller(t, "", nil, false)
	_, err := k.subnet.Get(context.Background(), &ipamv1.GetSubnetRequest{TenantId: tenant, Id: "x"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}

func TestInvalidArgument(t *testing.T) {
	k := newKit(t)
	withCaller(t, "spiffe://example.org/svc/deployer", nil, true)
	// Bad tenant id -> InvalidArgument from the caller guard.
	if _, err := k.subnet.Get(context.Background(), &ipamv1.GetSubnetRequest{TenantId: "not-a-uuid", Id: "x"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad tenant: want InvalidArgument, got %v", err)
	}
	// Bad CIDR -> ValidationError -> InvalidArgument from the domain.
	if _, err := k.subnet.Create(context.Background(), &ipamv1.CreateSubnetRequest{
		TenantId: tenant, Subnet: &ipamv1.Subnet{Name: "bad", Cidr: "not-a-cidr"},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad cidr: want InvalidArgument, got %v", err)
	}
}
