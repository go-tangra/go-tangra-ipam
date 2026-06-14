package service

import (
	"reflect"
	"testing"
)

func TestParseHostedVMs(t *testing.T) {
	meta := `{
		"machine_id": "abc",
		"hosted_vms": [
			{"vmid": "101", "name": "web01", "type": "qemu", "macs": ["aa:bb:cc:dd:ee:01"]},
			{"vmid": "200", "name": "ct-dns", "type": "lxc", "macs": ["bc:24:11:aa:bb:cc"]}
		]
	}`
	got := parseHostedVMs(meta)
	if len(got) != 2 {
		t.Fatalf("got %d guests, want 2", len(got))
	}
	if got[0].VMID != "101" || got[0].Name != "web01" || got[0].Type != "qemu" {
		t.Fatalf("guest[0] = %+v", got[0])
	}
	if want := []string{"aa:bb:cc:dd:ee:01"}; !reflect.DeepEqual(got[0].MACs, want) {
		t.Fatalf("guest[0].MACs = %v, want %v", got[0].MACs, want)
	}
}

func TestParseHostedVMs_EmptyOrMalformed(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", `{"hosted_vms": null}`, `{"other": 1}`} {
		if got := parseHostedVMs(in); len(got) != 0 {
			t.Fatalf("input %q: got %v, want empty", in, got)
		}
	}
}

func TestGuestPortLabel(t *testing.T) {
	cases := []struct {
		vm   hostedVM
		want string
	}{
		{hostedVM{VMID: "101", Name: "web01", Type: "qemu"}, "VM 101 (web01)"},
		{hostedVM{VMID: "200", Name: "ct-dns", Type: "lxc"}, "CT 200 (ct-dns)"},
		{hostedVM{VMID: "5", Type: "qemu"}, "VM 5"},
		{hostedVM{Type: "lxc"}, "CT"},
	}
	for _, c := range cases {
		if got := guestPortLabel(c.vm); got != c.want {
			t.Errorf("guestPortLabel(%+v) = %q, want %q", c.vm, got, c.want)
		}
	}
}

func TestDerefTenantID(t *testing.T) {
	if got := derefTenantID(nil); got != 0 {
		t.Fatalf("nil tenant = %d, want 0", got)
	}
	v := uint32(7)
	if got := derefTenantID(&v); got != 7 {
		t.Fatalf("tenant = %d, want 7", got)
	}
}
