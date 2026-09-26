package hostplan

import (
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func richReport() hostreport.Report {
	r := hvReport()
	r.PrimaryIPv6 = addrOf("2001:db8::5")
	r.BMC = &hostreport.BMC{Address: addrOf("10.9.0.5"), Prefix: 24, Ports: []hostreport.BMCPort{{MAC: "aa:bb:cc:00:00:10"}}}
	r.Updates = hostreport.Updates{Manager: "apt", Status: store.UpdAvailable, RebootRequired: hostreport.TriTrue, AutomaticUpdates: hostreport.TriFalse}
	r.Pending = []hostreport.Package{{Name: "openssl", Installed: "1", Available: "2", Security: true}}
	return r
}

func assertIdempotent(t *testing.T, st State, r hostreport.Report) {
	t.Helper()
	p := Build(st, r, params())
	if len(p.Ops) == 0 {
		t.Fatal("first plan must change something")
	}
	for _, o := range p.Ops {
		if len(o.Audit) == 0 {
			t.Fatalf("op %s without an audit row", o.Kind)
		}
		for _, a := range o.Audit {
			if a.ActorID != "hostsync" || a.ActorKind != "system" || a.TenantID != tenant || a.Detail["inventory_host_id"] != r.HostID || a.Detail["run_id"] != "run-1" {
				t.Fatalf("audit row %+v", a)
			}
		}
	}
	st2 := apply(t, st, p)
	p2 := Build(st2, r, params())
	if len(p2.Ops) != 0 {
		t.Fatalf("re-plan of the same report must be empty, got %v", actions(p2))
	}
	for _, e := range p2.Events {
		t.Fatalf("no events without changes: %+v", e)
	}
}

func TestIdempotentCreate(t *testing.T) {
	st := State{MACOwners: []repo.MACOwner{{MAC: "bc:24:11:00:00:01", DeviceID: "g1"}},
		GuestDevices: nil}
	assertIdempotent(t, st, richReport())
}

func TestIdempotentUpdate(t *testing.T) {
	d := adminDevice()
	st := State{Candidates: Candidates{ByName: []store.Device{d}}, Subnets: []store.Subnet{{ID: "s", Name: "lan", CIDR: "10.0.0.0/24"}},
		Addresses:  map[string]store.IPAddress{"10.0.0.5": {ID: "a", Address: "10.0.0.5", DeviceID: "someone", SubnetID: "s"}},
		Interfaces: []store.DeviceInterface{{ID: "i", DeviceID: "d1", Name: "eth0"}},
		Packages:   []store.DevicePackage{{ID: "p", Name: "old"}}}
	assertIdempotent(t, st, richReport())
}

func TestIdempotentGuestOwnReport(t *testing.T) {
	r := report()
	r.Interfaces = []hostreport.Interface{{Name: "eth0", MAC: "bc:24:11:00:00:01"}}
	st := State{GuestRows: []store.HypervisorGuest{{ID: "row1", HostDeviceID: "hv1", GuestRef: "101", MACs: []string{"bc:24:11:00:00:01"}}}}
	assertIdempotent(t, st, r)
}

func TestPlanIssuesAndState(t *testing.T) {
	r := report()
	r.Issues = []store.HostSyncIssue{{Field: "hostname", Reason: "too_long", Count: 1}}
	r.CollectedAt = time.Time{}
	st := State{MACOwners: []repo.MACOwner{{MAC: "bc:24:11:00:00:01", DeviceID: "a"}, {MAC: "bc:24:11:00:00:01", DeviceID: "b"},
		{MAC: "bc:24:11:00:00:02", DeviceID: "a"}, {MAC: "bc:24:11:00:00:02", DeviceID: "c"}}}
	r.Guests = hvReport().Guests
	p := Build(st, r, params())
	if p.State.CollectedAt != nil || len(p.Issues) != 2 || p.Issues[0].Field != "hostname" || p.Issues[1].Count != 2 {
		t.Fatalf("issues %+v", p.Issues)
	}
	for i := 0; i < hostreport.MaxIssues+3; i++ {
		r.Issues = append(r.Issues, store.HostSyncIssue{Field: "f", Reason: "r"})
	}
	if p := Build(st, r, params()); len(p.Issues) != hostreport.MaxIssues || len(p.State.Issues) != hostreport.MaxIssues {
		t.Fatal("issue cap")
	}
}

func TestDeviceChangesListsEveryField(t *testing.T) {
	a := store.Device{}
	b := store.Device{Name: "n", RebootRequired: true, UnattendedUpgrades: true, LastSeen: &t0, LastReportAt: &t0}
	ch := deviceChanges(a, b)
	for _, f := range []string{"name", "reboot_required", "unattended_upgrades", "last_seen", "last_report_at"} {
		if _, ok := ch[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	if !sameTime(nil, nil) || sameTime(&t0, nil) || fmtTime(&a, "last_seen") != nil {
		t.Fatal("time helpers")
	}
	c := addrChanges(store.IPAddress{}, store.IPAddress{IsPrimary: true, Conflict: true, MoveCount: 1})
	if len(c) != 3 {
		t.Fatalf("%v", c)
	}
	ic := ifaceChanges(store.DeviceInterface{}, store.DeviceInterface{MACAddress: "m", InterfaceType: "t", SpeedMbps: 1, Enabled: true, ReportState: "r"})
	if len(ic) != 5 {
		t.Fatalf("%v", ic)
	}
}

func TestGuestsEqualAndIssueOrder(t *testing.T) {
	a := []store.HypervisorGuest{{GuestRef: "1", MACs: []string{"m"}}}
	if guestsEqual(a, nil) || guestsEqual(a, []store.HypervisorGuest{{GuestRef: "2"}}) ||
		guestsEqual(a, []store.HypervisorGuest{{GuestRef: "1", MACs: []string{"x"}}}) || !guestsEqual(a, a) {
		t.Fatal("guestsEqual")
	}
	// Two planner issues are listed in a stable order.
	r := report()
	r.Guests = hvReport().Guests
	r.Interfaces = append(r.Interfaces, hostreport.Interface{Name: "bmc"})
	r.BMC = &hostreport.BMC{Address: addrOf("10.9.0.5")}
	st := State{MACOwners: []repo.MACOwner{{MAC: "bc:24:11:00:00:01", DeviceID: "a"}, {MAC: "bc:24:11:00:00:01", DeviceID: "b"}}}
	p := Build(st, r, params())
	if len(p.Issues) != 2 || p.Issues[0].Field != "bmc_interface" || p.Issues[1].Field != "guest_mac" {
		t.Fatalf("%+v", p.Issues)
	}
}
