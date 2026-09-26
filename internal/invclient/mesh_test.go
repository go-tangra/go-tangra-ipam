package invclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	invv1 "github.com/go-tangra/go-tangra-inventory/sdk/v4/api/proto/inventory/v1"
)

// fakeServer is an in-process inventory HostReportService.
type fakeServer struct {
	invv1.UnimplementedHostReportServiceServer
	pages   [][]*invv1.HostReport
	err     error
	delay   time.Duration
	tenants []string
}

func (s *fakeServer) ListReportTenants(ctx context.Context, _ *invv1.ListReportTenantsRequest) (*invv1.ListReportTenantsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &invv1.ListReportTenantsResponse{TenantIds: s.tenants, MaxChangedAt: 5000}, nil
}

func (s *fakeServer) ListHostReports(ctx context.Context, r *invv1.ListHostReportsRequest) (*invv1.ListHostReportsResponse, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, status.Error(codes.DeadlineExceeded, "slow")
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	i := 0
	if r.GetCursor() != "" {
		i = int(r.GetCursor()[0] - '0')
	}
	next := ""
	if i+1 < len(s.pages) {
		next = string(rune('0' + i + 1))
	}
	return &invv1.ListHostReportsResponse{Reports: s.pages[i], NextCursor: next}, nil
}

func (s *fakeServer) GetHostReport(ctx context.Context, r *invv1.GetHostReportRequest) (*invv1.HostReport, error) {
	if s.err != nil {
		return nil, s.err
	}
	if r.GetHostId() != "h1" {
		return nil, status.Error(codes.NotFound, "no host")
	}
	return s.pages[0][0], nil
}

func serve(t *testing.T, srv *fakeServer) (Dialer, *int32) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	invv1.RegisterHostReportServiceServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	var dials int32
	return func(ctx context.Context) (grpc.ClientConnInterface, error) {
		atomic.AddInt32(&dials, 1)
		return grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(CallOptions()...))
	}, &dials
}

func fullReport(id string) *invv1.HostReport {
	return &invv1.HostReport{
		TenantId: tA, Host: &invv1.Host{Id: id, Hostname: "web", SystemSerial: "SN", Status: invv1.HostStatus_HOST_STATUS_ACTIVE},
		SnapshotId: "s1", CollectedAt: 100, ReportChangedAt: 200000, ReportDigest: "d", OsFamily: "linux",
		NetworkInterfaces: []*invv1.NetworkInterface{{Name: "eth0", Mac: "aa:bb:cc:dd:ee:01", Type: "ethernet", Up: true, SpeedBps: 1e9,
			Addresses: []*invv1.InterfaceAddress{{Address: "10.0.0.5", PrefixLength: 24, Family: "ipv4", Dhcp: true}}}},
		PrimaryIpv4: "10.0.0.5", Virtualization: &invv1.Virtualization{Role: "vm", Kind: "kvm"},
		Bmc:              &invv1.Bmc{Address: "10.9.0.5", PrefixLength: 24, Ports: []*invv1.BmcPort{{Channel: 1, Mac: "aa:bb:cc:dd:ee:10"}}},
		HypervisorGuests: []*invv1.HypervisorGuest{{Id: "101", Name: "vm", Kind: "vm", Macs: []string{"bc:24:11:00:00:01"}}},
		UpdateState:      &invv1.UpdateState{PackageManager: "apt", Status: "updates_available", RebootRequired: "true"},
		PendingUpdates:   []*invv1.PendingUpdate{{Name: "openssl", InstalledVersion: "1", AvailableVersion: "2", Security: true}},
		Truncated:        &invv1.CollectionLimits{Packages: 3},
	}
}

func TestMeshPagesToExhaustion(t *testing.T) {
	srv := &fakeServer{tenants: []string{tA, tB}, pages: [][]*invv1.HostReport{{fullReport("h1"), fullReport("h2")}, {fullReport("h3")}}}
	dial, dials := serve(t, srv)
	m := NewMesh(dial, time.Second, 2)
	ctx := context.Background()
	ids, maxAt, err := m.ListReportTenants(ctx, time.Time{})
	if err != nil || len(ids) != 2 || maxAt.UnixMilli() != 5000 {
		t.Fatalf("tenants %v %v %v", ids, maxAt, err)
	}
	var got []Report
	if err := m.ListHostReports(ctx, tA, Filter{}, func(p []Report) error { got = append(got, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("pages: %d", len(got))
	}
	r := got[0]
	if r.Host.ID != "h1" || r.Interfaces[0].Addresses[0].PrefixLength != 24 || !r.Interfaces[0].Addresses[0].DHCP ||
		r.BMC == nil || r.BMC.Ports[0].MAC != "aa:bb:cc:dd:ee:10" || r.Guests[0].MACs[0] != "bc:24:11:00:00:01" ||
		r.PendingUpdates[0].Name != "openssl" || r.Truncated.Packages != 3 || r.Virtualization.Kind != "kvm" ||
		r.Updates.RebootRequired != "true" || r.Host.Status != "active" || r.ChangedAt.UnixMilli() != 200000 || r.CollectedAt.Unix() != 100 {
		t.Fatalf("mapping %+v", r)
	}
	if one, err := m.GetHostReport(ctx, tA, "h1"); err != nil || one.Host.ID != "h1" {
		t.Fatal(err)
	}
	if _, err := m.GetHostReport(ctx, tA, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	stop := errors.New("stop")
	if err := m.ListHostReports(ctx, tA, Filter{Digest: true}, func([]Report) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("callback error")
	}
	if *dials != 1 {
		t.Fatalf("dialled %d times", *dials)
	}
}

func TestMeshErrorCodes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		err  error
		code string
	}{
		{status.Error(codes.PermissionDenied, "policy"), CodePermissionDenied},
		{status.Error(codes.Unauthenticated, "no peer"), CodePermissionDenied},
		{status.Error(codes.Unimplemented, "old inventory"), CodeOutdated},
		{status.Error(codes.Unavailable, "down"), CodeUnavailable},
	} {
		dial, _ := serve(t, &fakeServer{err: c.err, pages: [][]*invv1.HostReport{{}}})
		m := NewMesh(dial, time.Second, 10)
		_, _, e1 := m.ListReportTenants(ctx, time.Time{})
		e2 := m.ListHostReports(ctx, tA, Filter{}, func([]Report) error { return nil })
		_, e3 := m.GetHostReport(ctx, tA, "h1")
		for _, e := range []error{e1, e2, e3} {
			if !errors.Is(e, ErrUnavailable) || Code(e) != c.code {
				t.Errorf("%v -> %v (%s)", c.err, e, Code(e))
			}
		}
	}
}

func TestMeshTimeoutAndDialFailure(t *testing.T) {
	dial, _ := serve(t, &fakeServer{delay: time.Second, pages: [][]*invv1.HostReport{{}}})
	m := NewMesh(dial, 50*time.Millisecond, 10)
	start := time.Now()
	err := m.ListHostReports(context.Background(), tA, Filter{}, func([]Report) error { return nil })
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("timeout not honoured: %v after %s", err, time.Since(start))
	}
	bad := NewMesh(func(context.Context) (grpc.ClientConnInterface, error) { return nil, errors.New("no route") }, time.Second, 10)
	if _, _, err := bad.ListReportTenants(context.Background(), time.Time{}); Code(err) != CodeUnavailable {
		t.Fatal("dial failure")
	}
	if err := bad.ListHostReports(context.Background(), tA, Filter{}, nil); Code(err) != CodeUnavailable {
		t.Fatal("dial failure list")
	}
	if _, err := bad.GetHostReport(context.Background(), tA, "h"); Code(err) != CodeUnavailable {
		t.Fatal("dial failure get")
	}
}

// oversizedAPI returns more reports than requested, or an endless cursor.
type oversizedAPI struct{ endless bool }

func (oversizedAPI) ListReportTenants(context.Context, time.Time) ([]string, time.Time, error) {
	return nil, time.Time{}, nil
}
func (o oversizedAPI) ListHostReports(_ context.Context, _ string, f inventoryReportFilter) ([]inventoryHostReport, string, error) {
	if o.endless {
		return nil, "again", nil
	}
	return make([]inventoryHostReport, f.Limit+1), "", nil
}
func (oversizedAPI) GetHostReport(context.Context, string, string) (inventoryHostReport, error) {
	return inventoryHostReport{}, nil
}

func TestMeshRejectsOversizedPagesAndEndlessCursors(t *testing.T) {
	for _, endless := range []bool{false, true} {
		m := NewMesh(func(context.Context) (grpc.ClientConnInterface, error) { return nil, nil }, time.Second, 2)
		m.newAPI = func(grpc.ClientConnInterface) reportAPI { return oversizedAPI{endless: endless} }
		err := m.ListHostReports(context.Background(), tA, Filter{}, func([]Report) error { return nil })
		if Code(err) != CodeUnavailable {
			t.Fatalf("endless=%v: %v", endless, err)
		}
	}
}

func TestMeshConcurrentFirstUseDialsOnce(t *testing.T) {
	dial, dials := serve(t, &fakeServer{tenants: []string{tA}, pages: [][]*invv1.HostReport{{}}})
	m := NewMesh(dial, time.Second, 10)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = m.ListReportTenants(context.Background(), time.Time{})
		}()
	}
	wg.Wait()
	if *dials != 1 {
		t.Fatalf("dialled %d times", *dials)
	}
}

type (
	inventoryReportFilter = sdkReportFilter
	inventoryHostReport   = sdkHostReport
)
