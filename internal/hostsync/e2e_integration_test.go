//go:build integration

// End-to-end host sync (T053, T050): a PostgreSQL IPAM (testcontainers)
// pulls host reports from an in-process inventory HostReportService (built
// from the inventory SDK's generated stubs) over the Freya mTLS transport with
// SPIFFE test identities and an inbound policy that admits only svc/ipam.
package hostsync_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	invv1 "github.com/go-tangra/go-tangra-inventory/sdk/v4/api/proto/inventory/v1"
	"github.com/go-tangra/go-tangra/v4/authz"
	"github.com/go-tangra/go-tangra/v4/freyatest/testrt"
	"github.com/go-tangra/go-tangra/v4/freyatest/testutil"
	tgrpc "github.com/go-tangra/go-tangra/v4/transport/grpc"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostsync"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
	hostA   = "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa01"
)

func openRepo(t *testing.T) *repodb.DB {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "timescale/timescaledb:latest-pg16", ExposedPorts: []string{"5432/tcp"},
			Env:        map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "ipam"},
			WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2 * time.Minute),
		}, Started: true,
	})
	if err != nil {
		t.Skipf("testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	adminDSN := "postgres://postgres:test@" + host + ":" + port.Port() + "/ipam?sslmode=disable"
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Exec(ctx, "CREATE ROLE ipam_app LOGIN PASSWORD 'app' NOBYPASSRLS")
	_ = conn.Close(ctx)
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, "postgres://ipam_app:app@"+host+":"+port.Port()+"/ipam?sslmode=disable", 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return repodb.New(st)
}

// inventory is a fake HostReportService holding the latest report per host.
type inventory struct {
	invv1.UnimplementedHostReportServiceServer
	mu      sync.Mutex
	reports map[string]map[string]*invv1.HostReport // tenant -> host -> report
}

func (s *inventory) put(r *invv1.HostReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reports[r.GetTenantId()] == nil {
		s.reports[r.GetTenantId()] = map[string]*invv1.HostReport{}
	}
	s.reports[r.GetTenantId()][r.GetHost().GetId()] = r
}

func (s *inventory) ListReportTenants(_ context.Context, r *invv1.ListReportTenantsRequest) (*invv1.ListReportTenantsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &invv1.ListReportTenantsResponse{MaxChangedAt: r.GetChangedSince()}
	for tid, hosts := range s.reports {
		changed := false
		for _, h := range hosts {
			if h.GetReportChangedAt() > r.GetChangedSince() {
				changed = true
				out.MaxChangedAt = max(out.MaxChangedAt, h.GetReportChangedAt())
			}
		}
		if changed {
			out.TenantIds = append(out.TenantIds, tid)
		}
	}
	return out, nil
}

func (s *inventory) ListHostReports(_ context.Context, r *invv1.ListHostReportsRequest) (*invv1.ListHostReportsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &invv1.ListHostReportsResponse{}
	for _, h := range s.reports[r.GetTenantId()] {
		if h.GetReportChangedAt() <= r.GetChangedSince() {
			continue
		}
		if r.GetView() == invv1.HostReportView_HOST_REPORT_VIEW_DIGEST {
			out.Reports = append(out.Reports, &invv1.HostReport{TenantId: h.GetTenantId(), Host: h.GetHost(), ReportDigest: h.GetReportDigest(), ReportChangedAt: h.GetReportChangedAt()})
			continue
		}
		out.Reports = append(out.Reports, h)
	}
	return out, nil
}

func (s *inventory) GetHostReport(_ context.Context, r *invv1.GetHostReportRequest) (*invv1.HostReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.reports[r.GetTenantId()][r.GetHostId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "no report")
	}
	return h, nil
}

func report(tenant, host, hostname string, changedMs int64, digest int, addrs ...string) *invv1.HostReport {
	iface := &invv1.NetworkInterface{Name: "eth0", Mac: "aa:bb:cc:00:00:01", Type: "ethernet", Up: true}
	for _, a := range addrs {
		ip, pfx, _ := strings.Cut(a, "/")
		var bits uint32
		_, _ = fmt.Sscan(pfx, &bits)
		iface.Addresses = append(iface.Addresses, &invv1.InterfaceAddress{Address: ip, PrefixLength: bits, Family: "ipv4", Scope: "global"})
	}
	return &invv1.HostReport{
		TenantId: tenant, Host: &invv1.Host{Id: host, Hostname: hostname, SystemSerial: "SN-" + host[len(host)-2:], Status: invv1.HostStatus_HOST_STATUS_ACTIVE},
		SnapshotId: fmt.Sprint("snap-", digest), CollectedAt: changedMs / 1000, ReportChangedAt: changedMs,
		ReportDigest: fmt.Sprintf("%064x", digest), OsFamily: "linux",
		NetworkInterfaces: []*invv1.NetworkInterface{iface, {Name: "docker0", Mac: "02:42:00:00:00:01", Type: "bridge",
			Addresses: []*invv1.InterfaceAddress{{Address: "172.17.0.1", PrefixLength: 16, Family: "ipv4"}}}},
		PrimaryIpv4:    strings.Split(addrs[0], "/")[0],
		Virtualization: &invv1.Virtualization{Role: "physical"},
		UpdateState:    &invv1.UpdateState{Status: "unknown"},
	}
}

// mesh starts the inventory service with the ipam-hostsync policy rule and
// returns a mesh client dialled with the given caller identity.
func mesh(t *testing.T, inv *inventory, caller string) *invclient.Mesh {
	t.Helper()
	ca := testutil.MustCA("example.org")
	rt := testrt.New(t, ca, "inventory")
	pol, err := authz.NewPolicy("test", []authz.Rule{{
		ID: "ipam-hostsync", From: []string{"spiffe://example.org/svc/ipam"}, To: []string{"inventory"}, Effect: authz.Allow,
		Operations: []string{"/inventory.v1.HostReportService/ListReportTenants", "/inventory.v1.HostReportService/ListHostReports",
			"/inventory.v1.HostReportService/GetHostReport", "/grpc.health.v1.Health/Check"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rt.Authz = pol
	srv, err := tgrpc.NewServer(rt, tgrpc.WithAddress("127.0.0.1:0"))
	if err != nil {
		t.Fatal(err)
	}
	invv1.RegisterHostReportServiceServer(srv, inv)
	t.Cleanup(testrt.StartServer(t, srv))
	ep, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewTLS(testrt.ClientTLS(t, ca, caller, "inventory"))
	return invclient.NewMesh(func(context.Context) (grpc.ClientConnInterface, error) {
		return grpc.NewClient(ep.Host, grpc.WithTransportCredentials(creds))
	}, 10*time.Second, 100)
}

type pub struct {
	mu  sync.Mutex
	evs []string
}

func (p *pub) Publish(_ context.Context, _ string, t string, _ any) {
	p.mu.Lock()
	p.evs = append(p.evs, t)
	p.mu.Unlock()
}

func countAudit(t *testing.T, db *repodb.DB, tenant, action string) int {
	t.Helper()
	n := 0
	err := db.St.Tx(context.Background(), store.Scope{TenantID: tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "SELECT count(*) FROM ipam_audit_events WHERE tenant_id=$1 AND action=$2 AND actor_id='hostsync'", tenant, action).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHostSyncEndToEnd(t *testing.T) {
	db := openRepo(t)
	ctx := context.Background()
	inv := &inventory{reports: map[string]map[string]*invv1.HostReport{}}
	cfg := hostsync.Config{PollInterval: time.Minute, Workers: 2, ConflictMoves: 3, ConflictWindow: 24 * time.Hour}

	// Not allowed by policy: degraded, nothing written.
	inv.put(report(tenantA, hostA, "web-01", 1_000_000, 1, "10.20.0.5/24"))
	denied := hostsync.New(db, mesh(t, inv, "asset"), &pub{}, cfg, nil, nil)
	if err := denied.Cycle(ctx); !errors.Is(err, invclient.ErrUnavailable) || invclient.Code(err) != invclient.CodePermissionDenied {
		t.Fatalf("policy denial: %v (%s)", err, invclient.Code(err))
	}
	if l, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{}); len(l) != 0 {
		t.Fatal("denied identity wrote devices")
	}

	p := &pub{}
	r := hostsync.New(db, mesh(t, inv, "ipam"), p, cfg, nil, nil)
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	devs, _ := db.HostDevices(ctx, tenantA)
	if len(devs) != 1 || devs[0].Name != "web-01" || devs[0].PrimaryIP != "10.20.0.5" || devs[0].Source != store.SrcHostReport {
		t.Fatalf("device %+v", devs)
	}
	dev := devs[0]
	ifs, _ := db.ListInterfaces(ctx, tenantA, dev.ID)
	if len(ifs) != 1 || ifs[0].Name != "eth0" || ifs[0].ReportState != store.RepReported {
		t.Fatalf("interfaces (docker0 excluded) %+v", ifs)
	}
	a, err := db.FindAddress(ctx, tenantA, "10.20.0.5")
	if err != nil || a.DeviceID != dev.ID || !a.IsPrimary || a.InterfaceName != "eth0" {
		t.Fatalf("address %+v %v", a, err)
	}
	sub, err := db.GetSubnet(ctx, tenantA, a.SubnetID)
	if err != nil || sub.CIDR != "10.20.0.0/24" || sub.Origin != store.OriginHostSync {
		t.Fatalf("auto subnet %+v %v", sub, err)
	}
	if _, err := db.FindAddress(ctx, tenantA, "172.17.0.1"); err == nil {
		t.Fatal("excluded bridge address recorded")
	}
	for _, act := range []string{"device_created", "interface_created", "subnet_created", "address_created", "hostsync_run"} {
		if countAudit(t, db, tenantA, act) == 0 {
			t.Errorf("audit %s", act)
		}
	}
	if st, err := db.GetHostSyncDeviceState(ctx, tenantA, dev.ID); err != nil || st.Trigger != "poll" || st.Changes == 0 {
		t.Fatalf("device state %+v %v", st, err)
	}

	// Address change on the host: new address recorded, old one released.
	inv.put(report(tenantA, hostA, "web-01", 2_000_000, 2, "10.20.0.6/24"))
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	old, _ := db.FindAddress(ctx, tenantA, "10.20.0.5")
	neu, _ := db.FindAddress(ctx, tenantA, "10.20.0.6")
	if old.DeviceID != "" || old.ReportState != store.RepNotReported || old.PreviousDeviceID != dev.ID || neu.DeviceID != dev.ID {
		t.Fatalf("release/move: %+v %+v", old, neu)
	}
	if countAudit(t, db, tenantA, "address_released") != 1 {
		t.Fatal("release audited")
	}
	// Tenant B sees nothing of tenant A.
	if l, _ := db.ListDevices(ctx, tenantB, store.DeviceFilter{}); len(l) != 0 {
		t.Fatal("tenant B sees tenant A devices")
	}
	if _, err := db.FindAddress(ctx, tenantB, "10.20.0.6"); err == nil {
		t.Fatal("tenant B sees tenant A address")
	}

	// A report failing mid-apply leaves nothing behind (audit included), and a
	// disabled tenant writes nothing (SR-006).
	s, _ := db.EnsureHostSyncSettings(ctx, tenantA)
	before := countAudit(t, db, tenantA, "address_created")
	boom := errors.New("boom")
	err = db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		if err := tx.AppendAudit(store.AuditRow{Action: "address_created", ActorKind: "system", ActorID: "hostsync"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || countAudit(t, db, tenantA, "address_created") != before {
		t.Fatal("audit rolled back with the failed apply")
	}
	s.Enabled = false
	_ = db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated", ActorKind: "user"})
	inv.put(report(tenantA, hostA, "web-01", 3_000_000, 3, "10.20.0.7/24"))
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FindAddress(ctx, tenantA, "10.20.0.7"); err == nil {
		t.Fatal("disabled tenant written")
	}
	// Re-enabling applies the latest report (reconcile requested).
	s.Enabled = true
	_ = db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated", ActorKind: "user"})
	if err := r.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if a, err := db.FindAddress(ctx, tenantA, "10.20.0.7"); err != nil || a.DeviceID != dev.ID {
		t.Fatalf("re-enable applies the latest report: %+v %v", a, err)
	}
	if len(p.evs) == 0 {
		t.Fatal("events published after commit")
	}

	// Two replicas applying the same new host concurrently: the advisory lock
	// serialises them and exactly one device results.
	inv.put(report(tenantA, "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa02", "web-02", 4_000_000, 4, "10.30.0.5/24"))
	full, _ := inv.GetHostReport(ctx, &invv1.GetHostReportRequest{TenantId: tenantA, HostId: "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaa02"})
	s, _ = db.EnsureHostSyncSettings(ctx, tenantA)
	m := mesh(t, inv, "ipam")
	rep, err := m.GetHostReport(ctx, tenantA, full.GetHost().GetId())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other := hostsync.New(db, m, &pub{}, cfg, nil, nil)
			_, errs[i] = other.Apply(ctx, s, rep, "poll", fmt.Sprint("run-", i))
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent applies: %v", errs)
	}
	if l, _ := db.ListDevices(ctx, tenantA, store.DeviceFilter{Query: "web-02"}); len(l) != 1 {
		t.Fatalf("exactly one device: %d", len(l))
	}
}
