package hostreport

import (
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
)

// FuzzNormalizeHardware feeds arbitrary strings and sizes into every
// hardware field. Contract: no panic, output within bounds, every string
// valid UTF-8 without control characters and at most MaxHWString bytes, the
// encoded profile within MaxHWProfileBytes.
func FuzzNormalizeHardware(f *testing.F) {
	f.Add("AMI", "2.5", "DDR4", "DIMM", "sda", "S64", "nvme_ssd", "nvme", "ok", "/", uint64(1<<40), uint32(3200), uint8(3))
	f.Add("\x00\xff", "", "", "", "", "", "flash", "tb", "great", "", uint64(1<<63), uint32(0), uint8(255))
	f.Fuzz(func(t *testing.T, vendor, version, mtype, ff, disk, serial, media, iface, avail, mount string, size uint64, speed uint32, n uint8) {
		hw := &invclient.Hardware{
			Schema:       2,
			BIOS:         invclient.BIOSInfo{Vendor: vendor, Version: version, ReleaseDate: version},
			System:       invclient.SystemInfo{Manufacturer: vendor, SerialNumber: serial},
			Memory:       invclient.MemoryInfo{TotalPhysicalBytes: size, Array: invclient.MemoryArray{Location: vendor, MaximumCapacity: size}},
			Availability: invclient.HardwareAvailability{SMBIOS: avail, Disks: avail},
		}
		for i := 0; i < int(n); i++ {
			hw.Processors = append(hw.Processors, invclient.Processor{SocketDesignation: disk, Version: version, CoreCount: speed, ThreadCount: speed, SocketPopulated: i%2 == 0})
			hw.Memory.Modules = append(hw.Memory.Modules, invclient.MemoryModule{DeviceLocator: disk, MemoryType: mtype, FormFactor: ff,
				CapacityBytes: size, SpeedMTs: speed, Populated: i%3 == 0, TypeDetail: []string{mtype, ff}})
			hw.Disks = append(hw.Disks, invclient.Disk{Name: disk, Serial: serial, SizeBytes: size, MediaType: media, Interface: iface, Removable: i%4 == 0})
			hw.Filesystems = append(hw.Filesystems, invclient.Filesystem{Mount: mount, SizeBytes: size, FreeBytes: size, Disks: []string{disk, serial}})
		}
		r := valid()
		r.Hardware = hw
		out, err := Normalize(r, tenantA)
		if err != nil {
			t.Fatal(err)
		}
		h := out.Hardware
		if h == nil {
			return // dropped as too large (with an issue)
		}
		p := h.Profile
		if len(p.Processors) > MaxHWProcessors || len(p.Memory.Slots) > MaxHWSlots || len(p.Disks) > MaxHWDisks || len(p.Filesystems) > MaxHWFilesystems {
			t.Fatal("bounds")
		}
		b, _ := json.Marshal(p)
		if len(b) > MaxHWProfileBytes || len(h.Digest) != 64 || h.Digest != HardwareDigest(p) {
			t.Fatalf("profile %d bytes, digest %q", len(b), h.Digest)
		}
		check := func(s string) {
			if len(s) > MaxHWString || !utf8.ValidString(s) {
				t.Fatalf("string %q", s)
			}
			for _, r := range s {
				if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
					t.Fatalf("control character in %q", s)
				}
			}
		}
		check(p.BIOS.Vendor)
		check(p.System.Serial)
		for _, d := range p.Disks {
			check(d.Name)
			check(d.Serial)
			if !oneOf(d.Media, MediaSSD, MediaHDD, MediaNVMeSSD, MediaUnknown) {
				t.Fatalf("media %q", d.Media)
			}
		}
		for _, s := range p.Memory.Slots {
			check(s.Locator)
			for _, td := range s.TypeDetail {
				check(td)
			}
		}
		if h.Summary.CPUCores < 0 || h.Summary.MemoryTotalBytes < 0 || h.Summary.DiskTotalBytes < 0 {
			t.Fatalf("negative summary %+v", h.Summary)
		}
	})
}
