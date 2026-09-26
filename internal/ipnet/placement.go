package ipnet

import (
	"errors"
	"net/netip"
)

// ErrPrefix is returned for a prefix length outside the address family.
var ErrPrefix = errors.New("ipnet: prefix length out of range")

// Candidate is a subnet considered when placing an address.
type Candidate struct {
	ID   string
	CIDR string
}

// MostSpecific returns the candidate with the longest prefix that contains
// addr (research D10). Ties (the same prefix stored twice) resolve to the
// lowest id so placement is deterministic. Candidates whose CIDR does not
// parse are ignored; IPv4-mapped IPv6 addresses are compared as IPv4.
func MostSpecific(cands []Candidate, addr netip.Addr) (Candidate, bool) {
	addr = addr.Unmap().WithZone("")
	best, bestBits, found := Candidate{}, -1, false
	for _, c := range cands {
		p, err := netip.ParsePrefix(c.CIDR)
		if err != nil || !p.Masked().Contains(addr) {
			continue
		}
		bits := p.Bits()
		if bits > bestBits || (bits == bestBits && c.ID < best.ID) {
			best, bestBits, found = c, bits, true
		}
	}
	return best, found
}

// NetworkOf returns the network of addr for the given prefix length.
func NetworkOf(addr netip.Addr, bits int) (netip.Prefix, error) {
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() || bits < 0 || bits > addr.BitLen() {
		return netip.Prefix{}, ErrPrefix
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}

// AutoPrefix is the network the host sync creates for a reported address that
// no subnet contains: the reported network, except that a host route or an
// implausibly short or invalid prefix never becomes a subnet as such — IPv4
// falls back to /32 (a single host) and IPv6 to /64 (a /128 is never a LAN).
func AutoPrefix(addr netip.Addr, bits int) netip.Prefix {
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		if bits < 8 || bits > 32 {
			bits = 32
		}
	} else if bits < 16 || bits >= 128 {
		bits = 64
	}
	p, _ := NetworkOf(addr, bits)
	return p
}

// Class is the kind of a unicast/multicast address as far as recording it
// in IPAM is concerned.
type Class int

// Address classes.
const (
	ClassInvalid Class = iota
	ClassUnspecified
	ClassLoopback
	ClassLinkLocal
	ClassMulticast
	ClassBroadcast
	ClassMapped
	ClassGlobal
	ClassULA
)

var classNames = [...]string{"invalid", "unspecified", "loopback", "link_local", "multicast", "broadcast", "mapped", "global", "ula"}

// String names the class.
func (c Class) String() string {
	if c < 0 || int(c) >= len(classNames) {
		return "invalid"
	}
	return classNames[c]
}

// Recordable reports whether an address of this class is recorded by the
// host sync: global unicast (incl. private IPv4) and IPv6 unique-local.
func (c Class) Recordable() bool { return c == ClassGlobal || c == ClassULA }

var ula = netip.MustParsePrefix("fc00::/7")

// Classify returns the class of addr (FR-022, research D10).
func Classify(addr netip.Addr) Class {
	switch {
	case !addr.IsValid():
		return ClassInvalid
	case addr.Is4In6():
		return ClassMapped
	case addr.IsUnspecified():
		return ClassUnspecified
	case addr.IsLoopback():
		return ClassLoopback
	case addr.IsMulticast():
		return ClassMulticast
	case addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return ClassBroadcast
	case addr.IsLinkLocalUnicast():
		return ClassLinkLocal
	case ula.Contains(addr.WithZone("")):
		return ClassULA
	}
	return ClassGlobal
}
