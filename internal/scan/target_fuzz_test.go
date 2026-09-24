package scan

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
)

// FuzzScanTargets asserts the target-enumeration invariants for arbitrary CIDR
// inputs: it never panics, never returns more than maxHosts targets, and never
// includes the network or broadcast address of an IPv4 subnet shorter than /31.
func FuzzScanTargets(f *testing.F) {
	seeds := []string{
		"10.0.0.0/29", "192.168.1.0/24", "172.16.0.0/30", "10.0.0.0/31",
		"10.0.0.5/32", "0.0.0.0/0", "10.0.0.0/20", "2001:db8::/64",
		"", "garbage", "10.0.0.0/33", "999.0.0.0/8", "10.0.0.0/-1",
	}
	for _, s := range seeds {
		f.Add(s, 1024)
	}

	f.Fuzz(func(t *testing.T, cidr string, maxHosts int) {
		if maxHosts < 0 {
			maxHosts = -maxHosts
		}
		if maxHosts > 1<<16 {
			maxHosts = 1 << 16
		}

		targets, err := enumerateTargets(cidr, maxHosts)
		if err != nil {
			// Invalid or too-large CIDRs are refused, not panicked on.
			return
		}

		if maxHosts > 0 && len(targets) > maxHosts {
			t.Fatalf("enumerated %d targets, exceeds bound %d", len(targets), maxHosts)
		}

		info, perr := ipnet.Parse(cidr)
		if perr != nil {
			return
		}
		network := info.Network.String()
		broadcast := info.Broadcast.String()
		for _, ip := range targets {
			s := ip.String()
			if info.Version == 4 && info.PrefixLen < 31 {
				if s == network {
					t.Fatalf("target %s is the network address", s)
				}
				if s == broadcast {
					t.Fatalf("target %s is the broadcast address", s)
				}
			}
		}
	})
}
