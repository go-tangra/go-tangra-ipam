package snmp

import (
	"context"
	"testing"

	"github.com/gosnmp/gosnmp"
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

// TestNewClientProtocols (T008): every supported protocol maps to its gosnmp
// constant and the security level decides the message flags.
func TestNewClientProtocols(t *testing.T) {
	auth := map[string]gosnmp.SnmpV3AuthProtocol{"MD5": gosnmp.MD5, "SHA": gosnmp.SHA, "SHA224": gosnmp.SHA224,
		"SHA256": gosnmp.SHA256, "SHA384": gosnmp.SHA384, "SHA512": gosnmp.SHA512}
	priv := map[string]gosnmp.SnmpV3PrivProtocol{"DES": gosnmp.DES, "AES": gosnmp.AES, "AES192": gosnmp.AES192, "AES256": gosnmp.AES256,
		"AES192C": gosnmp.AES192C, "AES256C": gosnmp.AES256C}
	for name, want := range auth {
		c, err := newClient("10.0.0.1", Creds{Version: 3, SecurityLevel: "authNoPriv", User: "u", AuthProtocol: name, AuthPassword: "authpass1"})
		if err != nil {
			t.Fatalf("auth %s: %v", name, err)
		}
		usm := c.SecurityParameters.(*gosnmp.UsmSecurityParameters)
		if usm.AuthenticationProtocol != want || c.MsgFlags != gosnmp.AuthNoPriv || usm.PrivacyProtocol != gosnmp.NoPriv || usm.PrivacyPassphrase != "" {
			t.Fatalf("auth %s: proto %v flags %v priv %v", name, usm.AuthenticationProtocol, c.MsgFlags, usm.PrivacyProtocol)
		}
		if c.Version != gosnmp.Version3 || c.SecurityModel != gosnmp.UserSecurityModel || usm.UserName != "u" {
			t.Fatalf("v3 client fields %+v", c)
		}
	}
	for name, want := range priv {
		c, err := newClient("10.0.0.1", Creds{Version: 3, SecurityLevel: "authPriv", User: "u", AuthProtocol: "SHA256",
			AuthPassword: "authpass1", PrivProtocol: name, PrivPassword: "privpass1"})
		if err != nil {
			t.Fatalf("priv %s: %v", name, err)
		}
		usm := c.SecurityParameters.(*gosnmp.UsmSecurityParameters)
		if usm.PrivacyProtocol != want || c.MsgFlags != gosnmp.AuthPriv || usm.PrivacyPassphrase != "privpass1" {
			t.Fatalf("priv %s: proto %v flags %v", name, usm.PrivacyProtocol, c.MsgFlags)
		}
	}
	for name, creds := range map[string]Creds{
		"unknown auth":  {Version: 3, SecurityLevel: "authNoPriv", User: "u", AuthProtocol: "SHA1", AuthPassword: "authpass1"},
		"unknown priv":  {Version: 3, SecurityLevel: "authPriv", User: "u", AuthProtocol: "SHA", AuthPassword: "authpass1", PrivProtocol: "3DES", PrivPassword: "privpass1"},
		"unknown level": {Version: 3, SecurityLevel: "noAuthNoPriv", User: "u", AuthProtocol: "SHA", AuthPassword: "authpass1"},
		"empty v2c":     {Version: 2},
		"version 1":     {Version: 1, Community: "c"},
	} {
		if _, err := newClient("10.0.0.1", creds); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// TestNewClientV2cAndTargets: no "public" default, IPv6 targets accepted,
// timeout and retries applied.
func TestNewClientV2cAndTargets(t *testing.T) {
	c, err := newClient("[2001:db8::5]:161", Creds{Version: 2, Community: "lab", TimeoutMs: 250, Retries: 3})
	if err != nil {
		t.Fatal(err)
	}
	if c.Target != "2001:db8::5" || c.Community != "lab" || c.Version != gosnmp.Version2c || c.Retries != 3 || c.Timeout.Milliseconds() != 250 || c.Port != defaultPort {
		t.Fatalf("client %+v", c)
	}
	c, err = newClient("fe80::1", Creds{Version: 2, Community: "lab"})
	if err != nil || c.Target != "fe80::1" || c.Timeout != defaultTimeout || c.Retries != defaultRetries {
		t.Fatalf("ipv6 target: %+v %v", c, err)
	}
}
