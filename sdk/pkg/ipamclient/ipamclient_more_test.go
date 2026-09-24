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

// dialSrv dials a bufconn client against an already-configured *grpc.Server,
// letting these tests register failing service implementations that the
// concrete-typed stubs struct in ipamclient_test.go cannot hold.
func dialSrv(t *testing.T, gs *grpc.Server) *ipamclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
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

// ---- failing stubs (return status errors for every method) ----

type failSubnet struct {
	ipamv1.UnimplementedSubnetServiceServer
}

func (failSubnet) Create(context.Context, *ipamv1.CreateSubnetRequest) (*ipamv1.Subnet, error) {
	return nil, status.Error(codes.InvalidArgument, "bad")
}

func (failSubnet) List(context.Context, *ipamv1.ListSubnetsRequest) (*ipamv1.ListSubnetsResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}

type failAddress struct {
	ipamv1.UnimplementedIpAddressServiceServer
}

func (failAddress) AllocateNext(context.Context, *ipamv1.AllocateNextRequest) (*ipamv1.IPAddress, error) {
	return nil, status.Error(codes.ResourceExhausted, "full")
}

func (failAddress) Find(context.Context, *ipamv1.FindIpAddressRequest) (*ipamv1.IPAddress, error) {
	return nil, status.Error(codes.NotFound, "nope")
}

type failDevice struct {
	ipamv1.UnimplementedDeviceServiceServer
}

func (failDevice) Create(context.Context, *ipamv1.CreateDeviceRequest) (*ipamv1.Device, error) {
	return nil, status.Error(codes.AlreadyExists, "dup")
}

func (failDevice) Get(context.Context, *ipamv1.GetDeviceRequest) (*ipamv1.Device, error) {
	return nil, status.Error(codes.NotFound, "no dev")
}

func (failDevice) PowerStatus(context.Context, *ipamv1.PowerStatusRequest) (*ipamv1.PowerStatusResponse, error) {
	return nil, status.Error(codes.Unavailable, "bmc down")
}

func (failDevice) Power(context.Context, *ipamv1.PowerRequest) (*ipamv1.PowerStatusResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "not admin")
}

type failIpGroup struct {
	ipamv1.UnimplementedIpGroupServiceServer
}

func (failIpGroup) CheckIpInGroup(context.Context, *ipamv1.CheckIpInGroupRequest) (*ipamv1.CheckIpInGroupResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}

type failScan struct {
	ipamv1.UnimplementedIpScanServiceServer
}

func (failScan) Start(context.Context, *ipamv1.StartScanRequest) (*ipamv1.IPScanJob, error) {
	return nil, status.Error(codes.FailedPrecondition, "no subnet")
}

type failSystem struct {
	ipamv1.UnimplementedSystemServiceServer
}

func (failSystem) GetStats(context.Context, *ipamv1.GetStatsRequest) (*ipamv1.Stats, error) {
	return nil, status.Error(codes.Unauthenticated, "who")
}

func TestErrorPropagationAllMethods(t *testing.T) {
	ctx := context.Background()

	t.Run("subnet", func(t *testing.T) {
		gs := grpc.NewServer()
		ipamv1.RegisterSubnetServiceServer(gs, &failSubnet{})
		c := dialSrv(t, gs)
		if _, err := c.CreateSubnet(ctx, "t1", ipamclient.Subnet{Name: "n"}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateSubnet err=%v", err)
		}
		if _, err := c.ListSubnets(ctx, "t1", ipamclient.SubnetFilter{}); status.Code(err) != codes.Internal {
			t.Fatalf("ListSubnets err=%v", err)
		}
	})

	t.Run("address", func(t *testing.T) {
		gs := grpc.NewServer()
		ipamv1.RegisterIpAddressServiceServer(gs, &failAddress{})
		c := dialSrv(t, gs)
		if _, err := c.AllocateNextAddress(ctx, "t1", ipamclient.AllocateRequest{SubnetID: "sn-1"}); status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("AllocateNextAddress err=%v", err)
		}
		if _, err := c.FindAddress(ctx, "t1", "10.0.0.5"); status.Code(err) != codes.NotFound {
			t.Fatalf("FindAddress err=%v", err)
		}
	})

	t.Run("device", func(t *testing.T) {
		gs := grpc.NewServer()
		ipamv1.RegisterDeviceServiceServer(gs, &failDevice{})
		c := dialSrv(t, gs)
		if _, err := c.CreateDevice(ctx, "t1", ipamclient.Device{Name: "d"}); status.Code(err) != codes.AlreadyExists {
			t.Fatalf("CreateDevice err=%v", err)
		}
		if _, err := c.GetDevice(ctx, "t1", "dev-1"); status.Code(err) != codes.NotFound {
			t.Fatalf("GetDevice err=%v", err)
		}
		if _, err := c.PowerStatus(ctx, "t1", "dev-1"); status.Code(err) != codes.Unavailable {
			t.Fatalf("PowerStatus err=%v", err)
		}
		if _, err := c.Power(ctx, "t1", "dev-1", "on"); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("Power err=%v", err)
		}
	})

	t.Run("group_scan_stats", func(t *testing.T) {
		gs := grpc.NewServer()
		ipamv1.RegisterIpGroupServiceServer(gs, &failIpGroup{})
		ipamv1.RegisterIpScanServiceServer(gs, &failScan{})
		ipamv1.RegisterSystemServiceServer(gs, &failSystem{})
		c := dialSrv(t, gs)
		if _, err := c.CheckIpInGroup(ctx, "t1", "10.0.0.5"); status.Code(err) != codes.Internal {
			t.Fatalf("CheckIpInGroup err=%v", err)
		}
		if _, err := c.StartScan(ctx, "t1", ipamclient.ScanOptions{SubnetID: "sn-1"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("StartScan err=%v", err)
		}
		if _, err := c.GetStatistics(ctx, "t1"); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("GetStatistics err=%v", err)
		}
	})
}
