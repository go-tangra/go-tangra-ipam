package hostplan

import (
	"fmt"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// BMCInterfaceName names the i-th (0-based) BMC LAN port: bmc, bmc-2, bmc-3…
func BMCInterfaceName(i int) string {
	if i == 0 {
		return "bmc"
	}
	return fmt.Sprintf("bmc-%d", i+1)
}

// IsBMCInterfaceName reports whether name is one of the BMC port names.
func IsBMCInterfaceName(name string) bool {
	if name == "bmc" {
		return true
	}
	var n int
	_, err := fmt.Sscanf(name, "bmc-%d", &n)
	return err == nil && n >= 2 && name == BMCInterfaceName(n-1)
}

// bmcInterfaces turns a reported BMC into management interfaces: one per LAN
// port (or a single "bmc" without MAC when only the address is known). The
// BMC address is recorded on the first one with the reported prefix (/24
// when the BMC does not report it). No credential is involved (SR-005).
func bmcInterfaces(b *hostreport.BMC, taken map[string]bool) ([]reportedIface, int) {
	if b == nil {
		return nil, 0
	}
	ports := b.Ports
	if len(ports) == 0 {
		ports = []hostreport.BMCPort{{}}
	}
	var out []reportedIface
	skipped := 0
	for i, p := range ports {
		ri := reportedIface{name: BMCInterfaceName(i), mac: p.MAC, kind: store.DevInterfaceKindManagement, up: true, bmc: true}
		if taken[strings.ToLower(ri.name)] {
			skipped++
			continue
		}
		if i == 0 && b.Address.IsValid() {
			prefix := b.Prefix
			if prefix == 0 {
				prefix = 24
				if b.Address.Is6() {
					prefix = 64
				}
			}
			ri.addrs = []hostreport.Address{{Addr: b.Address, Prefix: prefix}}
		}
		out = append(out, ri)
	}
	return out, skipped
}
