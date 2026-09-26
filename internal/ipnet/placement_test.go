package ipnet

import (
	"net/netip"
	"testing"
)

func TestMostSpecific(t *testing.T) {
	cands := []Candidate{
		{ID: "b", CIDR: "10.0.0.0/8"},
		{ID: "c", CIDR: "10.1.0.0/16"},
		{ID: "e", CIDR: "10.1.2.0/24"},
		{ID: "d", CIDR: "10.1.2.0/24"}, // same prefix stored twice: lowest id wins
		{ID: "a", CIDR: "garbage"},
		{ID: "v6", CIDR: "2001:db8::/32"},
		{ID: "v6b", CIDR: "2001:db8:1::/48"},
		{ID: "h", CIDR: "192.168.1.7/24"}, // host bits are masked
	}
	cases := []struct {
		addr, want string
		ok         bool
	}{
		{"10.1.2.3", "d", true},
		{"10.1.9.3", "c", true},
		{"10.9.9.9", "b", true},
		{"11.0.0.1", "", false},
		{"2001:db8:1::5", "v6b", true},
		{"2001:db8:2::5", "v6", true},
		{"::ffff:10.1.2.3", "d", true},
		{"192.168.1.200", "h", true},
		{"fe80::1%eth0", "", false},
	}
	for _, c := range cases {
		got, ok := MostSpecific(cands, netip.MustParseAddr(c.addr))
		if ok != c.ok || got.ID != c.want {
			t.Errorf("MostSpecific(%s) = %q,%v want %q,%v", c.addr, got.ID, ok, c.want, c.ok)
		}
	}
	if _, ok := MostSpecific(nil, netip.MustParseAddr("10.0.0.1")); ok {
		t.Fatal("no candidates must not match")
	}
}

func TestNetworkOf(t *testing.T) {
	cases := []struct {
		addr string
		bits int
		want string
		err  bool
	}{
		{"10.1.2.3", 24, "10.1.2.0/24", false},
		{"10.1.2.3", 32, "10.1.2.3/32", false},
		{"2001:db8::1", 64, "2001:db8::/64", false},
		{"2001:db8::1", 128, "2001:db8::1/128", false},
		{"::ffff:10.1.2.3", 16, "10.1.0.0/16", false},
		{"10.1.2.3", 33, "", true},
		{"10.1.2.3", -1, "", true},
	}
	for _, c := range cases {
		got, err := NetworkOf(netip.MustParseAddr(c.addr), c.bits)
		if (err != nil) != c.err || (err == nil && got.String() != c.want) {
			t.Errorf("NetworkOf(%s,%d) = %v,%v want %s err=%v", c.addr, c.bits, got, err, c.want, c.err)
		}
	}
	if _, err := NetworkOf(netip.Addr{}, 8); err == nil {
		t.Fatal("invalid address must fail")
	}
}

func TestAutoPrefix(t *testing.T) {
	cases := []struct {
		addr string
		bits int
		want string
	}{
		{"10.1.2.3", 24, "10.1.2.0/24"},
		{"10.1.2.3", 32, "10.1.2.3/32"},
		{"10.1.2.3", 0, "10.1.2.3/32"},
		{"10.1.2.3", 7, "10.1.2.3/32"},
		{"10.1.2.3", 40, "10.1.2.3/32"},
		{"2001:db8::1", 64, "2001:db8::/64"},
		{"2001:db8::1", 128, "2001:db8::/64"},
		{"2001:db8::1", 8, "2001:db8::/64"},
		{"2001:db8::1", 56, "2001:db8::/56"},
	}
	for _, c := range cases {
		if got := AutoPrefix(netip.MustParseAddr(c.addr), c.bits); got.String() != c.want {
			t.Errorf("AutoPrefix(%s,%d) = %s want %s", c.addr, c.bits, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		addr string
		want Class
		rec  bool
	}{
		{"10.0.0.1", ClassGlobal, true},
		{"8.8.8.8", ClassGlobal, true},
		{"2001:db8::1", ClassGlobal, true},
		{"fd00::1", ClassULA, true},
		{"127.0.0.1", ClassLoopback, false},
		{"::1", ClassLoopback, false},
		{"169.254.1.1", ClassLinkLocal, false},
		{"fe80::1", ClassLinkLocal, false},
		{"fe80::1%eth0", ClassLinkLocal, false},
		{"224.0.0.1", ClassMulticast, false},
		{"ff02::1", ClassMulticast, false},
		{"0.0.0.0", ClassUnspecified, false},
		{"::", ClassUnspecified, false},
		{"255.255.255.255", ClassBroadcast, false},
		{"::ffff:10.0.0.1", ClassMapped, false},
	}
	for _, c := range cases {
		got := Classify(netip.MustParseAddr(c.addr))
		if got != c.want || got.Recordable() != c.rec {
			t.Errorf("Classify(%s) = %s (rec %v) want %s (rec %v)", c.addr, got, got.Recordable(), c.want, c.rec)
		}
	}
	if Classify(netip.Addr{}) != ClassInvalid {
		t.Fatal("zero addr must be invalid")
	}
	if Class(99).String() != "invalid" || Class(-1).String() != "invalid" || ClassULA.String() != "ula" {
		t.Fatal("class names")
	}
}
