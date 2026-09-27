package hostplan

import (
	"reflect"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// FuzzHardwareDiff plans a stored profile against an arbitrarily changed one.
// Contract: no panic; at most one hardware op; a bounded, deterministic change
// list; re-planning after apply yields no hardware op.
func FuzzHardwareDiff(f *testing.F) {
	f.Add("2.5", "P1-DIMMA1", "B0", "S64-1", "sda", "CPU1", "/", uint8(3), int64(1<<30), true)
	f.Add("", "", "", "0", "", "", "", uint8(40), int64(-1), false)
	f.Fuzz(func(t *testing.T, bios, locator, bank, serial, disk, socket, mount string, n uint8, size int64, populated bool) {
		_, st0 := linked()
		base := hwProfile()
		st := apply(t, st0, Build(st0, hwReport(base), params()))
		p := hwProfile()
		p.BIOS.Version = bios
		for i := 0; i < int(n); i++ {
			p.Memory.Slots = append(p.Memory.Slots, store.HardwareMemorySlot{Locator: locator, Bank: bank, Populated: populated, SizeBytes: size})
			p.Disks = append(p.Disks, store.HardwareDisk{Name: disk, Serial: serial, SizeBytes: size})
			p.Processors = append(p.Processors, store.HardwareProcessor{Socket: socket, Cores: i, Populated: populated})
			p.Filesystems = append(p.Filesystems, store.HardwareFilesystem{Mount: mount, SizeBytes: size, FreeBytes: size})
		}
		plan1 := Build(st, hwReport(p), params())
		plan2 := Build(st, hwReport(p), params())
		hw := ops(plan1, OpReplaceHardware)
		if len(hw) > 1 {
			t.Fatalf("%d hardware ops", len(hw))
		}
		if len(hw) == 1 && len(hw[0].Audit) == 1 {
			ch := hw[0].Audit[0].Detail["changes"].([]any)
			if len(ch) > MaxHardwareChanges {
				t.Fatalf("%d changes", len(ch))
			}
			ch2 := ops(plan2, OpReplaceHardware)[0].Audit[0].Detail["changes"]
			if !reflect.DeepEqual(ch, ch2) {
				t.Fatal("change list not deterministic")
			}
		}
		st2 := apply(t, st, plan1)
		if n := len(ops(Build(st2, hwReport(p), params()), OpReplaceHardware)); n != 0 {
			t.Fatalf("not idempotent: %d hardware ops", n)
		}
	})
}
