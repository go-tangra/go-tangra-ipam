package ipnet

import (
	"net/netip"
	"testing"
)

// FuzzParseAllocate drives Parse and the allocators with arbitrary CIDR, skip
// and gateway strings. Contract: nothing panics, and whenever FirstFree returns
// an address it is a valid host inside the CIDR, is not the gateway and is not
// excluded.
func FuzzParseAllocate(f *testing.F) {
	f.Add("10.0.0.0/24", 0, 0, "10.0.0.1")
	f.Add("192.168.1.0/30", 1, 1, "")
	f.Add("2001:db8::/64", 0, 0, "2001:db8::1")
	f.Add("10.0.0.0/8", 100000, 100000, "10.0.0.1")
	f.Add("bad-cidr", -3, 7, "garbage")

	f.Fuzz(func(t *testing.T, cidr string, skipFirst, skipLast int, gateway string) {
		// Bound the skip magnitudes so the fuzzer cannot ask for a pathological
		// big.Int shift; the allocators themselves must still not panic.
		if skipFirst > 1<<20 {
			skipFirst = 1 << 20
		}
		if skipLast > 1<<20 {
			skipLast = 1 << 20
		}

		if _, err := Parse(cidr); err != nil {
			// Invalid CIDR: every entry point must return an error, never panic.
			if _, err := FirstFree(cidr, nil, gateway, skipFirst, skipLast); err == nil {
				t.Fatalf("FirstFree accepted invalid cidr %q", cidr)
			}
			return
		}

		// Exercise the read-only helpers (must not panic).
		_, _ = Hosts(cidr)
		_, _ = Overlaps(cidr, "10.0.0.0/24")
		_, _ = Contains(cidr, gateway)
		_ = InRange("10.0.0.1-10.0.0.20", gateway)

		exclude := map[string]bool{"10.0.0.1": true}
		ip, err := FirstFree(cidr, exclude, gateway, skipFirst, skipLast)
		if err != nil {
			return
		}
		// The returned address must sit inside the CIDR.
		if ok, cerr := Contains(cidr, ip.String()); cerr != nil || !ok {
			t.Fatalf("FirstFree(%q) returned %s not contained (ok=%v err=%v)", cidr, ip, ok, cerr)
		}
		// It must not be the gateway.
		if gw := normalizeIP(gateway); gw != "" && ip.String() == gw {
			t.Fatalf("FirstFree returned the gateway %s", gw)
		}
		// It must not be an excluded address.
		if exclude[ip.String()] {
			t.Fatalf("FirstFree returned excluded %s", ip)
		}

		// BulkFree of 2 must also stay inside the CIDR when it succeeds.
		if ips, berr := BulkFree(cidr, exclude, gateway, skipFirst, skipLast, 2); berr == nil {
			for _, b := range ips {
				if ok, _ := Contains(cidr, b.String()); !ok {
					t.Fatalf("BulkFree returned %s not contained in %q", b, cidr)
				}
			}
		}
	})
}

// FuzzMostSpecific: whenever MostSpecific returns a candidate, its prefix
// contains the address and no candidate with a longer prefix contains it.
func FuzzMostSpecific(f *testing.F) {
	f.Add("10.1.2.3", "10.0.0.0/8", "10.1.0.0/16", "10.1.2.0/24")
	f.Add("2001:db8::1", "2001:db8::/32", "::/0", "bad")
	f.Add("::ffff:1.2.3.4", "1.2.3.0/24", "1.0.0.0/8", "0.0.0.0/0")
	f.Fuzz(func(t *testing.T, addr, c1, c2, c3 string) {
		a, err := netip.ParseAddr(addr)
		if err != nil {
			return
		}
		cands := []Candidate{{"1", c1}, {"2", c2}, {"3", c3}}
		got, ok := MostSpecific(cands, a)
		a = a.Unmap().WithZone("")
		best := -1
		for _, c := range cands {
			p, err := netip.ParsePrefix(c.CIDR)
			if err == nil && p.Masked().Contains(a) && p.Bits() > best {
				best = p.Bits()
			}
		}
		if !ok {
			if best >= 0 {
				t.Fatalf("no match but %d-bit candidate contains %s", best, a)
			}
			return
		}
		p := netip.MustParsePrefix(got.CIDR)
		if !p.Masked().Contains(a) || p.Bits() != best {
			t.Fatalf("got %s for %s, best bits %d", got.CIDR, a, best)
		}
		if _, err := NetworkOf(a, p.Bits()); err != nil {
			t.Fatal(err)
		}
		_ = AutoPrefix(a, p.Bits())
		_ = Classify(a).Recordable()
	})
}
