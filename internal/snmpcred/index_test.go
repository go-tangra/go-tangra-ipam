package snmpcred

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestIndex(t *testing.T) {
	subs := []store.Subnet{
		{ID: "root", Name: "supernet", CIDR: "10.0.0.0/8"},
		{ID: "child", Name: "site", CIDR: "10.1.0.0/16", ParentID: "root"},
		{ID: "grand", Name: "lab", CIDR: "10.1.2.0/24", ParentID: "child"},
		{ID: "alone", Name: "other", CIDR: "192.168.0.0/24"},
	}
	rows := []store.SubnetSNMP{
		{SubnetID: "root", Version: 3, SecurityLevel: LevelAuthPriv, AuthProtocol: "SHA256", PrivProtocol: "DES", Sealed: []byte{1}},
	}
	x := NewIndex(subs, rows)
	if r, ok := x.Own("root"); !ok || r.Sealed != nil || r.Version != 3 {
		t.Fatalf("own root %+v %v", r, ok)
	}
	if _, ok := x.Own("child"); ok {
		t.Fatal("child has no own row")
	}
	sum, row, ok := x.Effective("grand")
	want := store.SNMPSummary{State: store.SNMPStateInherited, Version: 3, SecurityLevel: LevelAuthPriv, Weak: true,
		SourceSubnetID: "root", SourceName: "supernet", SourceCIDR: "10.0.0.0/8"}
	if !ok || sum != want || row.SubnetID != "root" {
		t.Fatalf("grand %+v %+v %v", sum, row, ok)
	}
	if sum, _, _ := x.Effective("root"); sum.State != store.SNMPStateOwn {
		t.Fatalf("root %+v", sum)
	}
	if sum, _, ok := x.Effective("alone"); ok || sum != (store.SNMPSummary{State: store.SNMPStateNone}) {
		t.Fatalf("alone %+v", sum)
	}
	if MetaOf(row) != (Meta{Version: 3, SecurityLevel: LevelAuthPriv, AuthProtocol: "SHA256", PrivProtocol: "DES"}) {
		t.Fatal("MetaOf")
	}
}
