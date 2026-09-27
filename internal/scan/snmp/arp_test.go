package snmp

import (
	"errors"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// fakeWalker serves canned PDUs per walked root (tests the ARP walk without
// SNMP traffic).
type fakeWalker struct {
	rows  map[string][]gosnmp.SnmpPDU
	errs  map[string]error
	walks []string
}

func (f *fakeWalker) BulkWalk(root string, fn gosnmp.WalkFunc) error {
	f.walks = append(f.walks, root)
	for _, p := range f.rows[root] {
		if err := fn(p); err != nil {
			return err
		}
	}
	return f.errs[root]
}

func pdu(oid string, v any) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: oid, Type: gosnmp.OctetString, Value: v}
}

var mac1 = []byte{0x0a, 0x5c, 0xd2, 0xf1, 0x00, 0x01}

func TestParseARPRowIPNetToPhysical(t *testing.T) {
	cases := []struct {
		name, oid string
		val       any
		want      ARPEntry
		ok        bool
	}{
		{"ipv4", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.192.168.100.10", mac1,
			ARPEntry{IP: "192.168.100.10", MAC: "0a:5c:d2:f1:00:01", IfIndex: 7}, true},
		{"ipv6", "." + oidIPNetToPhysicalPhysAddress + ".3.2.16.32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.1", mac1,
			ARPEntry{IP: "2001:db8::1", MAC: "0a:5c:d2:f1:00:01", IfIndex: 3}, true},
		{"incomplete mac kept empty", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.1", []byte{},
			ARPEntry{IP: "10.0.0.1", IfIndex: 7}, true},
		{"string mac", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.1", string(mac1),
			ARPEntry{IP: "10.0.0.1", MAC: "0a:5c:d2:f1:00:01", IfIndex: 7}, true},
		{"eight byte mac is not a mac", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.1", []byte{1, 2, 3, 4, 5, 6, 7, 8},
			ARPEntry{IP: "10.0.0.1", IfIndex: 7}, true},
		{"length mismatch", "." + oidIPNetToPhysicalPhysAddress + ".7.1.16.10.0.0.1", mac1, ARPEntry{}, false},
		{"ipv4 with 16 octets", "." + oidIPNetToPhysicalPhysAddress + ".7.1.16.0.0.0.0.0.0.0.0.0.0.0.0.10.0.0.1", mac1, ARPEntry{}, false},
		{"short", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0", mac1, ARPEntry{}, false},
		{"oversized", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.1.9", mac1, ARPEntry{}, false},
		{"octet out of range", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.300", mac1, ARPEntry{}, false},
		{"zoned type", "." + oidIPNetToPhysicalPhysAddress + ".7.3.8.10.0.0.1.0.0.0.1", mac1, ARPEntry{}, false},
		{"not a number", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.x.1", mac1, ARPEntry{}, false},
		{"negative", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.-1.1", mac1, ARPEntry{}, false},
		{"zero ifindex", "." + oidIPNetToPhysicalPhysAddress + ".0.1.4.10.0.0.1", mac1, ARPEntry{}, false},
		{"unspecified ip", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.0.0.0.0", mac1, ARPEntry{}, false},
		{"wrong table", "." + oidIPNetToMediaPhysAddress + ".7.10.0.0.1", mac1, ARPEntry{}, false},
		{"not a byte value", "." + oidIPNetToPhysicalPhysAddress + ".7.1.4.10.0.0.1", 42,
			ARPEntry{IP: "10.0.0.1", IfIndex: 7}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseARPRow(oidIPNetToPhysicalPhysAddress, c.oid, c.val)
			if ok != c.ok || got != c.want {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseARPRowIPNetToMedia(t *testing.T) {
	got, ok := parseARPRow(oidIPNetToMediaPhysAddress, "."+oidIPNetToMediaPhysAddress+".12.172.16.0.9", mac1)
	if !ok || got != (ARPEntry{IP: "172.16.0.9", MAC: "0a:5c:d2:f1:00:01", IfIndex: 12}) {
		t.Fatalf("legacy row: %+v %v", got, ok)
	}
	for _, oid := range []string{
		"." + oidIPNetToMediaPhysAddress + ".12.172.16.0",
		"." + oidIPNetToMediaPhysAddress + ".12.172.16.0.9.1",
		"." + oidIPNetToMediaPhysAddress + ".12.172.16.0.256",
		"." + oidIPNetToMediaPhysAddress,
		"garbage",
	} {
		if _, ok := parseARPRow(oidIPNetToMediaPhysAddress, oid, mac1); ok {
			t.Errorf("accepted malformed legacy index %q", oid)
		}
	}
	if _, ok := parseARPRow("1.2.3", "."+oidIPNetToMediaPhysAddress+".12.172.16.0.9", mac1); ok {
		t.Error("unknown table accepted")
	}
}

func TestWalkARPPrefersIPNetToPhysical(t *testing.T) {
	w := &fakeWalker{rows: map[string][]gosnmp.SnmpPDU{
		oidIPNetToPhysicalPhysAddress: {
			pdu("."+oidIPNetToPhysicalPhysAddress+".7.1.4.10.0.0.1", mac1),
			pdu("."+oidIPNetToPhysicalPhysAddress+".7.1.4.10.0.0", mac1), // malformed: dropped
		},
		oidIPNetToMediaPhysAddress: {pdu("."+oidIPNetToMediaPhysAddress+".7.10.0.0.2", mac1)},
	}}
	got, partial := walkARP(w, MaxARPEntries)
	if partial || len(got) != 1 || got[0].IP != "10.0.0.1" {
		t.Fatalf("got %+v partial=%v", got, partial)
	}
	if len(w.walks) != 1 {
		t.Fatalf("legacy table walked although the new one answered: %v", w.walks)
	}
}

func TestWalkARPFallsBackToLegacy(t *testing.T) {
	w := &fakeWalker{rows: map[string][]gosnmp.SnmpPDU{
		oidIPNetToMediaPhysAddress: {pdu("."+oidIPNetToMediaPhysAddress+".7.10.0.0.2", mac1)},
	}, errs: map[string]error{oidIPNetToPhysicalPhysAddress: errors.New("no such object")}}
	got, partial := walkARP(w, MaxARPEntries)
	if len(got) != 1 || got[0].IP != "10.0.0.2" || got[0].IfIndex != 7 {
		t.Fatalf("got %+v", got)
	}
	// An error with nothing read is an unsupported table, not a partial read.
	if partial {
		t.Fatal("fallback marked partial")
	}
}

func TestWalkARPCapSetsPartial(t *testing.T) {
	var rows []gosnmp.SnmpPDU
	for i := 1; i <= 5; i++ {
		rows = append(rows, pdu("."+oidIPNetToMediaPhysAddress+".1.10.0.0."+itoa(i), mac1))
	}
	w := &fakeWalker{rows: map[string][]gosnmp.SnmpPDU{oidIPNetToMediaPhysAddress: rows}}
	got, partial := walkARP(w, 3)
	if len(got) != 3 || !partial {
		t.Fatalf("cap: %d entries partial=%v", len(got), partial)
	}
}

func TestWalkARPErrorMidWaySetsPartial(t *testing.T) {
	w := &fakeWalker{rows: map[string][]gosnmp.SnmpPDU{
		oidIPNetToPhysicalPhysAddress: {pdu("."+oidIPNetToPhysicalPhysAddress+".7.1.4.10.0.0.1", mac1)},
	}, errs: map[string]error{oidIPNetToPhysicalPhysAddress: errors.New("request timeout")}}
	got, partial := walkARP(w, MaxARPEntries)
	if len(got) != 1 || !partial {
		t.Fatalf("mid-way error: %+v partial=%v", got, partial)
	}
}

func TestMaxARPEntries(t *testing.T) {
	if MaxARPEntries != 65536 {
		t.Fatalf("cap %d", MaxARPEntries)
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
