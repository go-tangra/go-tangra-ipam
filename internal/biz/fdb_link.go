package biz

// SelectAccessPorts decides, for each MAC in a switch's forwarding database, the
// switch port it is most likely *directly* attached to.
//
// A MAC is typically learned on several ports of a switch: the access port the
// host plugs into, plus every uplink/trunk that carries traffic toward it. The
// access port learns only a handful of MACs, whereas uplinks learn many. So for
// each MAC we pick the port that learned the fewest distinct MACs (the most
// edge-like). If even that port exceeds maxMACs distinct addresses, the MAC is
// only visible transitively (e.g. the host lives behind another switch we did
// not scan) and is omitted rather than linked to an uplink.
//
// The returned map is keyed by normalized MAC and yields the chosen FDB entry
// (carrying the winning IfIndex and VLAN). It is a pure function — no SNMP, no
// database — so the heuristic can be unit-tested directly.
func SelectAccessPorts(fdb []FDBEntry, maxMACs int) map[string]FDBEntry {
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

	type candidate struct {
		entry    FDBEntry
		macCount int
	}
	best := make(map[string]candidate)
	for _, fe := range fdb {
		mac := NormalizeMAC(fe.MAC)
		if mac == "" {
			continue
		}
		count := len(macsPerIf[fe.IfIndex])
		if cur, ok := best[mac]; !ok || count < cur.macCount {
			best[mac] = candidate{entry: FDBEntry{MAC: mac, IfIndex: fe.IfIndex, VLAN: fe.VLAN}, macCount: count}
		}
	}

	out := make(map[string]FDBEntry, len(best))
	for mac, c := range best {
		if c.macCount > maxMACs {
			continue
		}
		out[mac] = c.entry
	}
	return out
}
