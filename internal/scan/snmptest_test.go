package scan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func testSvc(t *testing.T, disc snmp.Discoverer) (*Service, *memstore.Mem) {
	t.Helper()
	m := memstore.New()
	clk := &clock{t: time.Now().UTC()}
	m.Now = clk.now
	for _, s := range []store.Subnet{
		{ID: "root", TenantID: "t1", Name: "root", CIDR: "10.1.0.0/16"},
		{ID: "s1", TenantID: "t1", Name: "mgmt", CIDR: "10.1.112.0/24", ParentID: "root"},
		{ID: "bare", TenantID: "t1", Name: "bare", CIDR: "192.168.5.0/24"},
		{ID: "v6", TenantID: "t1", Name: "v6", CIDR: "2001:db8::/64", ParentID: ""},
	} {
		if err := m.CreateSubnet(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	return newService(m, icmp.NewFake(), disc, &recPub{}, testConfig(), clk), m
}

// TestCredentialsOutcomes (T039): every outcome category, with the
// inherited source recorded and an audit row per test.
func TestCredentialsOutcomes(t *testing.T) {
	ctx := context.Background()
	disc := snmp.NewFake()
	disc.Set("10.1.112.20", snmp.DiscoveredDevice{SysName: "sw-core-1", SysDescr: "Cisco IOS " + strings.Repeat("x", 400)})
	disc.Fail("10.1.112.21", gosnmp.ErrWrongDigest)
	disc.Fail("10.1.112.22", gosnmp.ErrUnknownUsername)
	disc.Fail("10.1.112.23", gosnmp.ErrDecryption)
	disc.Fail("10.1.112.25", errors.New("odd failure echoing root-comm"))
	svc, m := testSvc(t, disc)
	setCreds(t, m, "t1", "root", snmpcred.Input{Version: 2, Community: "root-comm"})
	for addr, want := range map[string]string{
		"10.1.112.20": "ok", "10.1.112.21": "auth_failed", "10.1.112.22": "unknown_user",
		"10.1.112.23": "privacy_failed", "10.1.112.24": "no_response", "10.1.112.25": "error",
	} {
		res, err := svc.TestCredentials(ctx, adminSubj("t1"), "s1", addr)
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if res.Outcome != want || res.SourceSubnetID != "root" {
			t.Errorf("%s: outcome %q source %q, want %q", addr, res.Outcome, res.SourceSubnetID, want)
		}
		if want == "error" && (res.Detail == "" || strings.Contains(res.Detail, "root-comm")) {
			t.Errorf("error detail %q must be present and scrubbed", res.Detail)
		}
		if want != "error" && res.Detail != "" {
			t.Errorf("%s: unexpected detail %q", addr, res.Detail)
		}
		if want == "ok" && (res.SysName != "sw-core-1" || len([]rune(res.SysDescr)) != 256) {
			t.Errorf("ok result %q / %d chars", res.SysName, len(res.SysDescr))
		}
	}
	var tested int
	for _, a := range m.Audit() {
		if a.Action != "snmp_credentials_tested" {
			continue
		}
		tested++
		if a.SubjectID != "s1" || a.Target == "" || a.Detail["target"] != a.Target || a.Detail["source_subnet_id"] != "root" || a.Detail["outcome"] == nil {
			t.Fatalf("audit row %+v", a)
		}
		if (a.Detail["outcome"] == "ok") != (a.Outcome == "ok") {
			t.Fatalf("audit outcome %q for %v", a.Outcome, a.Detail["outcome"])
		}
		if strings.Contains(a.Target+a.Reason, "root-comm") {
			t.Fatal("audit carries the community")
		}
	}
	if tested != 6 {
		t.Fatalf("audit rows %d", tested)
	}
}

func TestCredentialsNoneAndUnreadable(t *testing.T) {
	ctx := context.Background()
	svc, m := testSvc(t, snmp.NewFake())
	res, err := svc.TestCredentials(ctx, adminSubj("t1"), "bare", "192.168.5.9")
	if err != nil || res.Outcome != "no_credentials" || res.SourceSubnetID != "" {
		t.Fatalf("no creds: %+v %v", res, err)
	}
	// A blob sealed with another KEK cannot be opened.
	setCreds(t, m, "t1", "bare", snmpcred.Input{Version: 2, Community: "c"})
	svc.SetEnvelope(nil)
	res, err = svc.TestCredentials(ctx, adminSubj("t1"), "bare", "192.168.5.9")
	if err != nil || res.Outcome != "credentials_unreadable" || res.SourceSubnetID != "bare" {
		t.Fatalf("unreadable: %+v %v", res, err)
	}
	m.FailNext("GetSubnetSNMP")
	svc.SetEnvelope(testEnv)
	if res, _ := svc.TestCredentials(ctx, adminSubj("t1"), "bare", "192.168.5.9"); res.Outcome != "credentials_unreadable" {
		t.Fatalf("row read failure: %+v", res)
	}
}

// TestCredentialsTargetConfined (SR-003): only usable addresses inside the
// subnet; the network and broadcast addresses and names are refused.
func TestCredentialsTargetConfined(t *testing.T) {
	ctx := context.Background()
	disc := snmp.NewFake()
	svc, m := testSvc(t, disc)
	setCreds(t, m, "t1", "s1", snmpcred.Input{Version: 2, Community: "c"})
	for _, addr := range []string{"10.1.113.5", "10.1.112.0", "10.1.112.255", "192.168.5.9", "sw.example.org", "", "10.1.112.020", "2001:db8::1"} {
		_, err := svc.TestCredentials(ctx, adminSubj("t1"), "s1", addr)
		var fe *snmpcred.FieldError
		if !errors.As(err, &fe) || fe.Field != "address" {
			t.Errorf("%q: want address field error, got %v", addr, err)
		}
	}
	if len(disc.Seen()) != 0 {
		t.Fatal("a refused target was probed")
	}
	if _, err := svc.TestCredentials(ctx, adminSubj("t1"), "missing", "10.1.112.5"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing subnet: %v", err)
	}
	if _, err := svc.TestCredentials(ctx, authz.Subjects{}, "s1", "10.1.112.5"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("no tenant: %v", err)
	}
	if _, err := svc.TestCredentials(ctx, adminSubj("t2"), "s1", "10.1.112.5"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	m.FailNext("AllSubnetCIDRs")
	if _, err := svc.TestCredentials(ctx, adminSubj("t1"), "s1", "10.1.112.5"); err == nil {
		t.Fatal("resolution failure")
	}
	// IPv6 targets inside an IPv6 subnet are probed.
	setCreds(t, m, "t1", "v6", snmpcred.Input{Version: 2, Community: "c"})
	if res, err := svc.TestCredentials(ctx, adminSubj("t1"), "v6", "2001:db8::10"); err != nil || res.Outcome != "no_response" {
		t.Fatalf("ipv6: %+v %v", res, err)
	}
}

// blockingDisc records the probe deadline; with block it hangs until the
// context ends (a silent agent).
type blockingDisc struct {
	snmp.Fake
	block    bool
	deadline time.Time
}

func (b *blockingDisc) Probe(ctx context.Context, _ string, _ snmp.Creds) (string, string, error) {
	b.deadline, _ = ctx.Deadline()
	if b.block {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	return "", "", context.DeadlineExceeded
}

// TestCredentialsDeadline (FR-020, SC-006): the probe runs under every SNMP
// attempt (timeout × (retries+1)) plus 2 seconds and a hung agent counts as no
// response. The SNMP timeout is its own setting (default 5 s), not the ICMP one.
func TestCredentialsDeadline(t *testing.T) {
	disc := &blockingDisc{}
	svc, m := testSvc(t, disc)
	setCreds(t, m, "t1", "s1", snmpcred.Input{Version: 2, Community: "c"})
	start := time.Now()
	if res, err := svc.TestCredentials(context.Background(), adminSubj("t1"), "s1", "10.1.112.9"); err != nil || res.Outcome != "no_response" {
		t.Fatalf("timeout: %+v %v", res, err)
	}
	want := start.Add(2*5*time.Second + 2*time.Second)
	if d := disc.deadline.Sub(want); d > 200*time.Millisecond || d < -200*time.Millisecond {
		t.Fatalf("probe deadline off by %v", d)
	}
	disc.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start = time.Now()
	res, err := svc.TestCredentials(ctx, adminSubj("t1"), "s1", "10.1.112.9")
	if err != nil || res.Outcome != "no_response" || time.Since(start) > time.Second {
		t.Fatalf("hung agent: %+v %v after %v", res, err, time.Since(start))
	}
}

// TestStartScanSNMPAuto: a scan request that does not choose SNMP (the subnet
// drawer's quick scan) runs SNMP discovery exactly when the subnet has
// effective credentials, own or inherited; an explicit false stays off.
func TestStartScanSNMPAuto(t *testing.T) {
	ctx := context.Background()
	svc, m := testSvc(t, snmp.NewFake())
	setCreds(t, m, "t1", "root", snmpcred.Input{Version: 2, Community: "root-comm"})
	inherited, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{SNMPAuto: true})
	if err != nil || !inherited.EnableSNMP {
		t.Fatalf("auto with inherited credentials: %+v %v", inherited.EnableSNMP, err)
	}
	bare, err := svc.StartScan(ctx, adminSubj("t1"), "bare", Options{SNMPAuto: true})
	if err != nil || bare.EnableSNMP {
		t.Fatalf("auto without credentials: %+v %v", bare.EnableSNMP, err)
	}
	if _, err := svc.CancelScan(ctx, adminSubj("t1"), inherited.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	off, err := svc.StartScan(ctx, adminSubj("t1"), "s1", Options{})
	if err != nil || off.EnableSNMP {
		t.Fatalf("explicit off: %+v %v", off.EnableSNMP, err)
	}
}
