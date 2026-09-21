package groups_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

func subj(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, UserID: "u", Roles: []string{"admin"}, ActorKind: authz.ActorUser}
}

func TestIPGroupCRUD(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	g, err := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "web"})
	if err != nil || g.ID == "" || g.Status != store.GroupActive {
		t.Fatalf("CreateIPGroup: %v %+v", err, g)
	}
	if _, err := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "web"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup: want ErrConflict, got %v", err)
	}
	if _, err := svc.CreateIPGroup(ctx, s, store.IPGroup{}); err == nil {
		t.Fatalf("empty name: want error")
	}

	got, err := svc.GetIPGroup(ctx, s, g.ID)
	if err != nil || got.Name != "web" {
		t.Fatalf("Get: %v %+v", err, got)
	}
	list, err := svc.ListIPGroups(ctx, s, 0, "")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v n=%d", err, len(list))
	}
	g.Name = "web2"
	g.Status = ""
	up, err := svc.UpdateIPGroup(ctx, s, g)
	if err != nil || up.Name != "web2" || up.Status != store.GroupActive {
		t.Fatalf("Update: %v %+v", err, up)
	}
	if err := svc.DeleteIPGroup(ctx, s, g.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetIPGroup(ctx, s, g.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestIPGroupMembersAndValidation(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	g, _ := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "g"})

	addr, err := svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g.ID, MemberType: store.MemberAddress, Value: "10.0.0.5"})
	if err != nil {
		t.Fatalf("add address: %v", err)
	}
	if _, err := svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g.ID, MemberType: store.MemberRange, Value: "10.0.1.1-10.0.1.10"}); err != nil {
		t.Fatalf("add range: %v", err)
	}
	if _, err := svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g.ID, MemberType: store.MemberSubnet, Value: "192.168.0.0/24"}); err != nil {
		t.Fatalf("add subnet: %v", err)
	}

	// invalid values
	bad := []store.IPGroupMember{
		{IPGroupID: g.ID, MemberType: store.MemberAddress, Value: "nope"},
		{IPGroupID: g.ID, MemberType: store.MemberRange, Value: "10.0.0.1"},
		{IPGroupID: g.ID, MemberType: store.MemberSubnet, Value: "10.0.0.0/33"},
		{IPGroupID: g.ID, MemberType: "weird", Value: "x"},
		{IPGroupID: g.ID, MemberType: store.MemberAddress, Value: ""},
	}
	for i, b := range bad {
		if _, err := svc.AddIPGroupMember(ctx, s, b); err == nil {
			t.Fatalf("bad member %d accepted", i)
		}
	}

	members, err := svc.ListIPGroupMembers(ctx, s, g.ID)
	if err != nil || len(members) != 3 {
		t.Fatalf("list members: %v n=%d", err, len(members))
	}

	// update member
	addr.Value = "10.0.0.9"
	if _, err := svc.UpdateIPGroupMember(ctx, s, addr); err != nil {
		t.Fatalf("update member: %v", err)
	}
	// remove
	if err := svc.RemoveIPGroupMember(ctx, s, addr.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	members, _ = svc.ListIPGroupMembers(ctx, s, g.ID)
	if len(members) != 2 {
		t.Fatalf("after remove n=%d", len(members))
	}
}

func TestCheckIpInGroup(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	g1, _ := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "g1"})
	g2, _ := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "g2"})
	g3, _ := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "g3"})

	_, _ = svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g1.ID, MemberType: store.MemberAddress, Value: "10.0.0.5"})
	_, _ = svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g2.ID, MemberType: store.MemberRange, Value: "10.0.0.1-10.0.0.10"})
	_, _ = svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g3.ID, MemberType: store.MemberSubnet, Value: "192.168.0.0/24"})

	// 10.0.0.5 matches g1 (exact) and g2 (range), not g3.
	matched, err := svc.CheckIpInGroup(ctx, s, "10.0.0.5", nil)
	if err != nil {
		t.Fatalf("CheckIp: %v", err)
	}
	gotNames := names(matched)
	if len(gotNames) != 2 || gotNames[0] != "g1" || gotNames[1] != "g2" {
		t.Fatalf("10.0.0.5 matched %v, want [g1 g2]", gotNames)
	}

	// scoped to a subset
	scoped, _ := svc.CheckIpInGroup(ctx, s, "10.0.0.5", []string{g2.ID})
	if len(scoped) != 1 || scoped[0].ID != g2.ID {
		t.Fatalf("scoped match wrong: %v", names(scoped))
	}

	// subnet-only ip
	sub, _ := svc.CheckIpInGroup(ctx, s, "192.168.0.77", nil)
	if len(sub) != 1 || sub[0].Name != "g3" {
		t.Fatalf("subnet match: %v", names(sub))
	}

	// no match
	none, _ := svc.CheckIpInGroup(ctx, s, "172.16.0.1", nil)
	if len(none) != 0 {
		t.Fatalf("expected no match, got %v", names(none))
	}
}

func TestMatchesUnit(t *testing.T) {
	cases := []struct {
		mt, val, ip string
		want        bool
	}{
		{store.MemberAddress, "10.0.0.1", "10.0.0.1", true},
		{store.MemberAddress, "10.0.0.1", "10.0.0.2", false},
		{store.MemberAddress, "::1", "0:0:0:0:0:0:0:1", true},
		{store.MemberRange, "10.0.0.1-10.0.0.20", "10.0.0.15", true},
		{store.MemberRange, "10.0.0.1-10.0.0.20", "10.0.0.21", false},
		{store.MemberSubnet, "10.0.0.0/24", "10.0.0.200", true},
		{store.MemberSubnet, "10.0.0.0/24", "10.0.1.1", false},
		{store.MemberAddress, "garbage", "10.0.0.1", false},
		{"unknown", "10.0.0.1", "10.0.0.1", false},
	}
	for i, c := range cases {
		got := groups.Matches(store.IPGroupMember{MemberType: c.mt, Value: c.val}, c.ip)
		if got != c.want {
			t.Errorf("case %d: Matches(%s,%q,%q)=%v want %v", i, c.mt, c.val, c.ip, got, c.want)
		}
	}
}

func TestHostGroups(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	tenant := store.NewID()
	s := subj(tenant)

	// two devices
	d1 := store.Device{TenantID: tenant, Name: "srv1", DeviceType: store.DevServer, PrimaryIP: "10.0.0.1"}
	d2 := store.Device{TenantID: tenant, Name: "srv2", DeviceType: store.DevServer}
	if err := m.CreateDevice(ctx, d1); err != nil {
		t.Fatalf("dev1: %v", err)
	}
	if err := m.CreateDevice(ctx, d2); err != nil {
		t.Fatalf("dev2: %v", err)
	}
	devs, _ := m.ListDevices(ctx, tenant, store.DeviceFilter{})
	var id1, id2 string
	for _, d := range devs {
		switch d.Name {
		case "srv1":
			id1 = d.ID
		case "srv2":
			id2 = d.ID
		}
	}

	g, err := svc.CreateHostGroup(ctx, s, store.HostGroup{Name: "cluster"})
	if err != nil || g.ID == "" {
		t.Fatalf("CreateHostGroup: %v %+v", err, g)
	}
	if _, err := svc.CreateHostGroup(ctx, s, store.HostGroup{Name: "cluster"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dup: want ErrConflict, got %v", err)
	}
	if _, err := svc.AddHostGroupMember(ctx, s, store.HostGroupMember{HostGroupID: g.ID, DeviceID: id1}); err != nil {
		t.Fatalf("add member1: %v", err)
	}
	if _, err := svc.AddHostGroupMember(ctx, s, store.HostGroupMember{HostGroupID: g.ID, DeviceID: id2}); err != nil {
		t.Fatalf("add member2: %v", err)
	}
	if _, err := svc.AddHostGroupMember(ctx, s, store.HostGroupMember{HostGroupID: g.ID}); err == nil {
		t.Fatalf("empty device_id: want error")
	}

	members, err := svc.ListHostGroupMembers(ctx, s, g.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("list members: %v n=%d", err, len(members))
	}
	var m1 store.HostGroupMember
	for _, mm := range members {
		if mm.DeviceID == id1 {
			m1 = mm
		}
	}
	if m1.DeviceName != "srv1" || m1.DevicePrimaryIP != "10.0.0.1" {
		t.Fatalf("member not enriched: %+v", m1)
	}

	groupsForDev, err := svc.ListDeviceHostGroups(ctx, s, id1)
	if err != nil || len(groupsForDev) != 1 || groupsForDev[0].ID != g.ID {
		t.Fatalf("ListDeviceHostGroups: %v %+v", err, groupsForDev)
	}

	// update + remove
	m1.DeviceID = id2
	if _, err := svc.UpdateHostGroupMember(ctx, s, m1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("update to dup device: want ErrConflict, got %v", err)
	}
	if err := svc.RemoveHostGroupMember(ctx, s, m1.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	list, _ := svc.ListHostGroups(ctx, s, 0, "")
	if len(list) != 1 {
		t.Fatalf("list host groups n=%d", len(list))
	}
	g.Name = "cluster2"
	if _, err := svc.UpdateHostGroup(ctx, s, g); err != nil {
		t.Fatalf("update host group: %v", err)
	}
	if err := svc.DeleteHostGroup(ctx, s, g.ID); err != nil {
		t.Fatalf("delete host group: %v", err)
	}
}

func TestForbidden(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.CreateIPGroup(ctx, bad, store.IPGroup{Name: "x"}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if _, err := svc.CheckIpInGroup(ctx, bad, "1.2.3.4", nil); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("CheckIp want ErrForbidden, got %v", err)
	}
	if _, err := svc.ListDeviceHostGroups(ctx, bad, "d"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("ListDeviceHostGroups want ErrForbidden, got %v", err)
	}
}

func names(gs []store.IPGroup) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Name)
	}
	sort.Strings(out)
	return out
}

func TestErrorString(t *testing.T) {
	if (groups.ValidationError{Msg: "x"}).Error() != "groups: x" {
		t.Fatalf("Error() wrong")
	}
}

func TestGetHostGroupAndListMembersHappy(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	g, _ := svc.CreateHostGroup(ctx, s, store.HostGroup{Name: "hg"})
	got, err := svc.GetHostGroup(ctx, s, g.ID)
	if err != nil || got.Name != "hg" {
		t.Fatalf("GetHostGroup: %v %+v", err, got)
	}
	ms, err := svc.ListHostGroupMembers(ctx, s, g.ID)
	if err != nil || len(ms) != 0 {
		t.Fatalf("ListHostGroupMembers: %v n=%d", err, len(ms))
	}
}

func TestForbiddenSweep(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	wantF := func(name string, err error) {
		if !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("%s: want ErrForbidden, got %v", name, err)
		}
	}
	_, e := svc.GetIPGroup(ctx, bad, "x")
	wantF("GetIPGroup", e)
	_, e = svc.ListIPGroups(ctx, bad, 0, "")
	wantF("ListIPGroups", e)
	_, e = svc.UpdateIPGroup(ctx, bad, store.IPGroup{Name: "n"})
	wantF("UpdateIPGroup", e)
	wantF("DeleteIPGroup", svc.DeleteIPGroup(ctx, bad, "x"))
	_, e = svc.AddIPGroupMember(ctx, bad, store.IPGroupMember{})
	wantF("AddIPGroupMember", e)
	wantF("RemoveIPGroupMember", svc.RemoveIPGroupMember(ctx, bad, "x"))
	_, e = svc.UpdateIPGroupMember(ctx, bad, store.IPGroupMember{})
	wantF("UpdateIPGroupMember", e)
	_, e = svc.ListIPGroupMembers(ctx, bad, "x")
	wantF("ListIPGroupMembers", e)

	_, e = svc.CreateHostGroup(ctx, bad, store.HostGroup{Name: "n"})
	wantF("CreateHostGroup", e)
	_, e = svc.GetHostGroup(ctx, bad, "x")
	wantF("GetHostGroup", e)
	_, e = svc.ListHostGroups(ctx, bad, 0, "")
	wantF("ListHostGroups", e)
	_, e = svc.UpdateHostGroup(ctx, bad, store.HostGroup{Name: "n"})
	wantF("UpdateHostGroup", e)
	wantF("DeleteHostGroup", svc.DeleteHostGroup(ctx, bad, "x"))
	_, e = svc.AddHostGroupMember(ctx, bad, store.HostGroupMember{DeviceID: "d"})
	wantF("AddHostGroupMember", e)
	wantF("RemoveHostGroupMember", svc.RemoveHostGroupMember(ctx, bad, "x"))
	_, e = svc.UpdateHostGroupMember(ctx, bad, store.HostGroupMember{DeviceID: "d"})
	wantF("UpdateHostGroupMember", e)
	_, e = svc.ListHostGroupMembers(ctx, bad, "x")
	wantF("ListHostGroupMembers", e)
}

func TestUpdateValidationAndNotFound(t *testing.T) {
	m := memstore.New()
	svc := groups.New(m)
	ctx := context.Background()
	s := subj(store.NewID())

	if _, err := svc.UpdateIPGroup(ctx, s, store.IPGroup{ID: "x"}); err == nil {
		t.Fatalf("empty name update: want error")
	}
	if _, err := svc.UpdateIPGroup(ctx, s, store.IPGroup{ID: "missing", Name: "n"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update missing ipgroup: %v", err)
	}
	if _, err := svc.UpdateHostGroup(ctx, s, store.HostGroup{ID: "x"}); err == nil {
		t.Fatalf("empty name host update: want error")
	}
	if _, err := svc.UpdateHostGroup(ctx, s, store.HostGroup{ID: "missing", Name: "n"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("update missing hostgroup: %v", err)
	}
	// member update with invalid value
	g, _ := svc.CreateIPGroup(ctx, s, store.IPGroup{Name: "g"})
	mem, _ := svc.AddIPGroupMember(ctx, s, store.IPGroupMember{IPGroupID: g.ID, MemberType: store.MemberAddress, Value: "10.0.0.1"})
	mem.Value = "bad"
	if _, err := svc.UpdateIPGroupMember(ctx, s, mem); err == nil {
		t.Fatalf("update member bad value: want error")
	}
	// host member update happy path (no conflict)
	hg, _ := svc.CreateHostGroup(ctx, s, store.HostGroup{Name: "h"})
	hm, _ := svc.AddHostGroupMember(ctx, s, store.HostGroupMember{HostGroupID: hg.ID, DeviceID: "dev-a"})
	hm.Sequence = 5
	if _, err := svc.UpdateHostGroupMember(ctx, s, hm); err != nil {
		t.Fatalf("update host member: %v", err)
	}
}
