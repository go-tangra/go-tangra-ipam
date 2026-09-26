package hostreport

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
)

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// FuzzNormalizeReport feeds arbitrary strings into every field of a report.
// Contract: no panic; the report is either rejected or every output list is
// within bounds and every output string is printable UTF-8.
func FuzzNormalizeReport(f *testing.F) {
	f.Add(tenantA, hostID, "web", "aa:bb:cc:dd:ee:ff", "10.0.0.1", uint32(24), "10.0.0.0/8", "eth0", "ethernet", "101", "vm", "openssl", "1.0")
	f.Add(tenantB, "x", "\x00", "zz", "::1", uint32(200), "bad", "", "", "", "", "", "")
	f.Fuzz(func(t *testing.T, tenant, host, hostname, mac, addr string, prefix uint32, cidr, ifname, kind, guest, gkind, pkg, ver string) {
		r := invclient.Report{
			TenantID: tenant,
			Host:     invclient.Host{ID: host, Hostname: hostname, SystemSerial: hostname, OSName: ver},
			Interfaces: []invclient.Interface{
				{Name: ifname, MAC: mac, Type: kind, Addresses: []invclient.Address{{Address: addr, PrefixLength: prefix}}},
				{Name: ifname + "1", MAC: mac, IPAddresses: []string{cidr}},
			},
			PrimaryIPv4: addr, PrimaryIPv6: addr,
			Virtualization: invclient.Virtualization{Role: kind, Kind: kind},
			BMC:            &invclient.BMC{Address: addr, PrefixLength: prefix, Gateway: cidr, Ports: []invclient.BMCPort{{MAC: mac}}},
			Guests:         []invclient.Guest{{ID: guest, Kind: gkind, Name: hostname, MACs: []string{mac}}},
			PendingUpdates: []invclient.PendingUpdate{{Name: pkg, InstalledVersion: ver, AvailableVersion: ver}},
		}
		out, err := Normalize(r, tenantA)
		if err != nil {
			return
		}
		if len(out.Interfaces) > MaxInterfaces || len(out.Guests) > MaxGuests || len(out.Pending) > MaxPackages || len(out.Issues) > MaxIssues {
			t.Fatal("bounds")
		}
		strs := []string{out.Hostname, out.Serial, out.OSName, out.VirtKind}
		for _, i := range out.Interfaces {
			if len(i.Addresses) > MaxAddrsPerIface || len(i.Name) > MaxIfaceName || i.Name == "" {
				t.Fatal("interface bounds")
			}
			strs = append(strs, i.Name, i.MAC, i.Kind)
			for _, a := range i.Addresses {
				if a.Prefix < 0 || a.Prefix > a.Addr.BitLen() {
					t.Fatal("prefix")
				}
			}
		}
		for _, g := range out.Guests {
			strs = append(strs, g.Ref, g.Name, g.Kind)
			strs = append(strs, g.MACs...)
		}
		for _, p := range out.Pending {
			strs = append(strs, p.Name, p.Installed, p.Available)
		}
		for _, s := range strs {
			if !printable(s) || s != strings.TrimSpace(s) {
				t.Fatalf("unsafe output string %q", s)
			}
		}
	})
}

// FuzzExclusionPattern: a validated pattern never panics Excluded and is
// always a well-formed path.Match pattern.
func FuzzExclusionPattern(f *testing.F) {
	f.Add("docker*", "docker0")
	f.Add("[", "x")
	f.Add("eth?", "eth1")
	f.Fuzz(func(t *testing.T, pattern, name string) {
		_ = Excluded(name, "", []string{pattern})
		if ValidatePatterns([]string{pattern}) != nil {
			return
		}
		if len(pattern) > MaxPatternLen || strings.ContainsAny(pattern, `[\`) {
			t.Fatalf("accepted %q", pattern)
		}
	})
}
