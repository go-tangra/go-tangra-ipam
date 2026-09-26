package hostplan

import (
	"slices"
	"sort"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// guests replaces the hypervisor's guest rows and links each guest whose MACs
// identify exactly one other device (FR-017, research D13); guests no longer
// reported are unlinked. A device is never its own hypervisor.
func (pl *planner) guests() {
	if len(pl.r.Guests) == 0 && len(pl.st.Guests) == 0 && len(pl.st.GuestDevices) == 0 {
		return
	}
	owners := map[string]map[string]string{} // mac -> device id -> its hypervisor
	for _, o := range pl.st.MACOwners {
		if o.DeviceID == pl.dev.ID {
			continue
		}
		if owners[o.MAC] == nil {
			owners[o.MAC] = map[string]string{}
		}
		owners[o.MAC][o.DeviceID] = o.HypervisorDeviceID
	}
	existing := map[string]store.HypervisorGuest{}
	for _, g := range pl.st.Guests {
		existing[g.GuestRef] = g
	}
	var want []store.HypervisorGuest
	linked := map[string]bool{}
	for _, g := range pl.r.Guests {
		devs := map[string]string{}
		for _, m := range g.MACs {
			for id, hv := range owners[m] {
				devs[id] = hv
			}
		}
		row := store.HypervisorGuest{TenantID: pl.p.TenantID, HostDeviceID: pl.dev.ID, GuestRef: g.Ref, Name: g.Name,
			Kind: g.Kind, Platform: g.Platform, MACs: slices.Clone(g.MACs), LastReportedAt: pl.p.Now}
		if ex, ok := existing[g.Ref]; ok {
			row.ID, row.LastReportedAt = ex.ID, ex.LastReportedAt
		} else {
			row.ID = pl.p.NewID()
		}
		switch len(devs) {
		case 0:
		case 1:
			for id, hv := range devs {
				row.GuestDeviceID = id
				if !linked[id] && hv != pl.dev.ID {
					pl.add(Op{Kind: OpSetHypervisor, DeviceID: id, RefID: pl.dev.ID, Audit: []store.AuditRow{
						pl.row(audit.HypervisorLinked, audit.SubjectDevice, id, map[string]any{"hypervisor_device_id": pl.dev.ID, "guest_ref": g.Ref}),
					}})
				}
				linked[id] = true
			}
		default:
			pl.issue("guest_mac", "duplicate_mac")
		}
		want = append(want, row)
	}
	for _, gd := range pl.st.GuestDevices {
		if !linked[gd.ID] {
			pl.add(Op{Kind: OpSetHypervisor, DeviceID: gd.ID, RefID: "", Audit: []store.AuditRow{
				pl.row(audit.HypervisorUnlinked, audit.SubjectDevice, gd.ID, map[string]any{"hypervisor_device_id": pl.dev.ID}),
			}})
		}
	}
	if guestsEqual(pl.st.Guests, want) {
		return
	}
	for i := range want {
		want[i].LastReportedAt = pl.p.Now
	}
	pl.add(Op{Kind: OpReplaceGuests, DeviceID: pl.dev.ID, Guests: want, Audit: []store.AuditRow{
		pl.row(audit.DeviceUpdated, audit.SubjectDevice, pl.dev.ID, map[string]any{"changes": map[string]any{
			"guests": map[string]any{"before": guestRefs(pl.st.Guests), "after": guestRefs(want)}}}),
	}})
}

func guestRefs(gs []store.HypervisorGuest) []any {
	refs := make([]string, len(gs))
	for i, g := range gs {
		refs[i] = g.GuestRef
	}
	sort.Strings(refs)
	return boundNames(refs)
}

func guestsEqual(a, b []store.HypervisorGuest) bool {
	if len(a) != len(b) {
		return false
	}
	byRef := map[string]store.HypervisorGuest{}
	for _, g := range a {
		byRef[g.GuestRef] = g
	}
	for _, g := range b {
		o, ok := byRef[g.GuestRef]
		if !ok || o.Name != g.Name || o.Kind != g.Kind || o.Platform != g.Platform ||
			o.GuestDeviceID != g.GuestDeviceID || !slices.Equal(o.MACs, g.MACs) {
			return false
		}
	}
	return true
}

// ownHypervisor links this device to the host that lists one of its MACs as a
// guest (US3 scenario 3: the guest's own report, whatever the report order).
func (pl *planner) ownHypervisor() {
	macs := map[string]bool{}
	for _, ri := range pl.ifaces {
		if ri.mac != "" {
			macs[ri.mac] = true
		}
	}
	hosts := map[string][]store.HypervisorGuest{}
	for _, g := range pl.st.GuestRows {
		if g.HostDeviceID == pl.dev.ID {
			continue
		}
		for _, m := range g.MACs {
			if macs[m] {
				hosts[g.HostDeviceID] = append(hosts[g.HostDeviceID], g)
				break
			}
		}
	}
	if len(hosts) > 1 {
		pl.issue("guest_mac", "duplicate_mac")
		return
	}
	for host, rows := range hosts {
		if pl.dev.HypervisorDeviceID != host {
			pl.add(Op{Kind: OpSetHypervisor, DeviceID: pl.dev.ID, RefID: host, Audit: []store.AuditRow{
				pl.row(audit.HypervisorLinked, audit.SubjectDevice, pl.dev.ID, map[string]any{"hypervisor_device_id": host, "guest_ref": rows[0].GuestRef}),
			}})
		}
		for _, g := range rows {
			if g.GuestDeviceID != pl.dev.ID {
				pl.add(Op{Kind: OpSetGuestDevice, GuestRowID: g.ID, RefID: pl.dev.ID, DeviceID: host, Audit: []store.AuditRow{
					pl.row(audit.DeviceUpdated, audit.SubjectDevice, host, map[string]any{"changes": map[string]any{
						"guest_device_id": map[string]any{"before": g.GuestDeviceID, "after": pl.dev.ID}}, "guest_ref": g.GuestRef}),
				}})
			}
		}
	}
}
