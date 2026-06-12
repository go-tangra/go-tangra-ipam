package biz

import (
	"reflect"
	"testing"
)

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "90:5a:08:be:ec:3e", "90:5a:08:be:ec:3e"},
		{"uppercase colon", "90:5A:08:BE:EC:3E", "90:5a:08:be:ec:3e"},
		{"dashes", "90-5A-08-BE-EC-3E", "90:5a:08:be:ec:3e"},
		{"cisco dotted", "905a.08be.ec3e", "90:5a:08:be:ec:3e"},
		{"no separators", "905a08beec3e", "90:5a:08:be:ec:3e"},
		{"surrounding space", "  90:5a:08:be:ec:3e  ", "90:5a:08:be:ec:3e"},
		{"not a mac passthrough lower", "Not-A-Mac", "not-a-mac"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeMAC(tt.in); got != tt.want {
				t.Fatalf("NormalizeMAC(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOIDSuffixHelpers(t *testing.T) {
	base := oidDot1qTpFdbPort
	oid := "." + base + ".10.144.90.8.190.236.62" // vlan 10 + MAC 90:5a:08:be:ec:3e

	suffix := oidSuffixInts(oid, base)
	wantSuffix := []int{10, 144, 90, 8, 190, 236, 62}
	if !reflect.DeepEqual(suffix, wantSuffix) {
		t.Fatalf("oidSuffixInts = %v, want %v", suffix, wantSuffix)
	}
	if mac := macFromOctets(suffix[1:]); mac != "90:5a:08:be:ec:3e" {
		t.Fatalf("macFromOctets = %q, want 90:5a:08:be:ec:3e", mac)
	}
	if got := lastOIDInt("."+oidDot1dBasePortIfIndex+".7", oidDot1dBasePortIfIndex); got != 7 {
		t.Fatalf("lastOIDInt = %d, want 7", got)
	}
	// Not under base.
	if oidSuffixInts(".1.2.3.4", base) != nil {
		t.Fatal("oidSuffixInts should be nil for an unrelated OID")
	}
	// Wrong octet length is rejected by callers via len(suffix) checks.
	if macFromOctets([]int{1, 2, 3}) != "" {
		t.Fatal("macFromOctets should return empty for non-6 octet input")
	}
}

func TestPduToInt(t *testing.T) {
	cases := []struct {
		v    interface{}
		want int
		ok   bool
	}{
		{7, 7, true},
		{uint(7), 7, true},
		{uint32(7), 7, true},
		{int64(7), 7, true},
		{"7", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := pduToInt(c.v)
		if got != c.want || ok != c.ok {
			t.Fatalf("pduToInt(%v) = (%d,%v), want (%d,%v)", c.v, got, ok, c.want, c.ok)
		}
	}
}

func TestRankAccessPorts(t *testing.T) {
	const (
		server  = "90:5a:08:be:ec:3e"
		otherA  = "aa:aa:aa:aa:aa:01"
		otherB  = "aa:aa:aa:aa:aa:02"
		otherC  = "aa:aa:aa:aa:aa:03"
		behind  = "bb:bb:bb:bb:bb:01"
	)

	// ifIndex 10 = access port (learns only the server MAC).
	// ifIndex 48 = uplink/trunk (learns the server MAC + many others).
	fdb := []FDBEntry{
		{MAC: server, IfIndex: 10, VLAN: 100},
		{MAC: server, IfIndex: 48, VLAN: 100}, // same MAC also seen via uplink
		{MAC: otherA, IfIndex: 48},
		{MAC: otherB, IfIndex: 48},
		{MAC: otherC, IfIndex: 48},
		{MAC: behind, IfIndex: 48}, // only ever seen on the uplink
	}

	got := RankAccessPorts(fdb)

	// Server MAC resolves to the access port (10, 1 MAC), not the uplink (48).
	entry, ok := got[server]
	if !ok {
		t.Fatalf("expected server MAC to be ranked")
	}
	if entry.IfIndex != 10 || entry.VLAN != 100 {
		t.Fatalf("server MAC chose ifIndex %d vlan %d, want 10/100 (access port)", entry.IfIndex, entry.VLAN)
	}
	if entry.MACCount != 1 {
		t.Fatalf("server access port MACCount = %d, want 1", entry.MACCount)
	}

	// A MAC only seen on the uplink keeps that port, with a high MACCount so the
	// caller's threshold can drop it.
	b, ok := got[behind]
	if !ok || b.IfIndex != 48 {
		t.Fatalf("behind MAC should rank to uplink ifIndex 48, got %+v", b)
	}
	if b.MACCount < 4 {
		t.Fatalf("uplink port MACCount = %d, want >= 4 (it carries many MACs)", b.MACCount)
	}
}

func TestRankAccessPortsUppercaseDeduped(t *testing.T) {
	// The same MAC in different cases must be treated as one.
	fdb := []FDBEntry{
		{MAC: "90:5A:08:BE:EC:3E", IfIndex: 5, VLAN: 1},
		{MAC: "90:5a:08:be:ec:3e", IfIndex: 5, VLAN: 1},
	}
	got := RankAccessPorts(fdb)
	if len(got) != 1 {
		t.Fatalf("expected 1 normalized MAC, got %d", len(got))
	}
	if _, ok := got["90:5a:08:be:ec:3e"]; !ok {
		t.Fatalf("expected normalized lower-case key")
	}
}

func TestParseDeviceTypeExtreme(t *testing.T) {
	// ExtremeXOS sysDescr lacks the word "switch"; detection must still classify
	// it as a switch (type 4) so the bridge-FDB walk runs.
	cases := []struct {
		name        string
		sysObjectID string
		sysDescr    string
		want        int32
	}{
		{"extreme by sysObjectID", "1.3.6.1.4.1.1916.2.76", "ExtremeXOS (X670G2-48x-4q) version 30.7.2.1", 4},
		{"extreme by descr only", "1.3.6.1.4.1.99999.1", "ExtremeXOS (X670G2-48x-4q) version 30.7.2.1", 4},
		{"extreme switch engine", "1.3.6.1.4.1.1916.2.169", "Extreme Networks Switch Engine (Stack) version 32.3.1.11", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDeviceType(c.sysObjectID, c.sysDescr); got != c.want {
				t.Fatalf("parseDeviceType(%q,%q) = %d, want %d", c.sysObjectID, c.sysDescr, got, c.want)
			}
		})
	}
}
