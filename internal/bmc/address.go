package bmc

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostplan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// bmcPort returns the 0-based index of a BMC interface name (bmc → 0,
// bmc-2 → 1, …) or -1 for any other interface.
func bmcPort(name string) int {
	if !hostplan.IsBMCInterfaceName(name) {
		return -1
	}
	if name == "bmc" {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(name, "bmc-"))
	return n - 1
}

// Address picks the BMC address of dev: its management IP, else the address
// the inventory agent reported on its first BMC interface (bmc, then bmc-2, …;
// IPv4 before IPv6; addresses no longer reported are skipped). The host's
// primary IP is never used: it is the operating system, not the BMC.
func Address(dev store.Device, addrs []store.IPAddress) (addr, source string) {
	if dev.ManagementIP != "" {
		return dev.ManagementIP, SourceManagementIP
	}
	best, bestRank := "", -1
	for _, a := range addrs {
		port := bmcPort(a.InterfaceName)
		if a.DeviceID != dev.ID || port < 0 || a.ReportState == store.RepNotReported {
			continue
		}
		rank := port * 2
		if ip := net.ParseIP(a.Address); ip == nil || ip.To4() == nil {
			rank++
		}
		if bestRank < 0 || rank < bestRank {
			best, bestRank = a.Address, rank
		}
	}
	if best == "" {
		return "", ""
	}
	return best, SourceReported
}

// IPMICreds maps warden credentials to IPMI-over-LAN credentials. The
// secret's host URL may select the session protocol and port:
// lanplus://host[:port] (IPMI 2.0), lan://host[:port] (1.5) or
// ipmi://host[:port] (auto); anything else keeps auto negotiation on 623.
func IPMICreds(c warden.Credentials) ipmi.Creds {
	out := ipmi.Creds{Username: c.Username, Password: c.Password}
	u, err := url.Parse(c.HostURL)
	if err != nil {
		return out
	}
	switch u.Scheme {
	case "lanplus":
		out.Protocol = "2.0"
	case "lan":
		out.Protocol = "1.5"
	case "ipmi":
	default:
		return out
	}
	if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 && p <= 65535 {
		out.Port = p
	}
	return out
}
