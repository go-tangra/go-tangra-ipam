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

func TestParseMetadataInterfacesEmptyOrInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", `{"interfaces":null}`, `{}`} {
		if got := parseMetadataInterfaces(in); len(got) != 0 {
			t.Fatalf("parseMetadataInterfaces(%q) = %v, want empty", in, got)
		}
	}
}
