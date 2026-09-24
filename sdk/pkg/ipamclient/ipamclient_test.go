package ipamclient_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	ipamv1 "github.com/go-tangra/go-tangra-ipam/sdk/v4/api/proto/ipam/v1"
	"github.com/go-tangra/go-tangra-ipam/sdk/v4/pkg/ipamclient"
)

// ---- stub servers ----

type stubSubnet struct {
	ipamv1.UnimplementedSubnetServiceServer
	lastCreate *ipamv1.CreateSubnetRequest
	lastList   *ipamv1.ListSubnetsRequest
	failGet    bool
}

func (s *stubSubnet) Create(_ context.Context, req *ipamv1.CreateSubnetRequest) (*ipamv1.Subnet, error) {
	s.lastCreate = req
	in := req.GetSubnet()
	return &ipamv1.Subnet{
		Id: "sn-1", TenantId: req.GetTenantId(), Name: in.GetName(), Cidr: in.GetCidr(),
		Status: ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE, IpVersion: 4, TotalAddresses: 256, Utilization: 0.1,
	}, nil
}

func (s *stubSubnet) Get(_ context.Context, req *ipamv1.GetSubnetRequest) (*ipamv1.Subnet, error) {
	if s.failGet {
		return nil, status.Error(codes.NotFound, "not_found")
	}
	return &ipamv1.Subnet{Id: req.GetId(), Cidr: "10.0.0.0/24", Status: ipamv1.SubnetStatus_SUBNET_STATUS_RESERVED}, nil
}

func (s *stubSubnet) List(_ context.Context, req *ipamv1.ListSubnetsRequest) (*ipamv1.ListSubnetsResponse, error) {
	s.lastList = req
	return &ipamv1.ListSubnetsResponse{Subnets: []*ipamv1.Subnet{
		{Id: "sn-1", Cidr: "10.0.0.0/24", Status: ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE},
	}}, nil
}

type stubAddress struct {
	ipamv1.UnimplementedIpAddressServiceServer
	lastAlloc *ipamv1.AllocateNextRequest
}

func (s *stubAddress) AllocateNext(_ context.Context, req *ipamv1.AllocateNextRequest) (*ipamv1.IPAddress, error) {
	s.lastAlloc = req
	return &ipamv1.IPAddress{
		Id: "ip-1", Address: "10.0.0.5", SubnetId: req.GetSubnetId(), Hostname: req.GetHostname(),
		Status: ipamv1.IpStatus_IP_STATUS_ACTIVE, AddressType: ipamv1.AddressType_ADDRESS_TYPE_HOST,
	}, nil
}

func (s *stubAddress) Find(_ context.Context, req *ipamv1.FindIpAddressRequest) (*ipamv1.IPAddress, error) {
	return &ipamv1.IPAddress{Id: "ip-1", Address: req.GetAddress(), Status: ipamv1.IpStatus_IP_STATUS_DHCP}, nil
}

type stubDevice struct {
	ipamv1.UnimplementedDeviceServiceServer
	lastCreate *ipamv1.CreateDeviceRequest
	lastPower  *ipamv1.PowerRequest
}

func (s *stubDevice) Create(_ context.Context, req *ipamv1.CreateDeviceRequest) (*ipamv1.Device, error) {
	s.lastCreate = req
	in := req.GetDevice()
	return &ipamv1.Device{
		Id: "dev-1", TenantId: req.GetTenantId(), Name: in.GetName(), DeviceType: in.GetDeviceType(),
		Status: ipamv1.DeviceStatus_DEVICE_STATUS_ACTIVE, ManagementIp: in.GetManagementIp(),
	}, nil
}

func (s *stubDevice) Get(_ context.Context, req *ipamv1.GetDeviceRequest) (*ipamv1.Device, error) {
	return &ipamv1.Device{Id: req.GetId(), Name: "dev-1", DeviceType: ipamv1.DeviceType_DEVICE_TYPE_ROUTER}, nil
}

func (s *stubDevice) PowerStatus(_ context.Context, _ *ipamv1.PowerStatusRequest) (*ipamv1.PowerStatusResponse, error) {
	return &ipamv1.PowerStatusResponse{State: "on"}, nil
}

func (s *stubDevice) Power(_ context.Context, req *ipamv1.PowerRequest) (*ipamv1.PowerStatusResponse, error) {
	s.lastPower = req
	return &ipamv1.PowerStatusResponse{State: "off"}, nil
}

type stubIpGroup struct {
	ipamv1.UnimplementedIpGroupServiceServer
}

func (s *stubIpGroup) CheckIpInGroup(_ context.Context, req *ipamv1.CheckIpInGroupRequest) (*ipamv1.CheckIpInGroupResponse, error) {
	return &ipamv1.CheckIpInGroupResponse{Groups: []*ipamv1.IPGroup{
		{Id: "g-1", Name: "grp", Status: ipamv1.GroupStatus_GROUP_STATUS_ACTIVE},
	}}, nil
}

type stubScan struct {
	ipamv1.UnimplementedIpScanServiceServer
	lastStart *ipamv1.StartScanRequest
}

func (s *stubScan) Start(_ context.Context, req *ipamv1.StartScanRequest) (*ipamv1.IPScanJob, error) {
	s.lastStart = req
	return &ipamv1.IPScanJob{
		Id: "job-1", SubnetId: req.GetSubnetId(), Status: ipamv1.ScanStatus_SCAN_STATUS_PENDING,
		TriggeredBy: ipamv1.ScanTrigger_SCAN_TRIGGER_MANUAL, TotalAddresses: 14,
	}, nil
}

type stubSystem struct {
	ipamv1.UnimplementedSystemServiceServer
}

func (s *stubSystem) GetStats(_ context.Context, _ *ipamv1.GetStatsRequest) (*ipamv1.Stats, error) {
	return &ipamv1.Stats{
		SubnetsTotal: 3, AddressesTotal: 100, AddressesUsed: 40, Utilization: 0.4, DevicesTotal: 2,
		DevicesByType: map[string]int64{"router": 1, "server": 1},
	}, nil
}

type stubs struct {
	subnet  *stubSubnet
	addr    *stubAddress
	device  *stubDevice
	ipGroup *stubIpGroup
	scan    *stubScan
	system  *stubSystem
}

func dial(t *testing.T, s stubs) *ipamclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	if s.subnet != nil {
		ipamv1.RegisterSubnetServiceServer(gs, s.subnet)
	}
	if s.addr != nil {
		ipamv1.RegisterIpAddressServiceServer(gs, s.addr)
	}
	if s.device != nil {
		ipamv1.RegisterDeviceServiceServer(gs, s.device)
	}
	if s.ipGroup != nil {
		ipamv1.RegisterIpGroupServiceServer(gs, s.ipGroup)
	}
	if s.scan != nil {
		ipamv1.RegisterIpScanServiceServer(gs, s.scan)
	}
	if s.system != nil {
		ipamv1.RegisterSystemServiceServer(gs, s.system)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return ipamclient.New(conn)
}

func TestSubnetMethods(t *testing.T) {
	ctx := context.Background()
	sub := &stubSubnet{}
	c := dial(t, stubs{subnet: sub})

	created, err := c.CreateSubnet(ctx, "t1", ipamclient.Subnet{Name: "n", CIDR: "10.0.0.0/24", Status: "active"})
	if err != nil || created.ID != "sn-1" || created.IPVersion != 4 {
		t.Fatalf("create: %v %+v", err, created)
	}
	if sub.lastCreate.GetSubnet().GetStatus() != ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE {
		t.Fatalf("status enum not encoded: %v", sub.lastCreate.GetSubnet().GetStatus())
	}

	got, err := c.GetSubnet(ctx, "t1", "sn-1")
	if err != nil || got.Status != "reserved" {
		t.Fatalf("get: %v %+v", err, got)
	}

	list, err := c.ListSubnets(ctx, "t1", ipamclient.SubnetFilter{Status: "active", Query: "10."})
	if err != nil || len(list) != 1 || list[0].Status != "active" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if sub.lastList.GetStatus() != ipamv1.SubnetStatus_SUBNET_STATUS_ACTIVE || sub.lastList.GetQuery() != "10." {
		t.Fatalf("list filter not encoded: %+v", sub.lastList)
	}
}

func TestAddressMethods(t *testing.T) {
	ctx := context.Background()
	addr := &stubAddress{}
	c := dial(t, stubs{addr: addr})

	a, err := c.AllocateNextAddress(ctx, "t1", ipamclient.AllocateRequest{SubnetID: "sn-1", Hostname: "h1"})
	if err != nil || a.Address != "10.0.0.5" || a.Status != "active" || a.AddressType != "host" {
		t.Fatalf("allocate: %v %+v", err, a)
	}
	if addr.lastAlloc.GetHostname() != "h1" {
		t.Fatalf("hostname not encoded: %+v", addr.lastAlloc)
	}

	found, err := c.FindAddress(ctx, "t1", "10.0.0.5")
	if err != nil || found.Status != "dhcp" {
		t.Fatalf("find: %v %+v", err, found)
	}
}

func TestDeviceAndPower(t *testing.T) {
	ctx := context.Background()
	dev := &stubDevice{}
	c := dial(t, stubs{device: dev})

	created, err := c.CreateDevice(ctx, "t1", ipamclient.Device{Name: "dev-1", DeviceType: "server", ManagementIP: "10.0.0.9"})
	if err != nil || created.ID != "dev-1" || created.Status != "active" {
		t.Fatalf("create device: %v %+v", err, created)
	}
	if dev.lastCreate.GetDevice().GetDeviceType() != ipamv1.DeviceType_DEVICE_TYPE_SERVER {
		t.Fatalf("device_type not encoded: %v", dev.lastCreate.GetDevice().GetDeviceType())
	}

	got, err := c.GetDevice(ctx, "t1", "dev-1")
	if err != nil || got.DeviceType != "router" {
		t.Fatalf("get device: %v %+v", err, got)
	}

	state, err := c.PowerStatus(ctx, "t1", "dev-1")
	if err != nil || state != "on" {
		t.Fatalf("power status: %v %q", err, state)
	}

	state, err = c.Power(ctx, "t1", "dev-1", "off")
	if err != nil || state != "off" {
		t.Fatalf("power: %v %q", err, state)
	}
	if dev.lastPower.GetAction() != ipamv1.PowerAction_POWER_ACTION_OFF {
		t.Fatalf("power action not encoded: %v", dev.lastPower.GetAction())
	}
}

func TestGroupScanStats(t *testing.T) {
	ctx := context.Background()
	sc := &stubScan{}
	c := dial(t, stubs{ipGroup: &stubIpGroup{}, scan: sc, system: &stubSystem{}})

	found, err := c.CheckIpInGroup(ctx, "t1", "10.0.0.5")
	if err != nil || len(found) != 1 || found[0].Status != "active" {
		t.Fatalf("check ip: %v %+v", err, found)
	}

	job, err := c.StartScan(ctx, "t1", ipamclient.ScanOptions{SubnetID: "sn-1", EnableSNMP: true})
	if err != nil || job.Status != "pending" || job.TriggeredBy != "manual" {
		t.Fatalf("start scan: %v %+v", err, job)
	}
	if !sc.lastStart.GetEnableSnmp() {
		t.Fatalf("enable_snmp not encoded: %+v", sc.lastStart)
	}

	st, err := c.GetStatistics(ctx, "t1")
	if err != nil || st.SubnetsTotal != 3 || st.DevicesByType["router"] != 1 {
		t.Fatalf("stats: %v %+v", err, st)
	}
}

func TestErrorPropagation(t *testing.T) {
	ctx := context.Background()
	c := dial(t, stubs{subnet: &stubSubnet{failGet: true}})
	_, err := c.GetSubnet(ctx, "t1", "missing")
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}
