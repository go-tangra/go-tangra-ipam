package snmp

import (
	"net/netip"
	"regexp"
	"testing"
)

var macRe = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

// FuzzARPIndex feeds arbitrary OIDs and values to the ARP row decoder: it
// never panics, and every accepted row carries a canonical IP, a positive
// ifIndex and either no MAC (incomplete entry) or a lower-case colon MAC.
func FuzzARPIndex(f *testing.F) {
	f.Add("."+oidIPNetToPhysicalPhysAddress+".7.1.4.192.168.1.1", []byte{1, 2, 3, 4, 5, 6}, true)
	f.Add("."+oidIPNetToPhysicalPhysAddress+".3.2.16.254.128.0.0.0.0.0.0.0.0.0.0.0.0.0.1", []byte{1, 2, 3, 4, 5, 6}, true)
	f.Add("."+oidIPNetToMediaPhysAddress+".1.10.0.0.1", []byte{}, false)
	f.Add("."+oidIPNetToMediaPhysAddress+".1.10.0.0.999", []byte{0xff}, false)
	f.Add("..", []byte(nil), true)
	f.Fuzz(func(t *testing.T, oid string, val []byte, physical bool) {
		base := oidIPNetToMediaPhysAddress
		if physical {
			base = oidIPNetToPhysicalPhysAddress
		}
		e, ok := parseARPRow(base, oid, val)
		if !ok {
			return
		}
		ip, err := netip.ParseAddr(e.IP)
		if err != nil || ip.String() != e.IP || ip.IsUnspecified() {
			t.Fatalf("non-canonical ip %q", e.IP)
		}
		if e.IfIndex <= 0 {
			t.Fatalf("ifIndex %d", e.IfIndex)
		}
		if e.MAC != "" && !macRe.MatchString(e.MAC) {
			t.Fatalf("mac %q", e.MAC)
		}
	})
}
