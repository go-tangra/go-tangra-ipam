// Package snmp is the active SNMP discovery client for the IPAM scan executor.
// It walks a reachable host's system and interface tables (and, for switches,
// the bridge forwarding database and LLDP neighbor table on a best-effort
// basis) and returns a content-safe DiscoveredDevice the executor persists as a
// device + interfaces + L2 links.
//
// Discovery is hidden behind the Discoverer interface so orchestration is
// unit-tested against a Fake without any SNMP traffic. The real implementation
// (Client) uses github.com/gosnmp/gosnmp for SNMPv2c and SNMPv3. Credentials
// (community for v2c; user/auth/priv for v3) are supplied by the caller — the
// executor fetches them from warden at use time and never stores or logs them.
package snmp

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMP OIDs.
const (
	oidSysName     = "1.3.6.1.2.1.1.5.0"
	oidSysDescr    = "1.3.6.1.2.1.1.1.0"
	oidSysObjectID = "1.3.6.1.2.1.1.2.0"

	oidIfDescr       = "1.3.6.1.2.1.2.2.1.2"     // interface name/description
	oidIfType        = "1.3.6.1.2.1.2.2.1.3"     // ifType
	oidIfSpeed       = "1.3.6.1.2.1.2.2.1.5"     // ifSpeed (bps)
	oidIfPhysAddress = "1.3.6.1.2.1.2.2.1.6"     // MAC
	oidIfName        = "1.3.6.1.2.1.31.1.1.1.1"  // ifXTable ifName
	oidIfAlias       = "1.3.6.1.2.1.31.1.1.1.18" // ifAlias

	// Bridge forwarding database (MAC address table).
	oidDot1dBasePortIfIndex = "1.3.6.1.2.1.17.1.4.1.2"     // bridge port -> ifIndex
	oidDot1dTpFdbPort       = "1.3.6.1.2.1.17.4.3.1.2"     // MAC -> bridge port
	oidDot1qTpFdbPort       = "1.3.6.1.2.1.17.7.1.2.2.1.2" // (VLAN,MAC) -> bridge port

	// LLDP remote neighbor table (best effort).
	oidLldpRemPortId  = "1.0.8802.1.1.2.1.4.1.1.7" // lldpRemPortId
	oidLldpRemSysName = "1.0.8802.1.1.2.1.4.1.1.9" // lldpRemSysName
)

const (
	defaultTimeout = 5 * time.Second
	defaultRetries = 1
	defaultPort    = 161
)

// Link source labels (mirror store.LinkSNMPFDB / store.LinkLLDP).
const (
	SourceSNMPFDB = "snmp_fdb"
	SourceLLDP    = "lldp"
)

// Creds carries the SNMP credentials for one probe. Version is 2 (v2c) or 3.
// For v2c only Community is used; for v3 the User/auth/priv fields apply. These
// values come from warden at use time and must never be persisted or logged.
type Creds struct {
	Version      int
	Community    string // v2c
	User         string // v3
	AuthPassword string // v3
	PrivPassword string // v3
	AuthProtocol string // v3: MD5 | SHA
	PrivProtocol string // v3: DES | AES
	TimeoutMs    int
	Retries      int
}

// Interface is one discovered NIC.
type Interface struct {
	Name    string
	IfIndex int
	MAC     string
	Speed   int // Mbps
	Type    string
}

// Link is one discovered L2 neighbor relationship (a MAC learned on a local
// port via the bridge FDB, or an LLDP-reported neighbor).
type Link struct {
	RemotePort string // remote port identifier (learned MAC, or LLDP remote port id)
	Source     string // snmp_fdb | lldp
	VLAN       int
	IfIndex    int    // local ifIndex the neighbor was learned on
	MAC        string // learned/remote MAC when known
}

// DiscoveredDevice is the content-safe result of an SNMP probe.
type DiscoveredDevice struct {
	Address      string
	SysName      string
	SysDescr     string
	DeviceType   string // mapped store.Dev* value
	Manufacturer string
	Model        string
	OSVersion    string
	Interfaces   []Interface
	Links        []Link
}

// Discoverer probes one host via SNMP and returns what it learned.
type Discoverer interface {
	// Discover connects to ip with creds and returns the device's system,
	// interface and (best-effort) link data. A reachable-but-silent host yields
	// an error; a partially-answering host yields a partial device and no error.
	Discover(ctx context.Context, ip string, creds Creds) (DiscoveredDevice, error)
}

// Client is the real gosnmp-backed Discoverer.
type Client struct{}

// NewClient builds a real SNMP discoverer.
func NewClient() *Client { return &Client{} }

// Discover implements Discoverer against a live host.
func (c *Client) Discover(ctx context.Context, ip string, creds Creds) (DiscoveredDevice, error) {
	dev := DiscoveredDevice{Address: ip}

	client, err := newClient(ip, creds)
	if err != nil {
		return dev, fmt.Errorf("snmp: build client %s: %w", ip, err)
	}
	if err := client.ConnectIPv4(); err != nil {
		return dev, fmt.Errorf("snmp: connect %s: %w", ip, err)
	}
	defer func() { _ = client.Conn.Close() }()

	if err := ctx.Err(); err != nil {
		return dev, err
	}

	res, err := client.Get([]string{oidSysName, oidSysDescr, oidSysObjectID})
	if err != nil {
		return dev, fmt.Errorf("snmp: get system oids %s: %w", ip, err)
	}
	var sysObjectID string
	for _, v := range res.Variables {
		if v.Type == gosnmp.NoSuchObject || v.Type == gosnmp.NoSuchInstance {
			continue
		}
		switch v.Name {
		case "." + oidSysName:
			dev.SysName = extractString(v)
		case "." + oidSysDescr:
			dev.SysDescr = extractString(v)
		case "." + oidSysObjectID:
			sysObjectID = extractString(v)
		}
	}

	dev.DeviceType = parseDeviceType(sysObjectID, dev.SysDescr)
	dev.Manufacturer = parseManufacturer(dev.SysDescr)
	dev.Model, dev.OSVersion = parseModelAndOS(dev.SysDescr)

	if err := ctx.Err(); err != nil {
		return dev, err
	}

	dev.Interfaces = walkInterfaces(client)

	// Only switches carry a useful bridge FDB.
	if dev.DeviceType == devSwitch {
		if fdb := walkBridgeFDB(client); len(fdb) > 0 {
			dev.Links = append(dev.Links, fdb...)
		}
	}
	if lldp := walkLLDP(client); len(lldp) > 0 {
		dev.Links = append(dev.Links, lldp...)
	}

	return dev, nil
}

// newClient builds a configured gosnmp client for the address and credentials.
func newClient(address string, creds Creds) (*gosnmp.GoSNMP, error) {
	timeout := defaultTimeout
	if creds.TimeoutMs > 0 {
		timeout = time.Duration(creds.TimeoutMs) * time.Millisecond
	}
	retries := defaultRetries
	if creds.Retries > 0 {
		retries = creds.Retries
	}
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}

	client := &gosnmp.GoSNMP{
		Target:  host,
		Port:    defaultPort,
		Timeout: timeout,
		Retries: retries,
	}

	if creds.Version == 3 {
		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = gosnmp.AuthPriv
		usm := &gosnmp.UsmSecurityParameters{UserName: creds.User}
		switch strings.ToUpper(creds.AuthProtocol) {
		case "SHA":
			usm.AuthenticationProtocol = gosnmp.SHA
		default:
			usm.AuthenticationProtocol = gosnmp.MD5
		}
		usm.AuthenticationPassphrase = creds.AuthPassword
		switch strings.ToUpper(creds.PrivProtocol) {
		case "AES":
			usm.PrivacyProtocol = gosnmp.AES
		default:
			usm.PrivacyProtocol = gosnmp.DES
		}
		usm.PrivacyPassphrase = creds.PrivPassword
		switch {
		case creds.AuthPassword == "":
			client.MsgFlags = gosnmp.NoAuthNoPriv
		case creds.PrivPassword == "":
			client.MsgFlags = gosnmp.AuthNoPriv
		}
		client.SecurityParameters = usm
		return client, nil
	}

	client.Version = gosnmp.Version2c
	community := creds.Community
	if community == "" {
		community = "public"
	}
	client.Community = community
	return client, nil
}

// walkInterfaces walks the interface table and returns the NICs sorted by
// ifIndex. Individual walk failures are tolerated (partial data is useful).
func walkInterfaces(client *gosnmp.GoSNMP) []Interface {
	ifs := make(map[int]*Interface)

	_ = client.Walk(oidIfDescr, func(pdu gosnmp.SnmpPDU) error {
		idx := ifIndex(pdu.Name, oidIfDescr)
		if idx <= 0 {
			return nil
		}
		ifs[idx] = &Interface{IfIndex: idx, Name: extractString(pdu)}
		return nil
	})
	_ = client.Walk(oidIfName, func(pdu gosnmp.SnmpPDU) error {
		idx := ifIndex(pdu.Name, oidIfName)
		if idx <= 0 {
			return nil
		}
		name := extractString(pdu)
		if cur, ok := ifs[idx]; ok {
			if cur.Name == "" {
				cur.Name = name
			}
		} else {
			ifs[idx] = &Interface{IfIndex: idx, Name: name}
		}
		return nil
	})
	if len(ifs) == 0 {
		return nil
	}
	_ = client.Walk(oidIfPhysAddress, func(pdu gosnmp.SnmpPDU) error {
		if cur, ok := ifs[ifIndex(pdu.Name, oidIfPhysAddress)]; ok {
			cur.MAC = formatMAC(pdu.Value)
		}
		return nil
	})
	_ = client.Walk(oidIfSpeed, func(pdu gosnmp.SnmpPDU) error {
		if cur, ok := ifs[ifIndex(pdu.Name, oidIfSpeed)]; ok {
			if speed, ok := pdu.Value.(uint); ok {
				cur.Speed = int(speed / 1_000_000)
			}
		}
		return nil
	})
	_ = client.Walk(oidIfType, func(pdu gosnmp.SnmpPDU) error {
		if cur, ok := ifs[ifIndex(pdu.Name, oidIfType)]; ok {
			if val, ok := pdu.Value.(int); ok {
				cur.Type = ifTypeToString(val)
			}
		}
		return nil
	})
	_ = client.Walk(oidIfAlias, func(pdu gosnmp.SnmpPDU) error {
		if cur, ok := ifs[ifIndex(pdu.Name, oidIfAlias)]; ok && cur.Name == "" {
			cur.Name = extractString(pdu)
		}
		return nil
	})

	indices := make([]int, 0, len(ifs))
	for idx := range ifs {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	out := make([]Interface, 0, len(ifs))
	for _, idx := range indices {
		out = append(out, *ifs[idx])
	}
	return out
}

// walkBridgeFDB walks the bridge forwarding database into per-MAC links. It
// prefers the VLAN-aware Q-BRIDGE table and falls back to the classic table.
func walkBridgeFDB(client *gosnmp.GoSNMP) []Link {
	portToIf := make(map[int]int)
	_ = client.Walk(oidDot1dBasePortIfIndex, func(pdu gosnmp.SnmpPDU) error {
		port := lastInt(pdu.Name, oidDot1dBasePortIfIndex)
		if idx, ok := pduInt(pdu.Value); ok && port > 0 {
			portToIf[port] = idx
		}
		return nil
	})
	resolve := func(port int) int {
		if idx, ok := portToIf[port]; ok {
			return idx
		}
		return port
	}

	var links []Link
	_ = client.Walk(oidDot1qTpFdbPort, func(pdu gosnmp.SnmpPDU) error {
		suffix := suffixInts(pdu.Name, oidDot1qTpFdbPort)
		if len(suffix) != 7 {
			return nil
		}
		port, ok := pduInt(pdu.Value)
		if !ok || port <= 0 {
			return nil
		}
		mac := macFromOctets(suffix[1:])
		links = append(links, Link{RemotePort: mac, MAC: mac, Source: SourceSNMPFDB, VLAN: suffix[0], IfIndex: resolve(port)})
		return nil
	})
	if len(links) > 0 {
		return links
	}
	_ = client.Walk(oidDot1dTpFdbPort, func(pdu gosnmp.SnmpPDU) error {
		suffix := suffixInts(pdu.Name, oidDot1dTpFdbPort)
		if len(suffix) != 6 {
			return nil
		}
		port, ok := pduInt(pdu.Value)
		if !ok || port <= 0 {
			return nil
		}
		mac := macFromOctets(suffix)
		links = append(links, Link{RemotePort: mac, MAC: mac, Source: SourceSNMPFDB, IfIndex: resolve(port)})
		return nil
	})
	return links
}

// walkLLDP walks the LLDP remote table into neighbor links (best effort).
func walkLLDP(client *gosnmp.GoSNMP) []Link {
	names := make(map[string]string)
	_ = client.Walk(oidLldpRemSysName, func(pdu gosnmp.SnmpPDU) error {
		names[suffixKey(pdu.Name, oidLldpRemSysName)] = extractString(pdu)
		return nil
	})
	var links []Link
	_ = client.Walk(oidLldpRemPortId, func(pdu gosnmp.SnmpPDU) error {
		key := suffixKey(pdu.Name, oidLldpRemPortId)
		if key == "" {
			return nil
		}
		suffix := suffixInts(pdu.Name, oidLldpRemPortId)
		local := 0
		if len(suffix) >= 2 {
			local = suffix[1] // lldpRemLocalPortNum
		}
		port := extractString(pdu)
		if name := names[key]; name != "" {
			port = name + " " + port
		}
		links = append(links, Link{RemotePort: port, Source: SourceLLDP, IfIndex: local})
		return nil
	})
	return links
}

// ---- OID / value helpers ----

func suffixInts(oid, base string) []int {
	prefix := "." + base + "."
	if !strings.HasPrefix(oid, prefix) {
		return nil
	}
	parts := strings.Split(oid[len(prefix):], ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func suffixKey(oid, base string) string {
	prefix := "." + base + "."
	if !strings.HasPrefix(oid, prefix) {
		return ""
	}
	return oid[len(prefix):]
}

func lastInt(oid, base string) int {
	s := suffixInts(oid, base)
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

func ifIndex(oid, base string) int {
	prefix := "." + base + "."
	if !strings.HasPrefix(oid, prefix) {
		return 0
	}
	var idx int
	_, _ = fmt.Sscanf(oid[len(prefix):], "%d", &idx)
	return idx
}

func macFromOctets(octets []int) string {
	if len(octets) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		octets[0]&0xff, octets[1]&0xff, octets[2]&0xff, octets[3]&0xff, octets[4]&0xff, octets[5]&0xff)
}

func pduInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case uint:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func extractString(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatMAC(val interface{}) string {
	switch v := val.(type) {
	case []byte:
		if len(v) == 6 {
			return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", v[0], v[1], v[2], v[3], v[4], v[5])
		}
		return hex.EncodeToString(v)
	case string:
		if len(v) == 6 {
			return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", v[0], v[1], v[2], v[3], v[4], v[5])
		}
		return v
	default:
		return ""
	}
}

// device_type values (mirror store.Dev*).
const (
	devServer  = "server"
	devRouter  = "router"
	devSwitch  = "switch"
	devFW      = "firewall"
	devLB      = "load_balancer"
	devAP      = "access_point"
	devPrinter = "printer"
	devOther   = "other"
)

// parseDeviceType infers a device_type from sysObjectID and sysDescr.
func parseDeviceType(sysObjectID, sysDescr string) string {
	lower := strings.ToLower(sysDescr)
	switch {
	case strings.Contains(sysObjectID, "1.3.6.1.4.1.9."): // Cisco
		switch {
		case strings.Contains(lower, "router") || strings.Contains(lower, "isr") || strings.Contains(lower, "asr"):
			return devRouter
		case strings.Contains(lower, "catalyst") || strings.Contains(lower, "switch") || strings.Contains(lower, "nexus"):
			return devSwitch
		case strings.Contains(lower, "asa") || strings.Contains(lower, "firewall") || strings.Contains(lower, "firepower"):
			return devFW
		}
	case strings.Contains(sysObjectID, "1.3.6.1.4.1.2636."): // Juniper
		switch {
		case strings.Contains(lower, "mx") || strings.Contains(lower, "router"):
			return devRouter
		case strings.Contains(lower, "ex") || strings.Contains(lower, "switch") || strings.Contains(lower, "qfx"):
			return devSwitch
		case strings.Contains(lower, "srx") || strings.Contains(lower, "firewall"):
			return devFW
		}
	case strings.Contains(sysObjectID, "1.3.6.1.4.1.1916."): // Extreme
		return devSwitch
	}
	switch {
	case strings.Contains(lower, "router") || strings.Contains(lower, "routing"):
		return devRouter
	case strings.Contains(lower, "switch") || strings.Contains(lower, "extremexos") || strings.Contains(lower, "extreme networks"):
		return devSwitch
	case strings.Contains(lower, "firewall") || strings.Contains(lower, "security"):
		return devFW
	case strings.Contains(lower, "load balancer") || strings.Contains(lower, "f5") || strings.Contains(lower, "netscaler"):
		return devLB
	case strings.Contains(lower, "access point") || strings.Contains(lower, "wireless"):
		return devAP
	case strings.Contains(lower, "printer") || strings.Contains(lower, "laserjet") || strings.Contains(lower, "officejet"):
		return devPrinter
	case strings.Contains(lower, "linux") || strings.Contains(lower, "windows") || strings.Contains(lower, "vmware"):
		return devServer
	}
	return devOther
}

func parseManufacturer(sysDescr string) string {
	lower := strings.ToLower(sysDescr)
	pairs := []struct{ key, name string }{
		{"cisco", "Cisco"}, {"juniper", "Juniper"}, {"arista", "Arista"}, {"huawei", "Huawei"},
		{"hewlett", "HP"}, {"dell", "Dell"}, {"mikrotik", "MikroTik"}, {"ubiquiti", "Ubiquiti"},
		{"fortinet", "Fortinet"}, {"fortigate", "Fortinet"}, {"palo alto", "Palo Alto"}, {"f5", "F5"},
		{"netscaler", "Citrix"}, {"aruba", "Aruba"}, {"extreme", "Extreme Networks"}, {"brocade", "Brocade"},
		{"vmware", "VMware"}, {"linux", "Linux"}, {"windows", "Microsoft"}, {"net-snmp", "Net-SNMP"},
		{"synology", "Synology"}, {"qnap", "QNAP"},
	}
	for _, p := range pairs {
		if strings.Contains(lower, p.key) {
			return p.name
		}
	}
	return ""
}

func parseModelAndOS(sysDescr string) (model, osVersion string) {
	if sysDescr == "" {
		return "", ""
	}
	if strings.Contains(strings.ToLower(sysDescr), "version") {
		parts := strings.SplitN(sysDescr, "Version", 2)
		if len(parts) == 2 {
			ver := strings.TrimSpace(parts[1])
			for _, sep := range []string{",", "\n", "\r", "("} {
				if idx := strings.Index(ver, sep); idx > 0 {
					ver = ver[:idx]
				}
			}
			osVersion = strings.TrimSpace(ver)
		}
	}
	firstLine := strings.TrimSpace(strings.SplitN(sysDescr, "\n", 2)[0])
	if idx := strings.Index(firstLine, "("); idx >= 0 {
		if end := strings.Index(firstLine[idx:], ")"); end > 0 {
			model = firstLine[idx+1 : idx+end]
		}
	}
	return model, osVersion
}

func ifTypeToString(ifType int) string {
	switch ifType {
	case 6:
		return "ethernet"
	case 24:
		return "loopback"
	case 53:
		return "virtualProp"
	case 131:
		return "tunnel"
	case 135:
		return "l2vlan"
	case 136:
		return "l3vlan"
	case 161:
		return "ieee8023ad"
	case 71:
		return "ieee80211"
	case 1:
		return "other"
	default:
		return fmt.Sprintf("type(%d)", ifType)
	}
}

// Fake is an in-memory Discoverer for tests, keyed by host IP.
type Fake struct {
	Devices map[string]DiscoveredDevice
	Err     error
}

// NewFake builds an empty Fake.
func NewFake() *Fake { return &Fake{Devices: map[string]DiscoveredDevice{}} }

// Set records the device an IP should resolve to.
func (f *Fake) Set(ip string, dev DiscoveredDevice) {
	dev.Address = ip
	f.Devices[ip] = dev
}

// Discover returns the configured device for ip, or an error when none is set
// (mirroring a silent host).
func (f *Fake) Discover(_ context.Context, ip string, _ Creds) (DiscoveredDevice, error) {
	if f.Err != nil {
		return DiscoveredDevice{}, f.Err
	}
	dev, ok := f.Devices[ip]
	if !ok {
		return DiscoveredDevice{Address: ip}, fmt.Errorf("snmp: no response from %s", ip)
	}
	return dev, nil
}

// interface conformance.
var (
	_ Discoverer = (*Client)(nil)
	_ Discoverer = (*Fake)(nil)
)
