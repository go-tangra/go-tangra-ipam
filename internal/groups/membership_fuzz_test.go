package groups_test

import (
	"math/big"
	"net"
	"strings"
	"testing"

	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// ipToInt is an independent reference conversion (v4 uses 4 bytes, v6 16) used to
// cross-check range/subnet matching without reusing the ipnet implementation.
func ipToInt(ip net.IP) (*big.Int, bool) {
	if ip == nil {
		return nil, false
	}
	i := new(big.Int)
	if v4 := ip.To4(); v4 != nil {
		i.SetBytes(v4)
		return i, true
	}
	i.SetBytes(ip.To16())
	return i, true
}

// refRange is an independent reference for inclusive lo-hi range membership.
func refRange(spec, ip string) bool {
	target := net.ParseIP(strings.TrimSpace(ip))
	ti, ok := ipToInt(target)
	if !ok {
		return false
	}
	lo, hi, dash := strings.Cut(strings.TrimSpace(spec), "-")
	if !dash {
		single := net.ParseIP(strings.TrimSpace(spec))
		si, ok := ipToInt(single)
		return ok && si.Cmp(ti) == 0
	}
	loIP := net.ParseIP(strings.TrimSpace(lo))
	hiIP := net.ParseIP(strings.TrimSpace(hi))
	li, ok1 := ipToInt(loIP)
	hj, ok2 := ipToInt(hiIP)
	if !ok1 || !ok2 {
		return false
	}
	return ti.Cmp(li) >= 0 && ti.Cmp(hj) <= 0
}

// refSubnet is an independent reference for CIDR containment.
func refSubnet(cidr, ip string) bool {
	_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	p := net.ParseIP(strings.TrimSpace(ip))
	if p == nil {
		return false
	}
	return n.Contains(p)
}

func FuzzMembership(f *testing.F) {
	seeds := []struct {
		mt        uint8
		value, ip string
	}{
		{0, "10.0.0.1", "10.0.0.1"},
		{1, "10.0.0.1-10.0.0.20", "10.0.0.15"},
		{2, "192.168.0.0/24", "192.168.0.42"},
		{3, "whatever", "not-an-ip"},
		{1, "10.0.0.1-10.0.0.20", "10.0.0.21"},
		{2, "2001:db8::/32", "2001:db8::1"},
		{0, "::1", "::1"},
	}
	for _, s := range seeds {
		f.Add(s.mt, s.value, s.ip)
	}

	types := []string{store.MemberAddress, store.MemberRange, store.MemberSubnet, "unknown"}

	f.Fuzz(func(t *testing.T, mtSel uint8, value, ip string) {
		mt := types[int(mtSel)%len(types)]
		member := store.IPGroupMember{MemberType: mt, Value: value}

		// Must never panic for any input.
		got := groups.Matches(member, ip)

		// Correctness cross-checks against independent references.
		switch mt {
		case store.MemberRange:
			if want := refRange(value, ip); got != want {
				t.Fatalf("range mismatch value=%q ip=%q: got %v want %v", value, ip, got, want)
			}
		case store.MemberSubnet:
			if want := refSubnet(value, ip); got != want {
				t.Fatalf("subnet mismatch value=%q ip=%q: got %v want %v", value, ip, got, want)
			}
		case store.MemberAddress:
			a := net.ParseIP(strings.TrimSpace(value))
			b := net.ParseIP(strings.TrimSpace(ip))
			want := a != nil && b != nil && a.Equal(b)
			if got != want {
				t.Fatalf("address mismatch value=%q ip=%q: got %v want %v", value, ip, got, want)
			}
		default:
			if got {
				t.Fatalf("unknown member type matched value=%q ip=%q", value, ip)
			}
		}
	})
}
