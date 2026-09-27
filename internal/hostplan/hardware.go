package hostplan

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// MaxHardwareChanges bounds the field changes listed in one hardware_updated
// audit row; the rest is counted in changes_truncated.
const MaxHardwareChanges = 100

// hardware plans the device's reported hardware (feature 023, research D6):
// a report without hardware leaves the stored row untouched; the first
// hardware is stored with hardware_reported; a changed digest replaces it
// with one hardware_updated row listing the field changes; an equal digest
// with other filesystem usage refreshes the row without an audit row (usage
// is not a change of the host); otherwise nothing happens. Device columns
// are never written from hardware.
func (pl *planner) hardware() {
	in := pl.r.Hardware
	if in == nil {
		return
	}
	at := pl.r.CollectedAt
	if at.IsZero() {
		at = pl.p.Now
	}
	sum := in.Summary
	sum.ReportedAt = at
	h := store.DeviceHardware{DeviceID: pl.dev.ID, TenantID: pl.p.TenantID, Profile: in.Profile, Digest: in.Digest, Summary: sum, ReportedAt: at}
	cur := pl.st.Hardware
	op := Op{Kind: OpReplaceHardware, DeviceID: pl.dev.ID, Hardware: &h}
	switch {
	case cur == nil:
		op.Audit = []store.AuditRow{pl.row(audit.HardwareReported, audit.SubjectDevice, pl.dev.ID, map[string]any{"summary": summaryDetail(sum)})}
	case cur.Digest != in.Digest:
		changes := hardwareChanges(cur.Profile, in.Profile)
		detail := map[string]any{"summary": summaryDetail(sum)}
		if len(changes) > MaxHardwareChanges {
			detail["changes_truncated"] = len(changes) - MaxHardwareChanges
			changes = changes[:MaxHardwareChanges]
		}
		list := make([]any, len(changes))
		for i, c := range changes {
			list[i] = c
		}
		detail["changes"] = list
		op.Audit = []store.AuditRow{pl.row(audit.HardwareUpdated, audit.SubjectDevice, pl.dev.ID, detail)}
	case sameUsage(cur.Profile.Filesystems, in.Profile.Filesystems):
		return
	}
	pl.add(op)
}

func summaryDetail(s store.HardwareSummary) map[string]any {
	return map[string]any{
		"cpu_model": s.CPUModel, "cpu_cores": s.CPUCores, "memory_total_bytes": s.MemoryTotalBytes,
		"memory_type": s.MemoryType, "disk_count": s.DiskCount, "disk_total_bytes": s.DiskTotalBytes,
	}
}

// sameUsage reports whether the filesystems' free bytes are unchanged.
func sameUsage(a, b []store.HardwareFilesystem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].FreeBytes != b[i].FreeBytes {
			return false
		}
	}
	return true
}

// hardwareChanges lists the field-level differences between two profiles:
// scalar fields in a fixed order, then keyed list entries (disks, filesystems,
// memory slots, processors) sorted by field. Filesystem free bytes are not a
// change.
func hardwareChanges(a, b store.HardwareProfile) []map[string]any {
	var out []map[string]any
	scalar := func(field string, x, y any) {
		if x != y {
			out = append(out, map[string]any{"field": field, "change": "changed", "before": fmt.Sprint(x), "after": fmt.Sprint(y)})
		}
	}
	scalar("bios.vendor", a.BIOS.Vendor, b.BIOS.Vendor)
	scalar("bios.version", a.BIOS.Version, b.BIOS.Version)
	scalar("bios.release_date", a.BIOS.ReleaseDate, b.BIOS.ReleaseDate)
	scalar("system.manufacturer", a.System.Manufacturer, b.System.Manufacturer)
	scalar("system.product", a.System.Product, b.System.Product)
	scalar("system.version", a.System.Version, b.System.Version)
	scalar("system.serial", a.System.Serial, b.System.Serial)
	scalar("system.uuid", a.System.UUID, b.System.UUID)
	scalar("system.sku", a.System.SKU, b.System.SKU)
	scalar("system.family", a.System.Family, b.System.Family)
	scalar("board.manufacturer", a.Board.Manufacturer, b.Board.Manufacturer)
	scalar("board.product", a.Board.Product, b.Board.Product)
	scalar("board.serial", a.Board.Serial, b.Board.Serial)
	scalar("chassis.type", a.Chassis.Type, b.Chassis.Type)
	scalar("chassis.manufacturer", a.Chassis.Manufacturer, b.Chassis.Manufacturer)
	scalar("chassis.serial", a.Chassis.Serial, b.Chassis.Serial)
	scalar("chassis.asset_tag", a.Chassis.AssetTag, b.Chassis.AssetTag)
	scalar("memory.total_bytes", a.Memory.TotalBytes, b.Memory.TotalBytes)
	scalar("memory.error_correction", a.Memory.ErrorCorrection, b.Memory.ErrorCorrection)
	scalar("memory.location", a.Memory.Location, b.Memory.Location)
	scalar("memory.use", a.Memory.Use, b.Memory.Use)
	scalar("memory.max_capacity_bytes", a.Memory.MaxCapacityBytes, b.Memory.MaxCapacityBytes)
	scalar("memory.slots_total", a.Memory.SlotsTotal, b.Memory.SlotsTotal)
	scalar("memory.slots_used", a.Memory.SlotsUsed, b.Memory.SlotsUsed)
	scalar("availability.smbios", a.Availability.SMBIOS, b.Availability.SMBIOS)
	scalar("availability.disks", a.Availability.Disks, b.Availability.Disks)
	scalar("schema", a.Schema, b.Schema)
	keys := map[string]bool{}
	for k := range a.Truncated {
		keys[k] = true
	}
	for k := range b.Truncated {
		keys[k] = true
	}
	for _, k := range sortedSet(keys) {
		scalar("truncated."+k, a.Truncated[k], b.Truncated[k])
	}

	var lists []map[string]any
	lists = append(lists, listChanges("disk", keyed(a.Disks, diskKey), keyed(b.Disks, diskKey), descDisk)...)
	lists = append(lists, listChanges("filesystem", keyed(noFree(a.Filesystems), fsKey), keyed(noFree(b.Filesystems), fsKey), descFS)...)
	lists = append(lists, listChanges("memory.slot", slotKeys(a.Memory.Slots), slotKeys(b.Memory.Slots), descSlot)...)
	lists = append(lists, listChanges("processor", keyed(a.Processors, procKey), keyed(b.Processors, procKey), descProc)...)
	sort.SliceStable(lists, func(i, j int) bool { return lists[i]["field"].(string) < lists[j]["field"].(string) })
	return append(out, lists...)
}

// keyed indexes entries by their key; an empty key becomes "#<position>",
// a repeated key gets "#<occurrence>".
func keyed[T any](in []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(in))
	seen := map[string]int{}
	for i, e := range in {
		k := key(e)
		if k == "" {
			k = "#" + strconv.Itoa(i+1)
		}
		seen[k]++
		if n := seen[k]; n > 1 {
			k += "#" + strconv.Itoa(n)
		}
		out[k] = e
	}
	return out
}

// slotKeys keys memory slots by locator, or locator/bank for a locator that
// appears more than once.
func slotKeys(in []store.HardwareMemorySlot) map[string]store.HardwareMemorySlot {
	count := map[string]int{}
	for _, s := range in {
		count[s.Locator]++
	}
	return keyed(in, func(s store.HardwareMemorySlot) string {
		if s.Locator != "" && count[s.Locator] > 1 {
			return s.Locator + "/" + s.Bank
		}
		return s.Locator
	})
}

// diskKey is the disk's serial, or its name when the serial is empty or a
// placeholder (kept as data, never used for matching).
func diskKey(d store.HardwareDisk) string {
	if d.Serial != "" && !IsPlaceholderSerial(d.Serial) {
		return d.Serial
	}
	return d.Name
}

func fsKey(f store.HardwareFilesystem) string  { return f.Mount }
func procKey(p store.HardwareProcessor) string { return p.Socket }
func noFree(in []store.HardwareFilesystem) []store.HardwareFilesystem {
	out := make([]store.HardwareFilesystem, len(in))
	for i, f := range in {
		f.FreeBytes = 0
		out[i] = f
	}
	return out
}

func listChanges[T any](kind string, a, b map[string]T, desc func(T) string) []map[string]any {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var out []map[string]any
	for _, k := range sortedSet(keys) {
		x, inA := a[k]
		y, inB := b[k]
		field := kind + "[" + k + "]"
		switch {
		case !inA:
			out = append(out, map[string]any{"field": field, "change": "added", "after": desc(y)})
		case !inB:
			out = append(out, map[string]any{"field": field, "change": "removed", "before": desc(x)})
		case !reflect.DeepEqual(x, y):
			out = append(out, map[string]any{"field": field, "change": "changed", "before": desc(x), "after": desc(y)})
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// join lists the non-empty parts of a description.
func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" && p != "0" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func descDisk(d store.HardwareDisk) string {
	rem := ""
	if d.Removable {
		rem = "removable"
	}
	return join(d.Name, d.Model, d.Vendor, d.Serial, strconv.FormatInt(d.SizeBytes, 10), d.Media, d.Interface, rem)
}

func descFS(f store.HardwareFilesystem) string {
	return join(f.FS, strconv.FormatInt(f.SizeBytes, 10), strings.Join(f.Disks, ","))
}

func descSlot(s store.HardwareMemorySlot) string {
	if !s.Populated {
		return join("empty", s.Bank)
	}
	return join(s.Bank, strconv.FormatInt(s.SizeBytes, 10), s.Type, s.FormFactor, strings.Join(s.TypeDetail, ","),
		strconv.Itoa(s.SpeedMTs), strconv.Itoa(s.ConfiguredMTs), s.Manufacturer, s.PartNumber, s.Serial)
}

func descProc(p store.HardwareProcessor) string {
	state := "empty"
	if p.Populated {
		state = "populated"
	}
	return join(p.Model, p.Family, p.Manufacturer, strconv.Itoa(p.Cores)+"c", strconv.Itoa(p.Threads)+"t",
		strconv.Itoa(p.MaxMHz), strconv.Itoa(p.CurrentMHz), state)
}
