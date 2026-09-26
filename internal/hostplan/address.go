package hostplan

import (
	"sort"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// reportedIface is an interface the report makes the device have.
type reportedIface struct {
	name, mac, kind string
	speed           int
	up              bool
	bmc             bool
	addrs           []hostreport.Address
}

// reportedInterfaces lists the report's interfaces minus the excluded ones
// (loopback always, tenant patterns), then the BMC ports.
func (pl *planner) reportedInterfaces() []reportedIface {
	var out []reportedIface
	taken := map[string]bool{}
	for _, i := range pl.r.Interfaces {
		if hostreport.Excluded(i.Name, i.Kind, pl.p.Exclusions) {
			continue
		}
		taken[strings.ToLower(i.Name)] = true
		out = append(out, reportedIface{name: i.Name, mac: i.MAC, kind: i.Kind, speed: i.SpeedMbps, up: i.Up, addrs: i.Addresses})
	}
	bmc, skipped := bmcInterfaces(pl.r.BMC, taken)
	for n := 0; n < skipped; n++ {
		pl.issue("bmc_interface", "name_taken")
	}
	return append(out, bmc...)
}

// interfaces plans interface creates/updates and marks interfaces the host
// no longer reports (FR-011, FR-014). Administrator fields (description) and
// the SNMP/port-link columns are never planned.
func (pl *planner) interfaces() {
	ifs := pl.reportedInterfaces()
	pl.ifaces = ifs
	existing := map[string]store.DeviceInterface{}
	for _, i := range pl.st.Interfaces {
		existing[i.Name] = i
	}
	seen := map[string]bool{}
	for _, ri := range ifs {
		seen[ri.name] = true
		ex, ok := existing[ri.name]
		if !ok {
			ni := store.DeviceInterface{ID: pl.p.NewID(), TenantID: pl.p.TenantID, DeviceID: pl.dev.ID, Name: ri.name,
				MACAddress: ri.mac, InterfaceType: ri.kind, SpeedMbps: ri.speed, Enabled: ri.up, ReportState: store.RepReported}
			pl.add(Op{Kind: OpCreateInterface, Interface: &ni, Audit: []store.AuditRow{
				pl.row(audit.InterfaceCreated, audit.SubjectInterface, ni.ID, map[string]any{"device_id": pl.dev.ID, "name": ni.Name, "kind": ni.InterfaceType}),
			}})
			continue
		}
		want := ex
		want.MACAddress, want.Enabled, want.ReportState = ri.mac, ri.up, store.RepReported
		setIf(&want.InterfaceType, ri.kind)
		if ri.speed > 0 {
			want.SpeedMbps = ri.speed
		}
		if ch := ifaceChanges(ex, want); len(ch) > 0 {
			pl.add(Op{Kind: OpUpdateInterface, Interface: &want, Audit: []store.AuditRow{
				pl.row(audit.InterfaceUpdated, audit.SubjectInterface, want.ID, map[string]any{"device_id": pl.dev.ID, "changes": ch}),
			}})
		}
	}
	names := make([]string, 0, len(existing))
	for n := range existing {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ex := existing[n]
		if seen[n] || ex.ReportState != store.RepReported {
			continue
		}
		if pl.r.BMC == nil && ex.InterfaceType == store.DevInterfaceKindManagement {
			continue // BMC absent or unreadable: its interfaces stay as they are
		}
		want := ex
		want.ReportState = store.RepNotReported
		pl.add(Op{Kind: OpUpdateInterface, Interface: &want, Audit: []store.AuditRow{
			pl.row(audit.InterfaceNotReported, audit.SubjectInterface, want.ID, map[string]any{"device_id": pl.dev.ID}),
		}})
	}
}

func ifaceChanges(a, b store.DeviceInterface) map[string]any {
	ch := map[string]any{}
	if a.MACAddress != b.MACAddress {
		change(ch, "mac_address", a.MACAddress, b.MACAddress)
	}
	if a.InterfaceType != b.InterfaceType {
		change(ch, "interface_type", a.InterfaceType, b.InterfaceType)
	}
	if a.SpeedMbps != b.SpeedMbps {
		change(ch, "speed_mbps", a.SpeedMbps, b.SpeedMbps)
	}
	if a.Enabled != b.Enabled {
		change(ch, "enabled", a.Enabled, b.Enabled)
	}
	if a.ReportState != b.ReportState {
		change(ch, "report_state", a.ReportState, b.ReportState)
	}
	return ch
}

// addresses records every reported global address (FR-012, FR-013) and
// releases the ones the host no longer reports (FR-014).
func (pl *planner) addresses() {
	pl.subnets = make([]ipnet.Candidate, 0, len(pl.st.Subnets))
	pl.subnetNames = map[string]bool{}
	for _, s := range pl.st.Subnets {
		pl.subnets = append(pl.subnets, ipnet.Candidate{ID: s.ID, CIDR: s.CIDR})
		pl.subnetNames[strings.ToLower(s.Name)] = true
	}
	reported := map[string]bool{}
	for _, ri := range pl.ifaces {
		for _, a := range ri.addrs {
			if !ipnet.Classify(a.Addr).Recordable() || a.Temporary || a.Deprecated {
				continue // loopback, link-local, multicast, IPv6 privacy/deprecated (FR-022)
			}
			key := a.Addr.String()
			if reported[key] {
				continue
			}
			reported[key] = true
			primary := !ri.bmc && (a.Addr == pl.r.PrimaryIPv4 || a.Addr == pl.r.PrimaryIPv6)
			hostname := pl.r.Hostname
			if ri.bmc {
				hostname = ""
			}
			pl.address(a, key, ri, primary, hostname)
		}
	}
	keys := make([]string, 0, len(pl.st.Addresses))
	for k := range pl.st.Addresses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row := pl.st.Addresses[k]
		if reported[k] || row.DeviceID != pl.dev.ID || row.ReportState != store.RepReported {
			continue
		}
		if pl.r.BMC == nil && IsBMCInterfaceName(row.InterfaceName) {
			continue // BMC unreadable this time: keep its address
		}
		want := row
		want.DeviceID, want.InterfaceName, want.IsPrimary = "", "", false
		want.PreviousDeviceID, want.ReportState = pl.dev.ID, store.RepNotReported
		pl.add(Op{Kind: OpUpdateAddress, Address: &want, Audit: []store.AuditRow{
			pl.row(audit.AddressReleased, audit.SubjectAddress, want.ID, map[string]any{"address": want.Address, "previous_device_id": pl.dev.ID}),
		}})
		pl.event(events.IPAddressUpdated, events.ActionReleased, want)
	}
}

// address plans one reported address: create in the most specific subnet,
// move from another device, claim an unowned row or refresh our own row.
func (pl *planner) address(a hostreport.Address, key string, ri reportedIface, primary bool, hostname string) {
	now := pl.p.Now
	seen := timePtr(pl.r.CollectedAt)
	row, ok := pl.st.Addresses[key]
	if !ok {
		na := store.IPAddress{ID: pl.p.NewID(), TenantID: pl.p.TenantID, Address: key, SubnetID: pl.placeSubnet(a),
			Hostname: hostname, MACAddress: ri.mac, DeviceID: pl.dev.ID, InterfaceName: ri.name, Status: store.IPActive,
			AddressType: store.AddrHost, IsPrimary: primary, LastSeen: seen, CreatedBy: audit.HostSyncActor, ReportState: store.RepReported}
		pl.add(Op{Kind: OpCreateAddress, Address: &na, Audit: []store.AuditRow{
			pl.row(audit.AddressCreated, audit.SubjectAddress, na.ID, map[string]any{"address": key, "device_id": pl.dev.ID, "interface": ri.name}),
		}})
		pl.event(events.IPAddressCreated, events.ActionCreated, na)
		return
	}
	want := row
	want.DeviceID, want.InterfaceName, want.MACAddress, want.Hostname = pl.dev.ID, ri.name, ri.mac, hostname
	want.IsPrimary, want.ReportState = primary, store.RepReported
	var rows []store.AuditRow
	action := events.ActionUpdated
	switch {
	case row.DeviceID != "" && row.DeviceID != pl.dev.ID:
		action = events.ActionMoved
		want.PreviousDeviceID, want.MovedAt = row.DeviceID, &now
		if row.MoveWindowStart == nil || now.Sub(*row.MoveWindowStart) > pl.p.ConflictWindow {
			want.MoveWindowStart, want.MoveCount = &now, 1
		} else {
			want.MoveCount = row.MoveCount + 1
		}
		rows = append(rows, pl.row(audit.AddressMoved, audit.SubjectAddress, row.ID, map[string]any{
			"address": key, "previous_device_id": row.DeviceID, "device_id": pl.dev.ID, "move_count": want.MoveCount}))
		if want.MoveCount >= pl.p.ConflictMoves && !row.Conflict {
			want.Conflict = true
			rows = append(rows, pl.row(audit.AddressConflict, audit.SubjectAddress, row.ID, map[string]any{
				"address": key, "move_count": want.MoveCount, "window_hours": int(pl.p.ConflictWindow / time.Hour)}))
		}
	case row.DeviceID == "":
		action = events.ActionClaimed
	}
	if want.Conflict && action != events.ActionMoved && want.MovedAt != nil && now.Sub(*want.MovedAt) > pl.p.ConflictWindow {
		want.Conflict, want.MoveCount, want.MoveWindowStart = false, 0, nil
		rows = append(rows, pl.row(audit.AddressConflictCleared, audit.SubjectAddress, row.ID, map[string]any{"address": key, "by": "window"}))
	}
	ch := addrChanges(row, want)
	if len(ch) == 0 {
		return
	}
	want.LastSeen = seen // refreshed together with a real change only
	if action != events.ActionMoved {
		d := map[string]any{"address": key, "changes": ch}
		if action == events.ActionClaimed {
			d["claimed"] = true
		}
		rows = append([]store.AuditRow{pl.row(audit.AddressUpdated, audit.SubjectAddress, row.ID, d)}, rows...)
	}
	pl.add(Op{Kind: OpUpdateAddress, Address: &want, Audit: rows})
	pl.event(events.IPAddressUpdated, action, want)
}

func addrChanges(a, b store.IPAddress) map[string]any {
	ch := map[string]any{}
	str := []struct{ f, x, y string }{
		{"device_id", a.DeviceID, b.DeviceID}, {"interface_name", a.InterfaceName, b.InterfaceName},
		{"mac_address", a.MACAddress, b.MACAddress}, {"hostname", a.Hostname, b.Hostname},
		{"report_state", a.ReportState, b.ReportState}, {"previous_device_id", a.PreviousDeviceID, b.PreviousDeviceID},
	}
	for _, s := range str {
		if s.x != s.y {
			change(ch, s.f, s.x, s.y)
		}
	}
	if a.IsPrimary != b.IsPrimary {
		change(ch, "is_primary", a.IsPrimary, b.IsPrimary)
	}
	if a.Conflict != b.Conflict {
		change(ch, "conflict", a.Conflict, b.Conflict)
	}
	if a.MoveCount != b.MoveCount {
		change(ch, "move_count", a.MoveCount, b.MoveCount)
	}
	return ch
}

// placeSubnet returns the most specific subnet containing the address, or
// plans a subnet for the reported network (origin host_sync, named after its
// CIDR; " (auto)" on a name collision). No existing subnet can contain the
// new network (it would contain the address), so the new subnet is a root.
func (pl *planner) placeSubnet(a hostreport.Address) string {
	if c, ok := ipnet.MostSpecific(pl.subnets, a.Addr); ok {
		return c.ID
	}
	p := ipnet.AutoPrefix(a.Addr, a.Prefix)
	info, _ := ipnet.Parse(p.String()) // p is a valid prefix
	name := p.String()
	if pl.subnetNames[strings.ToLower(name)] {
		name += " (auto)"
	}
	s := store.Subnet{ID: pl.p.NewID(), TenantID: pl.p.TenantID, Name: name, CIDR: p.String(), Status: store.SubnetActive,
		IPVersion: info.Version, NetworkAddress: info.Network.String(), BroadcastAddr: info.Broadcast.String(),
		Mask: info.Mask.String(), PrefixLength: info.PrefixLen, CreatedBy: audit.HostSyncActor, Origin: store.OriginHostSync}
	pl.subnets = append(pl.subnets, ipnet.Candidate{ID: s.ID, CIDR: s.CIDR})
	pl.subnetNames[strings.ToLower(name)] = true
	pl.add(Op{Kind: OpCreateSubnet, Subnet: &s, Audit: []store.AuditRow{
		pl.row(audit.SubnetCreated, audit.SubjectSubnet, s.ID, map[string]any{"cidr": s.CIDR, "origin": store.OriginHostSync, "parent_id": ""}),
	}})
	return s.ID
}
