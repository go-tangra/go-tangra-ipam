// Package ipnet is the pure CIDR/allocation library at the heart of IPAM. It
// depends only on the standard library (net, math/big) and holds no state: every
// function takes its inputs explicitly and returns a value or an error. Address
// arithmetic is done with math/big so IPv4 and IPv6 share one code path and
// nothing overflows.
package ipnet

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"strings"
)

// MaxHostEnumerate caps how many host addresses Hosts will materialize. A range
// whose usable host count exceeds this returns ErrTooLarge rather than trying to
// allocate an enormous slice; callers that need a single free address use
// FirstFree/BulkFree, which never enumerate the whole range.
const MaxHostEnumerate = 1 << 16 // 65536

// Errors.
var (
	ErrParse     = errors.New("ipnet: invalid CIDR")
	ErrTooLarge  = errors.New("ipnet: range too large to enumerate")
	ErrExhausted = errors.New("ipnet: no free address in range")
)

// Info describes a parsed CIDR block.
type Info struct {
	Network   net.IP // network (base) address
	Broadcast net.IP // broadcast (v4) or last address (v6)
	Mask      net.IP // netmask as an address
	PrefixLen int    // prefix length in bits
	Total     int64  // total addresses in the block (capped for huge v6)
	Version   int    // 4 or 6
}

// Parse derives the network, broadcast/last, mask, prefix, total and version of
// a CIDR block. The input may carry any host bits (e.g. "10.0.0.5/24"); the
// network is the masked base address.
func Parse(cidr string) (Info, error) {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return Info{}, ErrParse
	}
	version := 4
	network := ipnet.IP.To4()
	if network == nil {
		version = 6
		network = ipnet.IP.To16()
	}
	prefix, bits := ipnet.Mask.Size()
	hostBits := bits - prefix

	// last = network + 2^hostBits - 1 (big.Int keeps this exact for any size).
	netInt := ipToInt(network)
	size := new(big.Int).Lsh(big.NewInt(1), uint(hostBits)) // #nosec G115 -- hostBits in [0,128]
	last := new(big.Int).Add(netInt, size)
	last.Sub(last, big.NewInt(1))

	var total int64
	if hostBits >= 63 {
		total = math.MaxInt64
	} else {
		total = int64(1) << uint(hostBits)
	}

	return Info{
		Network:   network,
		Broadcast: intToIP(last, version),
		Mask:      net.IP(ipnet.Mask),
		PrefixLen: prefix,
		Total:     total,
		Version:   version,
	}, nil
}

// hostRange returns the inclusive [lo, hi] big.Int bounds of the usable host
// addresses of info. For IPv4 prefixes shorter than /31 the network and
// broadcast addresses are excluded; /31 and /32 use both addresses. For IPv6
// the network (subnet-router anycast) address is excluded for prefixes shorter
// than /127.
func hostRange(info Info) (lo, hi *big.Int) {
	netInt := ipToInt(info.Network)
	bcInt := ipToInt(info.Broadcast)
	lo = new(big.Int).Set(netInt)
	hi = new(big.Int).Set(bcInt)
	if info.Version == 4 {
		if info.PrefixLen < 31 {
			lo.Add(lo, big.NewInt(1))
			hi.Sub(hi, big.NewInt(1))
		}
		return lo, hi
	}
	if info.PrefixLen < 127 {
		lo.Add(lo, big.NewInt(1))
	}
	return lo, hi
}

// Hosts enumerates the usable host addresses of a CIDR. It returns ErrTooLarge
// when the usable count exceeds MaxHostEnumerate.
func Hosts(cidr string) ([]net.IP, error) {
	info, err := Parse(cidr)
	if err != nil {
		return nil, err
	}
	lo, hi := hostRange(info)
	count := new(big.Int).Sub(hi, lo)
	count.Add(count, big.NewInt(1))
	if count.Cmp(big.NewInt(MaxHostEnumerate)) > 0 {
		return nil, ErrTooLarge
	}
	n := int(count.Int64())
	out := make([]net.IP, 0, n)
	cur := new(big.Int).Set(lo)
	for i := 0; i < n; i++ {
		out = append(out, intToIP(cur, info.Version))
		cur.Add(cur, big.NewInt(1))
	}
	return out, nil
}

// FirstFree returns the lowest usable host that is not the gateway, not in
// exclude, and not carved off by skipFirst (addresses removed from the low end)
// or skipLast (addresses removed from the high end). It returns ErrExhausted
// when no such address exists. Because it always scans from the low end, its
// result is monotonic as allocations accumulate.
func FirstFree(cidr string, exclude map[string]bool, gateway string, skipFirst, skipLast int) (net.IP, error) {
	info, err := Parse(cidr)
	if err != nil {
		return nil, err
	}
	lo, hi := boundedRange(info, skipFirst, skipLast)
	blocked := normalizeExclude(exclude)
	if gw := normalizeIP(gateway); gw != "" {
		blocked[gw] = true
	}
	cur := new(big.Int).Set(lo)
	for cur.Cmp(hi) <= 0 {
		ip := intToIP(cur, info.Version)
		if !blocked[ip.String()] {
			return ip, nil
		}
		cur.Add(cur, big.NewInt(1))
	}
	return nil, ErrExhausted
}

// BulkFree returns up to n free hosts (lowest first) under the same rules as
// FirstFree. It returns ErrExhausted when fewer than n are available.
func BulkFree(cidr string, exclude map[string]bool, gateway string, skipFirst, skipLast, n int) ([]net.IP, error) {
	if n <= 0 {
		return nil, nil
	}
	info, err := Parse(cidr)
	if err != nil {
		return nil, err
	}
	lo, hi := boundedRange(info, skipFirst, skipLast)
	blocked := normalizeExclude(exclude)
	if gw := normalizeIP(gateway); gw != "" {
		blocked[gw] = true
	}
	out := make([]net.IP, 0, n)
	cur := new(big.Int).Set(lo)
	for cur.Cmp(hi) <= 0 && len(out) < n {
		ip := intToIP(cur, info.Version)
		if !blocked[ip.String()] {
			out = append(out, ip)
		}
		cur.Add(cur, big.NewInt(1))
	}
	if len(out) < n {
		return nil, ErrExhausted
	}
	return out, nil
}

// boundedRange applies skipFirst/skipLast to the usable host range.
func boundedRange(info Info, skipFirst, skipLast int) (lo, hi *big.Int) {
	lo, hi = hostRange(info)
	if skipFirst > 0 {
		lo = new(big.Int).Add(lo, big.NewInt(int64(skipFirst)))
	}
	if skipLast > 0 {
		hi = new(big.Int).Sub(hi, big.NewInt(int64(skipLast)))
	}
	return lo, hi
}

// Overlaps reports whether two CIDR blocks share any address.
func Overlaps(a, b string) (bool, error) {
	_, na, err := net.ParseCIDR(strings.TrimSpace(a))
	if err != nil {
		return false, ErrParse
	}
	_, nb, err := net.ParseCIDR(strings.TrimSpace(b))
	if err != nil {
		return false, ErrParse
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP), nil
}

// Within reports whether child is a strictly smaller block inside parent (same
// address family, longer prefix). Unparsable input is never within.
func Within(child, parent string) bool {
	_, nc, err := net.ParseCIDR(strings.TrimSpace(child))
	if err != nil {
		return false
	}
	_, np, err := net.ParseCIDR(strings.TrimSpace(parent))
	if err != nil {
		return false
	}
	cOnes, cBits := nc.Mask.Size()
	pOnes, pBits := np.Mask.Size()
	return cBits == pBits && cOnes > pOnes && np.Contains(nc.IP)
}

// MaxSubdivide caps how many child blocks Subdivide will enumerate, so a wide
// parent and a long prefix cannot allocate an unbounded slice.
const MaxSubdivide = 1024

// Subdivide splits cidr into the consecutive blocks of prefixLen it contains
// ("10.0.0.0/24" at 26 → the four /26s). prefixLen must be longer than the
// parent prefix and within the address family; at most MaxSubdivide blocks are
// produced.
func Subdivide(cidr string, prefixLen int) ([]string, error) {
	info, err := Parse(cidr)
	if err != nil {
		return nil, err
	}
	bits := 32
	if info.Version == 6 {
		bits = 128
	}
	if prefixLen <= info.PrefixLen || prefixLen > bits {
		return nil, fmt.Errorf("%w: prefix /%d is not inside /%d", ErrParse, prefixLen, info.PrefixLen)
	}
	countBits := prefixLen - info.PrefixLen
	if countBits > 20 { // 2^20 is already far beyond the cap; avoid a huge shift
		return nil, ErrTooLarge
	}
	count := 1 << uint(countBits) // #nosec G115 -- countBits in [1,20]
	if count > MaxSubdivide {
		return nil, ErrTooLarge
	}
	step := new(big.Int).Lsh(big.NewInt(1), uint(bits-prefixLen)) // #nosec G115 -- prefixLen <= bits
	base := ipToInt(info.Network)
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		ip := intToIP(new(big.Int).Add(base, new(big.Int).Mul(step, big.NewInt(int64(i)))), info.Version)
		out = append(out, fmt.Sprintf("%s/%d", ip.String(), prefixLen))
	}
	return out, nil
}

// Contains reports whether ip falls within cidr.
func Contains(cidr, ip string) (bool, error) {
	_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return false, ErrParse
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false, ErrParse
	}
	return n.Contains(parsed), nil
}

// GatewayInRange reports whether gateway is a valid host address inside cidr
// (contained, and not the network or broadcast address for IPv4 prefixes
// shorter than /31).
func GatewayInRange(cidr, gateway string) (bool, error) {
	info, err := Parse(cidr)
	if err != nil {
		return false, err
	}
	gw := net.ParseIP(strings.TrimSpace(gateway))
	if gw == nil {
		return false, ErrParse
	}
	_, n, _ := net.ParseCIDR(strings.TrimSpace(cidr))
	if !n.Contains(gw) {
		return false, nil
	}
	if info.Version == 4 && info.PrefixLen < 31 {
		if gw.Equal(info.Network) || gw.Equal(info.Broadcast) {
			return false, nil
		}
	}
	return true, nil
}

// Utilization returns used/total as a percentage in [0, 100]. A non-positive
// total yields 0.
func Utilization(used, total int64) float64 {
	if total <= 0 || used <= 0 {
		return 0
	}
	pct := (float64(used) / float64(total)) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

// InRange reports whether ip lies within an inclusive "lo-hi" range spec (e.g.
// "10.0.0.1-10.0.0.20"), used for IP-group range membership. A spec without a
// dash is treated as a single address. Any parse failure yields false.
func InRange(rangeSpec, ip string) bool {
	target := net.ParseIP(strings.TrimSpace(ip))
	if target == nil {
		return false
	}
	ti := ipToInt(target)
	spec := strings.TrimSpace(rangeSpec)
	lo, hi, ok := strings.Cut(spec, "-")
	if !ok {
		single := net.ParseIP(strings.TrimSpace(spec))
		if single == nil {
			return false
		}
		return ipToInt(single).Cmp(ti) == 0
	}
	loIP := net.ParseIP(strings.TrimSpace(lo))
	hiIP := net.ParseIP(strings.TrimSpace(hi))
	if loIP == nil || hiIP == nil {
		return false
	}
	li, hj := ipToInt(loIP), ipToInt(hiIP)
	return ti.Cmp(li) >= 0 && ti.Cmp(hj) <= 0
}

// --- helpers ---

func ipToInt(ip net.IP) *big.Int {
	i := new(big.Int)
	if v4 := ip.To4(); v4 != nil {
		i.SetBytes(v4)
		return i
	}
	i.SetBytes(ip.To16())
	return i
}

func intToIP(i *big.Int, version int) net.IP {
	size := 4
	if version == 6 {
		size = 16
	}
	b := i.Bytes()
	if len(b) > size {
		b = b[len(b)-size:]
	}
	ip := make(net.IP, size)
	copy(ip[size-len(b):], b)
	return ip
}

// normalizeExclude canonicalizes the keys of an exclude set to net.IP string
// form so lookups match the candidate addresses FirstFree/BulkFree produce.
func normalizeExclude(exclude map[string]bool) map[string]bool {
	out := make(map[string]bool, len(exclude))
	for k, v := range exclude {
		if !v {
			continue
		}
		if n := normalizeIP(k); n != "" {
			out[n] = true
		}
	}
	return out
}

func normalizeIP(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	return ip.String()
}
