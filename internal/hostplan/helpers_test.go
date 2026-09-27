package hostplan

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

const (
	tenant  = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	hostID  = "0190f7c2-aaaa-7c1a-9b2e-aaaaaaaaaaaa"
	hostID2 = "0190f7c2-bbbb-7c1a-9b2e-bbbbbbbbbbbb"
	digest1 = "1111111111111111111111111111111111111111111111111111111111111111"
)

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func ids() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id-%04d", n) }
}

func params() Params {
	return Params{TenantID: tenant, RunID: "run-1", Trigger: "poll", Now: t0, Exclusions: hostreport.DefaultExclusions,
		ConflictMoves: 3, ConflictWindow: 24 * time.Hour, NewID: ids()}
}

func addr(s string, prefix int) hostreport.Address {
	return hostreport.Address{Addr: netip.MustParseAddr(s), Prefix: prefix}
}

func report() hostreport.Report {
	return hostreport.Report{
		TenantID: tenant, HostID: hostID, Hostname: "web-01", Serial: "SN-1", Manufacturer: "Dell", Model: "R650",
		OSName: "Ubuntu", OSVersion: "24.04", OSFamily: "linux", HostStatus: "active",
		LastSeen: t0.Add(-time.Minute), SnapshotID: "snap-1", Digest: digest1, CollectedAt: t0.Add(-2 * time.Minute),
		Interfaces: []hostreport.Interface{
			{Name: "eth0", MAC: "aa:bb:cc:00:00:01", Kind: hostreport.KindEthernet, SpeedMbps: 1000, Up: true,
				Addresses: []hostreport.Address{addr("10.0.0.5", 24), addr("2001:db8::5", 64), addr("fe80::1", 64)}},
			{Name: "lo", Kind: hostreport.KindLoopback, Addresses: []hostreport.Address{addr("127.0.0.1", 8)}},
			{Name: "docker0", Kind: hostreport.KindBridge, MAC: "02:42:00:00:00:01", Addresses: []hostreport.Address{addr("172.17.0.1", 16)}},
		},
		PrimaryIPv4: netip.MustParseAddr("10.0.0.5"),
		VirtRole:    hostreport.RolePhysical,
		Updates:     hostreport.Updates{Status: store.UpdUnknown, RebootRequired: hostreport.TriUnknown, AutomaticUpdates: hostreport.TriUnknown},
	}
}

func ops(p Plan, k Kind) []Op {
	var out []Op
	for _, o := range p.Ops {
		if o.Kind == k {
			out = append(out, o)
		}
	}
	return out
}

func actions(p Plan) []string {
	var out []string
	for _, o := range p.Ops {
		for _, a := range o.Audit {
			out = append(out, a.Action)
		}
	}
	return out
}

func hasAction(p Plan, a string) bool { return slices.Contains(actions(p), a) }

// apply simulates the store executing a plan on the loaded state, so a test
// can re-plan against the result (idempotence).
func apply(t *testing.T, st State, p Plan) State {
	t.Helper()
	if st.Addresses == nil {
		st.Addresses = map[string]store.IPAddress{}
	}
	var self *store.Device
	for _, o := range p.Ops {
		switch o.Kind {
		case OpCreateDevice, OpUpdateDevice:
			d := *o.Device
			if self != nil {
				d.HypervisorDeviceID = self.HypervisorDeviceID
			} else if st.Candidates.ByHost != nil {
				d.HypervisorDeviceID = st.Candidates.ByHost.HypervisorDeviceID
			}
			self = &d
			st.Candidates.ByHost = self
		case OpCreateInterface:
			st.Interfaces = append(st.Interfaces, *o.Interface)
		case OpUpdateInterface:
			for i := range st.Interfaces {
				if st.Interfaces[i].ID == o.Interface.ID {
					st.Interfaces[i] = *o.Interface
				}
			}
		case OpCreateSubnet:
			st.Subnets = append(st.Subnets, *o.Subnet)
		case OpCreateAddress, OpUpdateAddress:
			st.Addresses[o.Address.Address] = *o.Address
		case OpReplacePackages:
			st.Packages = slices.Clone(o.Packages)
		case OpReplaceGuests:
			st.Guests = slices.Clone(o.Guests)
		case OpReplaceHardware:
			h := *o.Hardware
			st.Hardware = &h
		case OpSetHypervisor:
			for i := range st.MACOwners {
				if st.MACOwners[i].DeviceID == o.DeviceID {
					st.MACOwners[i].HypervisorDeviceID = o.RefID
				}
			}
			if st.Candidates.ByHost != nil && st.Candidates.ByHost.ID == o.DeviceID {
				st.Candidates.ByHost.HypervisorDeviceID = o.RefID
			}
			st.GuestDevices = slices.DeleteFunc(st.GuestDevices, func(d store.Device) bool { return d.ID == o.DeviceID })
			if o.RefID != "" && st.Candidates.ByHost != nil && o.RefID == st.Candidates.ByHost.ID {
				st.GuestDevices = append(st.GuestDevices, store.Device{ID: o.DeviceID, HypervisorDeviceID: o.RefID})
			}
		case OpSetGuestDevice:
			for i := range st.GuestRows {
				if st.GuestRows[i].ID == o.GuestRowID {
					st.GuestRows[i].GuestDeviceID = o.RefID
				}
			}
		default:
			t.Fatalf("unknown op %s", o.Kind)
		}
	}
	return st
}

var _ = repo.MACOwner{}

func addrOf(s string) netip.Addr { return netip.MustParseAddr(s) }
