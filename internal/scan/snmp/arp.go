package snmp

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// ARP / neighbour tables (feature 022, research D1).
const (
	// ipNetToPhysicalPhysAddress (IP-MIB, RFC 4293): IPv4 and IPv6, index
	// ifIndex.addrType.len.addr…
	oidIPNetToPhysicalPhysAddress = "1.3.6.1.2.1.4.35.1.4"
	// ipNetToMediaPhysAddress (legacy RFC 1213): IPv4 only, index
	// ifIndex.a.b.c.d
	oidIPNetToMediaPhysAddress = "1.3.6.1.2.1.4.22.1.2"

	// MaxARPEntries bounds the entries read from one device per scan (SR-002).
	MaxARPEntries = 65536
)

// ARPEntry is one IP-to-MAC mapping from a device's ARP or neighbour table.
// IP is canonical; MAC is lower-case colon notation, or empty for an
// incomplete entry (or a hardware address that is not a 6-byte MAC).
type ARPEntry struct {
	IP      string
	MAC     string
	IfIndex int
}

// bulkWalker is the part of *gosnmp.GoSNMP the ARP walk uses.
type bulkWalker interface {
	BulkWalk(rootOid string, walkFn gosnmp.WalkFunc) error
}

var errARPCap = errors.New("snmp: arp entry cap reached")

// walkARP reads the IP-to-physical table and falls back to the legacy ARP
// table when it yields nothing. partial is set when the cap was reached or a
// walk failed after rows were read.
func walkARP(w bulkWalker, limit int) ([]ARPEntry, bool) {
	entries, partial := walkARPTable(w, oidIPNetToPhysicalPhysAddress, limit)
	if len(entries) > 0 {
		return entries, partial
	}
	return walkARPTable(w, oidIPNetToMediaPhysAddress, limit)
}

func walkARPTable(w bulkWalker, base string, limit int) ([]ARPEntry, bool) {
	var out []ARPEntry
	err := w.BulkWalk(base, func(p gosnmp.SnmpPDU) error {
		e, ok := parseARPRow(base, p.Name, p.Value)
		if !ok {
			return nil
		}
		if len(out) >= limit {
			return errARPCap
		}
		out = append(out, e)
		return nil
	})
	return out, err != nil && len(out) > 0
}

// parseARPRow decodes one row of an ARP table walk. Rows whose index is
// malformed (wrong length, octets out of range, zoned or unknown address
// types, an unspecified address) are dropped.
func parseARPRow(base, oid string, val any) (ARPEntry, bool) {
	prefix := "." + base + "."
	if !strings.HasPrefix(oid, prefix) {
		return ARPEntry{}, false
	}
	idx, ok := parseIndex(oid[len(prefix):])
	if !ok || len(idx) == 0 || idx[0] <= 0 {
		return ARPEntry{}, false
	}
	var addr netip.Addr
	switch base {
	case oidIPNetToPhysicalPhysAddress:
		if len(idx) < 3 {
			return ARPEntry{}, false
		}
		typ, n, octets := idx[1], idx[2], idx[3:]
		if len(octets) != n || !((typ == 1 && n == 4) || (typ == 2 && n == 16)) {
			return ARPEntry{}, false
		}
		if addr, ok = addrFromOctets(octets); !ok {
			return ARPEntry{}, false
		}
	case oidIPNetToMediaPhysAddress:
		if len(idx) != 5 {
			return ARPEntry{}, false
		}
		if addr, ok = addrFromOctets(idx[1:]); !ok {
			return ARPEntry{}, false
		}
	default:
		return ARPEntry{}, false
	}
	if addr.IsUnspecified() {
		return ARPEntry{}, false
	}
	return ARPEntry{IP: addr.String(), MAC: macFromValue(val), IfIndex: idx[0]}, true
}

// parseIndex splits a dotted OID suffix into non-negative integers (strict:
// no signs, no empty components, bounded to int32).
func parseIndex(s string) ([]int, bool) {
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" || p[0] == '+' || p[0] == '-' {
			return nil, false
		}
		n, err := strconv.ParseUint(p, 10, 31)
		if err != nil {
			return nil, false
		}
		out = append(out, int(n))
	}
	return out, true
}

func addrFromOctets(o []int) (netip.Addr, bool) {
	b := make([]byte, len(o))
	for i, v := range o {
		if v > 255 {
			return netip.Addr{}, false
		}
		b[i] = byte(v)
	}
	return netip.AddrFromSlice(b)
}

// macFromValue formats a 6-byte hardware address; anything else (empty for
// an incomplete entry, other lengths, other types) yields "".
func macFromValue(v any) string {
	var b []byte
	switch x := v.(type) {
	case []byte:
		b = x
	case string:
		b = []byte(x)
	}
	if len(b) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}
