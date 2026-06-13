package service

import "testing"

func TestParseMetadataInterfaces(t *testing.T) {
	// Shape produced by tangra-client's buildMetadataJSON.
	const md = `{"machine_id":"abc","interfaces":[` +
		`{"name":"ens7f0","mac_address":"90:5a:08:c2:43:90","ips":null},` +
		`{"name":"bond0.11","mac_address":"90:5A:08:BE:EC:3E","ips":["10.1.111.20"],"cidrs":["10.1.111.20/24"]},` +
		`{"name":"","mac_address":"00:00:00:00:00:00"}]}`

	got := parseMetadataInterfaces(md)
	if len(got) != 3 {
		t.Fatalf("expected 3 parsed interfaces, got %d", len(got))
	}
	if got[1].Name != "bond0.11" || got[1].MACAddress != "90:5A:08:BE:EC:3E" {
		t.Fatalf("unexpected second interface: %+v", got[1])
	}
}

func TestParseMetadataInterfacesIncludesIPMI(t *testing.T) {
	const md = `{"interfaces":[{"name":"eth0","mac_address":"aa:bb:cc:dd:ee:ff"}],` +
		`"ipmi":{"ip":"10.1.112.18","mac":"7c:c2:55:61:15:c9","gateway":"10.1.112.254","subnet":"255.255.255.0"}}`

	got := parseMetadataInterfaces(md)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (eth0 + ipmi), got %d: %+v", len(got), got)
	}
	last := got[len(got)-1]
	if last.Name != "ipmi" || last.MACAddress != "7c:c2:55:61:15:c9" {
		t.Fatalf("expected synthetic ipmi interface, got %+v", last)
	}

	// No ipmi MAC -> no synthetic entry.
	noIPMI := parseMetadataInterfaces(`{"interfaces":[{"name":"eth0","mac_address":"aa:bb:cc:dd:ee:ff"}],"ipmi":{"ip":"10.1.112.18"}}`)
	if len(noIPMI) != 1 {
		t.Fatalf("expected only eth0 when ipmi has no MAC, got %d", len(noIPMI))
	}
}

func TestParseMetadataInterfacesMultipleIPMIMACs(t *testing.T) {
	// BMC with a shared (active, IP-configured) channel and a dedicated channel,
	// each with its own MAC. The primary becomes "ipmi"; the extra becomes
	// "ipmi-2"; duplicates of the primary are not repeated.
	const md = `{"interfaces":[],"ipmi":{"ip":"10.1.112.237","mac":"3c:ec:ef:ef:09:3d",` +
		`"macs":["3c:ec:ef:ef:09:3d","3c:ec:ef:ef:09:3e"]}}`

	got := parseMetadataInterfaces(md)
	if len(got) != 2 {
		t.Fatalf("expected ipmi + ipmi-2, got %d: %+v", len(got), got)
	}
	if got[0].Name != "ipmi" || got[0].MACAddress != "3c:ec:ef:ef:09:3d" {
		t.Fatalf("unexpected primary ipmi interface: %+v", got[0])
	}
	if got[1].Name != "ipmi-2" || got[1].MACAddress != "3c:ec:ef:ef:09:3e" {
		t.Fatalf("unexpected dedicated ipmi-2 interface: %+v", got[1])
	}
}

func TestParseMetadataInterfacesEmptyOrInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", `{"interfaces":null}`, `{}`} {
		if got := parseMetadataInterfaces(in); len(got) != 0 {
			t.Fatalf("parseMetadataInterfaces(%q) = %v, want empty", in, got)
		}
	}
}
