package subnets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const community = "c0mmunity-S3CRET"

func snmpSvc(t *testing.T) (*Service, *memstore.Mem, *sealed.Envelope) {
	t.Helper()
	svc, st := newSvc(t)
	env, err := sealed.NewEnvelope(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEnvelope(env)
	return svc, st, env
}

func mkSubnet(t *testing.T, svc *Service, name, cidr, parent string) store.Subnet {
	t.Helper()
	s, err := svc.Create(context.Background(), subj(), store.Subnet{Name: name, CIDR: cidr, ParentID: parent}, false)
	if err != nil {
		t.Fatalf("create %s: %v", cidr, err)
	}
	return s
}

func mustJSONText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetSNMPStoresSealedAndReturnsStatus (T019): the blob is sealed for the
// tenant+subnet, the response is status only, and an audit row is written.
func TestSetSNMPStoresSealedAndReturnsStatus(t *testing.T) {
	svc, st, env := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "mgmt", "10.1.112.0/24", "")

	status, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: community})
	if err != nil {
		t.Fatal(err)
	}
	if status.Own == nil || status.Own.Version != 2 || status.Effective.State != store.SNMPStateOwn || status.Effective.SourceSubnetID != s.ID {
		t.Fatalf("status %+v", status)
	}
	if strings.Contains(mustJSONText(t, status), community) {
		t.Fatal("status carries the community")
	}
	row, err := st.GetSubnetSNMP(ctx, "t1", s.ID)
	if err != nil || row.Version != 2 || row.UpdatedBy != "u1" || bytes.Contains(row.Sealed, []byte(community)) {
		t.Fatalf("row %+v %v", row, err)
	}
	sec, err := snmpcred.Open(env, "t1", s.ID, row.Sealed)
	if err != nil || sec.Community != community {
		t.Fatalf("blob does not open for tenant+subnet: %v", err)
	}
	got, err := svc.GetSNMP(ctx, subj(), s.ID)
	if err != nil || got.Own == nil || got.Own.Version != 2 || got.Effective.State != store.SNMPStateOwn {
		t.Fatalf("get %+v %v", got, err)
	}
	var set int
	for _, a := range st.Audit() {
		if a.Action == "snmp_credentials_set" && a.SubjectID == s.ID && a.ActorID == "u1" && a.Detail["protocol_version"] == 2 {
			set++
		}
		if strings.Contains(mustJSONText(t, a), community) {
			t.Fatal("audit row carries the community")
		}
	}
	if set != 1 {
		t.Fatalf("set audit rows %d", set)
	}
}

func TestSetSNMPErrors(t *testing.T) {
	svc, st, _ := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "mgmt", "10.1.112.0/24", "")
	var fe *snmpcred.FieldError
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2}); !errors.As(err, &fe) || fe.Field != "community" {
		t.Fatalf("validation: %v", err)
	}
	if _, err := svc.SetSNMP(ctx, subj(), "missing", snmpcred.Input{Version: 2, Community: "c"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing subnet: %v", err)
	}
	if _, err := svc.GetSNMP(ctx, subj(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	other := authz.Subjects{TenantID: "t2", UserID: "u2", ActorKind: authz.ActorUser}
	if _, err := svc.SetSNMP(ctx, other, s.ID, snmpcred.Input{Version: 2, Community: "c"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
	if _, err := svc.SetSNMP(ctx, authz.Subjects{}, s.ID, snmpcred.Input{Version: 2, Community: "c"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("no tenant: %v", err)
	}
	if _, err := svc.GetSNMP(ctx, authz.Subjects{}, s.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("get no tenant: %v", err)
	}
	st.FailNext("PutSubnetSNMP")
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: "c"}); err == nil {
		t.Fatal("store failure")
	}
	st.FailNext("GetSubnetSNMP")
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: "c"}); err == nil {
		t.Fatal("store read failure")
	}
	st.FailNext("ListSubnetSNMP")
	if _, err := svc.GetSNMP(ctx, subj(), s.ID); err == nil {
		t.Fatal("list failure")
	}
	st.FailNext("AllSubnetCIDRs")
	if _, err := svc.GetSNMP(ctx, subj(), s.ID); err == nil {
		t.Fatal("subnet list failure")
	}
	noEnv, _ := newSvc(t)
	s2 := mkSubnet(t, noEnv, "x", "10.2.0.0/24", "")
	if _, err := noEnv.SetSNMP(ctx, subj(), s2.ID, snmpcred.Input{Version: 2, Community: "c"}); !errors.Is(err, snmpcred.ErrNoEnvelope) {
		t.Fatalf("no envelope: %v", err)
	}
}

// TestSubnetUpdateKeepsCredentials (T019, FR-005): a subnet PUT carrying
// snmp_* fields leaves the credentials untouched and never writes the
// legacy columns.
func TestSubnetUpdateKeepsCredentials(t *testing.T) {
	svc, st, _ := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "mgmt", "10.1.112.0/24", "")
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: community}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.GetSubnetSNMP(ctx, "t1", s.ID)
	in := s
	in.Description = "edited"
	in.SNMPSecretRef, in.SNMPVersion = "warden-ref", 3
	in.SNMP = &store.SNMPSummary{State: store.SNMPStateNone}
	up, err := svc.Update(ctx, subj(), in, false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := st.GetSubnetSNMP(ctx, "t1", s.ID)
	if !bytes.Equal(before.Sealed, after.Sealed) || after.Version != 2 {
		t.Fatal("credentials changed by a subnet update")
	}
	raw, _ := st.GetSubnet(ctx, "t1", s.ID)
	if raw.SNMPSecretRef != "" || raw.SNMPVersion != 0 {
		t.Fatalf("legacy columns written: %q %d", raw.SNMPSecretRef, raw.SNMPVersion)
	}
	if up.SNMPSecretRef != "" || up.SNMPVersion != 2 || up.SNMP == nil || up.SNMP.State != store.SNMPStateOwn {
		t.Fatalf("response summary %+v %d %q", up.SNMP, up.SNMPVersion, up.SNMPSecretRef)
	}
	c, err := svc.Create(ctx, subj(), store.Subnet{Name: "legacy", CIDR: "10.9.0.0/24", SNMPSecretRef: "ref", SNMPVersion: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = st.GetSubnet(ctx, "t1", c.ID)
	if raw.SNMPSecretRef != "" || raw.SNMPVersion != 0 || c.SNMP == nil || c.SNMP.State != store.SNMPStateNone {
		t.Fatalf("create wrote legacy fields: %+v", raw)
	}
}

// TestListAndTreeCarrySummary: list, get and tree fill the effective summary.
func TestListAndTreeCarrySummary(t *testing.T) {
	svc, _, _ := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "mgmt", "10.1.112.0/24", "")
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: community}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx, subj(), store.SubnetFilter{})
	if err != nil || len(list) != 1 || list[0].SNMP == nil || list[0].SNMP.State != store.SNMPStateOwn || list[0].SNMPVersion != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	tree, err := svc.GetTree(ctx, subj())
	if err != nil || len(tree) != 1 || tree[0].SNMP == nil || tree[0].SNMP.Version != 2 {
		t.Fatalf("tree %+v %v", tree, err)
	}
	if strings.Contains(mustJSONText(t, list)+mustJSONText(t, tree), community) {
		t.Fatal("community in list/tree")
	}
}

// TestSetSNMPv3 (T031): authNoPriv and authPriv accepted; priv fields with
// authNoPriv rejected; the status carries level/protocols/weak only.
func TestSetSNMPv3(t *testing.T) {
	svc, st, env := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "core", "10.1.111.0/24", "")
	in := snmpcred.Input{Version: 3, User: "labuser", SecurityLevel: "authPriv", AuthProtocol: "SHA256", AuthPassword: "authpass-S3CRET",
		PrivProtocol: "AES256", PrivPassword: "privpass-S3CRET"}
	status, err := svc.SetSNMP(ctx, subj(), s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if status.Own.Version != 3 || status.Own.SecurityLevel != "authPriv" || status.Own.AuthProtocol != "SHA256" || status.Own.PrivProtocol != "AES256" || status.Own.Weak {
		t.Fatalf("own %+v", status.Own)
	}
	txt := mustJSONText(t, status)
	for _, v := range []string{"labuser", "authpass-S3CRET", "privpass-S3CRET"} {
		if strings.Contains(txt, v) {
			t.Fatalf("status carries %q", v)
		}
	}
	row, _ := st.GetSubnetSNMP(ctx, "t1", s.ID)
	sec, _ := snmpcred.Open(env, "t1", s.ID, row.Sealed)
	if sec.User != "labuser" || sec.PrivPassword != "privpass-S3CRET" || sec.Community != "" {
		t.Fatal("sealed v3 secret")
	}
	weak := snmpcred.Input{Version: 3, User: "u", SecurityLevel: "authNoPriv", AuthProtocol: "MD5", AuthPassword: "authpass1"}
	status, err = svc.SetSNMP(ctx, subj(), s.ID, weak)
	if err != nil || !status.Own.Weak || status.Own.PrivProtocol != "" || !status.Effective.Weak {
		t.Fatalf("authNoPriv MD5: %+v %v", status, err)
	}
	var fe *snmpcred.FieldError
	bad := weak
	bad.PrivProtocol = "AES"
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, bad); !errors.As(err, &fe) || fe.Field != "priv_protocol" {
		t.Fatalf("priv with authNoPriv: %v", err)
	}
}

// TestSNMPInheritance (T035): children inherit from the nearest ancestor;
// clearing or deleting the source falls back; host-sync subnets inherit too.
func TestSNMPInheritance(t *testing.T) {
	svc, st, _ := snmpSvc(t)
	ctx := context.Background()
	root := mkSubnet(t, svc, "supernet", "10.0.0.0/8", "")
	site := mkSubnet(t, svc, "site", "10.1.0.0/16", root.ID)
	lab := mkSubnet(t, svc, "lab", "10.1.2.0/24", site.ID)
	if _, err := svc.SetSNMP(ctx, subj(), root.ID, snmpcred.Input{Version: 3, User: "u", SecurityLevel: "authNoPriv", AuthProtocol: "SHA", AuthPassword: "authpass1"}); err != nil {
		t.Fatal(err)
	}
	st1, _ := svc.GetSNMP(ctx, subj(), lab.ID)
	if st1.Own != nil || st1.Effective.State != store.SNMPStateInherited || st1.Effective.SourceSubnetID != root.ID ||
		st1.Effective.SourceName != "supernet" || st1.Effective.SourceCIDR != "10.0.0.0/8" || st1.Effective.Version != 3 || !st1.Effective.Weak {
		t.Fatalf("grandchild %+v", st1)
	}
	tree, _ := svc.GetTree(ctx, subj())
	if tree[0].Children[0].Children[0].SNMP.State != store.SNMPStateInherited {
		t.Fatal("tree summary for the grandchild")
	}
	list, _ := svc.List(ctx, subj(), store.SubnetFilter{ParentID: site.ID})
	if len(list) != 1 || list[0].SNMP.SourceSubnetID != root.ID {
		t.Fatalf("filtered list still resolves ancestors outside the page: %+v", list)
	}
	// Own credentials on the middle subnet win for it and its children.
	if _, err := svc.SetSNMP(ctx, subj(), site.ID, snmpcred.Input{Version: 2, Community: "site-comm"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.Get(ctx, subj(), lab.ID); g.SNMP.SourceSubnetID != site.ID || g.SNMPVersion != 2 {
		t.Fatalf("nearest ancestor: %+v", g.SNMP)
	}
	// Clearing the site's own credentials: the lab inherits from the root again.
	if err := st.DeleteSubnetSNMP(ctx, "t1", site.ID, store.AuditRow{TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.Get(ctx, subj(), lab.ID); g.SNMP.SourceSubnetID != root.ID {
		t.Fatalf("fallback after clear: %+v", g.SNMP)
	}
	// A subnet the host sync created inherits like any other.
	auto := store.Subnet{ID: "auto-1", TenantID: "t1", Name: "auto", CIDR: "10.1.3.0/24", ParentID: site.ID, Origin: store.OriginHostSync}
	if err := st.CreateSubnet(ctx, auto); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.Get(ctx, subj(), "auto-1"); g.SNMP.State != store.SNMPStateInherited || g.SNMP.SourceSubnetID != root.ID {
		t.Fatalf("host-sync subnet: %+v", g.SNMP)
	}
	// Deleting the root (cascade) leaves the children with nothing.
	if err := svc.Delete(ctx, subj(), root.ID, true); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.Get(ctx, subj(), lab.ID); g.SNMP.State != store.SNMPStateNone || g.SNMPVersion != 0 {
		t.Fatalf("after parent delete: %+v", g.SNMP)
	}
}

// TestReplaceAndClearAudited (T048): replace needs every field and is audited
// with the previous version; clear deletes the row and is audited; clearing
// without own credentials is a no-op without audit; no audit detail carries
// a credential.
func TestReplaceAndClearAudited(t *testing.T) {
	svc, st, _ := snmpSvc(t)
	ctx := context.Background()
	s := mkSubnet(t, svc, "mgmt", "10.1.112.0/24", "")
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: community}); err != nil {
		t.Fatal(err)
	}
	var fe *snmpcred.FieldError
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 3, User: "u", SecurityLevel: "authPriv", AuthProtocol: "SHA", AuthPassword: "authpass1"}); !errors.As(err, &fe) || fe.Field != "priv_protocol" {
		t.Fatalf("replace must be complete: %v", err)
	}
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 3, User: "v3user-S3CRET", SecurityLevel: "authNoPriv", AuthProtocol: "SHA256", AuthPassword: "v3pass-S3CRET"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearSNMP(ctx, subj(), s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSubnetSNMP(ctx, "t1", s.ID); err == nil {
		t.Fatal("credentials not deleted")
	}
	if err := svc.ClearSNMP(ctx, subj(), s.ID); err != nil {
		t.Fatalf("clear without own credentials: %v", err)
	}
	want := []struct {
		action string
		detail map[string]any
	}{
		{"snmp_credentials_set", map[string]any{"protocol_version": 2, "security_level": ""}},
		{"snmp_credentials_replaced", map[string]any{"protocol_version": 3, "security_level": "authNoPriv", "previous_version": 2}},
		{"snmp_credentials_cleared", map[string]any{"previous_version": 3}},
	}
	var got []string
	for _, a := range st.Audit() {
		got = append(got, a.Action)
		txt := mustJSONText(t, a)
		for _, v := range []string{community, "v3user-S3CRET", "v3pass-S3CRET"} {
			if strings.Contains(txt, v) {
				t.Fatalf("audit %s carries %q", a.Action, v)
			}
		}
	}
	audits := st.Audit()
	if len(audits) != len(want) {
		t.Fatalf("audit actions %v", got)
	}
	for i, w := range want {
		a := audits[i]
		if a.Action != w.action || a.SubjectKind != "subnet" || a.SubjectID != s.ID || a.ActorID != "u1" || a.Outcome != "ok" {
			t.Fatalf("audit %d: %+v", i, a)
		}
		if mustJSONText(t, a.Detail) != mustJSONText(t, w.detail) {
			t.Fatalf("audit %d detail %v want %v", i, a.Detail, w.detail)
		}
	}
	if err := svc.ClearSNMP(ctx, subj(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing subnet: %v", err)
	}
	if err := svc.ClearSNMP(ctx, authz.Subjects{}, s.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("no tenant: %v", err)
	}
	if _, err := svc.SetSNMP(ctx, subj(), s.ID, snmpcred.Input{Version: 2, Community: "c"}); err != nil {
		t.Fatal(err)
	}
	st.FailNext("GetSubnetSNMP")
	if err := svc.ClearSNMP(ctx, subj(), s.ID); err == nil {
		t.Fatal("read failure")
	}
	st.FailNext("DeleteSubnetSNMP")
	if err := svc.ClearSNMP(ctx, subj(), s.ID); err == nil {
		t.Fatal("delete failure")
	}
}
