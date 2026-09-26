package hostplan

import (
	"net/netip"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// FuzzPlan drives the planner with arbitrary reports and existing state.
// Contract: no panic; administrator fields never change (SC-004); every op
// carries an audit row; the op count is bounded by the report bounds; and
// re-planning the same report on the applied state is empty (idempotence).
func FuzzPlan(f *testing.F) {
	f.Add("web", "aa:bb:cc:00:00:01", "10.0.0.5", 24, "10.0.0.0/24", "SN-1", "vm", true, "bc:24:11:00:00:01", "eth0", 0)
	f.Add("", "", "2001:db8::1", 128, "bad", "", "physical", false, "", "bmc", 2)
	f.Add("localhost", "zz", "169.254.1.1", 16, "0.0.0.0/0", "0", "container", true, "aa:bb:cc:00:00:01", "docker0", 1)
	f.Fuzz(func(t *testing.T, hostname, mac, ip string, prefix int, cidr, serial, role string, bmc bool, gmac, ifname string, matchMode int) {
		r := report()
		r.Hostname, r.Serial, r.VirtRole = hostname, serial, role
		a, err := netip.ParseAddr(ip)
		if err != nil {
			a = netip.MustParseAddr("10.0.0.5")
		}
		if prefix < 0 || prefix > a.BitLen() {
			prefix = a.BitLen()
		}
		if m, ok := hostreport.NormalizeMAC(mac); ok {
			mac = m
		} else {
			mac = ""
		}
		if m, ok := hostreport.NormalizeMAC(gmac); ok {
			gmac = m
		} else {
			gmac = "bc:24:11:00:00:09"
		}
		if ifname == "" || len(ifname) > hostreport.MaxIfaceName {
			ifname = "eth0"
		}
		r.Interfaces = []hostreport.Interface{{Name: ifname, MAC: mac, Kind: hostreport.KindEthernet, Addresses: []hostreport.Address{{Addr: a, Prefix: prefix}}}}
		r.PrimaryIPv4 = netip.Addr{}
		if a.Is4() {
			r.PrimaryIPv4 = a
		}
		if bmc {
			r.BMC = &hostreport.BMC{Address: a, Prefix: prefix, Ports: []hostreport.BMCPort{{MAC: gmac}}}
		}
		r.Guests = []hostreport.Guest{{Ref: "101", Kind: "vm", MACs: []string{gmac}}}
		r.Updates.Status = store.UpdAvailable
		r.Pending = []hostreport.Package{{Name: hostname + "pkg", Available: "2"}}

		admin := adminDevice()
		admin.SerialNumber = serial
		admin.Name = hostname
		st := State{Subnets: []store.Subnet{{ID: "s", Name: cidr, CIDR: cidr}},
			Addresses: map[string]store.IPAddress{a.String(): {ID: "a", Address: a.String(), DeviceID: "other", Note: "n", ReportState: store.RepReported}},
			MACOwners: []repo.MACOwner{{MAC: gmac, DeviceID: "g1"}}}
		switch matchMode % 3 {
		case 1:
			admin.InventoryHostID = hostID
			st.Candidates.ByHost = &admin
		case 2:
			st.Candidates.BySerial = []store.Device{admin}
			st.Candidates.ByName = []store.Device{admin}
		}
		p := Build(st, r, params())
		if len(p.Ops) > 8+2*len(r.Interfaces)+2*len(r.Guests)+4 {
			t.Fatalf("too many ops: %d", len(p.Ops))
		}
		assertAdminUntouched(t, admin, p)
		for _, o := range p.Ops {
			if len(o.Audit) == 0 {
				t.Fatalf("op %s without audit", o.Kind)
			}
			if o.Address != nil && o.Address.Note != "" && o.Address.Note != "n" {
				t.Fatal("address note changed")
			}
		}
		st2 := apply(t, st, p)
		if p2 := Build(st2, r, params()); len(p2.Ops) != 0 {
			t.Fatalf("not idempotent: %v", actions(p2))
		}
	})
}
