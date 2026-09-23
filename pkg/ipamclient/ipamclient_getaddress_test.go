package ipamclient_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/pkg/ipamclient"
)

// stubGetAddress answers IpAddressService/Get: a known id, NotFound, or an
// internal failure.
type stubGetAddress struct {
	stubAddress
	last *ipamv1.GetIpAddressRequest
}

func (s *stubGetAddress) Get(_ context.Context, req *ipamv1.GetIpAddressRequest) (*ipamv1.IPAddress, error) {
	s.last = req
	switch req.GetId() {
	case "ip-1":
		return &ipamv1.IPAddress{Id: "ip-1", TenantId: req.GetTenantId(), Address: "2001:db8::10", SubnetId: "sn-6",
			Hostname: "v6.example.com", Status: ipamv1.IpStatus_IP_STATUS_ACTIVE}, nil
	case "gone":
		return nil, status.Error(codes.NotFound, "not_found")
	}
	return nil, status.Error(codes.Internal, "boom")
}

// dialAddr serves only the address service with the given implementation.
func dialAddr(t *testing.T, srv ipamv1.IpAddressServiceServer) *ipamclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	ipamv1.RegisterIpAddressServiceServer(gs, srv)
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

// T045: GetAddress maps IpAddressService/Get; NotFound stays a gRPC NotFound
// status and other errors are propagated.
func TestGetAddress(t *testing.T) {
	ctx := context.Background()
	stub := &stubGetAddress{}
	c := dialAddr(t, stub)

	got, err := c.GetAddress(ctx, "t1", "ip-1")
	if err != nil || got.ID != "ip-1" || got.Address != "2001:db8::10" || got.SubnetID != "sn-6" || got.Hostname != "v6.example.com" || got.Status != "active" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if stub.last.GetTenantId() != "t1" || stub.last.GetId() != "ip-1" {
		t.Fatalf("request = %+v", stub.last)
	}
	if _, err := c.GetAddress(ctx, "t1", "gone"); status.Code(err) != codes.NotFound {
		t.Fatalf("not found = %v", err)
	}
	_, err = c.GetAddress(ctx, "t1", "other")
	if err == nil || status.Code(err) != codes.Internal || errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
