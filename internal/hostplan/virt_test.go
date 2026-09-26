package hostplan

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func hvReport() hostreport.Report {
	r := report()
	r.Guests = []hostreport.Guest{
		{Ref: "101", Name: "vm-a", Kind: "vm", Platform: "proxmox", MACs: []string{"bc:24:11:00:00:01"}},
		{Ref: "102", Name: "ct-b", Kind: "container", Platform: "proxmox", MACs: []string{"bc:24:11:00:00:02"}},
	}
	return r
}

func TestGuestsLinkedAndListed(t *testing.T) {
	_, st := linked()
	st.MACOwners = []repo.MACOwner{{MAC: "bc:24:11:00:00:01", DeviceID: "g1"}, {MAC: "aa:bb:cc:00:00:01", DeviceID: "d1"}}
	p := Build(st, hvReport(), params())
	rg := ops(p, OpReplaceGuests)
	if len(rg) != 1 || len(rg[0].Guests) != 2 || rg[0].Guests[0].GuestDeviceID != "g1" || rg[0].Guests[1].GuestDeviceID != "" {
		t.Fatalf("guests %+v", rg)
	}
	sh := ops(p, OpSetHypervisor)
	if len(sh) != 1 || sh[0].DeviceID != "g1" || sh[0].RefID != "d1" || sh[0].Audit[0].Action != "hypervisor_linked" {
		t.Fatalf("link %+v", sh)
	}
}

func TestGuestDuplicateMACAndSelf(t *testing.T) {
	_, st := linked()
	st.MACOwners = []repo.MACOwner{{MAC: "bc:24:11:00:00:01", DeviceID: "g1"}, {MAC: "bc:24:11:00:00:01", DeviceID: "g2"},
		{MAC: "bc:24:11:00:00:02", DeviceID: "d1"}}
	p := Build(st, hvReport(), params())
	if len(ops(p, OpSetHypervisor)) != 0 {
		t.Fatal("duplicate MAC or own MAC must not link")
	}
	found := false
	for _, i := range p.Issues {
		found = found || i.Reason == "duplicate_mac"
	}
	if !found {
		t.Fatal("duplicate_mac issue")
	}
}

func TestGuestUnlinked(t *testing.T) {
	_, st := linked()
	st.Guests = []store.HypervisorGuest{{ID: "row1", GuestRef: "101", Kind: "vm", GuestDeviceID: "g1", MACs: []string{"bc:24:11:00:00:01"}}}
	st.GuestDevices = []store.Device{{ID: "g1", HypervisorDeviceID: "d1"}}
	p := Build(st, report(), params()) // host no longer reports guest 101
	sh := ops(p, OpSetHypervisor)
	if len(sh) != 1 || sh[0].DeviceID != "g1" || sh[0].RefID != "" || sh[0].Audit[0].Action != "hypervisor_unlinked" {
		t.Fatalf("unlink %+v", sh)
	}
	if rg := ops(p, OpReplaceGuests); len(rg) != 1 || len(rg[0].Guests) != 0 {
		t.Fatal("guest rows replaced")
	}
}

func TestGuestOwnReportLinks(t *testing.T) {
	r := report()
	r.HostID, r.Hostname = hostID2, "vm-a"
	r.Interfaces = []hostreport.Interface{{Name: "eth0", MAC: "bc:24:11:00:00:01"}}
	st := State{GuestRows: []store.HypervisorGuest{
		{ID: "row1", HostDeviceID: "hv1", GuestRef: "101", MACs: []string{"bc:24:11:00:00:01"}},
		{ID: "row9", HostDeviceID: "hv1", GuestRef: "109", MACs: []string{"ff:00:00:00:00:00"}},
	}}
	p := Build(st, r, params())
	sh := ops(p, OpSetHypervisor)
	sg := ops(p, OpSetGuestDevice)
	if len(sh) != 1 || sh[0].DeviceID != p.DeviceID || sh[0].RefID != "hv1" || len(sg) != 1 || sg[0].GuestRowID != "row1" || sg[0].RefID != p.DeviceID {
		t.Fatalf("own link %+v %+v", sh, sg)
	}
	// Two hypervisors list the MAC -> no link, issue.
	st.GuestRows = append(st.GuestRows, store.HypervisorGuest{ID: "row2", HostDeviceID: "hv2", MACs: []string{"bc:24:11:00:00:01"}})
	if p := Build(st, r, params()); len(ops(p, OpSetHypervisor)) != 0 {
		t.Fatal("ambiguous hypervisor")
	}
	// Own guest rows never make a device its own hypervisor.
	st.GuestRows = []store.HypervisorGuest{{ID: "self", HostDeviceID: p.DeviceID, MACs: []string{"bc:24:11:00:00:01"}}}
	if p2 := Build(st, r, Params{TenantID: tenant, Now: t0, NewID: func() string { return p.DeviceID }, ConflictMoves: 3}); len(ops(p2, OpSetHypervisor)) != 0 {
		t.Fatal("self link")
	}
}
