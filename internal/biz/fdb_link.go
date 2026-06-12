package biz

// AccessPort is the most edge-like switch port a MAC was learned on, plus how
// many distinct MACs that port carries (lower = more directly attached).
type AccessPort struct {
	IfIndex  int
	VLAN     int
	MACCount int
}

// RankAccessPorts returns, for each MAC in a switch's forwarding database, the
// port that learned the fewest distinct MACs (the most edge-like), along with
// that port's MAC count.
//
// A MAC is typically learned on several ports of a switch: the access port the
// host plugs into (few MACs), plus every uplink/trunk that carries traffic
// toward it (many MACs). No threshold is applied here — the caller decides, and
// crucially can compare MACCount *across switches* to choose the globally most
// direct port. That cross-switch comparison matters: a host's MAC appears on
// its access switch's edge port (few MACs) and on a core switch's uplink port
// (many MACs); only the global minimum is the real connection.
//
// Pure function — no SNMP, no database — so the heuristic is unit-testable.
func RankAccessPorts(fdb []FDBEntry) map[string]AccessPort {
	// Distinct MACs learned per port ifIndex.
	macsPerIf := make(map[int]map[string]struct{})
	for _, fe := range fdb {
		mac := NormalizeMAC(fe.MAC)
		if mac == "" {
			continue
		}
		if macsPerIf[fe.IfIndex] == nil {
			macsPerIf[fe.IfIndex] = make(map[string]struct{})
		}
		macsPerIf[fe.IfIndex][mac] = struct{}{}
	}

	best := make(map[string]AccessPort)
	for _, fe := range fdb {
		mac := NormalizeMAC(fe.MAC)
		if mac == "" {
			continue
		}
		count := len(macsPerIf[fe.IfIndex])
		if cur, ok := best[mac]; !ok || count < cur.MACCount {
			best[mac] = AccessPort{IfIndex: fe.IfIndex, VLAN: fe.VLAN, MACCount: count}
		}
	}
	return best
}
