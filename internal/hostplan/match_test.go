package hostplan

import (
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestMatchByHostID(t *testing.T) {
	d := store.Device{ID: "d1", Name: "other-name", InventoryHostID: hostID}
	got, how := Match(Candidates{ByHost: &d, BySerial: []store.Device{{ID: "d2", SerialNumber: "SN-1"}}}, report())
	if got == nil || got.ID != "d1" || how != MatchHostID {
		t.Fatalf("got %v %s", got, how)
	}
}

func TestMatchBySerial(t *testing.T) {
	r := report()
	got, how := Match(Candidates{BySerial: []store.Device{{ID: "d2", SerialNumber: " sn-1 "}}}, r)
	if got == nil || got.ID != "d2" || how != MatchSerial {
		t.Fatalf("serial match: %v %s", got, how)
	}
	// Two devices with the serial -> not matched by serial (create or name).
	two := Candidates{BySerial: []store.Device{{ID: "a", SerialNumber: "SN-1"}, {ID: "b", SerialNumber: "SN-1"}}}
	if got, how := Match(two, r); got != nil || how != MatchCreated {
		t.Fatalf("ambiguous serial: %v %s", got, how)
	}
	// A device linked to another inventory host is never adopted.
	linked := Candidates{BySerial: []store.Device{{ID: "a", SerialNumber: "SN-1", InventoryHostID: hostID2}}}
	if got, _ := Match(linked, r); got != nil {
		t.Fatal("device of another host adopted by serial")
	}
	// A candidate list with a non-matching serial never matches.
	if got, _ := Match(Candidates{BySerial: []store.Device{{ID: "a", SerialNumber: "SN-2"}}}, r); got != nil {
		t.Fatal("different serial")
	}
	for _, ph := range []string{"To Be Filled By O.E.M.", "0", "Default string", "000000", "xxxx", "", "None", "System Serial Number"} {
		r.Serial = ph
		c := Candidates{BySerial: []store.Device{{ID: "p", SerialNumber: ph}}}
		if got, _ := Match(c, r); got != nil {
			t.Errorf("placeholder serial %q matched", ph)
		}
	}
}

func TestMatchByName(t *testing.T) {
	r := report()
	r.Serial = ""
	got, how := Match(Candidates{ByName: []store.Device{{ID: "n1", Name: "WEB-01"}}}, r)
	if got == nil || got.ID != "n1" || how != MatchName {
		t.Fatalf("name match: %v %s", got, how)
	}
	if got, _ := Match(Candidates{ByName: []store.Device{{ID: "n1", Name: "web-01", InventoryHostID: hostID2}}}, r); got != nil {
		t.Fatal("device of another host adopted by name")
	}
	for _, g := range []string{"localhost", "localhost.localdomain", "ubuntu", "Debian", "raspberrypi", "centos", "fedora", "unknown"} {
		r.Hostname = g
		if got, _ := Match(Candidates{ByName: []store.Device{{ID: "n", Name: g}}}, r); got != nil {
			t.Errorf("generic hostname %q merged", g)
		}
	}
	r.Hostname = ""
	if got, _ := Match(Candidates{ByName: []store.Device{{ID: "n", Name: ""}}}, r); got != nil {
		t.Fatal("empty hostname merged")
	}
}

func TestNamingRules(t *testing.T) {
	r := report()
	if n := desiredName(Candidates{}, r, "self"); n != "web-01" {
		t.Fatal(n)
	}
	c := Candidates{ByName: []store.Device{{ID: "other", Name: "web-01"}}}
	if n := desiredName(c, r, "self"); n != "web-01 (0190f7c2)" {
		t.Fatalf("collision name %q", n)
	}
	if n := desiredName(c, r, "other"); n != "web-01" {
		t.Fatal("own name is not a collision")
	}
	r.Hostname = ""
	if n := desiredName(Candidates{}, r, "x"); n != "host-0190f7c2" {
		t.Fatal(n)
	}
	if got := CandidateNames(report()); len(got) != 2 || got[1] != "web-01 (0190f7c2)" {
		t.Fatal(got)
	}
	if short("abc") != "abc" || FallbackName("abc") != "host-abc" {
		t.Fatal("short ids")
	}
	if !IsGenericName(" LocalHost ") || IsGenericName("web") || !IsPlaceholderSerial("N/A") || IsPlaceholderSerial("ABC123") {
		t.Fatal("helpers")
	}
}

func TestRenamedHostRenamesDevice(t *testing.T) {
	d := store.Device{ID: "d1", Name: "old-name", InventoryHostID: hostID, Source: store.SrcHostReport, ReportState: store.RepReported}
	p := Build(State{Candidates: Candidates{ByHost: &d}}, report(), params())
	up := ops(p, OpUpdateDevice)
	if len(up) != 1 || up[0].Device.Name != "web-01" || len(ops(p, OpCreateDevice)) != 0 {
		t.Fatalf("rename: %+v", p.Ops)
	}
}
