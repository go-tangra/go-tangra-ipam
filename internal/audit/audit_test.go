package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// fakeStore captures appended audit rows and can be told to fail.
type fakeStore struct {
	mu      sync.Mutex
	rows    []store.AuditRow
	failNow bool
	block   chan struct{} // if non-nil, AppendAudit blocks until closed
}

func (f *fakeStore) AppendAudit(ctx context.Context, row store.AuditRow) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNow {
		return errors.New("boom")
	}
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeStore) snapshot() []store.AuditRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.AuditRow, len(f.rows))
	copy(out, f.rows)
	return out
}

func validEvent() Event {
	return Event{
		TenantID:    "t1",
		EventType:   SubnetCreated,
		ActorKind:   ActorUser,
		ActorID:     "u1",
		SubjectKind: SubjectSubnet,
		SubjectID:   "s1",
		Target:      "10.0.0.0/24",
		Outcome:     OutcomeOK,
	}
}

func TestKnown(t *testing.T) {
	if !Known(string(AddressAllocated)) {
		t.Fatal("address_allocated should be known")
	}
	if !Known(string(PowerAction)) || !Known(string(KVMSessionStarted)) {
		t.Fatal("power/kvm actions should be known")
	}
	if Known("not_a_real_event") {
		t.Fatal("bogus event should not be known")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{"ok", func(e *Event) {}, false},
		{"unknown type", func(e *Event) { e.EventType = "nope" }, true},
		{"missing tenant", func(e *Event) { e.TenantID = "" }, true},
		{"bad actor", func(e *Event) { e.ActorKind = "robot" }, true},
		{"bad subject", func(e *Event) { e.SubjectKind = "planet" }, true},
		{"bad outcome", func(e *Event) { e.Outcome = "maybe" }, true},
		{"power subject ok", func(e *Event) { e.EventType = PowerAction; e.SubjectKind = SubjectPower }, false},
		{"kvm subject ok", func(e *Event) { e.EventType = KVMSessionStarted; e.SubjectKind = SubjectKVM }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := validEvent()
			tc.mutate(&e)
			err := Validate(e)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestRecordAndFlush(t *testing.T) {
	fs := &fakeStore{}
	w := NewWriter(fs, nil)
	defer w.Close()

	e := validEvent()
	e.Details = map[string]any{"note": "hello", "count": 3}
	if err := w.Record(context.Background(), e); err != nil {
		t.Fatalf("Record: %v", err)
	}
	w.Flush(context.Background())

	rows := fs.snapshot()
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.TenantID != "t1" || r.Action != string(SubnetCreated) || r.Outcome != OutcomeOK {
		t.Fatalf("unexpected row: %+v", r)
	}
	if r.Target != "10.0.0.0/24" {
		t.Fatalf("target not preserved: %+v", r)
	}
	if r.ID == "" || r.At.IsZero() {
		t.Fatalf("row missing ID/At: %+v", r)
	}
	if r.Detail["note"] != "hello" {
		t.Fatalf("detail not preserved: %+v", r.Detail)
	}
}

func TestRecordInvalidNotQueued(t *testing.T) {
	fs := &fakeStore{}
	w := NewWriter(fs, nil)
	defer w.Close()
	bad := validEvent()
	bad.EventType = "bogus"
	if err := w.Record(context.Background(), bad); err == nil {
		t.Fatal("expected error for invalid event")
	}
	w.Flush(context.Background())
	if fs.count() != 0 {
		t.Fatalf("invalid event should not be stored, got %d", fs.count())
	}
}

func TestRedaction(t *testing.T) {
	fs := &fakeStore{}
	w := NewWriter(fs, nil)
	defer w.Close()

	long := strings.Repeat("x", 300)
	e := validEvent()
	e.Details = map[string]any{
		"snmp_secret_ref": "w-123",
		"credential":      "creds",
		"ipmi_password":   "pw",
		"owner":           "alice",
		"contact_email":   "a@b.c",
		"kept":            "value",
		"long":            long,
		"nested": map[string]any{
			"snmp_community": "public",
			"ok":             "yes",
		},
		"list": []any{
			map[string]any{"password": "drop", "keep": "in"},
		},
	}
	if err := w.Record(context.Background(), e); err != nil {
		t.Fatalf("Record: %v", err)
	}
	w.Flush(context.Background())

	d := fs.snapshot()[0].Detail
	for _, k := range []string{"snmp_secret_ref", "credential", "ipmi_password", "owner", "contact_email"} {
		if _, ok := d[k]; ok {
			t.Fatalf("forbidden key %q not dropped: %+v", k, d)
		}
	}
	if d["kept"] != "value" {
		t.Fatalf("safe key dropped: %+v", d)
	}
	if s, ok := d["long"].(string); !ok || len(s) != 256 {
		t.Fatalf("long string not truncated to 256: %+v", d["long"])
	}
	nested, ok := d["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested map missing: %+v", d)
	}
	if _, ok := nested["snmp_community"]; ok {
		t.Fatalf("nested forbidden key not dropped: %+v", nested)
	}
	if nested["ok"] != "yes" {
		t.Fatalf("nested safe key dropped: %+v", nested)
	}
	list, ok := d["list"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list not preserved: %+v", d["list"])
	}
	elem := list[0].(map[string]any)
	if _, ok := elem["password"]; ok {
		t.Fatalf("forbidden key in list element not dropped: %+v", elem)
	}
	if elem["keep"] != "in" {
		t.Fatalf("safe key in list element dropped: %+v", elem)
	}
}

func TestDroppedOnOverflow(t *testing.T) {
	fs := &fakeStore{block: make(chan struct{})}
	var mu sync.Mutex
	var errCount int
	w := newWriter(fs, func(error) { mu.Lock(); errCount++; mu.Unlock() }, 2)
	w.start()

	dropped := false
	for i := 0; i < 50; i++ {
		_ = w.Record(context.Background(), validEvent())
		if w.Dropped() > 0 {
			dropped = true
			break
		}
	}
	if !dropped {
		t.Fatal("expected some events to be dropped on overflow")
	}
	mu.Lock()
	ec := errCount
	mu.Unlock()
	if ec < 1 {
		t.Fatal("onError should have fired on overflow")
	}
	close(fs.block)
	w.Close()
}

func TestRecordAfterClose(t *testing.T) {
	fs := &fakeStore{}
	w := NewWriter(fs, nil)
	w.Close()
	w.Close() // idempotent
	before := w.Dropped()
	if err := w.Record(context.Background(), validEvent()); err == nil {
		t.Fatal("Record after Close should error")
	}
	if w.Dropped() != before+1 {
		t.Fatalf("Dropped should increment after close, got %d", w.Dropped())
	}
	w.Flush(context.Background())
}

func TestWriteRowError(t *testing.T) {
	fs := &fakeStore{failNow: true}
	var mu sync.Mutex
	var gotErr error
	w := NewWriter(fs, func(e error) { mu.Lock(); gotErr = e; mu.Unlock() })
	defer w.Close()
	if err := w.Record(context.Background(), validEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	w.Flush(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		e := gotErr
		mu.Unlock()
		if e != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("onError never fired for store failure")
}

func TestFlushCanceled(t *testing.T) {
	fs := &fakeStore{}
	w := NewWriter(fs, nil)
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Flush(ctx)
}

func TestHostSyncVocabulary(t *testing.T) {
	for _, et := range []EventType{InterfaceCreated, InterfaceUpdated, InterfaceNotReported, AddressMoved,
		AddressReleased, AddressConflict, AddressConflictCleared, PackagesUpdated, HypervisorLinked,
		HypervisorUnlinked, PortLinked, PortUnlinked, DeviceNotReported, HostSyncSettingsUpdated,
		HostSyncResyncRequested, HostSyncRun} {
		if !Known(string(et)) {
			t.Errorf("%s not known", et)
		}
	}
	for _, sk := range []string{SubjectPackage, SubjectHostSync, SubjectInterface} {
		e := Event{TenantID: "t", EventType: HostSyncRun, ActorKind: ActorSystem, ActorID: HostSyncActor, SubjectKind: sk, Outcome: OutcomeOK}
		if err := Validate(e); err != nil {
			t.Errorf("subject %s: %v", sk, err)
		}
	}
}

func TestRowGuardsHostSyncDetail(t *testing.T) {
	at := time.Unix(10, 0)
	row, err := Row(Event{
		TenantID: "t", EventType: AddressMoved, ActorKind: ActorSystem, ActorID: HostSyncActor,
		SubjectKind: SubjectAddress, SubjectID: "a", Outcome: OutcomeOK,
		Details: map[string]any{
			"previous_device_id": "d1", "bmc_address": "10.0.0.9", "inventory_host_id": "h",
			"changes":        map[string]any{"hostname": map[string]any{"before": "a", "after": "b"}},
			"previous_owner": "dropped", "ipmi_address": "dropped",
		},
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"previous_device_id", "bmc_address", "inventory_host_id", "changes"} {
		if _, ok := row.Detail[k]; !ok {
			t.Errorf("%s must survive the guard", k)
		}
	}
	for _, k := range []string{"previous_owner", "ipmi_address"} {
		if _, ok := row.Detail[k]; ok {
			t.Errorf("%s must be dropped by the guard", k)
		}
	}
	if row.At != at || row.ID == "" || row.Action != "address_moved" || row.ActorID != "hostsync" {
		t.Fatalf("row %+v", row)
	}
	if _, err := Row(Event{EventType: "nope"}, at); err == nil {
		t.Fatal("unknown type")
	}
}

// TestSNMPCredentialEvents (021, contracts/audit-events.md): the four event
// types are known; their neutral detail keys survive the guard while any
// credential-looking key is dropped.
func TestSNMPCredentialEvents(t *testing.T) {
	for _, et := range []EventType{SNMPCredentialsSet, SNMPCredentialsReplaced, SNMPCredentialsCleared, SNMPCredentialsTested} {
		if !Known(string(et)) {
			t.Fatalf("%s unknown", et)
		}
	}
	row, err := Row(Event{TenantID: "t1", EventType: SNMPCredentialsTested, ActorKind: ActorUser, ActorID: "u1",
		SubjectKind: SubjectSubnet, SubjectID: "s1", Target: "10.0.0.5", Outcome: OutcomeError,
		Details: map[string]any{"protocol_version": 3, "security_level": "authPriv", "previous_version": 2,
			"target": "10.0.0.5", "outcome": "auth_failed", "source_subnet_id": "s0",
			"community": "c0mm", "auth_password": "p", "snmp_user": "u", "credential": "x"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"protocol_version", "security_level", "previous_version", "target", "outcome", "source_subnet_id"} {
		if _, ok := row.Detail[k]; !ok {
			t.Errorf("neutral key %s dropped", k)
		}
	}
	for _, k := range []string{"community", "auth_password", "snmp_user", "credential"} {
		if _, ok := row.Detail[k]; ok {
			t.Errorf("guarded key %s kept", k)
		}
	}
}

// TestARPEvents (022, contracts/ipam-http.md): the ARP event types are known,
// the tenant subject validates and the neutral MAC keys survive the guard.
func TestARPEvents(t *testing.T) {
	for _, et := range []EventType{MACLearned, MACChanged, MACConflict, ARPRun, ARPSettingsUpdated, AddressCreated} {
		if !Known(string(et)) {
			t.Fatalf("%s unknown", et)
		}
	}
	row, err := Row(Event{TenantID: "t1", EventType: MACChanged, ActorKind: ActorSystem, ActorID: ScanActor,
		SubjectKind: SubjectAddress, SubjectID: "a1", Outcome: OutcomeOK,
		Details: map[string]any{"address": "10.0.0.5", "mac": "0a:5c:d2:f1:00:01", "previous_mac": "0a:5c:d2:f1:00:02",
			"observed_mac": "0a:5c:d2:f1:00:03", "source_device_id": "r1", "job_id": "j1", "snmp_community": "x"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"address", "mac", "previous_mac", "observed_mac", "source_device_id", "job_id"} {
		if _, ok := row.Detail[k]; !ok {
			t.Errorf("neutral key %s dropped", k)
		}
	}
	if _, ok := row.Detail["snmp_community"]; ok {
		t.Error("guarded key kept")
	}
	if err := Validate(Event{TenantID: "t1", EventType: ARPSettingsUpdated, ActorKind: ActorUser, ActorID: "u1",
		SubjectKind: SubjectTenant, SubjectID: "t1", Outcome: OutcomeOK}); err != nil {
		t.Fatalf("tenant subject: %v", err)
	}
	if ScanActor != "scan" {
		t.Fatalf("scan actor %q", ScanActor)
	}
}

// TestBMCReferenceEvents (024 T006): the reference events are in the closed
// vocabulary and their neutral detail keys survive the guard, while a
// credential-named key is dropped.
func TestBMCReferenceEvents(t *testing.T) {
	for _, et := range []EventType{BMCReferenceSet, BMCReferenceChanged, BMCReferenceCleared} {
		if !Known(string(et)) {
			t.Fatalf("%s not known", et)
		}
		row, err := Row(Event{TenantID: "t", EventType: et, ActorKind: ActorUser, ActorID: "u",
			SubjectKind: SubjectDevice, SubjectID: "d", Outcome: OutcomeOK,
			Details: map[string]any{"reference": "r2", "previous_reference": "r1", "reference_name": "zax-5 IPMI", "ipmi_password": "x"}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if row.Detail["reference"] != "r2" || row.Detail["previous_reference"] != "r1" || row.Detail["reference_name"] != "zax-5 IPMI" {
			t.Fatalf("neutral keys dropped: %v", row.Detail)
		}
		if _, ok := row.Detail["ipmi_password"]; ok {
			t.Fatalf("guarded key kept: %v", row.Detail)
		}
	}
	if BMCReferenceSet != "bmc_reference_set" || BMCReferenceChanged != "bmc_reference_changed" || BMCReferenceCleared != "bmc_reference_cleared" {
		t.Fatal("event names changed")
	}
}
