package store

import (
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra/v4/listquery"
)

func TestListSpecs(t *testing.T) {
	specs := map[string]listquery.Spec{"addresses": AddressList, "devices": DeviceList, "subnets": SubnetList, "vlans": VlanList,
		"scans": ScanList, "ip members": IPMemberList, "host members": HostMemberList, "interfaces": InterfaceList,
		"packages": PackageList, "guests": GuestList}
	for name, s := range specs {
		if err := s.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if r := ListRequest(listquery.Request{}, AddressList); r != (listquery.Request{Page: 1, PageSize: 25, Sort: "address", Order: listquery.Asc}) {
		t.Fatalf("zero request = %+v", r)
	}
	if r := ListRequest(listquery.Request{Page: 3, PageSize: 500, Sort: "nope"}, ScanList); r != (listquery.Request{Page: 1, PageSize: 25, Sort: "created_at", Order: listquery.Desc}) {
		t.Fatalf("invalid request = %+v", r)
	}
	// Inet order goes through ipam_try_inet (an unparsable value is NULL and
	// sorts last instead of failing); the expression matches the 0011 indexes.
	if got := (listquery.Request{Sort: "cidr", Order: listquery.Asc}).OrderBy(SubnetList); got !=
		"ipam_try_inet(cidr) ASC NULLS LAST, id ASC" {
		t.Fatalf("cidr order = %s", got)
	}
	if got := (listquery.Request{Sort: "address", Order: listquery.Asc}).OrderBy(AddressList); got !=
		"ipam_try_inet(address) ASC NULLS LAST, id ASC" {
		t.Fatalf("address order = %s", got)
	}
	// The host-member name comes through a LEFT JOIN, so it keeps NULLS LAST.
	if got := (listquery.Request{Sort: "name", Order: listquery.Desc}).OrderBy(HostMemberList); got != "lower(d.name) DESC NULLS LAST, m.id DESC" {
		t.Fatalf("host member name order = %s", got)
	}
	// NOT NULL columns carry no NULLS clause so a plain btree serves both directions.
	for _, c := range []struct {
		spec listquery.Spec
		sort string
		want string
	}{
		{ScanList, "created_at", "created_at DESC, id DESC"},
		{AddressList, "hostname", "lower(hostname) DESC, id DESC"},
		{AddressList, "mac", "lower(mac_address) DESC, id DESC"},
		{DeviceList, "name", "lower(name) DESC, id DESC"},
		{IPMemberList, "sequence", "sequence DESC, id DESC"},
		{GuestList, "name", "lower(g.name) DESC, g.id DESC"},
	} {
		if got := (listquery.Request{Sort: c.sort, Order: listquery.Desc}).OrderBy(c.spec); got != c.want {
			t.Fatalf("%s order = %s, want %s", c.sort, got, c.want)
		}
	}
	if got := (listquery.Request{Sort: "last_seen", Order: listquery.Desc}).OrderBy(AddressList); !strings.Contains(got, "NULLS LAST") {
		t.Fatalf("last_seen is nullable: %s", got)
	}
}
