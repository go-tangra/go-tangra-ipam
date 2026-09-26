package hostplan

import (
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestBMCRecorded(t *testing.T) {
	d, st := linked()
	d.IPMISecretRef = "warden-ref"
	st.Candidates.ByHost = &d
	r := report()
	r.BMC = &hostreport.BMC{Address: addrOf("10.9.0.5"), Ports: []hostreport.BMCPort{{Channel: 1, MAC: "aa:bb:cc:00:00:10"}, {Channel: 2, MAC: "aa:bb:cc:00:00:11"}}}
	p := Build(st, r, params())
	dev := ops(p, OpUpdateDevice)[0].Device
	if dev.ManagementIP != "10.9.0.5" || dev.IPMISecretRef != "warden-ref" {
		t.Fatalf("device %+v", dev)
	}
	ifs := map[string]store.DeviceInterface{}
	for _, o := range ops(p, OpCreateInterface) {
		ifs[o.Interface.Name] = *o.Interface
	}
	if ifs["bmc"].InterfaceType != store.DevInterfaceKindManagement || ifs["bmc"].MACAddress != "aa:bb:cc:00:00:10" ||
		ifs["bmc-2"].MACAddress != "aa:bb:cc:00:00:11" {
		t.Fatalf("bmc interfaces %+v", ifs)
	}
	a := createdAddrs(p)["10.9.0.5"]
	if a.InterfaceName != "bmc" || a.IsPrimary || a.Hostname != "" {
		t.Fatalf("bmc address %+v", a)
	}
	var sub *store.Subnet
	for _, o := range ops(p, OpCreateSubnet) {
		if strings.HasPrefix(o.Subnet.CIDR, "10.9.0.") {
			sub = o.Subnet
		}
	}
	if sub == nil || sub.CIDR != "10.9.0.0/24" || a.SubnetID != sub.ID {
		t.Fatalf("missing prefix -> /24 subnet: %+v", sub)
	}
	for _, o := range p.Ops {
		for _, row := range o.Audit {
			for k := range row.Detail {
				if strings.Contains(k, "ipmi") || strings.Contains(k, "secret") {
					t.Fatalf("audit key %q", k)
				}
			}
		}
	}
}

func TestBMCAbsentLeavesManagementUntouched(t *testing.T) {
	d, st := linked()
	d.ManagementIP = "10.9.0.5"
	st.Candidates.ByHost = &d
	st.Interfaces = []store.DeviceInterface{{ID: "b", DeviceID: "d1", Name: "bmc", InterfaceType: store.DevInterfaceKindManagement, ReportState: store.RepReported}}
	p := Build(st, report(), params())
	for _, o := range p.Ops {
		if o.Device != nil && o.Device.ManagementIP != "10.9.0.5" {
			t.Fatal("management_ip changed without a BMC")
		}
		if o.Interface != nil && o.Interface.Name == "bmc" {
			t.Fatal("BMC interface touched")
		}
	}
}

func TestBMCAddressOnlyAndNameTaken(t *testing.T) {
	_, st := linked()
	r := report()
	r.BMC = &hostreport.BMC{Address: addrOf("2001:db8:9::5")}
	a := createdAddrs(Build(st, r, params()))["2001:db8:9::5"]
	if a.InterfaceName != "bmc" {
		t.Fatalf("%+v", a)
	}
	r.Interfaces = append(r.Interfaces, hostreport.Interface{Name: "bmc", Kind: hostreport.KindEthernet})
	p := Build(st, r, params())
	found := false
	for _, i := range p.Issues {
		found = found || (i.Field == "bmc_interface" && i.Reason == "name_taken")
	}
	if !found {
		t.Fatalf("issues %+v", p.Issues)
	}
}

func TestBMCInterfaceNames(t *testing.T) {
	if BMCInterfaceName(0) != "bmc" || BMCInterfaceName(2) != "bmc-3" {
		t.Fatal("names")
	}
	for name, want := range map[string]bool{"bmc": true, "bmc-2": true, "bmc-9": true, "bmc-1": false, "bmc-02": false, "bmcx": false, "eth0": false} {
		if IsBMCInterfaceName(name) != want {
			t.Errorf("IsBMCInterfaceName(%q)", name)
		}
	}
}
