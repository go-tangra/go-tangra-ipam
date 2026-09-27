package bmc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

const (
	tenant = "11111111-1111-7111-8111-111111111111"
	refA   = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0a"
	refB   = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0b"
	pass   = "bmc-P4ss-LEAK"
)

var (
	admin = authz.Subjects{TenantID: tenant, UserID: "u-admin", Roles: []string{"admin"}, ActorKind: authz.ActorUser}
	plain = authz.Subjects{TenantID: tenant, UserID: "u-plain", Roles: []string{"user"}, ActorKind: authz.ActorUser}
)

type fixture struct {
	svc *Service
	mem *memstore.Mem
	w   *warden.Fake
	ctx context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mem := memstore.New()
	w := warden.NewFake()
	w.Put(refA, warden.SecretMeta{Name: "zax-5 IPMI", Username: "ADMIN", FolderPath: "/infra/bmc", HostURL: "lanplus://10.1.112.14:6230"}, pass)
	w.Put(refB, warden.SecretMeta{Name: "other", Username: "root"}, "pw-b")
	s := New(mem, w)
	s.now = func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }
	return &fixture{svc: s, mem: mem, w: w, ctx: warden.WithUserToken(context.Background(), "tok-admin")}
}

func (f *fixture) device(t *testing.T, name, mgmt, ref string) string {
	t.Helper()
	id := store.NewID()
	if err := f.mem.CreateDevice(context.Background(), store.Device{ID: id, TenantID: tenant, Name: name, ManagementIP: mgmt, IPMISecretRef: ref, PrimaryIP: "10.1.111.20"}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddress(t *testing.T) {
	dev := store.Device{ID: "d1", ManagementIP: "10.0.0.9", PrimaryIP: "10.1.1.1"}
	bmcV6 := store.IPAddress{Address: "fd00::14", DeviceID: "d1", InterfaceName: "bmc", ReportState: store.RepReported}
	bmcV4 := store.IPAddress{Address: "10.1.112.14", DeviceID: "d1", InterfaceName: "bmc", ReportState: store.RepReported}
	bmc2 := store.IPAddress{Address: "10.1.112.15", DeviceID: "d1", InterfaceName: "bmc-2"}
	gone := store.IPAddress{Address: "10.1.112.16", DeviceID: "d1", InterfaceName: "bmc", ReportState: store.RepNotReported}
	eth := store.IPAddress{Address: "10.1.111.20", DeviceID: "d1", InterfaceName: "eth0"}
	other := store.IPAddress{Address: "10.1.112.99", DeviceID: "d2", InterfaceName: "bmc"}
	cases := []struct {
		name       string
		dev        store.Device
		addrs      []store.IPAddress
		addr, from string
	}{
		{"management ip wins", dev, []store.IPAddress{bmcV4}, "10.0.0.9", SourceManagementIP},
		{"reported bmc v4 before v6", store.Device{ID: "d1"}, []store.IPAddress{bmcV6, bmcV4, eth}, "10.1.112.14", SourceReported},
		{"bmc before bmc-2", store.Device{ID: "d1"}, []store.IPAddress{bmc2, bmcV6}, "fd00::14", SourceReported},
		{"bmc-2 when bmc is gone", store.Device{ID: "d1"}, []store.IPAddress{gone, bmc2}, "10.1.112.15", SourceReported},
		{"not reported ignored", store.Device{ID: "d1"}, []store.IPAddress{gone}, "", ""},
		{"primary ip never used", store.Device{ID: "d1", PrimaryIP: "10.1.111.20"}, []store.IPAddress{eth}, "", ""},
		{"other device ignored", store.Device{ID: "d1"}, []store.IPAddress{other}, "", ""},
		{"bad interface names ignored", store.Device{ID: "d1"}, []store.IPAddress{{Address: "10.9.9.9", DeviceID: "d1", InterfaceName: "bmc-1"}, {Address: "10.9.9.8", DeviceID: "d1", InterfaceName: "bmcx"}}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, s := Address(tc.dev, tc.addrs)
			if a != tc.addr || s != tc.from {
				t.Fatalf("Address = %q/%q, want %q/%q", a, s, tc.addr, tc.from)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t)
	none := f.device(t, "none", "", "")
	st, err := f.svc.Status(f.ctx, admin, none)
	if err != nil || st.Configured || st.Ready || st.Reason != ReasonNotConfigured || st.Access != "" || st.Secret != nil {
		t.Fatalf("none: %+v %v", st, err)
	}
	if len(f.w.Calls()) != 0 {
		t.Fatal("status without a reference called warden")
	}

	ok := f.device(t, "ok", "10.0.0.9", refA)
	st, err = f.svc.Status(f.ctx, admin, ok)
	if err != nil || !st.Configured || !st.Ready || st.Access != AccessOK || st.Reason != "" ||
		st.Secret == nil || st.Secret.Name != "zax-5 IPMI" || st.Secret.Username != "ADMIN" || st.Secret.FolderPath != "/infra/bmc" ||
		st.Address != "10.0.0.9" || st.AddressSource != SourceManagementIP || st.Reference != refA {
		t.Fatalf("ok: %+v %v", st, err)
	}

	noAddr := f.device(t, "noaddr", "", refA)
	st, _ = f.svc.Status(f.ctx, admin, noAddr)
	if st.Ready || st.Reason != ReasonNoAddress || st.Access != AccessOK {
		t.Fatalf("no address: %+v", st)
	}

	f.w.Deny("tok-admin", refA)
	st, _ = f.svc.Status(f.ctx, admin, ok)
	if st.Ready || st.Access != AccessForbidden || st.Reason != ReasonForbidden || st.Secret != nil {
		t.Fatalf("forbidden: %+v", st)
	}
	f.w.Remove(refB)
	gone := f.device(t, "gone", "10.0.0.8", refB)
	st, _ = f.svc.Status(f.ctx, admin, gone)
	if st.Access != AccessNotFound || st.Reason != ReasonSecretNotFound {
		t.Fatalf("not found: %+v", st)
	}
	f.w.SetUnavailable(true)
	st, _ = f.svc.Status(f.ctx, admin, gone)
	if st.Access != AccessUnavailable || st.Reason != ReasonWardenUnavailable {
		t.Fatalf("unavailable: %+v", st)
	}

	if _, err := f.svc.Status(f.ctx, admin, store.NewID()); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}
	other := admin
	other.TenantID = ""
	if _, err := f.svc.Status(f.ctx, other, ok); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("no tenant: %v", err)
	}
	f.mem.FailNext("AddressesForDevice")
	f.w.SetUnavailable(false)
	if _, err := f.svc.Status(f.ctx, admin, ok); err == nil {
		t.Fatal("address lookup failure ignored")
	}
}

func TestSetAndClear(t *testing.T) {
	f := newFixture(t)
	id := f.device(t, "node-1", "10.0.0.9", "")

	st, err := f.svc.Set(f.ctx, admin, id, refA)
	if err != nil || !st.Ready || st.Reference != refA || st.Secret.Name != "zax-5 IPMI" {
		t.Fatalf("set: %+v %v", st, err)
	}
	rows := f.mem.Audit()
	if len(rows) != 1 || rows[0].Action != string(audit.BMCReferenceSet) || rows[0].ActorID != "u-admin" ||
		rows[0].SubjectKind != audit.SubjectDevice || rows[0].SubjectID != id ||
		rows[0].Detail["reference"] != refA || rows[0].Detail["reference_name"] != "zax-5 IPMI" {
		t.Fatalf("set audit: %+v", rows)
	}

	// Same reference again: validated, no write, no row.
	if _, err := f.svc.Set(f.ctx, admin, id, strings.ToUpper(refA)); err != nil {
		t.Fatal(err)
	}
	if len(f.mem.Audit()) != 1 {
		t.Fatal("no-op set wrote a row")
	}

	if _, err := f.svc.Set(f.ctx, admin, id, refB); err != nil {
		t.Fatal(err)
	}
	rows = f.mem.Audit()
	if len(rows) != 2 || rows[1].Action != string(audit.BMCReferenceChanged) || rows[1].Detail["previous_reference"] != refA || rows[1].Detail["reference"] != refB {
		t.Fatalf("changed audit: %+v", rows)
	}

	// Refusals store nothing.
	f.w.Deny("tok-admin", refA)
	for name, tc := range map[string]struct {
		ctx  context.Context
		ref  string
		want error
	}{
		"forbidden":   {f.ctx, refA, warden.ErrForbidden},
		"no token":    {context.Background(), refA, warden.ErrNoUserToken},
		"not found":   {f.ctx, "01928f7e-3c1a-7b44-9d2e-000000000000", warden.ErrNotFound},
		"bad ref":     {f.ctx, "not-a-uuid", ErrInvalidReference},
		"empty":       {f.ctx, "", ErrInvalidReference},
		"missing dev": {f.ctx, refB, repo.ErrNotFound},
	} {
		devID := id
		if name == "missing dev" {
			devID = store.NewID()
		}
		if _, err := f.svc.Set(tc.ctx, admin, devID, tc.ref); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f.w.SetUnavailable(true)
	if _, err := f.svc.Set(f.ctx, admin, id, refA); !errors.Is(err, warden.ErrUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
	f.w.SetUnavailable(false)
	if d, _ := f.mem.GetDevice(context.Background(), tenant, id); d.IPMISecretRef != refB {
		t.Fatalf("refused set changed the device: %q", d.IPMISecretRef)
	}
	if len(f.mem.Audit()) != 2 {
		t.Fatalf("refusals wrote rows: %d", len(f.mem.Audit()))
	}
	fx := newFixture(t)
	d2 := fx.device(t, "n2", "10.0.0.2", "")
	fx.mem.FailNext("SetDeviceBMCRef")
	if _, err := fx.svc.Set(fx.ctx, admin, d2, refA); err == nil {
		t.Fatal("store failure ignored")
	}
	fx.mem.FailNext("AddressesForDevice")
	if _, err := fx.svc.Set(fx.ctx, admin, d2, refA); err == nil {
		t.Fatal("address failure ignored")
	}
	if _, err := fx.svc.Set(fx.ctx, authz.Subjects{UserID: "x"}, d2, refA); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("no tenant: %v", err)
	}

	// Clear.
	if err := f.svc.Clear(f.ctx, admin, id); err != nil {
		t.Fatal(err)
	}
	rows = f.mem.Audit()
	if rows[len(rows)-1].Action != string(audit.BMCReferenceCleared) || rows[len(rows)-1].Detail["previous_reference"] != refB {
		t.Fatalf("cleared audit: %+v", rows[len(rows)-1])
	}
	n := len(rows)
	if err := f.svc.Clear(f.ctx, admin, id); err != nil || len(f.mem.Audit()) != n {
		t.Fatalf("second clear: %v rows=%d", err, len(f.mem.Audit()))
	}
	if err := f.svc.Clear(f.ctx, admin, store.NewID()); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("clear missing: %v", err)
	}
	if err := f.svc.Clear(f.ctx, authz.Subjects{UserID: "x"}, id); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("clear no tenant: %v", err)
	}
}

func TestResolve(t *testing.T) {
	f := newFixture(t)
	ok := f.device(t, "ok", "10.0.0.9", refA)
	tg, err := f.svc.Resolve(f.ctx, admin, ok)
	if err != nil || tg.Address != "10.0.0.9" || tg.Device.ID != ok || tg.Creds.Password != pass {
		t.Fatalf("resolve: %v", err)
	}
	ic := tg.IPMI()
	if ic.Username != "ADMIN" || ic.Password != pass || ic.Protocol != "2.0" || ic.Port != 6230 {
		t.Fatalf("ipmi creds: user=%q proto=%q port=%d", ic.Username, ic.Protocol, ic.Port)
	}
	if kc := tg.KVM(); kc.Username != "ADMIN" || kc.Password != pass {
		t.Fatal("kvm creds")
	}

	// Rotation takes effect on the next resolve (no cache).
	f.w.SetPassword(refA, "rotated")
	if tg, _ := f.svc.Resolve(f.ctx, admin, ok); tg.Creds.Password != "rotated" {
		t.Fatal("cached credentials")
	}

	none := f.device(t, "none", "10.0.0.8", "")
	noAddr := f.device(t, "noaddr", "", refA)
	f.w.ResetCalls()
	for name, tc := range map[string]struct {
		subj authz.Subjects
		id   string
		want error
	}{
		"not platform admin": {plain, ok, authz.ErrForbidden},
		"not configured":     {admin, none, ErrNotConfigured},
		"no address":         {admin, noAddr, ErrNoAddress},
		"missing device":     {admin, store.NewID(), repo.ErrNotFound},
	} {
		if _, err := f.svc.Resolve(f.ctx, tc.subj, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(f.w.Calls()) != 0 {
		t.Fatalf("early refusals called warden: %+v", f.w.Calls())
	}
	f.w.Deny("tok-admin", refA)
	if _, err := f.svc.Resolve(f.ctx, admin, ok); !errors.Is(err, warden.ErrForbidden) {
		t.Fatalf("forbidden: %v", err)
	}
	f.mem.FailNext("AddressesForDevice")
	if _, err := f.svc.Resolve(f.ctx, admin, ok); err == nil {
		t.Fatal("address failure ignored")
	}
}

func TestIPMICreds(t *testing.T) {
	for url, want := range map[string]ipmi.Creds{
		"lanplus://h:6230":  {Protocol: "2.0", Port: 6230},
		"LANPLUS://h":       {Protocol: "2.0"},
		"lan://h:700":       {Protocol: "1.5", Port: 700},
		"ipmi://h:624":      {Port: 624},
		"https://h:443/ui":  {},
		"":                  {},
		"lanplus://h:99999": {Protocol: "2.0"},
		"lanplus://h:x":     {}, // unparseable port: auto on 623
		"::bad url":         {},
	} {
		got := IPMICreds(warden.Credentials{Username: "u", Password: "p", HostURL: url})
		want.Username, want.Password = "u", "p"
		if got != want {
			t.Errorf("IPMICreds(%q) = %+v, want %+v", url, got, want)
		}
	}
}

func TestReasonTable(t *testing.T) {
	cases := map[error]string{
		nil:                                     "",
		ErrNotConfigured:                        ReasonNotConfigured,
		ErrNoAddress:                            ReasonNoAddress,
		warden.ErrForbidden:                     ReasonForbidden,
		warden.ErrNoUserToken:                   ReasonForbidden,
		warden.ErrNotFound:                      ReasonSecretNotFound,
		warden.ErrEmptyRef:                      ReasonSecretNotFound,
		warden.ErrUnavailable:                   ReasonWardenUnavailable,
		warden.ErrPolicyDenied:                  ReasonWardenUnavailable,
		ipmi.ErrUnreachable:                     ReasonBMCUnreachable,
		fmt.Errorf("x: %w", ipmi.ErrAuthFailed): ReasonBMCAuthFailed,
		errors.New("completion code 0xc1"):      "",
		authz.ErrForbidden:                      "",
		repo.ErrNotFound:                        "",
		ipmi.ErrUnknownAction:                   "",
		ErrInvalidReference:                     "",
	}
	for err, want := range cases {
		if got := Reason(err); got != want {
			t.Errorf("Reason(%v) = %q, want %q", err, got, want)
		}
	}
	for err, want := range map[error]string{
		nil: "", ipmi.ErrUnknownAction: "", fmt.Errorf("x: %w", ipmi.ErrUnknownAction): "",
		ipmi.ErrUnreachable: ReasonBMCUnreachable, ipmi.ErrAuthFailed: ReasonBMCAuthFailed,
		errors.New("completion code 0xc1"): ReasonBMCError,
	} {
		if got := BMCReason(err); got != want {
			t.Errorf("BMCReason(%v) = %q, want %q", err, got, want)
		}
	}
	statuses := map[string]int{
		ReasonNotConfigured: http.StatusConflict, ReasonNoAddress: http.StatusConflict,
		ReasonForbidden: http.StatusForbidden, ReasonSecretNotFound: http.StatusConflict,
		ReasonWardenUnavailable: http.StatusServiceUnavailable, ReasonBMCUnreachable: http.StatusGatewayTimeout,
		ReasonBMCAuthFailed: http.StatusBadGateway, ReasonBMCError: http.StatusBadGateway, "": http.StatusInternalServerError,
	}
	for r, want := range statuses {
		if got := HTTPStatus(r); got != want {
			t.Errorf("HTTPStatus(%q) = %d, want %d", r, got, want)
		}
	}
	for _, r := range []string{ReasonBMCUnreachable, ReasonBMCAuthFailed, ReasonBMCError} {
		if !BMCSide(r) {
			t.Errorf("%s is BMC side", r)
		}
	}
	if BMCSide(ReasonForbidden) {
		t.Error("forbidden is not BMC side")
	}
	for _, r := range []string{ReasonNotConfigured, ReasonNoAddress, ReasonForbidden, ReasonSecretNotFound} {
		if Outcome(r) != audit.OutcomeRefused {
			t.Errorf("Outcome(%s)", r)
		}
	}
	for _, r := range []string{ReasonWardenUnavailable, ReasonBMCUnreachable, ReasonBMCAuthFailed, ReasonBMCError, "bad_request"} {
		if Outcome(r) != audit.OutcomeError {
			t.Errorf("Outcome(%s)", r)
		}
	}
	if Outcome("") != audit.OutcomeOK {
		t.Error("Outcome ok")
	}
}

func TestAuditAction(t *testing.T) {
	f := newFixture(t)
	id := f.device(t, "n", "10.0.0.9", refA)
	f.svc.AuditAction(context.Background(), admin, audit.PowerAction, id, "10.0.0.9", "cycle", "")
	f.svc.AuditAction(context.Background(), admin, audit.PowerAction, id, "", "off", ReasonForbidden)
	f.svc.AuditAction(context.Background(), admin, audit.KVMSessionStarted, id, "10.0.0.9", "", ReasonBMCUnreachable)
	svc := admin
	svc.ActorKind = authz.ActorService
	f.svc.AuditAction(context.Background(), svc, audit.PowerAction, id, "", "on", "")
	rows := f.mem.Audit()
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if r := rows[0]; r.Action != "power_action" || r.SubjectKind != audit.SubjectPower || r.SubjectID != id || r.Outcome != "ok" ||
		r.Target != "10.0.0.9" || r.Detail["action"] != "cycle" || r.ActorKind != "user" {
		t.Fatalf("ok row: %+v", r)
	}
	if r := rows[1]; r.Outcome != "refused" || r.Reason != ReasonForbidden {
		t.Fatalf("refused row: %+v", r)
	}
	if r := rows[2]; r.Action != "kvm_session_started" || r.SubjectKind != audit.SubjectKVM || r.Outcome != "error" || r.Reason != ReasonBMCUnreachable {
		t.Fatalf("kvm row: %+v", r)
	}
	if _, ok := rows[2].Detail["action"]; ok {
		t.Fatalf("kvm row carries an action: %+v", rows[2].Detail)
	}
	if rows[3].ActorKind != "service" {
		t.Fatalf("service actor: %+v", rows[3])
	}
	// Unknown event type is dropped (programming error, never panics).
	f.svc.AuditAction(context.Background(), admin, audit.EventType("nope"), id, "", "", "")
	if len(f.mem.Audit()) != 4 {
		t.Fatal("unknown event written")
	}
}

func TestValidReference(t *testing.T) {
	if !ValidReference(refA) || ValidReference("x") {
		t.Fatal("ValidReference")
	}
}

func TestNewDefaultClock(t *testing.T) {
	if s := New(memstore.New(), warden.NewFake()); s.now().IsZero() || s.now().Location() != time.UTC {
		t.Fatal("default clock")
	}
}
