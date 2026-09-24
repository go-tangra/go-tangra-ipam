package snmp

import (
	"context"
	"testing"
)

func TestFakeDiscover(t *testing.T) {
	f := NewFake()
	f.Set("10.0.0.1", DiscoveredDevice{
		SysName:    "sw1",
		DeviceType: devSwitch,
		Interfaces: []Interface{{Name: "Gi0/1", IfIndex: 1}},
	})
	dev, err := f.Discover(context.Background(), "10.0.0.1", Creds{Version: 2, Community: "public"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if dev.SysName != "sw1" || len(dev.Interfaces) != 1 {
		t.Fatalf("unexpected device: %+v", dev)
	}
	// A host with no configured response mirrors a silent host: error.
	if _, err := f.Discover(context.Background(), "10.0.0.2", Creds{}); err == nil {
		t.Fatal("expected error for silent host")
	}
}

func TestParseDeviceType(t *testing.T) {
	cases := []struct {
		oid, descr, want string
	}{
		{"1.3.6.1.4.1.9.1.1", "Cisco IOS, Catalyst 2960 switch", devSwitch},
		{"1.3.6.1.4.1.9.1.2", "Cisco ASR 1001 router", devRouter},
		{"1.3.6.1.4.1.1916.2.1", "ExtremeXOS (X670G2)", devSwitch},
		{"", "Linux server 5.10", devServer},
		{"", "something unknown", devOther},
	}
	for _, c := range cases {
		if got := parseDeviceType(c.oid, c.descr); got != c.want {
			t.Errorf("parseDeviceType(%q,%q) = %q, want %q", c.oid, c.descr, got, c.want)
		}
	}
}

func TestParseManufacturerAndModel(t *testing.T) {
	if parseManufacturer("Cisco IOS Software") != "Cisco" {
		t.Error("cisco manufacturer")
	}
	if parseManufacturer("totally unknown box") != "" {
		t.Error("unknown manufacturer should be empty")
	}
	model, os := parseModelAndOS("Cisco IOS XE Software (ASR1001-X), Version 16.9.1")
	if model != "ASR1001-X" || os != "16.9.1" {
		t.Fatalf("model=%q os=%q", model, os)
	}
}

func TestIfTypeToString(t *testing.T) {
	if ifTypeToString(6) != "ethernet" || ifTypeToString(24) != "loopback" {
		t.Error("known ifTypes")
	}
	if ifTypeToString(9999) != "type(9999)" {
		t.Error("unknown ifType fallback")
	}
}

func TestOIDHelpers(t *testing.T) {
	base := "1.3.6.1.2.1.2.2.1.2"
	if got := ifIndex("."+base+".7", base); got != 7 {
		t.Fatalf("ifIndex = %d, want 7", got)
	}
	if got := suffixInts("."+base+".10.20", base); len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Fatalf("suffixInts = %v", got)
	}
	if suffixInts(".9.9.9.1", base) != nil {
		t.Fatal("mismatched base should yield nil")
	}
	if got := lastInt("."+base+".3.99", base); got != 99 {
		t.Fatalf("lastInt = %d, want 99", got)
	}
}

func TestMacFromOctets(t *testing.T) {
	got := macFromOctets([]int{0x90, 0x5a, 0x08, 0xbe, 0xec, 0x3e})
	if got != "90:5a:08:be:ec:3e" {
		t.Fatalf("mac = %q", got)
	}
	if macFromOctets([]int{1, 2, 3}) != "" {
		t.Fatal("short octets should yield empty mac")
	}
}
