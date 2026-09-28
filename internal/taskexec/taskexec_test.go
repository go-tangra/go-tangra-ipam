package taskexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra/v4/authn"
	"github.com/go-tangra/go-tangra/v4/identity"

	schedulerv1 "github.com/go-tangra/go-tangra-scheduler/sdk/v4/api/proto/scheduler/v1"
	schedexec "github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/taskexec"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tenantA = "0190a0b0-0000-7000-8000-00000000000a"
	tenantB = "0190a0b0-0000-7000-8000-00000000000b"
	actor   = "spiffe://example.org/svc/scheduler"
)

type kit struct {
	mem  *memstore.Mem
	scan *scan.Service
	exec *Executor
}

func newKit(t *testing.T) kit {
	t.Helper()
	mem := memstore.New()
	svc := scan.New(mem, nil, nil, nil, events.HubPublisher{}, scan.Config{
		MaxHosts: 1024, Concurrency: 1, TimeoutMs: 1000, Workers: 1, MaxRetries: 0,
	}, nil)
	return kit{mem: mem, scan: svc, exec: New(mem, svc, actor)}
}

func (k kit) subnet(t *testing.T, tenant, cidr string, version int) string {
	t.Helper()
	id := store.NewID()
	if err := k.mem.CreateSubnet(context.Background(), store.Subnet{
		ID: id, TenantID: tenant, Name: "net-" + cidr, CIDR: cidr, IPVersion: version, Status: store.SubnetActive,
	}); err != nil {
		t.Fatalf("create subnet: %v", err)
	}
	return id
}

func req(tenant, payload string) schedexec.Request {
	return schedexec.Request{
		ExecutionID: "0190a0b0-0000-7000-8000-0000000000e1", Type: TypeScanNetwork,
		TenantID: tenant, Payload: json.RawMessage(payload), Attempt: 1, MaxAttempts: 3,
	}
}

func data(t *testing.T, r schedexec.Result) Summary {
	t.Helper()
	s, ok := r.Data.(Summary)
	if !ok {
		t.Fatalf("data = %#v, want Summary", r.Data)
	}
	return s
}

func (k kit) jobs(t *testing.T, tenant string) []store.IPScanJob {
	t.Helper()
	js, err := k.mem.ListScanJobs(context.Background(), tenant, store.ScanFilter{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return js
}

func TestScanBySubnetID(t *testing.T) {
	k := newKit(t)
	id := k.subnet(t, tenantA, "10.0.0.0/24", 4)
	r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+id+`","enableSnmp":true,"enableDnsUpdate":true}`))
	if !r.Success || r.Permanent || r.Message != "queued 1 scan(s), skipped 0" {
		t.Fatalf("result = %+v", r)
	}
	s := data(t, r)
	if s.Queued != 1 || s.Skipped != 0 || s.Failed != 0 || len(s.JobIDs) != 1 {
		t.Fatalf("summary = %+v", s)
	}
	js := k.jobs(t, tenantA)
	if len(js) != 1 {
		t.Fatalf("jobs = %d", len(js))
	}
	j := js[0]
	if j.ID != s.JobIDs[0] || j.SubnetID != id || !j.EnableSNMP || !j.EnableDNSUpdate ||
		j.TriggeredBy != store.TriggerAuto || j.CreatedBy != actor {
		t.Fatalf("job = %+v", j)
	}
	rows := k.mem.Audit()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d", len(rows))
	}
	a := rows[0]
	if a.Action != string(audit.ScanStarted) || a.ActorKind != audit.ActorService || a.ActorID != actor ||
		a.TenantID != tenantA || a.SubjectKind != audit.SubjectScan || a.SubjectID != j.ID || a.Outcome != audit.OutcomeOK ||
		a.Detail["trigger"] != "scheduler" || a.Detail["execution_id"] != "0190a0b0-0000-7000-8000-0000000000e1" ||
		a.Detail["subnet_id"] != id {
		t.Fatalf("audit = %+v", a)
	}
}

func TestSNMPChoice(t *testing.T) {
	k := newKit(t)
	auto := k.subnet(t, tenantA, "10.0.1.0/24", 4)
	off := k.subnet(t, tenantA, "10.0.2.0/24", 4)
	if r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+auto+`"}`)); !r.Success {
		t.Fatalf("auto: %+v", r)
	}
	if r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+off+`","enableSnmp":false}`)); !r.Success {
		t.Fatalf("off: %+v", r)
	}
	for _, j := range k.jobs(t, tenantA) {
		if j.EnableSNMP || j.EnableDNSUpdate {
			t.Fatalf("job %s: snmp=%v dns=%v, want off (no credentials, not chosen)", j.SubnetID, j.EnableSNMP, j.EnableDNSUpdate)
		}
	}
}

func TestScanByCIDR(t *testing.T) {
	k := newKit(t)
	k.subnet(t, tenantA, "10.1.0.0/24", 4)
	want := k.subnet(t, tenantA, "10.2.0.0/24", 4)
	// Host bits and surrounding space canonicalise to the subnet's network.
	r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"cidr":" 10.2.0.7/24 "}`))
	if !r.Success || r.Message != "queued 1 scan(s), skipped 0" {
		t.Fatalf("result = %+v", r)
	}
	if js := k.jobs(t, tenantA); len(js) != 1 || js[0].SubnetID != want {
		t.Fatalf("jobs = %+v", js)
	}
}

func TestScanByCIDRNoMatch(t *testing.T) {
	k := newKit(t)
	k.subnet(t, tenantA, "10.1.0.0/24", 4)
	k.subnet(t, tenantA, "not-a-cidr", 4) // a corrupt row never matches
	for _, p := range []string{`{"cidr":"10.1.0.0/25"}`, `{"cidr":"10.9.0.0/24"}`} {
		r := k.exec.ScanNetwork(context.Background(), req(tenantA, p))
		if r.Success || !r.Permanent || r.Message != "no subnet found for cidr" {
			t.Fatalf("%s: result = %+v", p, r)
		}
	}
	r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"cidr":"10.1.0/24x"}`))
	if r.Success || !r.Permanent || r.Message != "cidr is not a valid CIDR" {
		t.Fatalf("bad cidr: %+v", r)
	}
}

func TestScanAll(t *testing.T) {
	for _, p := range []string{`{}`, `{"all":true}`, ``} {
		k := newKit(t)
		k.subnet(t, tenantA, "10.0.0.0/24", 4)
		k.subnet(t, tenantA, "10.0.1.0/24", 4)
		busy := k.subnet(t, tenantA, "10.0.2.0/24", 4)
		k.subnet(t, tenantA, "2001:db8::/64", 6)
		k.subnet(t, tenantA, "10.8.0.0/16", 4) // 65534 hosts > max_hosts
		k.subnet(t, tenantB, "10.0.0.0/24", 4)
		if _, err := k.scan.StartScan(context.Background(), authz.Subjects{TenantID: tenantA, ActorKind: authz.ActorService}, busy, scan.Options{}); err != nil {
			t.Fatalf("pre-scan: %v", err)
		}
		r := k.exec.ScanNetwork(context.Background(), req(tenantA, p))
		if !r.Success || r.Message != "queued 2 scan(s), skipped 3" {
			t.Fatalf("%q: result = %+v", p, r)
		}
		s := data(t, r)
		if s.Queued != 2 || s.Skipped != 3 || s.Failed != 0 || len(s.JobIDs) != 2 {
			t.Fatalf("%q: summary = %+v", p, s)
		}
		if n := len(k.jobs(t, tenantB)); n != 0 {
			t.Fatalf("%q: tenant B got %d jobs", p, n)
		}
		if n := len(k.mem.Audit()); n != 2 {
			t.Fatalf("%q: audit rows = %d", p, n)
		}
	}
}

func TestScanAllNoSubnets(t *testing.T) {
	k := newKit(t)
	k.subnet(t, tenantB, "10.0.0.0/24", 4)
	r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"all":true}`))
	if !r.Success || r.Message != "no subnets to scan" || r.Data != nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestSkippedSingleSubnet(t *testing.T) {
	k := newKit(t)
	v6 := k.subnet(t, tenantA, "2001:db8::/64", 6)
	big := k.subnet(t, tenantA, "10.8.0.0/16", 4)
	for _, id := range []string{v6, big} {
		r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+id+`"}`))
		if !r.Success || r.Message != "queued 0 scan(s), skipped 1" {
			t.Fatalf("result = %+v", r)
		}
	}
	if n := len(k.mem.Audit()); n != 0 {
		t.Fatalf("audit rows for skipped scans = %d", n)
	}
}

func TestUnknownSubnetAndTenantIsolation(t *testing.T) {
	k := newKit(t)
	foreign := k.subnet(t, tenantA, "10.0.0.0/24", 4)
	for _, id := range []string{store.NewID(), foreign} {
		r := k.exec.ScanNetwork(context.Background(), req(tenantB, `{"subnetId":"`+id+`"}`))
		if r.Success || !r.Permanent || r.Message != "subnet not found" {
			t.Fatalf("result = %+v", r)
		}
	}
	// By CIDR the foreign subnet is equally invisible.
	r := k.exec.ScanNetwork(context.Background(), req(tenantB, `{"cidr":"10.0.0.0/24"}`))
	if r.Success || !r.Permanent || r.Message != "no subnet found for cidr" {
		t.Fatalf("cidr: %+v", r)
	}
	if n := len(k.jobs(t, tenantA)) + len(k.jobs(t, tenantB)); n != 0 {
		t.Fatalf("jobs created: %d", n)
	}
}

func TestInvalidPayload(t *testing.T) {
	k := newKit(t)
	id := k.subnet(t, tenantA, "10.0.0.0/24", 4)
	cases := []struct{ payload, msg string }{
		{`{"subnetId":"` + id + `","cidr":"10.0.0.0/24"}`, "subnetId and cidr are mutually exclusive"},
		{`{"subnetId":"` + id + `","all":true}`, "all cannot be combined with subnetId or cidr"},
		{`{"cidr":"10.0.0.0/24","all":true}`, "all cannot be combined with subnetId or cidr"},
		{`{"subnetId":"not-a-uuid"}`, "subnetId must be a UUID"},
		{`{"surprise":1}`, "invalid payload"},
		{`{"all":"yes"}`, "invalid payload"},
		{`[]`, "invalid payload"},
	}
	for _, c := range cases {
		r := k.exec.ScanNetwork(context.Background(), req(tenantA, c.payload))
		if r.Success || !r.Permanent || !strings.HasPrefix(r.Message, c.msg) {
			t.Fatalf("%s: result = %+v, want %q", c.payload, r, c.msg)
		}
		if strings.Contains(r.Message, "not-a-uuid") {
			t.Fatalf("message echoes the payload: %q", r.Message)
		}
	}
}

func TestListFailureRetries(t *testing.T) {
	k := newKit(t)
	k.subnet(t, tenantA, "10.0.0.0/24", 4)
	for _, p := range []string{`{}`, `{"cidr":"10.0.0.0/24"}`} {
		k.mem.FailNext("AllSubnetCIDRs")
		r := k.exec.ScanNetwork(context.Background(), req(tenantA, p))
		if r.Success || r.Permanent || r.Message != "listing subnets failed" {
			t.Fatalf("%s: result = %+v", p, r)
		}
	}
}

// fakeScanner fails every scan with err.
type fakeScanner struct{ err error }

func (f fakeScanner) StartScan(context.Context, authz.Subjects, string, scan.Options) (store.IPScanJob, error) {
	return store.IPScanJob{}, f.err
}

func TestFailuresRetryWithBoundedNotes(t *testing.T) {
	mem := memstore.New()
	for i := 0; i < 7; i++ {
		if err := mem.CreateSubnet(context.Background(), store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: fmt.Sprintf("n%d", i), CIDR: fmt.Sprintf("10.0.%d.0/24", i), IPVersion: 4}); err != nil {
			t.Fatalf("create subnet: %v", err)
		}
	}
	e := New(mem, fakeScanner{err: errors.New("db: connection reset by peer 10.9.9.9")}, actor)
	r := e.ScanNetwork(context.Background(), req(tenantA, `{}`))
	if r.Success || r.Permanent {
		t.Fatalf("result = %+v", r)
	}
	if !strings.HasPrefix(r.Message, "queued 0 scan(s), skipped 0, failed 7 (") || strings.Count(r.Message, "internal error") != 5 ||
		!strings.Contains(r.Message, "…") || strings.Contains(r.Message, "10.9.9.9") {
		t.Fatalf("message = %q", r.Message)
	}
	if s := data(t, r); s.Failed != 7 || s.JobIDs == nil {
		t.Fatalf("summary = %+v", s)
	}

	// A subnet deleted between listing and scanning is a failure in "all" mode.
	e = New(mem, fakeScanner{err: fmt.Errorf("get subnet: %w", repo.ErrNotFound)}, actor)
	r = e.ScanNetwork(context.Background(), req(tenantA, `{}`))
	if r.Success || r.Permanent || !strings.Contains(r.Message, "subnet not found") {
		t.Fatalf("deleted: %+v", r)
	}

	// An authorization refusal is named without detail.
	e = New(mem, fakeScanner{err: fmt.Errorf("%w: tenant mismatch", authz.ErrForbidden)}, actor)
	r = e.ScanNetwork(context.Background(), req(tenantA, `{}`))
	if r.Success || r.Permanent || !strings.Contains(r.Message, "refused") || strings.Contains(r.Message, "mismatch") {
		t.Fatalf("refused: %+v", r)
	}

	// A single-subnet transient failure is retryable too.
	e = New(mem, fakeScanner{err: errors.New("boom")}, actor)
	r = e.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+store.NewID()+`"}`))
	if r.Success || r.Permanent || r.Message != "queued 0 scan(s), skipped 0, failed 1 (internal error)" {
		t.Fatalf("single: %+v", r)
	}
}

func TestAuditFailureDoesNotFailTheRun(t *testing.T) {
	k := newKit(t)
	id := k.subnet(t, tenantA, "10.0.0.0/24", 4)
	k.mem.FailNext("AppendAudit")
	if r := k.exec.ScanNetwork(context.Background(), req(tenantA, `{"subnetId":"`+id+`"}`)); !r.Success {
		t.Fatalf("result = %+v", r)
	}
}

func TestDescriptorsAndHandlers(t *testing.T) {
	ds := Descriptors()
	if len(ds) != 1 {
		t.Fatalf("descriptors = %d", len(ds))
	}
	d := ds[0]
	if d.Type != TypeScanNetwork || d.DisplayName != "Scan network" || d.DefaultCron != "0 3 * * *" ||
		d.DefaultMaxRetry != 2 || d.Platform || d.Description == "" {
		t.Fatalf("descriptor = %+v", d)
	}
	var schema struct {
		Type       string                     `json:"type"`
		Additional *bool                      `json:"additionalProperties"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(d.PayloadSchema), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.Type != "object" || schema.Additional == nil || *schema.Additional || len(schema.Properties) != 5 {
		t.Fatalf("schema = %+v", schema)
	}
	for _, f := range []string{"all", "subnetId", "cidr", "enableSnmp", "enableDnsUpdate"} {
		if _, ok := schema.Properties[f]; !ok {
			t.Fatalf("schema lacks %s", f)
		}
	}
	k := newKit(t)
	h := k.exec.Handlers()
	if len(h) != 1 || h[TypeScanNetwork] == nil {
		t.Fatalf("handlers = %v", h)
	}
}

func TestActor(t *testing.T) {
	if got := Actor("example.org", "scheduler"); got != actor {
		t.Fatalf("Actor = %q", got)
	}
	if got := Actor("", "scheduler"); got != "spiffe:///svc/scheduler" {
		t.Fatalf("Actor(empty td) = %q", got)
	}
}

func peer(t *testing.T, td, svc string) context.Context {
	t.Helper()
	id, err := identity.NewSPIFFEID(td, svc)
	if err != nil {
		t.Fatalf("spiffe id: %v", err)
	}
	return authn.WithPeer(context.Background(), authn.PeerIdentity{ID: id, ServiceName: svc})
}

func TestCaller(t *testing.T) {
	c := Caller("example.org")
	if svc, ok := c(peer(t, "example.org", "scheduler")); !ok || svc != "scheduler" {
		t.Fatalf("scheduler peer = %q %v", svc, ok)
	}
	if _, ok := c(peer(t, "other.org", "scheduler")); ok {
		t.Fatal("a foreign trust domain must be refused")
	}
	if _, ok := c(context.Background()); ok {
		t.Fatal("no peer must be refused")
	}
}

// TestThroughExecutorServer runs the handler behind the SDK server as app.go
// wires it: only the scheduler of the service's trust domain may call.
func TestThroughExecutorServer(t *testing.T) {
	k := newKit(t)
	id := k.subnet(t, tenantA, "10.0.0.0/24", 4)
	srv := schedexec.NewServer(k.exec.Handlers(), schedexec.Options{Caller: Caller("example.org")})
	in := &schedulerv1.ExecuteTaskRequest{
		ExecutionId: "0190a0b0-0000-7000-8000-0000000000e1", TaskType: TypeScanNetwork,
		TenantId: tenantA, Payload: []byte(`{"subnetId":"` + id + `"}`), Attempt: 1, MaxAttempts: 3,
	}
	if _, err := srv.ExecuteTask(peer(t, "example.org", "ipam"), in); err == nil {
		t.Fatal("a non-scheduler peer must be refused")
	}
	if _, err := srv.ExecuteTask(peer(t, "evil.org", "scheduler"), in); err == nil {
		t.Fatal("a scheduler of another trust domain must be refused")
	}
	out, err := srv.ExecuteTask(peer(t, "example.org", "scheduler"), in)
	if err != nil || !out.GetSuccess() {
		t.Fatalf("execute: %v %+v", err, out)
	}
	var s Summary
	if err := json.Unmarshal(out.GetResultData(), &s); err != nil || s.Queued != 1 || len(s.JobIDs) != 1 {
		t.Fatalf("result data %s: %v", out.GetResultData(), err)
	}
}
