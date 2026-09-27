package memstore

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestARPSettingsDefaultsAndPut(t *testing.T) {
	ctx := context.Background()
	m := New()
	got, err := m.GetARPSettings(ctx, "t1")
	if err != nil || !got.Enabled || got.ProxyThreshold != store.ARPDefaultProxyThreshold || len(got.ExcludedDevices) != 0 || got.TenantID != "t1" {
		t.Fatalf("defaults %+v %v", got, err)
	}
	row := store.AuditRow{Action: "arp_settings_updated", SubjectID: "t1"}
	in := store.ARPSettings{TenantID: "t1", Enabled: false, ExcludedDevices: []string{"d1"}, ProxyThreshold: 20, UpdatedBy: "u1"}
	if err := m.PutARPSettings(ctx, in, row); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetARPSettings(ctx, "t1")
	if got.Enabled || got.ProxyThreshold != 20 || len(got.ExcludedDevices) != 1 || got.UpdatedBy != "u1" || got.UpdatedAt.IsZero() {
		t.Fatalf("stored %+v", got)
	}
	if other, _ := m.GetARPSettings(ctx, "t2"); !other.Enabled || other.ProxyThreshold != 8 {
		t.Fatalf("tenant isolation %+v", other)
	}
	if a := m.Audit(); len(a) != 1 || a[0].TenantID != "t1" || a[0].Action != "arp_settings_updated" {
		t.Fatalf("audit %+v", a)
	}
	m.FailNext("PutARPSettings")
	if err := m.PutARPSettings(ctx, in, row); err == nil {
		t.Fatal("injected failure ignored")
	}
	m.FailNext("GetARPSettings")
	if _, err := m.GetARPSettings(ctx, "t1"); err == nil {
		t.Fatal("injected failure ignored")
	}
}

func seedARPAddresses(t *testing.T, m *Mem) {
	t.Helper()
	ctx := context.Background()
	for _, s := range []store.Subnet{
		{ID: "s1", TenantID: "t1", Name: "a", CIDR: "10.0.0.0/24"},
		{ID: "s9", TenantID: "t2", Name: "z", CIDR: "10.0.0.0/24"},
	} {
		if err := m.CreateSubnet(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []store.IPAddress{
		{ID: "a-empty", TenantID: "t1", SubnetID: "s1", Address: "10.0.0.1"},
		{ID: "a-arp", TenantID: "t1", SubnetID: "s1", Address: "10.0.0.2", MACAddress: "00:11:22:33:44:02", MACSource: store.MACSourceARP},
		{ID: "a-manual", TenantID: "t1", SubnetID: "s1", Address: "10.0.0.3", MACAddress: "00:11:22:33:44:03", MACSource: store.MACSourceManual},
		{ID: "a-agent", TenantID: "t1", SubnetID: "s1", Address: "10.0.0.4", MACAddress: "00:11:22:33:44:04", MACSource: store.MACSourceAgent, MACConflict: "0a:0a:0a:0a:0a:0a"},
		{ID: "a-other", TenantID: "t2", SubnetID: "s9", Address: "10.0.0.1"},
	} {
		if err := m.CreateAddress(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
}

func arpAudit(action, subject string) store.AuditRow {
	return store.AuditRow{Action: action, SubjectKind: "address", SubjectID: subject}
}

func TestApplyARPOps(t *testing.T) {
	ctx := context.Background()
	m := New()
	seedARPAddresses(t, m)
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	ops := []store.ARPOp{
		{Kind: store.ARPFill, AddressID: "a-empty", Address: "10.0.0.1", MAC: "0a:5c:d2:f1:00:01", SourceDeviceID: "r1", At: at, Audit: []store.AuditRow{arpAudit("mac_learned", "a-empty")}},
		{Kind: store.ARPUpdate, AddressID: "a-arp", Address: "10.0.0.2", MAC: "0a:5c:d2:f1:00:02", SourceDeviceID: "r1", At: at, Audit: []store.AuditRow{arpAudit("mac_changed", "a-arp")}},
		{Kind: store.ARPConflict, AddressID: "a-manual", Address: "10.0.0.3", MAC: "0a:5c:d2:f1:00:03", SourceDeviceID: "r1", At: at, Audit: []store.AuditRow{arpAudit("mac_conflict", "a-manual")}},
		{Kind: store.ARPClearConflict, AddressID: "a-agent", Address: "10.0.0.4", MAC: "00:11:22:33:44:04", SourceDeviceID: "r1", At: at},
		{Kind: store.ARPCreate, AddressID: "a-new", Address: "10.0.0.9", SubnetID: "s1", MAC: "0a:5c:d2:f1:00:09", SourceDeviceID: "r1", At: at, Audit: []store.AuditRow{arpAudit("address_created", "a-new")}},
		{Kind: store.ARPTouch, AddressID: "a-arp", Address: "10.0.0.2", MAC: "0a:5c:d2:f1:00:02", SourceDeviceID: "r2", At: at.Add(time.Minute)},
	}
	summary := []store.AuditRow{{Action: "arp_run", SubjectKind: "scan", SubjectID: "j1"}}
	if err := m.ApplyARP(ctx, "t1", ops, summary); err != nil {
		t.Fatal(err)
	}
	get := func(id string) store.IPAddress {
		a, err := m.GetAddress(ctx, "t1", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return a
	}
	if a := get("a-empty"); a.MACAddress != "0a:5c:d2:f1:00:01" || a.MACSource != store.MACSourceARP || a.MACSourceDeviceID != "r1" || a.MACSeenAt == nil || !a.MACSeenAt.Equal(at) {
		t.Fatalf("fill %+v", a)
	}
	if a := get("a-arp"); a.MACAddress != "0a:5c:d2:f1:00:02" || a.MACSourceDeviceID != "r2" || !a.MACSeenAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("update+touch %+v", a)
	}
	if a := get("a-manual"); a.MACAddress != "00:11:22:33:44:03" || a.MACSource != store.MACSourceManual || a.MACConflict != "0a:5c:d2:f1:00:03" {
		t.Fatalf("conflict %+v", a)
	}
	if a := get("a-agent"); a.MACConflict != "" || a.MACSource != store.MACSourceAgent || a.MACSeenAt == nil {
		t.Fatalf("clear conflict %+v", a)
	}
	n := get("a-new")
	if n.Origin != store.OriginARP || n.MACSource != store.MACSourceARP || n.Status != store.IPActive || n.SubnetID != "s1" || n.MACAddress != "0a:5c:d2:f1:00:09" || n.LastSeen == nil {
		t.Fatalf("create %+v", n)
	}
	if len(m.Audit()) != 5 {
		t.Fatalf("audit rows %d", len(m.Audit()))
	}
	for _, r := range m.Audit() {
		if r.TenantID != "t1" {
			t.Fatalf("audit tenant %+v", r)
		}
	}
}

// SC-003 defence in depth: even a wrong plan never overwrites an agent or
// manual MAC, never crosses tenants, and a create never replaces a row.
func TestApplyARPGuards(t *testing.T) {
	ctx := context.Background()
	m := New()
	seedARPAddresses(t, m)
	at := time.Now().UTC()
	ops := []store.ARPOp{
		{Kind: store.ARPFill, AddressID: "a-manual", MAC: "0a:00:00:00:00:01", At: at, Audit: []store.AuditRow{arpAudit("mac_learned", "a-manual")}},
		{Kind: store.ARPUpdate, AddressID: "a-agent", MAC: "0a:00:00:00:00:02", At: at, Audit: []store.AuditRow{arpAudit("mac_changed", "a-agent")}},
		{Kind: store.ARPConflict, AddressID: "a-arp", MAC: "0a:00:00:00:00:03", At: at, Audit: []store.AuditRow{arpAudit("mac_conflict", "a-arp")}},
		{Kind: store.ARPFill, AddressID: "a-other", MAC: "0a:00:00:00:00:04", At: at, Audit: []store.AuditRow{arpAudit("mac_learned", "a-other")}},
		{Kind: store.ARPCreate, AddressID: "dup", Address: "10.0.0.1", SubnetID: "s1", MAC: "0a:00:00:00:00:05", At: at, Audit: []store.AuditRow{arpAudit("address_created", "dup")}},
		{Kind: store.ARPCreate, AddressID: "x", Address: "10.0.0.50", SubnetID: "s9", MAC: "0a:00:00:00:00:06", At: at, Audit: []store.AuditRow{arpAudit("address_created", "x")}},
		{Kind: "bogus", AddressID: "a-empty", MAC: "0a:00:00:00:00:07", At: at},
	}
	if err := m.ApplyARP(ctx, "t1", ops, nil); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.GetAddress(ctx, "t1", "a-manual"); a.MACAddress != "00:11:22:33:44:03" || a.MACSource != store.MACSourceManual {
		t.Fatalf("manual overwritten %+v", a)
	}
	if a, _ := m.GetAddress(ctx, "t1", "a-agent"); a.MACAddress != "00:11:22:33:44:04" || a.MACSource != store.MACSourceAgent {
		t.Fatalf("agent overwritten %+v", a)
	}
	if a, _ := m.GetAddress(ctx, "t1", "a-arp"); a.MACConflict != "" {
		t.Fatalf("conflict on arp source %+v", a)
	}
	if a, _ := m.GetAddress(ctx, "t2", "a-other"); a.MACAddress != "" {
		t.Fatalf("cross-tenant write %+v", a)
	}
	if _, err := m.GetAddress(ctx, "t1", "dup"); err == nil {
		t.Fatal("duplicate address created")
	}
	if _, err := m.GetAddress(ctx, "t1", "x"); err == nil {
		t.Fatal("address created in another tenant's subnet")
	}
	if a, _ := m.GetAddress(ctx, "t1", "a-empty"); a.MACAddress != "" {
		t.Fatalf("unknown op applied %+v", a)
	}
	if len(m.Audit()) != 0 {
		t.Fatalf("audit for skipped ops: %+v", m.Audit())
	}
	m.FailNext("ApplyARP")
	if err := m.ApplyARP(ctx, "t1", nil, nil); err == nil {
		t.Fatal("injected failure ignored")
	}
}

func TestNetworkMACs(t *testing.T) {
	ctx := context.Background()
	m := New()
	for _, d := range []store.Device{
		{ID: "r1", TenantID: "t1", Name: "gw", DeviceType: store.DevRouter},
		{ID: "sw", TenantID: "t1", Name: "sw", DeviceType: store.DevSwitch},
		{ID: "fw", TenantID: "t1", Name: "fw", DeviceType: store.DevFirewall},
		{ID: "srv", TenantID: "t1", Name: "srv", DeviceType: store.DevServer},
		{ID: "r2", TenantID: "t2", Name: "gw2", DeviceType: store.DevRouter},
	} {
		if err := m.CreateDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, i := range []store.DeviceInterface{
		{ID: "i1", TenantID: "t1", DeviceID: "r1", Name: "ether1", MACAddress: "4C:5E:0C:00:00:01"},
		{ID: "i2", TenantID: "t1", DeviceID: "sw", Name: "vlan30", MACAddress: "00:04:96:00:00:02"},
		{ID: "i3", TenantID: "t1", DeviceID: "fw", Name: "port1", MACAddress: "00:09:0f:00:00:03"},
		{ID: "i4", TenantID: "t1", DeviceID: "srv", Name: "eth0", MACAddress: "52:54:00:00:00:04"},
		{ID: "i5", TenantID: "t2", DeviceID: "r2", Name: "ether1", MACAddress: "4c:5e:0c:00:00:05"},
		{ID: "i6", TenantID: "t1", DeviceID: "r1", Name: "lo", MACAddress: ""},
	} {
		if err := m.CreateInterface(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.NetworkMACs(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got["4c:5e:0c:00:00:01"] || !got["00:04:96:00:00:02"] || !got["00:09:0f:00:00:03"] {
		t.Fatalf("network macs %v", got)
	}
	m.FailNext("NetworkMACs")
	if _, err := m.NetworkMACs(ctx, "t1"); err == nil {
		t.Fatal("injected failure ignored")
	}
}

func TestListAddressesMACFilter(t *testing.T) {
	ctx := context.Background()
	m := New()
	seedARPAddresses(t, m)
	for q, want := range map[string]int{"0011223344": 3, "334403": 1, "001122334404": 1, "ffff": 0} {
		got, err := m.ListAddresses(ctx, "t1", store.AddressFilter{MAC: q})
		if err != nil || len(got) != want {
			t.Errorf("mac %q: %d rows (want %d) %v", q, len(got), want, err)
		}
	}
}

func TestScanUpsertKeepsMACProvenance(t *testing.T) {
	ctx := context.Background()
	m := New()
	seedARPAddresses(t, m)
	if _, err := m.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: "t1", Address: "10.0.0.3", SubnetID: "s1", Status: store.IPActive}); err != nil {
		t.Fatal(err)
	}
	a, _ := m.GetAddress(ctx, "t1", "a-manual")
	if a.MACAddress != "00:11:22:33:44:03" || a.MACSource != store.MACSourceManual {
		t.Fatalf("scan upsert dropped the MAC %+v", a)
	}
}
