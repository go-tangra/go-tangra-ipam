package snmpcred

// Resolve returns the subnet whose own credentials apply to subnetID: the
// subnet itself when it has its own, else its nearest ancestor that has
// them (FR-011). parents maps every known subnet of ONE tenant to its parent
// id ("" for a root); own marks the subnets with own credentials. The walk
// stops at an unknown subnet, a cycle, or after MaxDepth steps.
func Resolve(subnetID string, parents map[string]string, own map[string]bool) (string, bool) {
	seen := map[string]bool{}
	id := subnetID
	for step := 0; step <= MaxDepth && id != "" && !seen[id]; step++ {
		parent, known := parents[id]
		if !known {
			return "", false
		}
		if own[id] {
			return id, true
		}
		seen[id] = true
		id = parent
	}
	return "", false
}
