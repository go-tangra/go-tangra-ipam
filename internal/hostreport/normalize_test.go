package hostreport

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
	hostID  = "0190f7c2-6a3e-7c1a-9b2e-aaaaaaaaaaaa"
	digest  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func valid() invclient.Report {
	return invclient.Report{
		TenantID: tenantA,
		Host: invclient.Host{ID: hostID, Hostname: " web-01 ", SystemSerial: "SN123", Manufacturer: "Dell Inc.",
			Model: "R650", OSName: "Ubuntu", OSVersion: "24.04", Status: "active", LastSeen: time.Unix(100, 0)},
		SnapshotID: "snap-1", CollectedAt: time.Unix(90, 0), ChangedAt: time.Unix(95, 0), Digest: digest,
		AgentVersion: "4.3.0", OSFamily: "linux",
		Interfaces: []invclient.Interface{
			{Name: "eth0", MAC: "AA-BB-CC-DD-EE-01", Type: "ethernet", Up: true, SpeedBps: 10_000_000_000,
				Addresses: []invclient.Address{
					{Address: "10.0.0.5", PrefixLength: 24, Family: "ipv4"},
					{Address: "10.0.0.5", PrefixLength: 24}, // duplicate
					{Address: "2001:db8::5", PrefixLength: 64, Temporary: true},
				}},
			{Name: "lo", Type: "", IPAddresses: []string{"127.0.0.1/8", "::1/128"}},
			{Name: "ens3", MAC: "", Type: "", IPAddresses: []string{"192.168.1.10/24", "bad"}},
		},
		PrimaryIPv4: "10.0.0.5", PrimaryIPv6: "2001:db8::5",
		Virtualization: invclient.Virtualization{Role: "vm", Kind: "KVM"},
		BMC: &invclient.BMC{Address: "10.9.0.5", PrefixLength: 24, Gateway: "10.9.0.1",
			Ports: []invclient.BMCPort{{Channel: 1, MAC: "aa:bb:cc:dd:ee:10"}, {Channel: 2, MAC: "aa:bb:cc:dd:ee:10"}}},
		Guests: []invclient.Guest{{ID: "101", Name: "vm-a", Kind: "vm", MACs: []string{"BC:24:11:00:00:01", "bc:24:11:00:00:01", "zz"}}},
		Updates: invclient.UpdateState{PackageManager: "apt", Status: "updates_available", RebootRequired: "true",
			AutomaticUpdates: "false", SecurityClassified: true},
		PendingUpdates: []invclient.PendingUpdate{{Name: "openssl", InstalledVersion: "3.0.1", AvailableVersion: "3.0.2", Security: true}},
	}
}

func issue(t *testing.T, r Report, field, reason string) int {
	t.Helper()
	for _, i := range r.Issues {
		if i.Field == field && i.Reason == reason {
			return i.Count
		}
	}
	return 0
}

func TestNormalizeValid(t *testing.T) {
	r, err := Normalize(valid(), tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "web-01" || r.HostID != hostID || r.Serial != "SN123" || r.OSFamily != "linux" || r.Digest != digest {
		t.Fatalf("identity: %+v", r)
	}
	if len(r.Interfaces) != 3 {
		t.Fatalf("interfaces: %+v", r.Interfaces)
	}
	eth := r.Interfaces[0]
	if eth.MAC != "aa:bb:cc:dd:ee:01" || eth.Kind != KindEthernet || eth.SpeedMbps != 10000 || len(eth.Addresses) != 2 || !eth.Addresses[1].Temporary {
		t.Fatalf("eth0: %+v", eth)
	}
	if r.Interfaces[1].Kind != KindLoopback || len(r.Interfaces[1].Addresses) != 2 {
		t.Fatalf("old-agent lo: %+v", r.Interfaces[1])
	}
	if r.Interfaces[2].Kind != "" || len(r.Interfaces[2].Addresses) != 1 || r.Interfaces[2].Addresses[0].Prefix != 24 {
		t.Fatalf("old-agent ens3: %+v", r.Interfaces[2])
	}
	if issue(t, r, "address", "invalid") != 1 {
		t.Fatalf("bad CIDR issue: %+v", r.Issues)
	}
	if r.PrimaryIPv4 != netip.MustParseAddr("10.0.0.5") || r.PrimaryIPv6 != netip.MustParseAddr("2001:db8::5") {
		t.Fatal("primary")
	}
	if r.VirtRole != RoleVM || r.VirtKind != "kvm" {
		t.Fatalf("virt %q %q", r.VirtRole, r.VirtKind)
	}
	if r.BMC == nil || r.BMC.Prefix != 24 || len(r.BMC.Ports) != 1 || r.BMC.Gateway != netip.MustParseAddr("10.9.0.1") {
		t.Fatalf("bmc: %+v", r.BMC)
	}
	if len(r.Guests) != 1 || len(r.Guests[0].MACs) != 1 || r.Guests[0].Platform != "proxmox" || issue(t, r, "guest_mac", "invalid_mac") != 1 {
		t.Fatalf("guests: %+v issues %+v", r.Guests, r.Issues)
	}
	if r.Updates.Manager != "apt" || r.Updates.Status != store.UpdAvailable || r.Updates.RebootRequired != TriTrue || len(r.Pending) != 1 {
		t.Fatalf("updates %+v", r.Updates)
	}
	if !strings.Contains(r.String(), hostID) {
		t.Fatal("String")
	}
}

func TestNormalizeRejects(t *testing.T) {
	r := valid()
	if _, err := Normalize(r, tenantB); !errors.Is(err, ErrTenant) {
		t.Fatalf("cross-tenant report must be rejected: %v", err)
	}
	if _, err := Normalize(r, "not-a-uuid"); !errors.Is(err, ErrTenant) {
		t.Fatal("bad requested tenant")
	}
	r.Host.ID = "host-1"
	if _, err := Normalize(r, tenantA); !errors.Is(err, ErrHostID) {
		t.Fatalf("non-uuid host id must be rejected: %v", err)
	}
	up := valid()
	up.TenantID = strings.ToUpper(tenantA)
	if out, err := Normalize(up, tenantA); err != nil || out.TenantID != tenantA {
		t.Fatalf("case-insensitive tenant: %v", err)
	}
}

func TestNormalizeMAC(t *testing.T) {
	cases := map[string]string{
		"AA:BB:CC:DD:EE:FF":       "", // broadcast-ish? no: first byte 0xAA is even -> valid
		"aa-bb-cc-dd-ee-ff":       "",
		"00:00:00:00:00:00":       "x",
		"ff:ff:ff:ff:ff:ff":       "x",
		"01:00:5e:00:00:01":       "x", // multicast
		"zz":                      "x",
		"00:11:22:33:44:55:66:77": "x", // EUI-64
	}
	for in, bad := range cases {
		m, ok := NormalizeMAC(in)
		if ok == (bad == "x") {
			t.Errorf("NormalizeMAC(%q) ok=%v", in, ok)
		}
		if ok && m != strings.ToLower(strings.ReplaceAll(in, "-", ":")) {
			t.Errorf("NormalizeMAC(%q) = %q", in, m)
		}
	}
}

func TestNormalizeBadStrings(t *testing.T) {
	r := valid()
	r.Host.Hostname = "web\x07bell"
	r.Host.Manufacturer = strings.Repeat("m", MaxText+1)
	r.Host.Model = "\xff\xfe"
	r.Host.Status = "exploded"
	r.OSFamily = "plan9"
	r.Digest = "nothex"
	r.Interfaces = []invclient.Interface{
		{Name: strings.Repeat("i", MaxIfaceName+1)},
		{Name: "eth\n0"},
		{Name: "   "},
		{Name: "eth0", MAC: "01:02:03:04:05:06", Type: "Token-Ring", SpeedBps: MaxSpeedBps + 1},
		{Name: "eth0"},
	}
	r.PrimaryIPv4 = "2001:db8::1"
	r.PrimaryIPv6 = "garbage"
	r.Virtualization = invclient.Virtualization{Role: "robot", Kind: "Not Valid!"}
	r.Updates = invclient.UpdateState{PackageManager: "brew", Status: "great", RebootRequired: "maybe", AutomaticUpdates: ""}
	out, err := Normalize(r, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if out.Hostname != "" || issue(t, out, "hostname", "control_characters") != 1 {
		t.Fatal("hostname with control characters must be dropped with an issue")
	}
	if out.Manufacturer != "" || issue(t, out, "manufacturer", "too_long") != 1 || issue(t, out, "model", "invalid_utf8") != 1 {
		t.Fatalf("text issues %+v", out.Issues)
	}
	if out.HostStatus != "" || out.OSFamily != "" || out.Digest != "" || issue(t, out, "report_digest", "invalid") != 1 {
		t.Fatal("closed sets")
	}
	if len(out.Interfaces) != 1 || out.Interfaces[0].MAC != "" || out.Interfaces[0].Kind != KindOther || out.Interfaces[0].SpeedMbps != 0 {
		t.Fatalf("interfaces %+v", out.Interfaces)
	}
	for _, want := range [][2]string{{"interface_name", "too_long"}, {"interface_name", "control_characters"},
		{"interface_name", "empty"}, {"interface_name", "duplicate"}, {"interface_mac", "invalid_mac"},
		{"interface_kind", "unknown_value"}, {"interface_speed", "out_of_range"}, {"virtualization_role", "unknown_value"},
		{"virtualization_kind", "invalid"}, {"package_manager", "unknown_value"}, {"update_status", "unknown_value"},
		{"reboot_required", "unknown_value"}} {
		if issue(t, out, want[0], want[1]) == 0 {
			t.Errorf("missing issue %v in %+v", want, out.Issues)
		}
	}
	if issue(t, out, "primary_address", "invalid") != 2 || out.PrimaryIPv4.IsValid() || out.PrimaryIPv6.IsValid() {
		t.Fatal("primary addresses of the wrong family / unparsable must be dropped")
	}
	if out.VirtRole != RoleUnknown || out.Updates.Status != store.UpdUnknown || out.Updates.AutomaticUpdates != TriUnknown {
		t.Fatal("defaults")
	}
	for _, i := range out.Issues {
		if strings.ContainsAny(i.Field+i.Reason, "\x07\n") {
			t.Fatal("issue text must never carry reported bytes")
		}
	}
}

func TestNormalizeAddresses(t *testing.T) {
	r := valid()
	var addrs []invclient.Address
	for i := 0; i < MaxAddrsPerIface+1; i++ {
		addrs = append(addrs, invclient.Address{Address: fmt.Sprintf("10.1.%d.%d", i/250, i%250+1), PrefixLength: 16})
	}
	addrs = append(addrs, invclient.Address{Address: "10.0.0.1", PrefixLength: 33}, invclient.Address{Address: "x"})
	r.Interfaces = []invclient.Interface{{Name: "eth0", Addresses: addrs}}
	out, _ := Normalize(r, tenantA)
	if len(out.Interfaces[0].Addresses) != MaxAddrsPerIface || issue(t, out, "addresses", "truncated") != 1 || issue(t, out, "address", "invalid") != 2 {
		t.Fatalf("per-interface bound: %d %+v", len(out.Interfaces[0].Addresses), out.Issues)
	}
	// total bound
	var ifs []invclient.Interface
	for i := 0; i < 20; i++ {
		var as []invclient.Address
		for j := 0; j < 60; j++ {
			as = append(as, invclient.Address{Address: fmt.Sprintf("10.%d.%d.1", i, j), PrefixLength: 24})
		}
		ifs = append(ifs, invclient.Interface{Name: fmt.Sprintf("eth%d", i), Addresses: as})
	}
	r.Interfaces = ifs
	out, _ = Normalize(r, tenantA)
	n := 0
	for _, i := range out.Interfaces {
		n += len(i.Addresses)
	}
	if n != MaxAddrs || issue(t, out, "addresses", "truncated") != 20*60-MaxAddrs {
		t.Fatalf("total bound: %d %+v", n, out.Issues)
	}
}

func TestNormalizeBounds(t *testing.T) {
	r := valid()
	r.Interfaces = make([]invclient.Interface, MaxInterfaces+1)
	for i := range r.Interfaces {
		r.Interfaces[i] = invclient.Interface{Name: fmt.Sprintf("if%d", i)}
	}
	r.Guests = make([]invclient.Guest, MaxGuests+1)
	for i := range r.Guests {
		r.Guests[i] = invclient.Guest{ID: fmt.Sprint(100 + i), Kind: "container"}
	}
	r.Guests[0].MACs = make([]string, MaxGuestMACs+1)
	for i := range r.Guests[0].MACs {
		r.Guests[0].MACs[i] = fmt.Sprintf("02:00:00:00:00:%02x", i)
	}
	r.PendingUpdates = make([]invclient.PendingUpdate, MaxPackages+1)
	for i := range r.PendingUpdates {
		r.PendingUpdates[i] = invclient.PendingUpdate{Name: fmt.Sprintf("p%d", i), AvailableVersion: "2"}
	}
	r.BMC = &invclient.BMC{Ports: make([]invclient.BMCPort, MaxBMCPorts+1)}
	for i := range r.BMC.Ports {
		r.BMC.Ports[i] = invclient.BMCPort{Channel: uint32(i), MAC: fmt.Sprintf("02:00:00:00:01:%02x", i)}
	}
	r.Truncated = invclient.Limits{Interfaces: 3, Addresses: 4, Guests: 5, Packages: 6, BMCPorts: 7}
	out, _ := Normalize(r, tenantA)
	if len(out.Interfaces) != MaxInterfaces || len(out.Guests) != MaxGuests || len(out.Guests[0].MACs) != MaxGuestMACs ||
		len(out.Pending) != MaxPackages || len(out.BMC.Ports) != MaxBMCPorts {
		t.Fatal("bounds not applied")
	}
	for f, n := range map[string]int{"interfaces": 4, "addresses": 4, "guests": 6, "packages": 7, "bmc_ports": 8, "guest_macs": 1} {
		reason := "truncated"
		if got := issue(t, out, f, reason); got != n {
			t.Errorf("%s truncated = %d want %d", f, got, n)
		}
	}
	if out.BMC.Address.IsValid() {
		t.Fatal("bmc without address keeps its ports only")
	}
}

func TestNormalizeGuestsPackagesBMC(t *testing.T) {
	r := valid()
	r.Guests = []invclient.Guest{
		{ID: "bad ref", Kind: "vm"}, {ID: "101", Kind: "vm"}, {ID: "101", Kind: "vm"},
		{ID: "102", Kind: "pod"}, {ID: "103", Kind: "CONTAINER", Name: "a\x00b", Platform: "lxd"},
	}
	r.PendingUpdates = []invclient.PendingUpdate{
		{Name: ""}, {Name: "a\x01"}, {Name: "ok", InstalledVersion: strings.Repeat("1", MaxVersion+1)},
		{Name: "ok2"}, {Name: "ok2"},
	}
	r.BMC = &invclient.BMC{Address: "x", PrefixLength: 40, Gateway: "0.0.0.0", Ports: []invclient.BMCPort{{MAC: "ff:ff:ff:ff:ff:ff"}}}
	out, _ := Normalize(r, tenantA)
	if len(out.Guests) != 2 || out.Guests[1].Kind != "container" || out.Guests[1].Name != "" || out.Guests[1].Platform != "lxd" {
		t.Fatalf("guests %+v", out.Guests)
	}
	for _, want := range [][2]string{{"guest_ref", "invalid"}, {"guest_ref", "duplicate"}, {"guest_kind", "invalid"},
		{"guest_name", "control_characters"}, {"package_name", "empty"}, {"package_name", "control_characters"},
		{"package_version", "too_long"}, {"package_name", "duplicate"}, {"bmc_address", "invalid"}, {"bmc_prefix", "invalid"},
		{"bmc_mac", "invalid_mac"}} {
		if issue(t, out, want[0], want[1]) == 0 {
			t.Errorf("missing issue %v in %+v", want, out.Issues)
		}
	}
	if out.BMC != nil || len(out.Pending) != 1 {
		t.Fatalf("unreadable BMC must be absent (%+v), packages %+v", out.BMC, out.Pending)
	}
	// Unspecified BMC address = not configured.
	r.BMC = &invclient.BMC{Address: "0.0.0.0"}
	if out, _ := Normalize(r, tenantA); out.BMC != nil {
		t.Fatal("0.0.0.0 BMC must be absent")
	}
	r.BMC = nil
	if out, _ := Normalize(r, tenantA); out.BMC != nil {
		t.Fatal("nil BMC")
	}
}

func TestIssueCap(t *testing.T) {
	is := issues{}
	for i := 0; i < MaxIssues+5; i++ {
		is.add(fmt.Sprintf("f%03d", i), "r", 1)
	}
	is.add("zero", "r", 0)
	if l := is.list(); len(l) != MaxIssues || l[0].Field != "f000" {
		t.Fatalf("cap: %d", len(l))
	}
	two := issues{}
	two.add("a", "y", 1)
	two.add("a", "x", 1)
	if l := two.list(); l[0].Reason != "x" {
		t.Fatal("sort by reason")
	}
}

func TestNormalizeNoPrimary(t *testing.T) {
	r := valid()
	r.PrimaryIPv4, r.PrimaryIPv6 = "", " "
	out, _ := Normalize(r, tenantA)
	if out.PrimaryIPv4.IsValid() || out.PrimaryIPv6.IsValid() || issue(t, out, "primary_address", "invalid") != 0 {
		t.Fatal("absent primary is not an issue")
	}
}
