package hostreport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Hardware bounds (feature 023, research D5): the same bounds as inventory,
// applied again before anything is stored.
const (
	MaxHWProcessors   = 256
	MaxHWSlots        = 1024
	MaxHWDisks        = 256
	MaxHWFilesystems  = 1024
	MaxHWFSDisks      = 64
	MaxHWTypeDetail   = 16
	MaxHWString       = 256
	MaxHWProfileBytes = 256 << 10 // encoded (compact JSON) profile
	hardwareSchema    = 2
)

// Disk media, interfaces and availability (closed sets).
const (
	MediaSSD     = "ssd"
	MediaHDD     = "hdd"
	MediaNVMeSSD = "nvme_ssd"
	MediaUnknown = "unknown"

	IfNVMe   = "nvme"
	IfSATA   = "sata"
	IfSAS    = "sas"
	IfSCSI   = "scsi"
	IfUSB    = "usb"
	IfVirtio = "virtio"
	IfHyperV = "hyperv"
	IfXen    = "xen"
	IfMMC    = "mmc"
	IfOther  = "other"

	AvailOK          = "ok"
	AvailPartial     = "partial"
	AvailUnavailable = "unavailable"
	AvailUnsupported = "unsupported"
	AvailUnknown     = "unknown"
)

var (
	mediaSet = []string{MediaSSD, MediaHDD, MediaNVMeSSD, MediaUnknown}
	ifaceSet = []string{IfNVMe, IfSATA, IfSAS, IfSCSI, IfUSB, IfVirtio, IfHyperV, IfXen, IfMMC, IfOther}
	availSet = []string{AvailOK, AvailPartial, AvailUnavailable, AvailUnsupported, AvailUnknown}
)

// Hardware is the validated hardware of a report: the normalised profile,
// its device summary and its digest (sha256 of the canonical JSON of the
// profile without filesystem free bytes, which move on every report).
type Hardware struct {
	Profile store.HardwareProfile
	Summary store.HardwareSummary
	Digest  string
}

// HardwareDigest is the change signal of a profile: the hex sha256 of its
// canonical JSON encoding with every filesystem's free bytes cleared.
func HardwareDigest(p store.HardwareProfile) string {
	c := p
	c.Filesystems = make([]store.HardwareFilesystem, len(p.Filesystems))
	for i, f := range p.Filesystems {
		f.FreeBytes = 0
		c.Filesystems[i] = f
	}
	b, _ := json.Marshal(c) // plain structs, slices and a string-keyed map always encode
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// normalizeHardware validates the report's hardware section. A missing
// section, or one from a legacy decoding schema, yields nil (the stored
// hardware stays untouched); lists above their bound keep the first N;
// strings are cleaned (valid UTF-8, no control characters, at most
// MaxHWString bytes); closed sets fall back to unknown/other; a profile
// encoding above MaxHWProfileBytes is dropped. Every correction is an issue.
func normalizeHardware(in *invclient.Hardware, lim invclient.Limits, is issues) *Hardware {
	if in == nil {
		return nil
	}
	if in.Schema < hardwareSchema {
		is.add("hardware", "legacy_schema", 1)
		return nil
	}
	h := hwNormalizer{is: is, trunc: map[string]int{}}
	p := store.HardwareProfile{
		Schema: hardwareSchema,
		BIOS:   store.HardwareBIOS{Vendor: h.text(in.BIOS.Vendor), Version: h.text(in.BIOS.Version), ReleaseDate: h.text(in.BIOS.ReleaseDate)},
		System: store.HardwareSystem{Manufacturer: h.text(in.System.Manufacturer), Product: h.text(in.System.ProductName),
			Version: h.text(in.System.Version), Serial: h.text(in.System.SerialNumber), UUID: h.text(in.System.UUID),
			SKU: h.text(in.System.SKUNumber), Family: h.text(in.System.Family)},
		Board: store.HardwareBoard{Manufacturer: h.text(in.Board.Manufacturer), Product: h.text(in.Board.Product),
			Serial: h.text(in.Board.SerialNumber)},
		Chassis: store.HardwareChassis{Type: h.text(in.Chassis.Type), Manufacturer: h.text(in.Chassis.Manufacturer),
			Serial: h.text(in.Chassis.SerialNumber), AssetTag: h.text(in.Chassis.AssetTag)},
		Availability: store.HardwareAvailability{
			SMBIOS: h.closed("hardware_availability", in.Availability.SMBIOS, AvailUnknown, "", availSet),
			Disks:  h.closed("hardware_availability", in.Availability.Disks, AvailUnknown, "", availSet),
		},
	}

	procs := h.bound("processors", len(in.Processors), MaxHWProcessors, lim.Processors)
	p.Processors = make([]store.HardwareProcessor, 0, procs)
	for _, c := range in.Processors[:procs] {
		p.Processors = append(p.Processors, store.HardwareProcessor{
			Socket: h.text(c.SocketDesignation), Manufacturer: h.text(c.Manufacturer), Model: h.text(c.Version),
			Family: h.text(c.Family), MaxMHz: int(c.MaxSpeedMHz), CurrentMHz: int(c.CurrentSpeedMHz),
			Cores: int(c.CoreCount), Threads: int(c.ThreadCount), Populated: c.SocketPopulated,
		})
	}

	m := in.Memory
	h.bound("memory_arrays", 0, 0, lim.MemoryArrays)
	p.Memory = store.HardwareMemory{
		TotalBytes: clampI64(m.TotalPhysicalBytes), ErrorCorrection: h.text(m.Array.ErrorCorrection),
		Location: h.text(m.Array.Location), Use: h.text(m.Array.Use), MaxCapacityBytes: clampI64(m.Array.MaximumCapacity),
	}
	slots := h.bound("memory_slots", len(m.Modules), MaxHWSlots, lim.MemorySlots)
	p.Memory.Slots = make([]store.HardwareMemorySlot, 0, slots)
	used := 0
	for _, mod := range m.Modules[:slots] {
		if mod.Populated {
			used++
		}
		p.Memory.Slots = append(p.Memory.Slots, store.HardwareMemorySlot{
			Locator: h.text(mod.DeviceLocator), Bank: h.text(mod.BankLocator), Populated: mod.Populated,
			SizeBytes: clampI64(mod.CapacityBytes), Type: h.text(mod.MemoryType), FormFactor: h.text(mod.FormFactor),
			TypeDetail: h.list(mod.TypeDetail, MaxHWTypeDetail, ""), SpeedMTs: int(mod.SpeedMTs),
			ConfiguredMTs: int(mod.ConfiguredSpeedMTs), Manufacturer: h.text(mod.Manufacturer),
			PartNumber: h.text(mod.PartNumber), Serial: h.text(mod.SerialNumber),
		})
	}
	p.Memory.SlotsTotal, p.Memory.SlotsUsed = int(m.SlotsTotal), int(m.SlotsPopulated)
	if p.Memory.SlotsTotal == 0 {
		p.Memory.SlotsTotal, p.Memory.SlotsUsed = len(p.Memory.Slots), used
	}

	disks := h.bound("disks", len(in.Disks), MaxHWDisks, lim.Disks)
	p.Disks = make([]store.HardwareDisk, 0, disks)
	for _, d := range in.Disks[:disks] {
		p.Disks = append(p.Disks, store.HardwareDisk{
			Name: h.text(d.Name), Model: h.text(d.Model), Vendor: h.text(d.Vendor), Serial: h.text(d.Serial),
			SizeBytes: clampI64(d.SizeBytes), Removable: d.Removable,
			Media:     h.closed("hardware_disk_media", d.MediaType, MediaUnknown, MediaUnknown, mediaSet),
			Interface: h.closed("hardware_disk_interface", d.Interface, IfOther, IfOther, ifaceSet),
		})
	}

	fss := h.bound("filesystems", len(in.Filesystems), MaxHWFilesystems, lim.Filesystems)
	p.Filesystems = make([]store.HardwareFilesystem, 0, fss)
	for _, f := range in.Filesystems[:fss] {
		p.Filesystems = append(p.Filesystems, store.HardwareFilesystem{
			Mount: h.text(f.Mount), FS: h.text(f.FS), SizeBytes: clampI64(f.SizeBytes), FreeBytes: clampI64(f.FreeBytes),
			Disks: h.list(f.Disks, MaxHWFSDisks, "hardware_filesystem_disks"),
		})
	}
	if len(h.trunc) > 0 {
		p.Truncated = h.trunc
	}

	if b, _ := json.Marshal(p); len(b) > MaxHWProfileBytes {
		is.add("hardware", "too_large", 1)
		return nil
	}
	return &Hardware{Profile: p, Summary: Summarize(p), Digest: HardwareDigest(p)}
}

// Summarize derives the device summary from a normalised profile (reported
// time excluded): CPU from the populated sockets, memory total (reported, or
// the sum of the populated slots) with the most common type, and the
// physical, non-removable disks.
func Summarize(p store.HardwareProfile) store.HardwareSummary {
	s := store.HardwareSummary{MemoryTotalBytes: p.Memory.TotalBytes, MemorySlotsTotal: p.Memory.SlotsTotal, MemorySlotsUsed: p.Memory.SlotsUsed}
	for _, c := range p.Processors {
		if !c.Populated {
			continue
		}
		if s.CPUSockets == 0 {
			s.CPUModel = c.Model
			if s.CPUModel == "" {
				s.CPUModel = c.Family
			}
		}
		s.CPUSockets++
		s.CPUCores += c.Cores
		s.CPUThreads += c.Threads
	}
	types := map[string]int{}
	var sum int64
	for _, sl := range p.Memory.Slots {
		if !sl.Populated {
			continue
		}
		sum = addSat(sum, sl.SizeBytes)
		if sl.Type != "" {
			types[sl.Type]++
		}
	}
	if s.MemoryTotalBytes == 0 {
		s.MemoryTotalBytes = sum
	}
	best := 0
	for t, n := range types {
		if n > best || (n == best && t < s.MemoryType) {
			s.MemoryType, best = t, n
		}
	}
	for _, d := range p.Disks {
		if d.Removable {
			continue
		}
		s.DiskCount++
		s.DiskTotalBytes = addSat(s.DiskTotalBytes, d.SizeBytes)
	}
	return s
}

type hwNormalizer struct {
	is    issues
	trunc map[string]int
}

// bound returns how many of n entries fit max, recording the dropped ones
// and inventory's own counter under the list name.
func (h hwNormalizer) bound(list string, n, max int, reported uint32) int {
	keep, dropped := n, 0
	if n > max {
		keep, dropped = max, n-max
	}
	if total := dropped + int(reported); total > 0 {
		h.trunc[list] += total
		h.is.add("hardware_"+list, "truncated", total)
	}
	return keep
}

// text cleans one reported string.
func (h hwNormalizer) text(s string) string {
	out := cleanHW(s)
	h.is.add("hardware_text", "cleaned", boolInt(out != strings.TrimSpace(s)))
	return out
}

// list cleans every entry, drops empty ones and keeps at most max; dropped
// entries are an issue under field (when set). Empty yields nil.
func (h hwNormalizer) list(in []string, max int, field string) []string {
	var out []string
	dropped := 0
	for _, s := range in {
		if v := h.text(s); v != "" {
			if len(out) == max {
				dropped++
				continue
			}
			out = append(out, v)
		}
	}
	if field != "" {
		h.is.add(field, "truncated", dropped)
	}
	return out
}

// closed maps v into set: "" becomes empty, a value outside the set bad
// (with an issue).
func (h hwNormalizer) closed(field, v, bad, empty string, set []string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "":
		return empty
	case oneOf(v, set...):
		return v
	}
	h.is.add(field, "unknown_value", 1)
	return bad
}

// cleanHW returns s as trimmed, valid UTF-8 without control characters and
// at most MaxHWString bytes (cut at a rune boundary).
func cleanHW(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) <= MaxHWString {
		return s
	}
	n := MaxHWString
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

func clampI64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v) // #nosec G115 -- bounded above
}

func addSat(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
