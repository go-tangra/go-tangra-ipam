package hostplan

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func hwProfile() store.HardwareProfile {
	return store.HardwareProfile{
		Schema:  2,
		BIOS:    store.HardwareBIOS{Vendor: "American Megatrends International, LLC.", Version: "2.5", ReleaseDate: "11/26/2025"},
		System:  store.HardwareSystem{Manufacturer: "Supermicro", Product: "Super Server", Serial: "SYS-1"},
		Board:   store.HardwareBoard{Manufacturer: "Supermicro", Product: "X12DPi-NT6", Serial: "BB-1"},
		Chassis: store.HardwareChassis{Type: "Rack Mount Chassis"},
		Processors: []store.HardwareProcessor{
			{Socket: "CPU1", Model: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz", Cores: 12, Threads: 24, Populated: true},
			{Socket: "CPU2", Model: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz", Cores: 12, Threads: 24, Populated: true},
		},
		Memory: store.HardwareMemory{TotalBytes: 32 << 30, ErrorCorrection: "Single-bit ECC", SlotsTotal: 3, SlotsUsed: 2, Slots: []store.HardwareMemorySlot{
			{Locator: "P1-DIMMA1", Bank: "B0", Populated: true, SizeBytes: 16 << 30, Type: "DDR4", Serial: "M-1"},
			{Locator: "P1-DIMMB1", Bank: "B1", Populated: true, SizeBytes: 16 << 30, Type: "DDR4", Serial: "M-2"},
			{Locator: "P1-DIMMC1", Bank: "B2"},
		}},
		Disks: []store.HardwareDisk{
			{Name: "nvme0n1", Model: "PM9A3", Serial: "S64-1", SizeBytes: 3840755982336, Media: "nvme_ssd", Interface: "nvme"},
			{Name: "sda", Model: "ST4000NM000A", Serial: "ZC1", SizeBytes: 4000787030016, Media: "hdd", Interface: "sata"},
			{Name: "sdb", Model: "Cruzer", SizeBytes: 16 << 30, Media: "unknown", Interface: "usb", Removable: true},
		},
		Filesystems:  []store.HardwareFilesystem{{Mount: "/", FS: "ext4", SizeBytes: 100 << 30, FreeBytes: 40 << 30, Disks: []string{"nvme0n1"}}},
		Availability: store.HardwareAvailability{SMBIOS: "ok", Disks: "ok"},
	}
}

func hwOf(p store.HardwareProfile) *hostreport.Hardware {
	return &hostreport.Hardware{Profile: p, Summary: hostreport.Summarize(p), Digest: hostreport.HardwareDigest(p)}
}

func hwReport(p store.HardwareProfile) hostreport.Report {
	r := report()
	r.Hardware = hwOf(p)
	return r
}

// stored returns the state after the first hardware report was applied.
func storedState(t *testing.T, p store.HardwareProfile) State {
	t.Helper()
	_, st := linked()
	return apply(t, st, Build(st, hwReport(p), params()))
}

func changesOf(t *testing.T, p Plan) []map[string]any {
	t.Helper()
	hw := ops(p, OpReplaceHardware)
	if len(hw) != 1 || len(hw[0].Audit) != 1 || hw[0].Audit[0].Action != "hardware_updated" {
		t.Fatalf("want one hardware_updated op, got %d ops %v", len(hw), actions(p))
	}
	var out []map[string]any
	for _, c := range hw[0].Audit[0].Detail["changes"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func fields(cs []map[string]any) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c["field"].(string))
	}
	return out
}

func TestHardwareFirstReport(t *testing.T) {
	d, st := linked()
	p := Build(st, hwReport(hwProfile()), params())
	hw := ops(p, OpReplaceHardware)
	if len(hw) != 1 {
		t.Fatalf("ops %v", actions(p))
	}
	h := hw[0].Hardware
	if h.DeviceID != d.ID || h.TenantID != tenant || h.Digest != hostreport.HardwareDigest(hwProfile()) || !h.ReportedAt.Equal(report().CollectedAt) ||
		h.Summary.CPUCores != 24 || !h.Summary.ReportedAt.Equal(h.ReportedAt) || h.Profile.BIOS.Version != "2.5" {
		t.Fatalf("hardware %+v", h)
	}
	a := hw[0].Audit
	if len(a) != 1 || a[0].Action != "hardware_reported" || a[0].SubjectKind != "device" || a[0].SubjectID != d.ID {
		t.Fatalf("audit %+v", a)
	}
	sum := a[0].Detail["summary"].(map[string]any)
	if sum["cpu_model"] != "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz" || sum["cpu_cores"] != 24 || sum["memory_total_bytes"] != int64(32<<30) ||
		sum["memory_type"] != "DDR4" || sum["disk_count"] != 2 || sum["disk_total_bytes"] != int64(3840755982336+4000787030016) {
		t.Fatalf("summary %+v", sum)
	}
	if _, ok := a[0].Detail["changes"]; ok {
		t.Fatal("first report lists no field changes")
	}
	// Re-planning the same report on the applied state yields no hardware op.
	st2 := apply(t, st, p)
	if n := len(ops(Build(st2, hwReport(hwProfile()), params()), OpReplaceHardware)); n != 0 {
		t.Fatalf("idempotence: %d ops", n)
	}
}

func TestHardwareCreatedDevice(t *testing.T) {
	p := Build(State{}, hwReport(hwProfile()), params())
	dev := ops(p, OpCreateDevice)[0].Device
	hw := ops(p, OpReplaceHardware)
	if len(hw) != 1 || hw[0].Hardware.DeviceID != dev.ID {
		t.Fatal("hardware of a new device targets the created device")
	}
}

func TestHardwareAbsentLeavesStoredRow(t *testing.T) {
	st := storedState(t, hwProfile())
	if st.Hardware == nil {
		t.Fatal("setup")
	}
	if n := len(ops(Build(st, report(), params()), OpReplaceHardware)); n != 0 {
		t.Fatal("a report without hardware must leave the stored row untouched")
	}
}

func TestHardwareUsageRefreshWithoutAudit(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	p2.Filesystems[0].FreeBytes = 1
	p := Build(st, hwReport(p2), params())
	hw := ops(p, OpReplaceHardware)
	if len(hw) != 1 || len(hw[0].Audit) != 0 || hw[0].Hardware.Profile.Filesystems[0].FreeBytes != 1 {
		t.Fatalf("usage refresh: %+v", hw)
	}
}

func TestHardwareBIOSChange(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	p2.BIOS.Version, p2.BIOS.ReleaseDate = "2.6", "03/01/2026"
	cs := changesOf(t, Build(st, hwReport(p2), params()))
	if got := fields(cs); !reflect.DeepEqual(got, []string{"bios.version", "bios.release_date"}) {
		t.Fatalf("fields %v", got)
	}
	if cs[0]["before"] != "2.5" || cs[0]["after"] != "2.6" || cs[0]["change"] != "changed" {
		t.Fatalf("change %+v", cs[0])
	}
}

func TestHardwareMemorySlots(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	p2.Memory.Slots[2] = store.HardwareMemorySlot{Locator: "P1-DIMMC1", Bank: "B2", Populated: true, SizeBytes: 16 << 30, Type: "DDR4", Serial: "M-3"}
	p2.Memory.Slots[1].Serial = "M-9"
	p2.Memory.Slots = append(p2.Memory.Slots, store.HardwareMemorySlot{Locator: "P1-DIMMD1"})
	p2.Memory.Slots = p2.Memory.Slots[1:] // A1 removed
	p2.Memory.TotalBytes = 48 << 30
	cs := changesOf(t, Build(st, hwReport(p2), params()))
	want := map[string]string{"memory.total_bytes": "changed", "memory.slot[P1-DIMMA1]": "removed", "memory.slot[P1-DIMMB1]": "changed",
		"memory.slot[P1-DIMMC1]": "changed", "memory.slot[P1-DIMMD1]": "added"}
	if len(cs) != len(want) {
		t.Fatalf("changes %v", fields(cs))
	}
	for _, c := range cs {
		if want[c["field"].(string)] != c["change"] {
			t.Errorf("%v", c)
		}
	}
}

func TestHardwareDuplicateLocators(t *testing.T) {
	p := hwProfile()
	p.Memory.Slots = []store.HardwareMemorySlot{{Locator: "DIMM", Bank: "A"}, {Locator: "DIMM", Bank: "B"}, {Locator: "DIMM", Bank: "B"}, {Locator: ""}}
	st := storedState(t, p)
	p2 := p
	p2.Memory.Slots = append([]store.HardwareMemorySlot(nil), p.Memory.Slots...)
	p2.Memory.Slots[1].Populated = true
	cs := changesOf(t, Build(st, hwReport(p2), params()))
	if got := fields(cs); !reflect.DeepEqual(got, []string{"memory.slot[DIMM/B]"}) {
		t.Fatalf("fields %v", got)
	}
}

func TestHardwareDisks(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	p2.Disks[1] = store.HardwareDisk{Name: "sda", Model: "ST4000NM000A", Serial: "ZC2", SizeBytes: 4000787030016, Media: "hdd", Interface: "sata"} // replaced
	p2.Disks[0].SizeBytes = 1                                                                                                                      // changed
	p2.Disks = p2.Disks[:2]                                                                                                                        // USB stick removed (no serial → keyed by name)
	cs := changesOf(t, Build(st, hwReport(p2), params()))
	want := map[string]string{"disk[S64-1]": "changed", "disk[ZC1]": "removed", "disk[ZC2]": "added", "disk[sdb]": "removed"}
	if len(cs) != len(want) {
		t.Fatalf("changes %v", fields(cs))
	}
	for _, c := range cs {
		if want[c["field"].(string)] != c["change"] {
			t.Errorf("%v", c)
		}
	}
}

func TestHardwarePlaceholderSerialsNotKeys(t *testing.T) {
	p := hwProfile()
	p.Disks = []store.HardwareDisk{{Name: "sda", Serial: "To Be Filled By O.E.M."}, {Name: "sdb", Serial: "0000000000"}}
	st := storedState(t, p)
	p2 := p
	p2.Disks = []store.HardwareDisk{{Name: "sda", Serial: "To Be Filled By O.E.M.", SizeBytes: 5}, {Name: "sdb", Serial: "0000000000"}}
	if got := fields(changesOf(t, Build(st, hwReport(p2), params()))); !reflect.DeepEqual(got, []string{"disk[sda]"}) {
		t.Fatalf("fields %v", got)
	}
}

func TestHardwareProcessorsAndScalars(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	p2.Processors[1].Cores = 16
	p2.Processors = append(p2.Processors, store.HardwareProcessor{Socket: "CPU1"}, store.HardwareProcessor{}) // duplicate + unnamed
	p2.System.Serial, p2.Chassis.AssetTag, p2.Board.Serial = "SYS-2", "A2", "BB-2"
	p2.Availability.Disks = "partial"
	p2.Filesystems = append(p2.Filesystems, store.HardwareFilesystem{Mount: "/data", SizeBytes: 5})
	p2.Truncated = map[string]int{"disks": 3}
	p2.Memory.ErrorCorrection = "None"
	got := fields(changesOf(t, Build(st, hwReport(p2), params())))
	want := []string{"system.serial", "board.serial", "chassis.asset_tag", "memory.error_correction", "availability.disks",
		"truncated.disks", "filesystem[/data]", "processor[#4]", "processor[CPU1#2]", "processor[CPU2]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields\n got %v\nwant %v", got, want)
	}
}

func TestHardwareChangesBounded(t *testing.T) {
	st := storedState(t, hwProfile())
	p2 := hwProfile()
	for i := 0; i < MaxHardwareChanges+20; i++ {
		p2.Disks = append(p2.Disks, store.HardwareDisk{Name: fmt.Sprintf("sd%03d", i)})
	}
	hw := ops(Build(st, hwReport(p2), params()), OpReplaceHardware)[0]
	det := hw.Audit[0].Detail
	if n := len(det["changes"].([]any)); n != MaxHardwareChanges || det["changes_truncated"] != 20 {
		t.Fatalf("changes %d truncated %v", n, det["changes_truncated"])
	}
}

// TestHardwareNeverTouchesDeviceColumns: planning with hardware produces the
// same non-hardware ops as planning without it, so no device column
// (firmware_version, serial_number, description, ...) is ever written from
// hardware (FR-008).
func TestHardwareNeverTouchesDeviceColumns(t *testing.T) {
	admin := adminDevice()
	admin.InventoryHostID = hostID
	for _, st := range []State{{Candidates: Candidates{ByHost: &admin}}, {}} {
		without := Build(st, report(), params())
		with := Build(st, hwReport(hwProfile()), params())
		var rest []Op
		for _, o := range with.Ops {
			if o.Kind != OpReplaceHardware {
				rest = append(rest, o)
			}
		}
		if len(rest) != len(without.Ops) {
			t.Fatalf("hardware changed the other ops: %v vs %v", actions(with), actions(without))
		}
		for i := range rest {
			if rest[i].Device != nil && !reflect.DeepEqual(*rest[i].Device, *without.Ops[i].Device) {
				t.Fatalf("device op differs:\n%+v\n%+v", *rest[i].Device, *without.Ops[i].Device)
			}
		}
		assertAdminUntouched(t, admin, with)
	}
}

func TestHardwareReportedAtFallsBackToNow(t *testing.T) {
	_, st := linked()
	r := hwReport(hwProfile())
	r.CollectedAt = t0.Add(-time.Hour)
	if h := ops(Build(st, r, params()), OpReplaceHardware)[0].Hardware; !h.ReportedAt.Equal(r.CollectedAt) {
		t.Fatalf("reported at %v, want the collection time", h.ReportedAt)
	}
	r.CollectedAt = time.Time{}
	if h := ops(Build(st, r, params()), OpReplaceHardware)[0].Hardware; !h.ReportedAt.Equal(t0) {
		t.Fatalf("reported at %v, want now without a collection time", h.ReportedAt)
	}
}

func TestHardwareHelpers(t *testing.T) {
	if sameUsage([]store.HardwareFilesystem{{}}, nil) {
		t.Fatal("different filesystem counts are not the same usage")
	}
	a, b := hwProfile(), hwProfile()
	a.Truncated = map[string]int{"memory_slots": 2}
	if got := fields(hardwareChangesAsList(a, b)); !reflect.DeepEqual(got, []string{"truncated.memory_slots"}) {
		t.Fatalf("fields %v", got)
	}
}

func hardwareChangesAsList(a, b store.HardwareProfile) []map[string]any { return hardwareChanges(a, b) }
