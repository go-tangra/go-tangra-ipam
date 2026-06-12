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

func TestSelectAccessPorts(t *testing.T) {
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

	got := SelectAccessPorts(fdb, 3)

	// Server MAC resolves to the access port (10), not the uplink (48).
	entry, ok := got[server]
	if !ok {
		t.Fatalf("expected server MAC to be selected")
	}
	if entry.IfIndex != 10 {
		t.Fatalf("server MAC chose ifIndex %d, want 10 (access port)", entry.IfIndex)
	}
	if entry.VLAN != 100 {
		t.Fatalf("server MAC VLAN = %d, want 100", entry.VLAN)
	}

	// A MAC only ever seen on the over-threshold uplink must be dropped.
	if _, ok := got[behind]; ok {
		t.Fatalf("MAC seen only on uplink (5 MACs > maxMACs 3) should be omitted")
	}
}

func TestSelectAccessPortsUppercaseDeduped(t *testing.T) {
	// The same MAC in different cases must be treated as one.
	fdb := []FDBEntry{
		{MAC: "90:5A:08:BE:EC:3E", IfIndex: 5, VLAN: 1},
		{MAC: "90:5a:08:be:ec:3e", IfIndex: 5, VLAN: 1},
	}
	got := SelectAccessPorts(fdb, 16)
	if len(got) != 1 {
		t.Fatalf("expected 1 normalized MAC, got %d", len(got))
	}
	if _, ok := got["90:5a:08:be:ec:3e"]; !ok {
		t.Fatalf("expected normalized lower-case key")
	}
}
