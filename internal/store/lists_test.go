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
	// Inet order guards the cast so an unparsable value sorts last instead of failing.
	if got := (listquery.Request{Sort: "cidr", Order: listquery.Asc}).OrderBy(SubnetList); got !=
		"(CASE WHEN pg_input_is_valid(cidr, 'inet') THEN cidr::inet END) ASC NULLS LAST, id ASC" {
		t.Fatalf("cidr order = %s", got)
	}
	if got := (listquery.Request{Sort: "name", Order: listquery.Desc}).OrderBy(HostMemberList); !strings.HasPrefix(got, "lower(d.name) DESC") {
		t.Fatalf("host member name order = %s", got)
	}
}
