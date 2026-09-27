package hostreport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
)

// node1Hardware is a node-1-like profile (2× Xeon Silver 4310, 16 slots of
// which two are listed here, NVMe + SATA HDD + USB stick).
func node1Hardware() *invclient.Hardware {
	arr := invclient.MemoryArray{Location: "System board or motherboard", Use: "System memory", ErrorCorrection: "Single-bit ECC",
		MaximumCapacity: 12 << 40, NumberOfDevices: 16, Handle: 0x1000}
	return &invclient.Hardware{
		Schema: 2,
		BIOS:   invclient.BIOSInfo{Vendor: "American Megatrends International, LLC.", Version: "2.5", ReleaseDate: "11/26/2025"},
		System: invclient.SystemInfo{Manufacturer: "Supermicro", ProductName: "Super Server", Version: "0123456789",
			SerialNumber: "SYS-SN-1", UUID: "u-1", SKUNumber: "sku", Family: "fam"},
		Board:   invclient.BaseboardInfo{Manufacturer: "Supermicro", Product: "X12DPi-NT6", SerialNumber: "BB-1"},
		Chassis: invclient.ChassisInfo{Manufacturer: "Supermicro", Type: "Rack Mount Chassis", SerialNumber: "CH-1", AssetTag: "A1"},
		Processors: []invclient.Processor{
			{SocketDesignation: "CPU1", Manufacturer: "Intel(R) Corporation", Version: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz",
				MaxSpeedMHz: 4000, CurrentSpeedMHz: 2100, CoreCount: 12, ThreadCount: 24, SocketPopulated: true, Family: "Xeon"},
			{SocketDesignation: "CPU2", Manufacturer: "Intel(R) Corporation", Version: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz",
				MaxSpeedMHz: 4000, CurrentSpeedMHz: 2100, CoreCount: 12, ThreadCount: 24, SocketPopulated: true, Family: "Xeon"},
		},
		Memory: invclient.MemoryInfo{TotalPhysicalBytes: 32 << 30, Array: arr, Arrays: []invclient.MemoryArray{arr}, SlotsTotal: 3, SlotsPopulated: 2,
			Modules: []invclient.MemoryModule{
				{DeviceLocator: "P1-DIMMA1", BankLocator: "P0_Node0_Channel0_Dimm0", CapacityBytes: 16 << 30, FormFactor: "DIMM", MemoryType: "DDR4",
					SpeedMTs: 3200, ConfiguredSpeedMTs: 2666, Manufacturer: "Samsung", PartNumber: "M393A2K43EB3-CWE", SerialNumber: "M-1",
					Populated: true, TypeDetail: []string{"Synchronous", "Registered (Buffered)"}},
				{DeviceLocator: "P1-DIMMB1", BankLocator: "P0_Node0_Channel1_Dimm0", CapacityBytes: 16 << 30, FormFactor: "DIMM", MemoryType: "DDR4",
					SpeedMTs: 3200, ConfiguredSpeedMTs: 2666, Manufacturer: "Samsung", PartNumber: "M393A2K43EB3-CWE", SerialNumber: "M-2", Populated: true},
				{DeviceLocator: "P1-DIMMC1", BankLocator: "P0_Node0_Channel2_Dimm0"},
			}},
		Disks: []invclient.Disk{
			{Name: "nvme0n1", Model: "SAMSUNG MZQL23T8HCLS", Vendor: "Samsung", Serial: "S64-1", SizeBytes: 3840755982336, MediaType: "nvme_ssd", Interface: "nvme"},
			{Name: "sda", Model: "ST4000NM000A", Serial: "ZC1", SizeBytes: 4000787030016, MediaType: "hdd", Interface: "sata"},
			{Name: "sdb", Model: "Cruzer", SizeBytes: 16 << 30, MediaType: "unknown", Interface: "usb", Removable: true},
		},
		Filesystems:  []invclient.Filesystem{{Mount: "/", FS: "ext4", Device: "/dev/mapper/vg-root", SizeBytes: 100 << 30, FreeBytes: 40 << 30, Disks: []string{"nvme0n1"}}},
		Availability: invclient.HardwareAvailability{SMBIOS: "ok", Disks: "ok"},
	}
}

func withHardware(hw *invclient.Hardware) invclient.Report {
	r := valid()
	r.Hardware = hw
	return r
}

func TestNormalizeHardwareValid(t *testing.T) {
	r, err := Normalize(withHardware(node1Hardware()), tenantA)
	if err != nil {
		t.Fatal(err)
	}
	hw := r.Hardware
	if hw == nil {
		t.Fatal("hardware missing")
	}
	p := hw.Profile
	if p.BIOS.Version != "2.5" || p.BIOS.ReleaseDate != "11/26/2025" || p.System.Product != "Super Server" || p.System.Serial != "SYS-SN-1" ||
		p.Board.Product != "X12DPi-NT6" || p.Chassis.Type != "Rack Mount Chassis" || p.Schema != 2 {
		t.Fatalf("identity parts %+v", p)
	}
	if len(p.Processors) != 2 || p.Processors[0].Model != "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz" || p.Processors[0].Cores != 12 ||
		p.Processors[0].Threads != 24 || !p.Processors[0].Populated || p.Processors[0].Socket != "CPU1" || p.Processors[1].MaxMHz != 4000 {
		t.Fatalf("processors %+v", p.Processors)
	}
	m := p.Memory
	if m.TotalBytes != 32<<30 || m.ErrorCorrection != "Single-bit ECC" || m.Location != "System board or motherboard" || m.Use != "System memory" ||
		m.MaxCapacityBytes != 12<<40 || m.SlotsTotal != 3 || m.SlotsUsed != 2 || len(m.Slots) != 3 {
		t.Fatalf("memory %+v", m)
	}
	if s := m.Slots[0]; s.Locator != "P1-DIMMA1" || s.Type != "DDR4" || s.FormFactor != "DIMM" || s.ConfiguredMTs != 2666 ||
		len(s.TypeDetail) != 2 || !s.Populated || s.SizeBytes != 16<<30 {
		t.Fatalf("slot %+v", s)
	}
	if m.Slots[2].Populated {
		t.Fatal("empty slot")
	}
	if len(p.Disks) != 3 || p.Disks[0].Media != MediaNVMeSSD || p.Disks[0].Interface != IfNVMe || !p.Disks[2].Removable || p.Disks[0].Vendor != "Samsung" {
		t.Fatalf("disks %+v", p.Disks)
	}
	if len(p.Filesystems) != 1 || p.Filesystems[0].Disks[0] != "nvme0n1" || p.Filesystems[0].FreeBytes != 40<<30 {
		t.Fatalf("filesystems %+v", p.Filesystems)
	}
	if p.Availability.SMBIOS != AvailOK || p.Truncated != nil {
		t.Fatalf("availability/truncated %+v %v", p.Availability, p.Truncated)
	}
	s := hw.Summary
	if s.CPUModel != "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz" || s.CPUSockets != 2 || s.CPUCores != 24 || s.CPUThreads != 48 ||
		s.MemoryTotalBytes != 32<<30 || s.MemoryType != "DDR4" || s.MemorySlotsTotal != 3 || s.MemorySlotsUsed != 2 ||
		s.DiskCount != 2 || s.DiskTotalBytes != 3840755982336+4000787030016 {
		t.Fatalf("summary %+v", s)
	}
	if len(hw.Digest) != 64 {
		t.Fatalf("digest %q", hw.Digest)
	}
}

func TestNormalizeHardwareAbsentOrLegacy(t *testing.T) {
	r, err := Normalize(valid(), tenantA)
	if err != nil || r.Hardware != nil {
		t.Fatalf("report without hardware: %+v %v", r.Hardware, err)
	}
	hw := node1Hardware()
	hw.Schema = 1
	r, _ = Normalize(withHardware(hw), tenantA)
	if r.Hardware != nil || issue(t, r, "hardware", "legacy_schema") != 1 {
		t.Fatalf("legacy schema hardware must be ignored: %+v %+v", r.Hardware, r.Issues)
	}
}

func TestNormalizeHardwareDigest(t *testing.T) {
	a, _ := Normalize(withHardware(node1Hardware()), tenantA)
	b, _ := Normalize(withHardware(node1Hardware()), tenantA)
	if a.Hardware.Digest != b.Hardware.Digest || a.Hardware.Digest != HardwareDigest(a.Hardware.Profile) {
		t.Fatal("digest not deterministic")
	}
	hw := node1Hardware()
	hw.Filesystems[0].FreeBytes = 1
	c, _ := Normalize(withHardware(hw), tenantA)
	if c.Hardware.Digest != a.Hardware.Digest {
		t.Fatal("free bytes must not change the digest")
	}
	hw = node1Hardware()
	hw.BIOS.Version = "2.6"
	d, _ := Normalize(withHardware(hw), tenantA)
	if d.Hardware.Digest == a.Hardware.Digest {
		t.Fatal("BIOS change must change the digest")
	}
}

func TestNormalizeHardwareBounds(t *testing.T) {
	hw := node1Hardware()
	hw.Processors, hw.Memory.Modules, hw.Disks, hw.Filesystems = nil, nil, nil, nil
	for i := 0; i < MaxHWProcessors+1; i++ {
		hw.Processors = append(hw.Processors, invclient.Processor{SocketDesignation: fmt.Sprint("CPU", i)})
	}
	for i := 0; i < MaxHWSlots+1; i++ {
		hw.Memory.Modules = append(hw.Memory.Modules, invclient.MemoryModule{DeviceLocator: fmt.Sprint("D", i)})
	}
	for i := 0; i < MaxHWDisks+1; i++ {
		hw.Disks = append(hw.Disks, invclient.Disk{Name: fmt.Sprint("sd", i)})
	}
	for i := 0; i < MaxHWFilesystems+1; i++ {
		hw.Filesystems = append(hw.Filesystems, invclient.Filesystem{Mount: fmt.Sprint("/m", i)})
	}
	for i := 0; i < MaxHWFSDisks+1; i++ {
		hw.Filesystems[0].Disks = append(hw.Filesystems[0].Disks, fmt.Sprint("sd", i))
	}
	r := valid()
	r.Hardware = hw
	r.Truncated.Disks, r.Truncated.MemorySlots, r.Truncated.MemoryArrays = 5, 6, 7
	r.Truncated.Processors, r.Truncated.Filesystems = 8, 9
	out, err := Normalize(r, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	p := out.Hardware.Profile
	if len(p.Processors) != MaxHWProcessors || len(p.Memory.Slots) != MaxHWSlots || len(p.Disks) != MaxHWDisks ||
		len(p.Filesystems) != MaxHWFilesystems || len(p.Filesystems[0].Disks) != MaxHWFSDisks {
		t.Fatal("bounds")
	}
	for list, want := range map[string]int{"processors": 1 + 8, "memory_slots": 1 + 6, "disks": 1 + 5, "filesystems": 1 + 9, "filesystem_disks": 1, "memory_arrays": 7} {
		if got := issue(t, out, "hardware_"+list, "truncated"); got != want {
			t.Errorf("hardware_%s truncated = %d, want %d", list, got, want)
		}
		if list != "filesystem_disks" && p.Truncated[list] != want {
			t.Errorf("profile truncated[%s] = %d, want %d", list, p.Truncated[list], want)
		}
	}
}

func TestNormalizeHardwareStrings(t *testing.T) {
	hw := node1Hardware()
	hw.BIOS.Vendor = "American\x00 Megatrends\n"
	hw.BIOS.Version = strings.Repeat("é", 200)
	hw.System.Family = string([]byte{'a', 0xff, 'b'})
	hw.Disks[0].MediaType, hw.Disks[0].Interface = "flash", "thunderbolt"
	hw.Disks[1].MediaType, hw.Disks[1].Interface = "", ""
	hw.Availability = invclient.HardwareAvailability{SMBIOS: "great", Disks: ""}
	hw.Memory.Modules[0].TypeDetail = []string{"Synchronous\x01", "", "Registered (Buffered)"}
	hw.Filesystems[0].Disks = []string{"nvme0n1\n", ""}
	out, _ := Normalize(withHardware(hw), tenantA)
	p := out.Hardware.Profile
	if p.BIOS.Vendor != "American Megatrends" || len(p.BIOS.Version) > MaxHWString || p.System.Family != "ab" {
		t.Fatalf("strings %q %d %q", p.BIOS.Vendor, len(p.BIOS.Version), p.System.Family)
	}
	if p.Disks[0].Media != MediaUnknown || p.Disks[0].Interface != IfOther || p.Disks[1].Media != MediaUnknown || p.Disks[1].Interface != IfOther {
		t.Fatalf("closed sets %+v", p.Disks[:2])
	}
	if p.Availability.SMBIOS != AvailUnknown || p.Availability.Disks != "" {
		t.Fatalf("availability %+v", p.Availability)
	}
	if td := p.Memory.Slots[0].TypeDetail; len(td) != 2 || td[0] != "Synchronous" {
		t.Fatalf("type detail %q", td)
	}
	if fd := p.Filesystems[0].Disks; len(fd) != 1 || fd[0] != "nvme0n1" {
		t.Fatalf("fs disks %q", fd)
	}
	if issue(t, out, "hardware_text", "cleaned") == 0 || issue(t, out, "hardware_disk_media", "unknown_value") != 1 ||
		issue(t, out, "hardware_disk_interface", "unknown_value") != 1 || issue(t, out, "hardware_availability", "unknown_value") != 1 {
		t.Fatalf("issues %+v", out.Issues)
	}
}

func TestNormalizeHardwareTooLarge(t *testing.T) {
	hw := node1Hardware()
	hw.Memory.Modules = nil
	long := strings.Repeat("x", MaxHWString)
	for i := 0; i < MaxHWSlots; i++ {
		hw.Memory.Modules = append(hw.Memory.Modules, invclient.MemoryModule{DeviceLocator: fmt.Sprint(i, long), BankLocator: long, PartNumber: long})
	}
	out, err := Normalize(withHardware(hw), tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if out.Hardware != nil || issue(t, out, "hardware", "too_large") != 1 {
		t.Fatalf("oversized profile must be dropped with an issue: %v", out.Issues)
	}
}

func TestNormalizeHardwareSummaryFallbacks(t *testing.T) {
	hw := node1Hardware()
	hw.Memory.TotalPhysicalBytes = 0
	hw.Processors[0].Version, hw.Processors[0].SocketPopulated = "", true
	hw.Processors[1].SocketPopulated = false
	hw.Memory.Modules[1].MemoryType = "DDR5"
	hw.Memory.Modules = append(hw.Memory.Modules, invclient.MemoryModule{DeviceLocator: "X", MemoryType: "DDR5", Populated: true, CapacityBytes: 1},
		invclient.MemoryModule{DeviceLocator: "Y", MemoryType: "DDR4", Populated: true, CapacityBytes: 1})
	out, _ := Normalize(withHardware(hw), tenantA)
	s := out.Hardware.Summary
	if s.CPUModel != "Xeon" || s.CPUSockets != 1 || s.MemoryTotalBytes != 32<<30+2 {
		t.Fatalf("fallbacks %+v", s)
	}
	// Tie between DDR4 (2) and DDR5 (2): the alphabetically first wins.
	if s.MemoryType != "DDR4" {
		t.Fatalf("memory type %q", s.MemoryType)
	}
	hw = node1Hardware()
	hw.Processors = nil
	hw.Memory = invclient.MemoryInfo{}
	out, _ = Normalize(withHardware(hw), tenantA)
	if s := out.Hardware.Summary; s.CPUModel != "" || s.CPUSockets != 0 || s.MemoryType != "" || s.MemoryTotalBytes != 0 {
		t.Fatalf("empty summary %+v", s)
	}
}

func TestNormalizeHardwareClampsHugeSizes(t *testing.T) {
	hw := node1Hardware()
	hw.Disks[0].SizeBytes = 1 << 63
	hw.Memory.TotalPhysicalBytes = 1<<64 - 1
	out, _ := Normalize(withHardware(hw), tenantA)
	if out.Hardware.Profile.Disks[0].SizeBytes <= 0 || out.Hardware.Profile.Memory.TotalBytes <= 0 {
		t.Fatalf("sizes must be clamped into int64: %+v", out.Hardware.Profile.Disks[0])
	}
	if _, err := json.Marshal(out.Hardware.Profile); err != nil {
		t.Fatal(err)
	}
}

func TestCleanHWRuneBoundary(t *testing.T) {
	s := cleanHW("a" + strings.Repeat("é", 200)) // byte 256 falls inside a rune
	if len(s) != 255 || !strings.HasPrefix(s, "aé") {
		t.Fatalf("clip = %d bytes", len(s))
	}
}
