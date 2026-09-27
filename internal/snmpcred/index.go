package snmpcred

import "github.com/go-tangra/go-tangra-ipam/v4/internal/store"

// Index resolves effective credentials over ONE tenant's subnets and
// credential metadata rows (research D3): list views, the scan executor and
// the credentials test share it so inheritance has a single definition.
type Index struct {
	subnets map[string]store.Subnet
	parents map[string]string
	own     map[string]bool
	rows    map[string]store.SubnetSNMP
}

// NewIndex builds the index. Rows are metadata; their blobs are ignored.
func NewIndex(subnets []store.Subnet, rows []store.SubnetSNMP) *Index {
	x := &Index{subnets: map[string]store.Subnet{}, parents: map[string]string{}, own: map[string]bool{}, rows: map[string]store.SubnetSNMP{}}
	for _, s := range subnets {
		x.subnets[s.ID] = s
		x.parents[s.ID] = s.ParentID
	}
	for _, r := range rows {
		r.Sealed = nil
		x.rows[r.SubnetID] = r
		x.own[r.SubnetID] = true
	}
	return x
}

// Own returns the subnet's own credential metadata.
func (x *Index) Own(subnetID string) (store.SubnetSNMP, bool) {
	r, ok := x.rows[subnetID]
	return r, ok
}

// Effective returns the subnet's effective state and, when credentials
// apply, the metadata row of the subnet they come from.
func (x *Index) Effective(subnetID string) (store.SNMPSummary, store.SubnetSNMP, bool) {
	src, ok := Resolve(subnetID, x.parents, x.own)
	if !ok {
		return store.SNMPSummary{State: store.SNMPStateNone}, store.SubnetSNMP{}, false
	}
	row := x.rows[src]
	state := store.SNMPStateInherited
	if src == subnetID {
		state = store.SNMPStateOwn
	}
	meta := Meta{Version: row.Version, SecurityLevel: row.SecurityLevel, AuthProtocol: row.AuthProtocol, PrivProtocol: row.PrivProtocol}
	sub := x.subnets[src]
	return store.SNMPSummary{State: state, Version: row.Version, SecurityLevel: row.SecurityLevel, Weak: meta.Weak(),
		SourceSubnetID: src, SourceName: sub.Name, SourceCIDR: sub.CIDR}, row, true
}

// MetaOf returns the metadata of a stored row.
func MetaOf(r store.SubnetSNMP) Meta {
	return Meta{Version: r.Version, SecurityLevel: r.SecurityLevel, AuthProtocol: r.AuthProtocol, PrivProtocol: r.PrivProtocol}
}
