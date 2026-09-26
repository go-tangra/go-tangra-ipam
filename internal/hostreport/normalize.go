package hostreport

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Bounds applied to a report (data-model §4).
const (
	MaxInterfaces       = 256
	MaxAddrsPerIface    = 64
	MaxAddrs            = 1024
	MaxGuests           = 1000
	MaxGuestMACs        = 32
	MaxPackages         = 5000
	MaxBMCPorts         = 8
	MaxHostname         = 253
	MaxIfaceName        = 64
	MaxGuestName        = 128
	MaxPackageName      = 256
	MaxVersion          = 128
	MaxText             = 128
	MaxIssues           = 50
	MaxSpeedBps         = 1_000_000_000_000 // 1 Tbit/s
	maxSnapshotIDLength = 64
)

// Interface kinds (closed set).
const (
	KindEthernet = "ethernet"
	KindWireless = "wireless"
	KindBond     = "bond"
	KindBridge   = "bridge"
	KindVLAN     = "vlan"
	KindVirtual  = "virtual"
	KindLoopback = "loopback"
	KindOther    = "other"
)

// Virtualization roles and tristate values.
const (
	RolePhysical  = "physical"
	RoleVM        = "vm"
	RoleContainer = "container"
	RoleUnknown   = "unknown"

	TriUnknown = "unknown"
	TriTrue    = "true"
	TriFalse   = "false"
)

// Errors that reject a whole report.
var (
	ErrTenant = errors.New("hostreport: report tenant does not match")
	ErrHostID = errors.New("hostreport: host id is not a uuid")
)

// Address is one validated interface address.
type Address struct {
	Addr       netip.Addr
	Prefix     int
	DHCP       bool
	Temporary  bool
	Deprecated bool
}

// Interface is one validated interface.
type Interface struct {
	Name      string
	MAC       string
	Kind      string // "" = not reported (old agents)
	SpeedMbps int
	Up        bool
	Addresses []Address
}

// BMCPort is one validated BMC LAN channel.
type BMCPort struct {
	Channel int
	MAC     string
}

// BMC is the validated out-of-band controller configuration.
type BMC struct {
	Address netip.Addr // zero when not configured
	Prefix  int        // 0 = not reported
	Gateway netip.Addr
	Ports   []BMCPort
}

// Guest is one validated hypervisor guest.
type Guest struct {
	Ref      string
	Name     string
	Kind     string
	Platform string
	MACs     []string
}

// Updates is the validated update state.
type Updates struct {
	Manager            string
	Status             string // store.Upd*
	RebootRequired     string // tristate
	AutomaticUpdates   string // tristate
	SecurityClassified bool
	CheckedAt          time.Time
}

// Package is one validated pending update.
type Package struct {
	Name      string
	Installed string
	Available string
	Security  bool
}

// Report is a validated, bounded host report.
type Report struct {
	TenantID     string
	HostID       string
	Hostname     string
	Serial       string
	Manufacturer string
	Model        string
	OSName       string
	OSVersion    string
	OSFamily     string
	HostStatus   string
	LastSeen     time.Time
	SnapshotID   string
	Digest       string
	AgentVersion string
	CollectedAt  time.Time
	ChangedAt    time.Time
	Interfaces   []Interface
	PrimaryIPv4  netip.Addr
	PrimaryIPv6  netip.Addr
	VirtRole     string
	VirtKind     string
	BMC          *BMC
	Guests       []Guest
	Updates      Updates
	Pending      []Package
	Issues       []store.HostSyncIssue
}

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	guestRefRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	virtKindRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	digestRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// IsUUID reports whether s is a textual uuid.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

type issues map[[2]string]int

func (is issues) add(field, reason string, n int) {
	if n > 0 {
		is[[2]string{field, reason}] += n
	}
}

func (is issues) list() []store.HostSyncIssue {
	out := make([]store.HostSyncIssue, 0, len(is))
	for k, n := range is {
		out = append(out, store.HostSyncIssue{Field: k[0], Reason: k[1], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > MaxIssues {
		out = out[:MaxIssues]
	}
	return out
}

// cleanText trims s and accepts it when it is valid UTF-8 of printable
// characters within max bytes. ok=false carries the reason.
func cleanText(s string, max int) (string, string) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", "invalid_utf8"
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", "control_characters"
		}
	}
	if len(s) > max {
		return "", "too_long"
	}
	return s, ""
}

// text validates an optional free-text field; an invalid value becomes "".
func (is issues) text(field, s string, max int) string {
	v, reason := cleanText(s, max)
	is.add(field, reason, boolInt(reason != ""))
	return v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NormalizeMAC returns the canonical lower-case colon form of a unicast,
// non-zero 6-byte MAC, or ok=false.
func NormalizeMAC(s string) (string, bool) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil || len(hw) != 6 || hw[0]&1 == 1 {
		return "", false // unparsable, EUI-64/InfiniBand, multicast or broadcast
	}
	zero := true
	for _, b := range hw {
		zero = zero && b == 0
	}
	if zero {
		return "", false
	}
	return hw.String(), true
}

// mac validates an optional MAC field.
func (is issues) mac(field, s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	m, ok := NormalizeMAC(s)
	is.add(field, "invalid_mac", boolInt(!ok))
	return m
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// closed returns v when it is in set; "" stays def silently, anything else
// becomes def with an issue.
func (is issues) closed(field, v, def string, set ...string) string {
	if oneOf(v, set...) {
		return v
	}
	is.add(field, "unknown_value", boolInt(v != ""))
	return def
}

func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.WithZone(""), true
}

// Normalize validates a report fetched for tenantID. It rejects the report
// when it belongs to another tenant or its host id is not a uuid; every other
// problem skips the offending entry and is returned as an issue.
func Normalize(r invclient.Report, tenantID string) (Report, error) {
	if !IsUUID(tenantID) || !strings.EqualFold(r.TenantID, tenantID) {
		return Report{}, ErrTenant
	}
	if !IsUUID(r.Host.ID) {
		return Report{}, ErrHostID
	}
	is := issues{}
	out := Report{
		TenantID:    tenantID,
		HostID:      strings.ToLower(r.Host.ID),
		LastSeen:    r.Host.LastSeen,
		CollectedAt: r.CollectedAt,
		ChangedAt:   r.ChangedAt,
	}
	host, reason := cleanText(r.Host.Hostname, MaxHostname)
	is.add("hostname", reason, boolInt(reason != ""))
	out.Hostname = host
	out.Serial = is.text("system_serial", r.Host.SystemSerial, MaxText)
	out.Manufacturer = is.text("manufacturer", r.Host.Manufacturer, MaxText)
	out.Model = is.text("model", r.Host.Model, MaxText)
	out.OSName = is.text("os_name", r.Host.OSName, MaxText)
	out.OSVersion = is.text("os_version", r.Host.OSVersion, MaxText)
	out.AgentVersion = is.text("agent_version", r.AgentVersion, MaxText)
	out.SnapshotID = is.text("snapshot_id", r.SnapshotID, maxSnapshotIDLength)
	out.HostStatus = is.closed("host_status", r.Host.Status, "", "active", "stale", "retired")
	out.OSFamily = is.closed("os_family", r.OSFamily, "", "linux", "windows")
	if digestRe.MatchString(r.Digest) {
		out.Digest = r.Digest
	} else {
		is.add("report_digest", "invalid", boolInt(r.Digest != ""))
	}

	out.Interfaces = normalizeInterfaces(r.Interfaces, is)
	out.PrimaryIPv4 = primary(r.PrimaryIPv4, true, is)
	out.PrimaryIPv6 = primary(r.PrimaryIPv6, false, is)

	out.VirtRole = is.closed("virtualization_role", r.Virtualization.Role, RoleUnknown, RolePhysical, RoleVM, RoleContainer, RoleUnknown)
	if k := strings.ToLower(strings.TrimSpace(r.Virtualization.Kind)); virtKindRe.MatchString(k) {
		out.VirtKind = k
	} else {
		is.add("virtualization_kind", "invalid", boolInt(k != ""))
	}

	out.BMC = normalizeBMC(r.BMC, is)
	out.Guests = normalizeGuests(r.Guests, is)
	out.Updates = Updates{
		Manager:            is.closed("package_manager", r.Updates.PackageManager, "", "apt", "dnf", "yum", "apk", "pacman"),
		Status:             is.closed("update_status", r.Updates.Status, store.UpdUnknown, store.UpdUnknown, store.UpdUpToDate, store.UpdAvailable, store.UpdUnsupported, store.UpdError),
		RebootRequired:     is.closed("reboot_required", r.Updates.RebootRequired, TriUnknown, TriUnknown, TriTrue, TriFalse),
		AutomaticUpdates:   is.closed("automatic_updates", r.Updates.AutomaticUpdates, TriUnknown, TriUnknown, TriTrue, TriFalse),
		SecurityClassified: r.Updates.SecurityClassified,
		CheckedAt:          r.Updates.CheckedAt,
	}
	out.Pending = normalizePackages(r.PendingUpdates, is)

	is.add("interfaces", "truncated", int(r.Truncated.Interfaces))
	is.add("addresses", "truncated", int(r.Truncated.Addresses))
	is.add("guests", "truncated", int(r.Truncated.Guests))
	is.add("packages", "truncated", int(r.Truncated.Packages))
	is.add("bmc_ports", "truncated", int(r.Truncated.BMCPorts))
	out.Issues = is.list()
	return out, nil
}

func primary(s string, v4 bool, is issues) netip.Addr {
	if strings.TrimSpace(s) == "" {
		return netip.Addr{}
	}
	a, ok := parseAddr(s)
	if !ok || a.Is4() != v4 {
		is.add("primary_address", "invalid", 1)
		return netip.Addr{}
	}
	return a
}

func normalizeInterfaces(in []invclient.Interface, is issues) []Interface {
	if len(in) > MaxInterfaces {
		is.add("interfaces", "truncated", len(in)-MaxInterfaces)
		in = in[:MaxInterfaces]
	}
	out := make([]Interface, 0, len(in))
	seen := map[string]bool{}
	total := 0
	for _, raw := range in {
		name, reason := cleanText(raw.Name, MaxIfaceName)
		if reason == "" && name == "" {
			reason = "empty"
		}
		if reason != "" {
			is.add("interface_name", reason, 1)
			continue
		}
		if seen[name] {
			is.add("interface_name", "duplicate", 1)
			continue
		}
		seen[name] = true
		ifc := Interface{
			Name: name,
			MAC:  is.mac("interface_mac", raw.MAC),
			Kind: is.closed("interface_kind", strings.ToLower(raw.Type), KindOther, KindEthernet, KindWireless, KindBond, KindBridge, KindVLAN, KindVirtual, KindLoopback, KindOther),
			Up:   raw.Up,
		}
		if raw.Type == "" {
			ifc.Kind = ""
			if name == "lo" {
				ifc.Kind = KindLoopback // old agents report no kind
			}
		}
		if raw.SpeedBps > MaxSpeedBps {
			is.add("interface_speed", "out_of_range", 1)
		} else {
			ifc.SpeedMbps = int(raw.SpeedBps / 1_000_000) // #nosec G115 -- bounded above
		}
		ifc.Addresses, total = normalizeAddresses(raw, is, total)
		out = append(out, ifc)
	}
	return out
}

// normalizeAddresses reads the per-address list, or for old agents the CIDR
// strings, bounded per interface and in total.
func normalizeAddresses(raw invclient.Interface, is issues, total int) ([]Address, int) {
	var cands []Address
	if len(raw.Addresses) > 0 {
		for _, a := range raw.Addresses {
			addr, ok := parseAddr(a.Address)
			bits := int(a.PrefixLength) // #nosec G115 -- range-checked below
			if !ok || bits > addr.BitLen() {
				is.add("address", "invalid", 1)
				continue
			}
			cands = append(cands, Address{Addr: addr, Prefix: bits, DHCP: a.DHCP, Temporary: a.Temporary, Deprecated: a.Deprecated})
		}
	} else {
		for _, c := range raw.IPAddresses {
			p, err := netip.ParsePrefix(strings.TrimSpace(c))
			if err != nil {
				is.add("address", "invalid", 1)
				continue
			}
			cands = append(cands, Address{Addr: p.Addr().WithZone(""), Prefix: p.Bits()})
		}
	}
	var out []Address
	seen := map[netip.Addr]bool{}
	for _, a := range cands {
		if seen[a.Addr] {
			continue
		}
		if len(out) >= MaxAddrsPerIface || total >= MaxAddrs {
			is.add("addresses", "truncated", 1)
			continue
		}
		seen[a.Addr] = true
		out = append(out, a)
		total++
	}
	return out, total
}

func normalizeBMC(in *invclient.BMC, is issues) *BMC {
	if in == nil {
		return nil
	}
	out := &BMC{}
	if strings.TrimSpace(in.Address) != "" {
		if a, ok := parseAddr(in.Address); ok && !a.IsUnspecified() {
			out.Address = a
		} else if !ok {
			is.add("bmc_address", "invalid", 1)
		}
	}
	if bits := int(in.PrefixLength); out.Address.IsValid() && bits >= 1 && bits <= out.Address.BitLen() { // #nosec G115
		out.Prefix = bits
	} else if in.PrefixLength != 0 {
		is.add("bmc_prefix", "invalid", 1)
	}
	if g, ok := parseAddr(in.Gateway); ok && !g.IsUnspecified() {
		out.Gateway = g
	}
	ports := in.Ports
	if len(ports) > MaxBMCPorts {
		is.add("bmc_ports", "truncated", len(ports)-MaxBMCPorts)
		ports = ports[:MaxBMCPorts]
	}
	seen := map[string]bool{}
	for _, p := range ports {
		m := is.mac("bmc_mac", p.MAC)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out.Ports = append(out.Ports, BMCPort{Channel: int(p.Channel & 0xff), MAC: m}) // #nosec G115 -- masked
	}
	if !out.Address.IsValid() && len(out.Ports) == 0 {
		return nil // no BMC configured or unreadable (US2 scenario 2)
	}
	return out
}

func normalizeGuests(in []invclient.Guest, is issues) []Guest {
	if len(in) > MaxGuests {
		is.add("guests", "truncated", len(in)-MaxGuests)
		in = in[:MaxGuests]
	}
	var out []Guest
	seen := map[string]bool{}
	for _, g := range in {
		ref := strings.TrimSpace(g.ID)
		kind := strings.ToLower(strings.TrimSpace(g.Kind))
		switch {
		case !guestRefRe.MatchString(ref):
			is.add("guest_ref", "invalid", 1)
			continue
		case seen[ref]:
			is.add("guest_ref", "duplicate", 1)
			continue
		case !oneOf(kind, store.GuestVM, store.GuestContainer):
			is.add("guest_kind", "invalid", 1)
			continue
		}
		seen[ref] = true
		ng := Guest{Ref: ref, Kind: kind, Name: is.text("guest_name", g.Name, MaxGuestName), Platform: is.text("guest_platform", g.Platform, 32)}
		if ng.Platform == "" {
			ng.Platform = "proxmox"
		}
		macs := g.MACs
		if len(macs) > MaxGuestMACs {
			is.add("guest_macs", "truncated", len(macs)-MaxGuestMACs)
			macs = macs[:MaxGuestMACs]
		}
		ms := map[string]bool{}
		for _, m := range macs {
			if n := is.mac("guest_mac", m); n != "" && !ms[n] {
				ms[n] = true
				ng.MACs = append(ng.MACs, n)
			}
		}
		out = append(out, ng)
	}
	return out
}

func normalizePackages(in []invclient.PendingUpdate, is issues) []Package {
	if len(in) > MaxPackages {
		is.add("packages", "truncated", len(in)-MaxPackages)
		in = in[:MaxPackages]
	}
	var out []Package
	seen := map[string]bool{}
	for _, p := range in {
		name, r1 := cleanText(p.Name, MaxPackageName)
		inst, r2 := cleanText(p.InstalledVersion, MaxVersion)
		avail, r3 := cleanText(p.AvailableVersion, MaxVersion)
		switch {
		case r1 != "" || name == "":
			is.add("package_name", firstNonEmpty(r1, "empty"), 1)
			continue
		case r2 != "" || r3 != "":
			is.add("package_version", firstNonEmpty(r2, r3), 1)
			continue
		case seen[name]:
			is.add("package_name", "duplicate", 1)
			continue
		}
		seen[name] = true
		out = append(out, Package{Name: name, Installed: inst, Available: avail, Security: p.Security})
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// String summarises a report without its contents (for logs).
func (r Report) String() string {
	return fmt.Sprintf("host %s: %d interfaces, %d guests, %d packages, %d issues",
		r.HostID, len(r.Interfaces), len(r.Guests), len(r.Pending), len(r.Issues))
}
