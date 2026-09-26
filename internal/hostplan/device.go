package hostplan

import (
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// deviceType applies the D8 rule: vm/container when reported; physical is a
// server on create and replaces only vm/container on update (a physical host
// an administrator typed workstation, storage... keeps its type); unknown
// (old agents) is a server on create and unchanged on update.
func deviceType(role, current string, create bool) string {
	switch role {
	case hostreport.RoleVM:
		return store.DevVM
	case hostreport.RoleContainer:
		return store.DevContainer
	case hostreport.RolePhysical:
		if create || current == store.DevVM || current == store.DevContainer {
			return store.DevServer
		}
	}
	if create {
		return store.DevServer
	}
	return current
}

func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// device plans the create or the D8-column update of the matched device.
func (pl *planner) device() {
	cur, how := Match(pl.st.Candidates, pl.r)
	pl.plan.Match = how
	r := pl.r
	var d store.Device
	if cur == nil {
		pl.created = true
		d = store.Device{ID: pl.p.NewID(), TenantID: pl.p.TenantID, Status: store.DevStActive,
			CreatedBy: audit.HostSyncActor, DeviceHeightU: 1, UpdateStatus: store.UpdUnknown}
	} else {
		d = *cur
	}
	before := d
	d.Name = desiredName(pl.st.Candidates, r, d.ID)
	d.DeviceType = deviceType(r.VirtRole, d.DeviceType, pl.created)
	switch r.VirtRole {
	case hostreport.RoleVM, hostreport.RoleContainer:
		d.VirtualizationKind = r.VirtKind
	case hostreport.RolePhysical:
		d.VirtualizationKind = ""
	}
	setIf(&d.OSType, r.OSFamily)
	setIf(&d.OSVersion, strings.TrimSpace(r.OSName+" "+r.OSVersion))
	setIf(&d.Manufacturer, r.Manufacturer)
	setIf(&d.Model, r.Model)
	setIf(&d.SerialNumber, r.Serial)
	if r.PrimaryIPv4.IsValid() {
		d.PrimaryIP = r.PrimaryIPv4.String()
	}
	if r.PrimaryIPv6.IsValid() {
		d.PrimaryIPv6 = r.PrimaryIPv6.String()
	}
	if r.BMC != nil && r.BMC.Address.IsValid() {
		d.ManagementIP = r.BMC.Address.String()
	}
	switch r.Updates.RebootRequired {
	case hostreport.TriTrue, hostreport.TriFalse:
		d.RebootRequired = r.Updates.RebootRequired == hostreport.TriTrue
	}
	switch r.Updates.AutomaticUpdates {
	case hostreport.TriTrue, hostreport.TriFalse:
		d.UnattendedUpgrades = r.Updates.AutomaticUpdates == hostreport.TriTrue
	}
	d.UpdateStatus = r.Updates.Status
	if !r.LastSeen.IsZero() {
		d.LastSeen = timePtr(r.LastSeen)
	}
	d.Source, d.InventoryHostID, d.ReportState = store.SrcHostReport, r.HostID, store.RepReported
	d.LastReportAt = timePtr(r.CollectedAt)
	d.ReportDigest = r.Digest
	pl.dev = d
	pl.plan.DeviceID = d.ID

	if pl.created {
		dc := d
		pl.add(Op{Kind: OpCreateDevice, Device: &dc, Audit: []store.AuditRow{
			pl.row(audit.DeviceCreated, audit.SubjectDevice, d.ID, map[string]any{"match": MatchCreated, "name": d.Name}),
		}})
		return
	}
	if ch := deviceChanges(before, d); len(ch) > 0 {
		dc := d
		pl.add(Op{Kind: OpUpdateDevice, Device: &dc, Audit: []store.AuditRow{
			pl.row(audit.DeviceUpdated, audit.SubjectDevice, d.ID, map[string]any{"match": how, "changes": ch}),
		}})
	}
}

func change(ch map[string]any, field string, before, after any) {
	ch[field] = map[string]any{"before": before, "after": after}
}

func fmtTime(t *store.Device, which string) any {
	var p = t.LastSeen
	if which == "last_report_at" {
		p = t.LastReportAt
	}
	if p == nil {
		return nil
	}
	return p.UTC().Format("2006-01-02T15:04:05Z07:00")
}

// deviceChanges lists the D8 fields that differ, with before/after values.
func deviceChanges(a, b store.Device) map[string]any {
	ch := map[string]any{}
	str := []struct {
		f    string
		x, y string
	}{
		{"name", a.Name, b.Name}, {"device_type", a.DeviceType, b.DeviceType},
		{"virtualization_kind", a.VirtualizationKind, b.VirtualizationKind}, {"os_type", a.OSType, b.OSType},
		{"os_version", a.OSVersion, b.OSVersion}, {"manufacturer", a.Manufacturer, b.Manufacturer},
		{"model", a.Model, b.Model}, {"serial_number", a.SerialNumber, b.SerialNumber},
		{"primary_ip", a.PrimaryIP, b.PrimaryIP}, {"primary_ipv6", a.PrimaryIPv6, b.PrimaryIPv6},
		{"management_ip", a.ManagementIP, b.ManagementIP}, {"update_status", a.UpdateStatus, b.UpdateStatus},
		{"source", a.Source, b.Source}, {"inventory_host_id", a.InventoryHostID, b.InventoryHostID},
		{"report_state", a.ReportState, b.ReportState}, {"report_digest", a.ReportDigest, b.ReportDigest},
	}
	for _, s := range str {
		if s.x != s.y {
			change(ch, s.f, s.x, s.y)
		}
	}
	if a.RebootRequired != b.RebootRequired {
		change(ch, "reboot_required", a.RebootRequired, b.RebootRequired)
	}
	if a.UnattendedUpgrades != b.UnattendedUpgrades {
		change(ch, "unattended_upgrades", a.UnattendedUpgrades, b.UnattendedUpgrades)
	}
	if !sameTime(a.LastSeen, b.LastSeen) {
		change(ch, "last_seen", fmtTime(&a, "last_seen"), fmtTime(&b, "last_seen"))
	}
	if !sameTime(a.LastReportAt, b.LastReportAt) {
		change(ch, "last_report_at", fmtTime(&a, "last_report_at"), fmtTime(&b, "last_report_at"))
	}
	return ch
}
