package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// seedLists creates subnets (one IPv6), addresses with hostnames, a device
// with interfaces and packages, and a host group with a member.
func seedLists(t *testing.T, f *apiFixture) (deviceID, hostGroupID string) {
	t.Helper()
	ctx := context.Background()
	net4 := f.createSubnet(t, "lan", "10.0.0.0/24")
	f.createSubnet(t, "v6", "2001:db8::/64")
	for i, o := range []int{100, 9, 2, 10, 25} {
		host := []string{"web-1", "db-1", "Web-2", "mail", ""}[i]
		if err := f.mem.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: apiTenant, SubnetID: net4, Address: fmt.Sprintf("10.0.0.%d", o), Hostname: host}); err != nil {
			t.Fatal(err)
		}
	}
	dev := store.Device{ID: store.NewID(), TenantID: apiTenant, Name: "srv"}
	if err := f.mem.CreateDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"eth1", "eth0"} {
		if err := f.mem.CreateInterface(ctx, store.DeviceInterface{ID: store.NewID(), TenantID: apiTenant, DeviceID: dev.ID, Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.mem.ReplaceDevicePackages(ctx, apiTenant, dev.ID, []store.DevicePackage{{ID: store.NewID(), TenantID: apiTenant, Name: "zlib"}, {ID: store.NewID(), TenantID: apiTenant, Name: "bash"}}); err != nil {
		t.Fatal(err)
	}
	hg := store.HostGroup{ID: store.NewID(), TenantID: apiTenant, Name: "hg"}
	if err := f.mem.CreateHostGroup(ctx, hg); err != nil {
		t.Fatal(err)
	}
	if err := f.mem.AddHostGroupMember(ctx, store.HostGroupMember{ID: store.NewID(), TenantID: apiTenant, HostGroupID: hg.ID, DeviceID: dev.ID}); err != nil {
		t.Fatal(err)
	}
	return dev.ID, hg.ID
}

func TestListContractNegatives(t *testing.T) {
	f := newAPI(t)
	dev, hg := seedLists(t, f)
	paths := []string{"/subnets", "/ip-addresses", "/devices", "/vlans", "/ip-scans", "/devices/" + dev + "/interfaces",
		"/devices/" + dev + "/packages", "/devices/" + dev + "/addresses", "/host-groups/" + hg + "/members", "/ip-groups/" + hg + "/members"}
	cases := map[string]string{"page=0": "page", "page=abc": "page", "page_size=0": "page_size", "page_size=201": "page_size",
		"sort=password": "sort", "order=up": "order", "page=1&cursor=x": "cursor", "limit=5&order=asc": "cursor"}
	for _, path := range paths {
		for q, param := range cases {
			w := f.req(t, "GET", p+path+"?"+q, "admin", "")
			if w.Code != 422 {
				t.Fatalf("%s?%s: want 422, got %d %s", path, q, w.Code, w.Body)
			}
			d, _ := decodeBody(t, w)["detail"].(map[string]any)
			if d["param"] != param {
				t.Fatalf("%s?%s: detail %v, want param %s", path, q, d, param)
			}
		}
	}
}

func TestListContractPages(t *testing.T) {
	f := newAPI(t)
	dev, hg := seedLists(t, f)

	// Default sort: address in inet order; the page shape carries the applied request.
	m := decodeBody(t, f.req(t, "GET", p+"/ip-addresses?page_size=3", "admin", ""))
	items, _ := m["items"].([]any)
	if m["total"] != 5.0 || m["page"] != 1.0 || m["page_size"] != 3.0 || m["sort"] != "address" || m["order"] != "asc" || len(items) != 3 {
		t.Fatalf("addresses page = %v", m)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.(map[string]any)["address"].(string))
	}
	if fmt.Sprint(got) != "[10.0.0.2 10.0.0.9 10.0.0.10]" {
		t.Fatalf("inet order = %v", got)
	}
	// A page beyond the end answers the last page.
	if m := decodeBody(t, f.req(t, "GET", p+"/ip-addresses?page=9&page_size=3&sort=hostname&order=desc", "admin", "")); m["page"] != 2.0 || len(m["items"].([]any)) != 2 {
		t.Fatalf("clamped page = %v", m)
	}
	// hostname filter (case-insensitive substring) is honoured before the count.
	if m := decodeBody(t, f.req(t, "GET", p+"/ip-addresses?hostname=web", "admin", "")); m["total"] != 2.0 {
		t.Fatalf("hostname filter = %v", m)
	}
	// ip_version filter on subnets.
	if m := decodeBody(t, f.req(t, "GET", p+"/subnets?ip_version=6", "admin", "")); m["total"] != 1.0 || m["sort"] != "cidr" {
		t.Fatalf("ip_version filter = %v", m)
	}
	// Legacy cursor/limit: old shape plus total.
	m = decodeBody(t, f.req(t, "GET", p+"/ip-addresses?limit=2", "admin", ""))
	if _, paged := m["page"]; paged || m["total"] != 5.0 || len(m["items"].([]any)) != 2 {
		t.Fatalf("legacy = %v", m)
	}
	if m := decodeBody(t, f.req(t, "GET", p+"/devices?limit=1&cursor=zzzz", "admin", "")); m["total"] != 1.0 {
		t.Fatalf("legacy devices = %v", m)
	}
	// Sub-lists.
	m = decodeBody(t, f.req(t, "GET", p+"/devices/"+dev+"/interfaces", "admin", ""))
	if m["total"] != 2.0 || m["sort"] != "name" || m["items"].([]any)[0].(map[string]any)["name"] != "eth0" {
		t.Fatalf("interfaces = %v", m)
	}
	m = decodeBody(t, f.req(t, "GET", p+"/devices/"+dev+"/packages?order=desc", "admin", ""))
	if m["total"] != 2.0 || m["items"].([]any)[0].(map[string]any)["name"] != "zlib" {
		t.Fatalf("packages = %v", m)
	}
	if m := decodeBody(t, f.req(t, "GET", p+"/host-groups/"+hg+"/members?sort=name", "admin", "")); m["total"] != 1.0 || m["sort"] != "name" {
		t.Fatalf("host members = %v", m)
	}
	if m := decodeBody(t, f.req(t, "GET", p+"/ip-scans", "admin", "")); m["total"] != 0.0 || m["sort"] != "created_at" || m["order"] != "desc" {
		t.Fatalf("scans = %v", m)
	}
	if m := decodeBody(t, f.req(t, "GET", p+"/vlans?sort=name", "admin", "")); m["sort"] != "name" || len(m["items"].([]any)) != 0 {
		t.Fatalf("vlans = %v", m)
	}
}
