package arpplan

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

var now = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func ids() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("new-%d", n) }
}

func baseInput() Input {
	return Input{
		TenantID: "t1", JobID: "j1", Now: now, NewID: ids(),
		Subnets: []store.Subnet{
			{ID: "s-srv", TenantID: "t1", CIDR: "10.0.0.0/24"},
			{ID: "s-ipmi", TenantID: "t1", CIDR: "10.0.1.0/24"},
			{ID: "s-big", TenantID: "t1", CIDR: "10.0.0.0/16"},
			{ID: "s-v6", TenantID: "t1", CIDR: "2001:db8::/64"},
			{ID: "s-other", TenantID: "t2", CIDR: "172.16.0.0/24"},
		},
	}
}

func entry(ip, mac string) snmp.ARPEntry { return snmp.ARPEntry{IP: ip, MAC: mac, IfIndex: 1} }

func opsByAddress(p Plan) map[string]store.ARPOp {
	out := map[string]store.ARPOp{}
	for _, op := range p.Ops {
		out[op.Address] = op
	}
	return out
}

// TestProvenanceTable covers data-model §5 row by row.
func TestProvenanceTable(t *testing.T) {
	in := baseInput()
	in.Addresses = []store.IPAddress{
		{ID: "a1", TenantID: "t1", Address: "10.0.0.1"},
		{ID: "a2", TenantID: "t1", Address: "10.0.0.2", MACAddress: "02:00:00:00:00:02", MACSource: store.MACSourceARP},
		{ID: "a3", TenantID: "t1", Address: "10.0.0.3", MACAddress: "02:00:00:00:00:99", MACSource: store.MACSourceARP},
		{ID: "a4", TenantID: "t1", Address: "10.0.0.4", MACAddress: "02-00-00-00-00-04", MACSource: store.MACSourceManual, MACConflict: "02:00:00:00:00:44"},
		{ID: "a5", TenantID: "t1", Address: "10.0.0.5", MACAddress: "02:00:00:00:00:99", MACSource: store.MACSourceManual},
		{ID: "a6", TenantID: "t1", Address: "10.0.0.6", MACAddress: "02:00:00:00:00:99", MACSource: store.MACSourceAgent, MACConflict: "02:00:00:00:00:06"},
		{ID: "a7", TenantID: "t1", Address: "10.0.0.7", MACAddress: "02:00:00:00:00:99"}, // pre-022 MAC: manual
		{ID: "a8", TenantID: "t1", Address: "10.0.0.8", MACAddress: "02:00:00:00:00:08", MACSource: store.MACSourceAgent},
		{ID: "a10", TenantID: "t1", Address: "10.0.0.10", MACAddress: "02:00:00:00:00:10", MACSource: store.MACSourceARP, MACConflict: "stale"},
	}
	in.Observations = []Observation{{DeviceID: "r1", Entries: []snmp.ARPEntry{
		entry("10.0.0.1", "02:00:00:00:00:01"),
		entry("10.0.0.2", "02:00:00:00:00:02"),
		entry("10.0.0.3", "02:00:00:00:00:03"),
		entry("10.0.0.4", "02:00:00:00:00:04"),
		entry("10.0.0.5", "02:00:00:00:00:05"),
		entry("10.0.0.6", "02:00:00:00:00:06"),
		entry("10.0.0.7", "02:00:00:00:00:07"),
		entry("10.0.0.8", "02:00:00:00:00:08"),
		entry("10.0.0.9", "02:00:00:00:00:09"),
		entry("10.0.0.10", "02:00:00:00:00:10"),
		entry("192.168.9.9", "02:00:00:00:00:0a"),
	}}}
	p := Build(in)
	got := opsByAddress(p)
	want := map[string]string{
		"10.0.0.1": store.ARPFill, "10.0.0.2": store.ARPTouch, "10.0.0.3": store.ARPUpdate,
		"10.0.0.4": store.ARPClearConflict, "10.0.0.5": store.ARPConflict, "10.0.0.7": store.ARPConflict,
		"10.0.0.8": store.ARPTouch, "10.0.0.9": store.ARPCreate, "10.0.0.10": store.ARPClearConflict,
	}
	if len(got) != len(want) {
		t.Fatalf("ops %+v", p.Ops)
	}
	for addr, kind := range want {
		if got[addr].Kind != kind {
			t.Errorf("%s: %q want %q", addr, got[addr].Kind, kind)
		}
	}
	if _, ok := got["10.0.0.6"]; ok {
		t.Error("an already recorded conflict is not re-planned")
	}
	c := got["10.0.0.9"]
	if c.AddressID != "new-1" || c.SubnetID != "s-srv" || c.MAC != "02:00:00:00:00:09" || c.SourceDeviceID != "r1" || !c.At.Equal(now) {
		t.Errorf("create %+v", c)
	}
	if u := got["10.0.0.3"]; u.AddressID != "a3" || u.MAC != "02:00:00:00:00:03" {
		t.Errorf("update %+v", u)
	}
	if cf := got["10.0.0.5"]; cf.MAC != "02:00:00:00:00:05" {
		t.Errorf("conflict stores the observed MAC: %+v", cf)
	}
	if p.Entries != 11 || p.Applied != 2 || p.Created != 1 || p.Conflicts != 3 {
		t.Errorf("counters entries=%d applied=%d created=%d conflicts=%d", p.Entries, p.Applied, p.Created, p.Conflicts)
	}
	if len(p.Ignored) != 1 || p.Ignored[store.ARPIgnoredOutside] != 1 {
		t.Errorf("ignored %v", p.Ignored)
	}
	// Ops come out in address order (deterministic plan).
	for i := 1; i < len(p.Ops); i++ {
		if !less(p.Ops[i-1].Address, p.Ops[i].Address) {
			t.Fatalf("ops not ordered: %s before %s", p.Ops[i-1].Address, p.Ops[i].Address)
		}
	}
}

func less(a, b string) bool { return mustAddr(a).Less(mustAddr(b)) }

func mustAddr(s string) netip.Addr { a, _ := netip.ParseAddr(s); return a }

func TestAuditRowsPerOp(t *testing.T) {
	in := baseInput()
	in.Addresses = []store.IPAddress{
		{ID: "a1", TenantID: "t1", Address: "10.0.0.1"},
		{ID: "a3", TenantID: "t1", Address: "10.0.0.3", MACAddress: "02:00:00:00:00:99", MACSource: store.MACSourceARP},
		{ID: "a5", TenantID: "t1", Address: "10.0.0.5", MACAddress: "02:00:00:00:00:99", MACSource: store.MACSourceManual},
		{ID: "a2", TenantID: "t1", Address: "10.0.0.2", MACAddress: "02:00:00:00:00:02", MACSource: store.MACSourceARP},
	}
	in.Observations = []Observation{{DeviceID: "r1", Entries: []snmp.ARPEntry{
		entry("10.0.0.1", "02:00:00:00:00:01"), entry("10.0.0.3", "02:00:00:00:00:03"),
		entry("10.0.0.5", "02:00:00:00:00:05"), entry("10.0.1.9", "02:00:00:00:00:09"),
		entry("10.0.0.2", "02:00:00:00:00:02"),
	}}}
	got := opsByAddress(Build(in))
	check := func(addr, action string, keys map[string]any) {
		t.Helper()
		op := got[addr]
		if len(op.Audit) != 1 {
			t.Fatalf("%s: %d audit rows", addr, len(op.Audit))
		}
		r := op.Audit[0]
		if r.Action != action || r.ActorKind != "system" || r.ActorID != "scan" || r.SubjectKind != "address" ||
			r.SubjectID != op.AddressID || r.TenantID != "t1" || r.Outcome != "ok" || !r.At.Equal(now) {
			t.Fatalf("%s row %+v", addr, r)
		}
		for k, v := range keys {
			if r.Detail[k] != v {
				t.Errorf("%s detail %s=%v want %v (%v)", addr, k, r.Detail[k], v, r.Detail)
			}
		}
		for k := range r.Detail {
			for _, bad := range []string{"secret", "credential", "snmp", "password", "community"} {
				if strings.Contains(k, bad) {
					t.Errorf("guarded key %s", k)
				}
			}
		}
	}
	check("10.0.0.1", "mac_learned", map[string]any{"address": "10.0.0.1", "mac": "02:00:00:00:00:01", "source_device_id": "r1", "job_id": "j1"})
	check("10.0.0.3", "mac_changed", map[string]any{"mac": "02:00:00:00:00:03", "previous_mac": "02:00:00:00:00:99", "job_id": "j1"})
	check("10.0.0.5", "mac_conflict", map[string]any{"mac": "02:00:00:00:00:99", "observed_mac": "02:00:00:00:00:05", "source_device_id": "r1"})
	check("10.0.1.9", "address_created", map[string]any{"origin": "arp", "mac": "02:00:00:00:00:09", "subnet_id": "s-ipmi", "source_device_id": "r1"})
	if len(got["10.0.0.2"].Audit) != 0 {
		t.Error("touch is not audited")
	}
}

func TestSameIPFromTwoDevices(t *testing.T) {
	in := baseInput()
	in.Addresses = []store.IPAddress{{ID: "a1", TenantID: "t1", Address: "10.0.0.1"}}
	in.Observations = []Observation{
		{DeviceID: "r2", Entries: []snmp.ARPEntry{entry("10.0.0.1", "02:00:00:00:00:02"), entry("10.0.0.2", "02:00:00:00:00:22")}},
		{DeviceID: "r1", Entries: []snmp.ARPEntry{entry("10.0.0.1", "02:00:00:00:00:01"), entry("10.0.0.2", "02:00:00:00:00:22")}},
	}
	p := Build(in)
	got := opsByAddress(p)
	// Observations are processed in device-id order; the last one wins.
	if op := got["10.0.0.1"]; op.MAC != "02:00:00:00:00:02" || op.SourceDeviceID != "r2" {
		t.Fatalf("winner %+v", op)
	}
	if p.Conflicts != 1 {
		t.Fatalf("conflicts %d (agreeing devices are not a conflict)", p.Conflicts)
	}
}

func TestTenantIsolationAndInvalid(t *testing.T) {
	in := baseInput()
	in.Addresses = []store.IPAddress{
		{ID: "x1", TenantID: "t2", Address: "10.0.0.1"},
		{ID: "x2", TenantID: "t2", Address: "172.16.0.5"},
	}
	in.Observations = []Observation{{DeviceID: "r1", Entries: []snmp.ARPEntry{
		entry("10.0.0.1", "02:00:00:00:00:01"),   // other tenant's row: a create in our subnet
		entry("172.16.0.5", "02:00:00:00:00:05"), // only another tenant's subnet
		entry("not-an-ip", "02:00:00:00:00:06"),
		entry("10.0.0.7", ""),
		entry("10.0.0.8", "zz:00"),
		entry("2001:0db8:0000::0010", "02:00:00:00:00:10"),
	}}}
	p := Build(in)
	got := opsByAddress(p)
	if op := got["10.0.0.1"]; op.Kind != store.ARPCreate || op.AddressID == "x1" {
		t.Fatalf("cross-tenant address targeted: %+v", op)
	}
	if _, ok := got["172.16.0.5"]; ok {
		t.Fatal("applied into another tenant's subnet")
	}
	if op := got["2001:db8::10"]; op.Kind != store.ARPCreate || op.SubnetID != "s-v6" {
		t.Fatalf("ipv6 canonical form: %+v", p.Ops)
	}
	if p.Ignored[store.ARPIgnoredInvalid] != 3 || p.Ignored[store.ARPIgnoredOutside] != 1 {
		t.Fatalf("ignored %v", p.Ignored)
	}
}

func TestNonCanonicalStoredAddress(t *testing.T) {
	in := baseInput()
	in.Addresses = []store.IPAddress{
		{ID: "v6", TenantID: "t1", Address: "2001:DB8:0:0::10"},
		{ID: "bad", TenantID: "t1", Address: "garbage"},
	}
	in.Observations = []Observation{{DeviceID: "r1", Entries: []snmp.ARPEntry{entry("2001:db8::10", "02:00:00:00:00:10")}}}
	p := Build(in)
	if len(p.Ops) != 1 || p.Ops[0].Kind != store.ARPFill || p.Ops[0].AddressID != "v6" {
		t.Fatalf("ops %+v", p.Ops)
	}
}

func TestDefaultsAndEmpty(t *testing.T) {
	p := Build(Input{TenantID: "t1"})
	if len(p.Ops) != 0 || p.Entries != 0 || len(p.Ignored) != 0 {
		t.Fatalf("empty plan %+v", p)
	}
	// Without an injected id generator creates still get ids.
	in := baseInput()
	in.NewID = nil
	in.Observations = []Observation{{DeviceID: "r1", Entries: []snmp.ARPEntry{entry("10.0.0.1", "02:00:00:00:00:01")}}}
	p = Build(in)
	if len(p.Ops) != 1 || p.Ops[0].AddressID == "" {
		t.Fatalf("generated id %+v", p.Ops)
	}
}
