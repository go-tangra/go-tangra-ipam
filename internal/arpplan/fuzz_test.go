package arpplan

import (
	"fmt"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// FuzzPlan builds random observations against random addresses: the planner
// never panics, never plans a MAC write (fill/update) on an address whose
// MAC came from the agent or a user (SC-003), never plans a create for an
// address that exists, and never touches another tenant's address.
func FuzzPlan(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, uint8(8))
	f.Add([]byte{0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}, uint8(2))
	f.Add([]byte("\x05\x01\x02\x05\x01\x02\x05\x01\x02"), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, threshold uint8) {
		sources := []string{"", store.MACSourceManual, store.MACSourceAgent, store.MACSourceARP}
		var addrs []store.IPAddress
		byID := map[string]store.IPAddress{}
		for i := 0; i+2 < len(data) && i < 60; i += 3 {
			a := store.IPAddress{ID: fmt.Sprintf("a%d", i), TenantID: "t1", Address: fmt.Sprintf("10.0.0.%d", data[i]%8),
				MACSource: sources[int(data[i+1])%4]}
			if data[i+2]%3 != 0 {
				a.MACAddress = fmt.Sprintf("02:00:00:00:00:%02x", data[i+2]%6)
			}
			if data[i+1]%5 == 0 {
				a.TenantID = "t2"
			}
			dup := false
			for _, o := range addrs {
				dup = dup || (o.Address == a.Address && o.TenantID == a.TenantID)
			}
			if !dup {
				addrs = append(addrs, a)
				byID[a.ID] = a
			}
		}
		var obs []Observation
		for i := 0; i+1 < len(data); i += 2 {
			dev := fmt.Sprintf("r%d", data[i]%3)
			e := snmp.ARPEntry{IP: fmt.Sprintf("10.0.%d.%d", data[i]%2, data[i]%8), MAC: fmt.Sprintf("%02x:00:00:00:00:%02x", data[i+1]%4*2, data[i+1]%6)}
			if len(obs) == 0 || obs[len(obs)-1].DeviceID != dev {
				obs = append(obs, Observation{DeviceID: dev})
			}
			obs[len(obs)-1].Entries = append(obs[len(obs)-1].Entries, e)
		}
		p := Build(Input{TenantID: "t1", Observations: obs, Addresses: addrs, ProxyThreshold: int(threshold),
			Subnets: []store.Subnet{{ID: "s1", TenantID: "t1", CIDR: "10.0.0.0/24"}, {ID: "s2", TenantID: "t2", CIDR: "10.0.1.0/24"}},
			NetworkMACs: map[string]bool{"02:00:00:00:00:05": true}})
		existing := map[string]bool{}
		for _, a := range addrs {
			if a.TenantID == "t1" {
				existing[a.Address] = true
			}
		}
		for _, op := range p.Ops {
			if op.Kind == store.ARPCreate {
				if existing[op.Address] || op.SubnetID != "s1" {
					t.Fatalf("create for %s in %s", op.Address, op.SubnetID)
				}
				continue
			}
			a, ok := byID[op.AddressID]
			if !ok || a.TenantID != "t1" {
				t.Fatalf("op on unknown or foreign address %+v", op)
			}
			if (op.Kind == store.ARPFill || op.Kind == store.ARPUpdate) && a.MACAddress != "" && a.MACSource != store.MACSourceARP {
				t.Fatalf("SC-003: %s would overwrite a %q MAC", op.Kind, a.MACSource)
			}
			if op.MAC == "02:00:00:00:00:05" {
				t.Fatal("network-device MAC applied")
			}
		}
	})
}
