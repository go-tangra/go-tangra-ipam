package ipnet

import (
	"math/big"
	"net"
	"testing"
)

func TestParseV4(t *testing.T) {
	info, err := Parse("10.0.0.5/24")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.Version != 4 || info.PrefixLen != 24 || info.Total != 256 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if info.Network.String() != "10.0.0.0" {
		t.Errorf("network=%s want 10.0.0.0", info.Network)
	}
	if info.Broadcast.String() != "10.0.0.255" {
		t.Errorf("broadcast=%s want 10.0.0.255", info.Broadcast)
	}
	if info.Mask.String() != "255.255.255.0" {
		t.Errorf("mask=%s want 255.255.255.0", info.Mask)
	}
}

func TestParseEdges(t *testing.T) {
	// /32 single address.
	i32, err := Parse("192.168.1.7/32")
	if err != nil || i32.Total != 1 {
		t.Fatalf("/32: %+v err=%v", i32, err)
	}
	if i32.Network.String() != "192.168.1.7" || i32.Broadcast.String() != "192.168.1.7" {
		t.Errorf("/32 net/bcast: %+v", i32)
	}
	// /31 point-to-point.
	i31, err := Parse("192.168.1.0/31")
	if err != nil || i31.Total != 2 {
		t.Fatalf("/31: %+v err=%v", i31, err)
	}
	// IPv6, total capped for a huge block.
	i6, err := Parse("2001:db8::/64")
	if err != nil {
		t.Fatalf("v6: %v", err)
	}
	if i6.Version != 6 || i6.PrefixLen != 64 {
		t.Fatalf("v6 info: %+v", i6)
	}
	if i6.Total <= 0 {
		t.Errorf("v6 total should be capped positive, got %d", i6.Total)
	}
	// Small v6 block, exact total.
	i6s, err := Parse("2001:db8::/126")
	if err != nil || i6s.Total != 4 {
		t.Fatalf("v6 /126: %+v err=%v", i6s, err)
	}
}

func TestParseInvalid(t *testing.T) {
	for _, c := range []string{"", "not-a-cidr", "10.0.0.0", "10.0.0.0/33", "999.1.1.1/24"} {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) expected error", c)
		}
	}
}

func TestHosts(t *testing.T) {
	hs, err := Hosts("10.0.0.0/24")
	if err != nil {
		t.Fatalf("Hosts: %v", err)
	}
	if len(hs) != 254 {
		t.Fatalf("want 254 hosts, got %d", len(hs))
	}
	if hs[0].String() != "10.0.0.1" || hs[253].String() != "10.0.0.254" {
		t.Errorf("host bounds: %s .. %s", hs[0], hs[len(hs)-1])
	}
	// /30 -> 2 usable, /31 -> both, /32 -> 1.
	if h30, _ := Hosts("10.0.0.0/30"); len(h30) != 2 {
		t.Errorf("/30 usable = %d, want 2", len(h30))
	}
	if h31, _ := Hosts("10.0.0.0/31"); len(h31) != 2 {
		t.Errorf("/31 usable = %d, want 2", len(h31))
	}
	if h32, _ := Hosts("10.0.0.0/32"); len(h32) != 1 {
		t.Errorf("/32 usable = %d, want 1", len(h32))
	}
}

func TestHostsTooLarge(t *testing.T) {
	if _, err := Hosts("10.0.0.0/8"); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestHostsInvalid(t *testing.T) {
	if _, err := Hosts("bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFirstFree(t *testing.T) {
	// Empty subnet: lowest usable host.
	ip, err := FirstFree("10.0.0.0/24", nil, "", 0, 0)
	if err != nil || ip.String() != "10.0.0.1" {
		t.Fatalf("FirstFree=%v err=%v want 10.0.0.1", ip, err)
	}
	// Gateway is skipped.
	ip, _ = FirstFree("10.0.0.0/24", nil, "10.0.0.1", 0, 0)
	if ip.String() != "10.0.0.2" {
		t.Errorf("gateway not skipped: %s", ip)
	}
	// Allocated addresses are skipped and the result is monotonic.
	exclude := map[string]bool{"10.0.0.1": true, "10.0.0.2": true, "10.0.0.3": false, "garbage": true}
	ip, _ = FirstFree("10.0.0.0/24", exclude, "", 0, 0)
	if ip.String() != "10.0.0.3" { // .3 is present-but-false => usable
		t.Errorf("exclude handling wrong: %s", ip)
	}
	// skipFirst removes low addresses.
	ip, _ = FirstFree("10.0.0.0/24", nil, "", 2, 0)
	if ip.String() != "10.0.0.3" {
		t.Errorf("skipFirst wrong: %s", ip)
	}
	// v6 first free.
	ip, err = FirstFree("2001:db8::/64", nil, "", 0, 0)
	if err != nil || ip.String() != "2001:db8::1" {
		t.Fatalf("v6 FirstFree=%v err=%v", ip, err)
	}
}

func TestFirstFreeMonotonic(t *testing.T) {
	exclude := map[string]bool{}
	prev := ""
	for i := 0; i < 5; i++ {
		ip, err := FirstFree("10.0.0.0/29", exclude, "", 0, 0)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if prev != "" && !lessIP(prev, ip.String()) {
			t.Fatalf("not monotonic: %s then %s", prev, ip)
		}
		prev = ip.String()
		exclude[ip.String()] = true
	}
}

func TestFirstFreeExhausted(t *testing.T) {
	// /30 has 2 usable hosts; exclude both.
	exclude := map[string]bool{"10.0.0.1": true, "10.0.0.2": true}
	if _, err := FirstFree("10.0.0.0/30", exclude, "", 0, 0); err != ErrExhausted {
		t.Fatalf("want ErrExhausted, got %v", err)
	}
	// skipLast can also exhaust.
	if _, err := FirstFree("10.0.0.0/30", nil, "", 2, 2); err != ErrExhausted {
		t.Fatalf("skip exhaust: want ErrExhausted, got %v", err)
	}
}

func TestFirstFreeInvalid(t *testing.T) {
	if _, err := FirstFree("nope", nil, "", 0, 0); err != ErrParse {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestBulkFree(t *testing.T) {
	ips, err := BulkFree("10.0.0.0/24", map[string]bool{"10.0.0.1": true}, "10.0.0.2", 0, 0, 3)
	if err != nil {
		t.Fatalf("BulkFree: %v", err)
	}
	got := []string{ips[0].String(), ips[1].String(), ips[2].String()}
	want := []string{"10.0.0.3", "10.0.0.4", "10.0.0.5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BulkFree got %v want %v", got, want)
		}
	}
	// n <= 0 returns nil, nil.
	if r, err := BulkFree("10.0.0.0/24", nil, "", 0, 0, 0); r != nil || err != nil {
		t.Fatalf("n<=0: %v %v", r, err)
	}
	// Not enough addresses.
	if _, err := BulkFree("10.0.0.0/30", nil, "", 0, 0, 10); err != ErrExhausted {
		t.Fatalf("want ErrExhausted, got %v", err)
	}
	// Invalid cidr.
	if _, err := BulkFree("bad", nil, "", 0, 0, 1); err != ErrParse {
		t.Fatalf("want ErrParse, got %v", err)
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.0.0.0/24", "10.0.0.128/25", true},
		{"10.0.0.0/24", "10.0.1.0/24", false},
		{"10.0.0.0/16", "10.0.5.0/24", true},
		{"10.0.0.0/24", "2001:db8::/64", false}, // cross-family
	}
	for _, c := range cases {
		got, err := Overlaps(c.a, c.b)
		if err != nil {
			t.Fatalf("Overlaps(%s,%s): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("Overlaps(%s,%s)=%v want %v", c.a, c.b, got, c.want)
		}
	}
	if _, err := Overlaps("bad", "10.0.0.0/24"); err != ErrParse {
		t.Errorf("Overlaps bad a: %v", err)
	}
	if _, err := Overlaps("10.0.0.0/24", "bad"); err != ErrParse {
		t.Errorf("Overlaps bad b: %v", err)
	}
}

func TestContains(t *testing.T) {
	if ok, err := Contains("10.0.0.0/24", "10.0.0.5"); err != nil || !ok {
		t.Errorf("expected contained: ok=%v err=%v", ok, err)
	}
	if ok, _ := Contains("10.0.0.0/24", "10.0.1.5"); ok {
		t.Error("should not be contained")
	}
	if _, err := Contains("bad", "10.0.0.5"); err != ErrParse {
		t.Errorf("bad cidr: %v", err)
	}
	if _, err := Contains("10.0.0.0/24", "bad-ip"); err != ErrParse {
		t.Errorf("bad ip: %v", err)
	}
}

func TestGatewayInRange(t *testing.T) {
	if ok, err := GatewayInRange("10.0.0.0/24", "10.0.0.1"); err != nil || !ok {
		t.Errorf("valid gateway: ok=%v err=%v", ok, err)
	}
	if ok, _ := GatewayInRange("10.0.0.0/24", "10.0.0.0"); ok {
		t.Error("network address must not be a valid gateway")
	}
	if ok, _ := GatewayInRange("10.0.0.0/24", "10.0.0.255"); ok {
		t.Error("broadcast must not be a valid gateway")
	}
	if ok, _ := GatewayInRange("10.0.0.0/24", "10.0.1.1"); ok {
		t.Error("out-of-range gateway must be false")
	}
	// /31 uses both addresses.
	if ok, _ := GatewayInRange("10.0.0.0/31", "10.0.0.0"); !ok {
		t.Error("/31 gateway should be valid")
	}
	if _, err := GatewayInRange("bad", "10.0.0.1"); err == nil {
		t.Error("bad cidr should error")
	}
	if _, err := GatewayInRange("10.0.0.0/24", "bad"); err != ErrParse {
		t.Errorf("bad gateway: %v", err)
	}
}

func TestUtilization(t *testing.T) {
	cases := []struct {
		used, total int64
		want        float64
	}{
		{0, 0, 0},
		{0, 100, 0},
		{-5, 100, 0},
		{128, 256, 50},
		{50, 200, 25},
		{300, 100, 100}, // clamped
	}
	for _, c := range cases {
		if got := Utilization(c.used, c.total); got != c.want {
			t.Errorf("Utilization(%d,%d)=%v want %v", c.used, c.total, got, c.want)
		}
	}
}

func TestInRange(t *testing.T) {
	if !InRange("10.0.0.1-10.0.0.20", "10.0.0.5") {
		t.Error("10.0.0.5 should be in range")
	}
	if InRange("10.0.0.1-10.0.0.20", "10.0.0.25") {
		t.Error("10.0.0.25 should be out of range")
	}
	if InRange("10.0.0.1-10.0.0.20", "10.0.0.0") {
		t.Error("10.0.0.0 should be below range")
	}
	// single-address spec.
	if !InRange("10.0.0.5", "10.0.0.5") {
		t.Error("single spec should match")
	}
	if InRange("10.0.0.5", "10.0.0.6") {
		t.Error("single spec mismatch")
	}
	// parse failures yield false.
	if InRange("bad-spec", "10.0.0.5") {
		t.Error("bad lo should be false")
	}
	if InRange("10.0.0.1-bad", "10.0.0.5") {
		t.Error("bad hi should be false")
	}
	if InRange("not-a-range", "10.0.0.5") {
		t.Error("bad single should be false")
	}
	if InRange("10.0.0.1-10.0.0.20", "bad-ip") {
		t.Error("bad target should be false")
	}
}

func TestInRangeNoDashInvalid(t *testing.T) {
	// A spec with no dash and an unparseable single address is false.
	if InRange("bad", "10.0.0.5") {
		t.Error("no-dash invalid single spec should be false")
	}
}

func TestIntToIPTruncate(t *testing.T) {
	// A value wider than the target size is truncated to the low bytes
	// (defensive path in intToIP).
	big5 := new(big.Int).SetBytes([]byte{0xff, 10, 0, 0, 1}) // 5 bytes, v4 target
	if got := intToIP(big5, 4).String(); got != "10.0.0.1" {
		t.Errorf("intToIP truncate = %s, want 10.0.0.1", got)
	}
}

// lessIP compares two IP strings numerically (test helper for monotonicity).
func lessIP(a, b string) bool {
	return ipToInt(net.ParseIP(a)).Cmp(ipToInt(net.ParseIP(b))) < 0
}
