//go:build integration

package repodb_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// pager is one paged list under test.
type pager[T any] func(listquery.Request) ([]T, int, listquery.Request, error)

// walkAll pages a list with every sort field of spec in both directions (page
// size 3) and checks that every matching record appears exactly once, the
// total is want, and a page beyond the end answers the last page.
func walkAll[T any](t *testing.T, name string, spec listquery.Spec, page pager[T], id func(T) string, want int) {
	t.Helper()
	for field := range spec.Fields {
		for _, dir := range []listquery.Dir{listquery.Asc, listquery.Desc} {
			seen := map[string]bool{}
			pages := (want + 2) / 3
			for p := 1; p <= max(pages, 1); p++ {
				items, total, applied, err := page(listquery.Request{Page: p, PageSize: 3, Sort: field, Order: dir})
				if err != nil {
					t.Fatalf("%s %s %s page %d: %v", name, field, dir, p, err)
				}
				if total != want || applied.Page != p || applied.Sort != field || applied.Order != dir {
					t.Fatalf("%s %s %s page %d: total %d applied %+v (want total %d)", name, field, dir, p, total, applied, want)
				}
				for _, it := range items {
					if seen[id(it)] {
						t.Fatalf("%s %s %s: %s seen twice", name, field, dir, id(it))
					}
					seen[id(it)] = true
				}
			}
			if len(seen) != want {
				t.Fatalf("%s %s %s: %d records seen, want %d", name, field, dir, len(seen), want)
			}
		}
	}
	items, _, applied, err := page(listquery.Request{Page: 999, PageSize: 3})
	lastLen := want - 3*((want-1)/3)
	if want == 0 {
		lastLen = 0
	}
	if err != nil || applied.Page != max((want+2)/3, 1) || len(items) != lastLen {
		t.Fatalf("%s beyond the end: page %d, %d items (%v)", name, applied.Page, len(items), err)
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestPagedLists covers the list contract variants (T091): exactly-once paging
// for every sort field and direction, inet ordering of addresses and CIDRs
// (invalid values last), the hostname and ip_version filters applied before
// the count, tenant isolation, and the keyset lists the gRPC API and backup
// walk staying unchanged (id DESC, cursor).
func TestPagedLists(t *testing.T) {
	db := openRepo(t)
	ctx := context.Background()

	// --- subnets: inet order, ip_version filter, an unparsable CIDR last ---
	cidrs := []struct {
		cidr string
		ver  int
	}{{"10.10.0.0/24", 4}, {"192.168.1.0/24", 4}, {"2001:db8::/64", 6}, {"10.0.0.0/24", 4}, {"9.0.0.0/8", 4}, {"10.0.0.0/16", 4}, {"not-a-cidr", 4}}
	subnetID := map[string]string{}
	for i, c := range cidrs {
		s := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: fmt.Sprintf("Net-%c", 'g'-i), CIDR: c.cidr, IPVersion: c.ver}
		mustDo(t, db.CreateSubnet(ctx, s))
		subnetID[c.cidr] = s.ID
	}
	netB := store.Subnet{ID: store.NewID(), TenantID: tenantB, Name: "other", CIDR: "1.0.0.0/8", IPVersion: 4}
	mustDo(t, db.CreateSubnet(ctx, netB))
	subs, total, _, err := db.PageSubnets(ctx, tenantA, store.SubnetFilter{}, listquery.Request{PageSize: 50})
	mustDo(t, err)
	var got []string
	for _, s := range subs {
		got = append(got, s.CIDR)
	}
	if want := []string{"9.0.0.0/8", "10.0.0.0/16", "10.0.0.0/24", "10.10.0.0/24", "192.168.1.0/24", "2001:db8::/64", "not-a-cidr"}; total != 7 || !slices.Equal(got, want) {
		t.Fatalf("subnets in inet order = %v (total %d)", got, total)
	}
	if subs[0].TotalAddresses == 0 {
		t.Fatal("subnet counts are computed for the page")
	}
	if _, total, _, err := db.PageSubnets(ctx, tenantA, store.SubnetFilter{IPVersion: 6}, listquery.Request{}); err != nil || total != 1 {
		t.Fatalf("ip_version 6 total = %d (%v)", total, err)
	}
	walkAll(t, "subnets", store.SubnetList, func(r listquery.Request) ([]store.Subnet, int, listquery.Request, error) {
		return db.PageSubnets(ctx, tenantA, store.SubnetFilter{}, r)
	}, func(s store.Subnet) string { return s.ID }, 7)

	// --- addresses: inet order (not text order), hostname filter, tenant isolation ---
	net24 := subnetID["10.0.0.0/24"]
	seen := time.Now().UTC().Truncate(time.Second)
	octets := []int{100, 9, 2, 10, 25, 3, 200, 1, 11, 99, 4}
	for i, o := range octets {
		a := store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: net24, Address: fmt.Sprintf("10.0.0.%d", o),
			Hostname: []string{"Web-1", "db-1", "web-2", "", "Mail"}[i%5], MACAddress: fmt.Sprintf("aa:bb:cc:00:00:%02x", 20-i),
			Status: []string{"active", "reserved", "offline"}[i%3]}
		if i%2 == 0 {
			ls := seen.Add(-time.Duration(i) * time.Minute)
			a.LastSeen = &ls
		}
		mustDo(t, db.CreateAddress(ctx, a))
	}
	mustDo(t, db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: net24, Address: "2001:db8::1"}))
	mustDo(t, db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: net24, Address: "bogus"}))
	mustDo(t, db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantB, SubnetID: netB.ID, Address: "10.0.0.7", Hostname: "web-b"}))
	addrs, total, _, err := db.PageAddresses(ctx, tenantA, store.AddressFilter{}, listquery.Request{PageSize: 5})
	mustDo(t, err)
	got = got[:0]
	for _, a := range addrs {
		got = append(got, a.Address)
	}
	if want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.9"}; total != 13 || !slices.Equal(got, want) {
		t.Fatalf("addresses page 1 in inet order = %v (total %d)", got, total)
	}
	last, _, applied, err := db.PageAddresses(ctx, tenantA, store.AddressFilter{}, listquery.Request{Page: 3, PageSize: 5, Sort: "address", Order: listquery.Asc})
	mustDo(t, err)
	if applied.Page != 3 || len(last) != 3 || last[1].Address != "2001:db8::1" || last[2].Address != "bogus" {
		t.Fatalf("addresses last page = %+v", last)
	}
	if hs, total, _, err := db.PageAddresses(ctx, tenantA, store.AddressFilter{HostnamePattern: "web"}, listquery.Request{Sort: "hostname"}); err != nil || total != 5 || len(hs) != 5 {
		t.Fatalf("hostname filter total = %d, %d items (%v)", total, len(hs), err)
	}
	walkAll(t, "addresses", store.AddressList, func(r listquery.Request) ([]store.IPAddress, int, listquery.Request, error) {
		return db.PageAddresses(ctx, tenantA, store.AddressFilter{}, r)
	}, func(a store.IPAddress) string { return a.ID }, 13)
	walkAll(t, "addresses of tenant B", store.AddressList, func(r listquery.Request) ([]store.IPAddress, int, listquery.Request, error) {
		return db.PageAddresses(ctx, tenantB, store.AddressFilter{}, r)
	}, func(a store.IPAddress) string { return a.ID }, 1)

	// Keyset list (gRPC, backup) unchanged: id DESC through the cursor.
	var walked []string
	cursor := ""
	for {
		page, err := db.ListAddresses(ctx, tenantA, store.AddressFilter{Limit: 4, CursorID: cursor})
		mustDo(t, err)
		if len(page) == 0 {
			break
		}
		for _, a := range page {
			walked = append(walked, a.ID)
		}
		cursor = page[len(page)-1].ID
	}
	if len(walked) != 13 || !slices.IsSortedFunc(walked, func(a, b string) int { return -compare(a, b) }) {
		t.Fatalf("keyset walk = %d ids, sorted desc %v", len(walked), slices.IsSortedFunc(walked, func(a, b string) int { return -compare(a, b) }))
	}

	// --- devices, interfaces, packages, device addresses ---
	var devIDs []string
	for i, n := range []string{"zeta", "Alpha", "beta", "Gamma", "delta"} {
		d := store.Device{ID: store.NewID(), TenantID: tenantA, Name: n, DeviceType: []string{"server", "switch"}[i%2], Manufacturer: []string{"Dell", "hp", ""}[i%3]}
		mustDo(t, db.CreateDevice(ctx, d))
		devIDs = append(devIDs, d.ID)
	}
	devs, _, _, err := db.PageDevices(ctx, tenantA, store.DeviceFilter{}, listquery.Request{})
	mustDo(t, err)
	got = got[:0]
	for _, d := range devs {
		got = append(got, d.Name)
	}
	if !slices.Equal(got, []string{"Alpha", "beta", "delta", "Gamma", "zeta"}) {
		t.Fatalf("devices by name = %v", got)
	}
	walkAll(t, "devices", store.DeviceList, func(r listquery.Request) ([]store.Device, int, listquery.Request, error) {
		return db.PageDevices(ctx, tenantA, store.DeviceFilter{}, r)
	}, func(d store.Device) string { return d.ID }, 5)
	host := devIDs[0]
	for _, n := range []string{"eth1", "eth0", "bond0", "lo"} {
		mustDo(t, db.CreateInterface(ctx, store.DeviceInterface{ID: store.NewID(), TenantID: tenantA, DeviceID: host, Name: n}))
	}
	walkAll(t, "interfaces", store.InterfaceList, func(r listquery.Request) ([]store.DeviceInterface, int, listquery.Request, error) {
		return db.PageInterfaces(ctx, tenantA, host, r)
	}, func(i store.DeviceInterface) string { return i.ID }, 4)
	mustDo(t, db.ReplaceDevicePackages(ctx, tenantA, host, []store.DevicePackage{
		{Name: "openssl", CurrentVersion: "3.0", NeedsUpdate: true}, {Name: "bash", CurrentVersion: "5.2"}, {Name: "curl", CurrentVersion: "8.1", NeedsUpdate: true}, {Name: "zlib", CurrentVersion: "1.3"},
	}))
	walkAll(t, "packages", store.PackageList, func(r listquery.Request) ([]store.DevicePackage, int, listquery.Request, error) {
		return db.PageDevicePackages(ctx, tenantA, host, nil, nil, "", r)
	}, func(p store.DevicePackage) string { return p.ID }, 4)
	upd := true
	if _, total, _, err := db.PageDevicePackages(ctx, tenantA, host, &upd, nil, "", listquery.Request{}); err != nil || total != 2 {
		t.Fatalf("packages needing update = %d (%v)", total, err)
	}
	for _, o := range []string{"10.0.0.150", "10.0.0.20"} {
		mustDo(t, db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantA, SubnetID: net24, Address: o, DeviceID: host}))
	}
	if da, total, _, err := db.PageAddresses(ctx, tenantA, store.AddressFilter{DeviceID: host}, listquery.Request{}); err != nil || total != 2 || da[0].Address != "10.0.0.20" {
		t.Fatalf("device addresses = %+v (%v)", da, err)
	}

	// --- guests ---
	s, err := db.EnsureHostSyncSettings(ctx, tenantA)
	mustDo(t, err)
	s.Enabled = true
	mustDo(t, db.UpdateHostSyncSettings(ctx, s, store.AuditRow{TenantID: tenantA, Action: "hostsync_settings_updated"}))
	mustDo(t, db.ApplyHostReport(ctx, tenantA, func(tx repo.HostTx) error {
		var gs []store.HypervisorGuest
		for i, n := range []string{"vm-b", "VM-a", "", "vm-c"} {
			gs = append(gs, store.HypervisorGuest{ID: store.NewID(), GuestRef: fmt.Sprint(100 + i), Name: n, Kind: "vm", MACs: []string{}, LastReportedAt: seen})
		}
		return tx.ReplaceGuests(host, gs)
	}))
	walkAll(t, "guests", store.GuestList, func(r listquery.Request) ([]store.HypervisorGuest, int, listquery.Request, error) {
		return db.PageGuests(ctx, tenantA, host, r)
	}, func(g store.HypervisorGuest) string { return g.ID }, 4)

	// --- vlans ---
	for i, v := range []int{300, 10, 2000, 42} {
		mustDo(t, db.CreateVlan(ctx, store.Vlan{ID: store.NewID(), TenantID: tenantA, VlanID: v, Name: fmt.Sprintf("v%d", i), Domain: []string{"core", "Edge"}[i%2]}))
	}
	vl, _, _, err := db.PageVlans(ctx, tenantA, store.VlanFilter{}, listquery.Request{})
	mustDo(t, err)
	if vl[0].VlanID != 10 || vl[3].VlanID != 2000 {
		t.Fatalf("vlans by number = %+v", vl)
	}
	walkAll(t, "vlans", store.VlanList, func(r listquery.Request) ([]store.Vlan, int, listquery.Request, error) {
		return db.PageVlans(ctx, tenantA, store.VlanFilter{}, r)
	}, func(v store.Vlan) string { return v.ID }, 4)

	// --- scan jobs: newest first by default ---
	var jobs []string
	for i := range 5 {
		j := store.IPScanJob{ID: store.NewID(), TenantID: tenantA, SubnetID: subs[i%2].ID, Status: []string{"pending", "completed"}[i%2]}
		mustDo(t, db.CreateScanJob(ctx, j))
		jobs = append(jobs, j.ID)
		time.Sleep(2 * time.Millisecond)
	}
	sj, _, _, err := db.PageScanJobs(ctx, tenantA, store.ScanFilter{}, listquery.Request{})
	mustDo(t, err)
	if sj[0].ID != jobs[4] || sj[4].ID != jobs[0] {
		t.Fatal("scan jobs are not newest first")
	}
	walkAll(t, "scans", store.ScanList, func(r listquery.Request) ([]store.IPScanJob, int, listquery.Request, error) {
		return db.PageScanJobs(ctx, tenantA, store.ScanFilter{}, r)
	}, func(j store.IPScanJob) string { return j.ID }, 5)
	if _, total, _, err := db.PageScanJobs(ctx, tenantA, store.ScanFilter{Status: "completed"}, listquery.Request{}); err != nil || total != 2 {
		t.Fatalf("completed scans = %d (%v)", total, err)
	}

	// --- group members ---
	ipg := store.IPGroup{ID: store.NewID(), TenantID: tenantA, Name: "g1"}
	mustDo(t, db.CreateIPGroup(ctx, ipg))
	for i, v := range []string{"10.0.0.0/8", "192.168.0.1", "172.16.0.0/12", "10.1.1.1"} {
		mustDo(t, db.AddIPGroupMember(ctx, store.IPGroupMember{ID: store.NewID(), TenantID: tenantA, IPGroupID: ipg.ID, MemberType: "address", Value: v, Sequence: 4 - i}))
	}
	walkAll(t, "ip members", store.IPMemberList, func(r listquery.Request) ([]store.IPGroupMember, int, listquery.Request, error) {
		return db.PageIPGroupMembers(ctx, tenantA, ipg.ID, r)
	}, func(m store.IPGroupMember) string { return m.ID }, 4)
	hg := store.HostGroup{ID: store.NewID(), TenantID: tenantA, Name: "h1"}
	mustDo(t, db.CreateHostGroup(ctx, hg))
	for i, d := range devIDs {
		mustDo(t, db.AddHostGroupMember(ctx, store.HostGroupMember{ID: store.NewID(), TenantID: tenantA, HostGroupID: hg.ID, DeviceID: d, Sequence: i}))
	}
	hm, _, _, err := db.PageHostGroupMembers(ctx, tenantA, hg.ID, listquery.Request{Sort: "name"})
	mustDo(t, err)
	if hm[0].DeviceName != "Alpha" {
		t.Fatalf("host members by name = %+v", hm)
	}
	walkAll(t, "host members", store.HostMemberList, func(r listquery.Request) ([]store.HostGroupMember, int, listquery.Request, error) {
		return db.PageHostGroupMembers(ctx, tenantA, hg.ID, r)
	}, func(m store.HostGroupMember) string { return m.ID }, 5)
	// Another tenant sees none of tenant A's members.
	if _, total, _, err := db.PageHostGroupMembers(ctx, tenantB, hg.ID, listquery.Request{}); err != nil || total != 0 {
		t.Fatalf("tenant B host members = %d (%v)", total, err)
	}
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
