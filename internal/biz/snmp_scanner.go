package biz

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	kratosLog "github.com/go-kratos/kratos/v2/log"
	"github.com/gosnmp/gosnmp"
)

const (
	// SNMP OIDs for system information
	oidSysName     = "1.3.6.1.2.1.1.5.0"
	oidSysDescr    = "1.3.6.1.2.1.1.1.0"
	oidSysContact  = "1.3.6.1.2.1.1.4.0"
	oidSysLocation = "1.3.6.1.2.1.1.6.0"
	oidSysObjectID = "1.3.6.1.2.1.1.2.0"

	// SNMP OIDs for interface table
	oidIfDescr       = "1.3.6.1.2.1.2.2.1.2"  // Interface description/name
	oidIfType        = "1.3.6.1.2.1.2.2.1.3"  // Interface type
	oidIfSpeed       = "1.3.6.1.2.1.2.2.1.5"  // Interface speed (bps)
	oidIfPhysAddress = "1.3.6.1.2.1.2.2.1.6"  // MAC address
	oidIfAdminStatus = "1.3.6.1.2.1.2.2.1.7"  // Admin status (1=up, 2=down)
	oidIfAlias       = "1.3.6.1.2.1.31.1.1.1.18" // Interface alias/description

	// Default SNMP settings
	defaultSNMPTimeout     = 5 * time.Second
	defaultSNMPRetries     = 1
	defaultSNMPConcurrency = 10
	defaultSNMPPort        = 161
)

// SNMPConfig holds SNMP connection parameters
type SNMPConfig struct {
	Version      int    // 2 or 3
	Community    string // v2c
	User         string // v3
	AuthPassword string // v3
	PrivPassword string // v3
	AuthProtocol string // v3: MD5, SHA
	PrivProtocol string // v3: DES, AES
	TimeoutMs    int32
	Retries      int
}

// SNMPDeviceInfo holds discovered device information
type SNMPDeviceInfo struct {
	Address      string
	SysName      string
	SysDescr     string
	SysContact   string
	SysLocation  string
	SysObjectID  string
	DeviceType   int32 // Inferred: ROUTER=3, SWITCH=4, etc.
	Manufacturer string
	Model        string
	OsVersion    string
	Interfaces   []SNMPInterface
}

// SNMPInterface holds discovered interface information
type SNMPInterface struct {
	Index       int
	Name        string // ifDescr
	MAC         string // ifPhysAddress
	SpeedMbps   int32  // ifSpeed / 1_000_000
	Enabled     bool   // ifAdminStatus == 1
	Type        string // ifType mapped to string
	Description string // ifAlias
}

// ScanDevice connects to a single IP via SNMP and queries system + interface
// information. For SNMPv3 with AES privacy, it transparently falls back to
// DES on the first non-timeout failure — some legacy devices do not support
// AES and silently reject (or fail to decrypt) the initial authenticated GET.
func ScanDevice(ctx context.Context, logger *kratosLog.Helper, address string, config SNMPConfig) (*SNMPDeviceInfo, error) {
	info, err := scanDeviceOnce(ctx, logger, address, config)
	if err == nil || !shouldFallbackToDES(config, err) {
		return info, err
	}

	fallback := config
	fallback.PrivProtocol = "DES"
	logger.Infof("[SNMP] AES failed for %s (%v); retrying with DES privacy for legacy-device compatibility", address, err)
	return scanDeviceOnce(ctx, logger, address, fallback)
}

// shouldFallbackToDES reports whether the error from an SNMPv3 AES probe is
// consistent with the remote agent not supporting AES (vs. the host being
// dead). We only retry for SNMPv3 when AES was attempted and the failure is
// not a network timeout or a cancelled context — those won't get better with
// a different privacy protocol and would double the wait for dead hosts.
func shouldFallbackToDES(config SNMPConfig, err error) bool {
	if err == nil {
		return false
	}
	if config.Version != 3 {
		return false
	}
	if strings.ToUpper(config.PrivProtocol) != "AES" {
		return false
	}
	if config.PrivPassword == "" {
		// No privacy configured, nothing to fall back to.
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return false
	}
	// Plain gosnmp timeouts surface as errors that wrap a deadline exceeded
	// but also as strings; check substring as a last resort.
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return false
	}
	return true
}

// scanDeviceOnce is the single-shot probe. ScanDevice wraps it with the
// AES->DES fallback so tests and future callers can target just one attempt.
func scanDeviceOnce(ctx context.Context, logger *kratosLog.Helper, address string, config SNMPConfig) (*SNMPDeviceInfo, error) {
	logger.Debugf("[SNMP] Connecting to %s via SNMPv%d (port %d, timeout %v, retries %d, priv=%s)",
		address, config.Version, defaultSNMPPort, resolveTimeout(config), resolveRetries(config), config.PrivProtocol)

	snmpClient, err := newSNMPClient(address, config)
	if err != nil {
		logger.Errorf("Failed to create SNMP client for %s: %v", address, err)
		return nil, fmt.Errorf("create SNMP client: %w", err)
	}

	if err := snmpClient.ConnectIPv4(); err != nil {
		logger.Warnf("SNMP connection failed to %s: %v", address, err)
		return nil, fmt.Errorf("SNMP connect to %s: %w", address, err)
	}
	defer snmpClient.Conn.Close()
	logger.Debugf("SNMP connection established to %s", address)

	// Check context cancellation
	select {
	case <-ctx.Done():
		logger.Infof("SNMP scan cancelled for %s", address)
		return nil, ctx.Err()
	default:
	}

	// Query system information
	info := &SNMPDeviceInfo{Address: address}
	sysOIDs := []string{oidSysName, oidSysDescr, oidSysContact, oidSysLocation, oidSysObjectID}

	logger.Debugf("Querying system OIDs from %s", address)
	result, err := snmpClient.Get(sysOIDs)
	if err != nil {
		logger.Warnf("SNMP GET system OIDs failed for %s: %v", address, err)
		return nil, fmt.Errorf("SNMP GET system OIDs from %s: %w", address, err)
	}
	logger.Debugf("SNMP GET returned %d variables from %s", len(result.Variables), address)

	for _, variable := range result.Variables {
		if variable.Type == gosnmp.NoSuchObject || variable.Type == gosnmp.NoSuchInstance {
			logger.Debugf("OID %s returned NoSuchObject/NoSuchInstance on %s", variable.Name, address)
			continue
		}
		val := extractStringValue(variable)
		switch variable.Name {
		case "." + oidSysName:
			info.SysName = val
		case "." + oidSysDescr:
			info.SysDescr = val
		case "." + oidSysContact:
			info.SysContact = val
		case "." + oidSysLocation:
			info.SysLocation = val
		case "." + oidSysObjectID:
			info.SysObjectID = val
		}
	}

	logger.Infof("SNMP device discovered at %s: sysName=%q, sysObjectID=%q, sysDescr=%q",
		address, info.SysName, info.SysObjectID, truncate(info.SysDescr, 120))

	// Infer device type and manufacturer
	info.DeviceType = parseDeviceType(info.SysObjectID, info.SysDescr)
	info.Manufacturer = parseManufacturer(info.SysDescr)
	info.Model, info.OsVersion = parseModelAndOS(info.SysDescr)

	logger.Debugf("Inferred for %s: type=%d, manufacturer=%q, model=%q, os=%q",
		address, info.DeviceType, info.Manufacturer, info.Model, info.OsVersion)

	// Query interfaces
	select {
	case <-ctx.Done():
		logger.Infof("SNMP scan cancelled during interface walk for %s", address)
		return info, ctx.Err()
	default:
	}

	logger.Debugf("Walking interface table on %s", address)
	info.Interfaces, err = walkInterfaces(snmpClient)
	if err != nil {
		logger.Warnf("Interface walk failed for %s (returning partial result): %v", address, err)
		return info, nil
	}
	logger.Infof("Discovered %d interfaces on %s", len(info.Interfaces), address)

	return info, nil
}

func resolveTimeout(config SNMPConfig) time.Duration {
	if config.TimeoutMs > 0 {
		return time.Duration(config.TimeoutMs) * time.Millisecond
	}
	return defaultSNMPTimeout
}

func resolveRetries(config SNMPConfig) int {
	if config.Retries > 0 {
		return config.Retries
	}
	return defaultSNMPRetries
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// ScanSubnetSNMP scans multiple IPs concurrently via SNMP
func ScanSubnetSNMP(ctx context.Context, logger *kratosLog.Helper, addresses []string, config SNMPConfig, concurrency int, progressCb func(scanned, discovered int)) ([]SNMPDeviceInfo, error) {
	if concurrency <= 0 {
		concurrency = defaultSNMPConcurrency
	}

	versionStr := fmt.Sprintf("v%d", config.Version)
	if config.Version == 2 {
		versionStr = "v2c"
	}

	logger.Infof("Starting SNMP subnet scan: %d alive hosts to probe, version=%s, concurrency=%d",
		len(addresses), versionStr, concurrency)

	if config.Version == 2 {
		if config.Community == "" {
			logger.Warnf("SNMPv2c community string is empty, will default to 'public'")
		} else {
			logger.Debugf("SNMPv2c community string configured (length=%d)", len(config.Community))
		}
	} else if config.Version == 3 {
		logger.Debugf("SNMPv3 config: user=%q, authProto=%s, privProto=%s, hasAuthPwd=%v, hasPrivPwd=%v",
			config.User, config.AuthProtocol, config.PrivProtocol,
			config.AuthPassword != "", config.PrivPassword != "")
	}

	var (
		mu         sync.Mutex
		results    []SNMPDeviceInfo
		scanned    int
		discovered int
		failed     int
		wg         sync.WaitGroup
	)

	sem := make(chan struct{}, concurrency)

	for _, addr := range addresses {
		select {
		case <-ctx.Done():
			wg.Wait()
			logger.Infof("SNMP scan cancelled: %d/%d scanned, %d discovered, %d failed",
				scanned, len(addresses), discovered, failed)
			return results, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			defer func() { <-sem }()

			info, err := ScanDevice(ctx, logger, address, config)

			mu.Lock()
			scanned++
			if err != nil {
				failed++
				logger.Debugf("SNMP probe failed for %s: %v", address, err)
			} else if info != nil {
				results = append(results, *info)
				discovered++
			}
			if progressCb != nil {
				progressCb(scanned, discovered)
			}
			mu.Unlock()
		}(addr)
	}

	wg.Wait()
	logger.Infof("SNMP subnet scan completed: %d/%d scanned, %d discovered, %d failed (no SNMP response)",
		scanned, len(addresses), discovered, failed)
	return results, nil
}

// newSNMPClient creates a configured gosnmp client
func newSNMPClient(address string, config SNMPConfig) (*gosnmp.GoSNMP, error) {
	timeout := defaultSNMPTimeout
	if config.TimeoutMs > 0 {
		timeout = time.Duration(config.TimeoutMs) * time.Millisecond
	}
	retries := defaultSNMPRetries
	if config.Retries > 0 {
		retries = config.Retries
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// No port specified, use address as-is
		host = address
	}

	client := &gosnmp.GoSNMP{
		Target:  host,
		Port:    defaultSNMPPort,
		Timeout: timeout,
		Retries: retries,
	}

	switch config.Version {
	case 3:
		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = gosnmp.AuthPriv

		usmParams := &gosnmp.UsmSecurityParameters{
			UserName: config.User,
		}

		switch strings.ToUpper(config.AuthProtocol) {
		case "SHA":
			usmParams.AuthenticationProtocol = gosnmp.SHA
		default:
			usmParams.AuthenticationProtocol = gosnmp.MD5
		}
		usmParams.AuthenticationPassphrase = config.AuthPassword

		switch strings.ToUpper(config.PrivProtocol) {
		case "AES":
			usmParams.PrivacyProtocol = gosnmp.AES
		default:
			usmParams.PrivacyProtocol = gosnmp.DES
		}
		usmParams.PrivacyPassphrase = config.PrivPassword

		// Adjust MsgFlags based on available credentials
		if config.AuthPassword == "" {
			client.MsgFlags = gosnmp.NoAuthNoPriv
		} else if config.PrivPassword == "" {
			client.MsgFlags = gosnmp.AuthNoPriv
		}

		client.SecurityParameters = usmParams
	default:
		// Default to v2c
		client.Version = gosnmp.Version2c
		community := config.Community
		if community == "" {
			community = "public"
		}
		client.Community = community
	}

	return client, nil
}

// walkInterfaces performs SNMP walk on the interface table
func walkInterfaces(client *gosnmp.GoSNMP) ([]SNMPInterface, error) {
	ifMap := make(map[int]*SNMPInterface)

	// Walk ifDescr to discover interfaces
	err := client.Walk(oidIfDescr, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfDescr)
		if idx <= 0 {
			return nil
		}
		iface := &SNMPInterface{Index: idx, Name: extractStringValue(pdu), Enabled: true}
		ifMap[idx] = iface
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk ifDescr: %w", err)
	}

	if len(ifMap) == 0 {
		return nil, nil
	}

	// Walk ifPhysAddress (MAC)
	_ = client.Walk(oidIfPhysAddress, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfPhysAddress)
		if iface, ok := ifMap[idx]; ok {
			iface.MAC = formatMAC(pdu.Value)
		}
		return nil
	})

	// Walk ifSpeed
	_ = client.Walk(oidIfSpeed, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfSpeed)
		if iface, ok := ifMap[idx]; ok {
			if speed, ok := pdu.Value.(uint); ok {
				iface.SpeedMbps = int32(speed / 1_000_000)
			}
		}
		return nil
	})

	// Walk ifAdminStatus
	_ = client.Walk(oidIfAdminStatus, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfAdminStatus)
		if iface, ok := ifMap[idx]; ok {
			if val, ok := pdu.Value.(int); ok {
				iface.Enabled = val == 1
			}
		}
		return nil
	})

	// Walk ifType
	_ = client.Walk(oidIfType, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfType)
		if iface, ok := ifMap[idx]; ok {
			if val, ok := pdu.Value.(int); ok {
				iface.Type = ifTypeToString(val)
			}
		}
		return nil
	})

	// Walk ifAlias (description)
	_ = client.Walk(oidIfAlias, func(pdu gosnmp.SnmpPDU) error {
		idx := extractIfIndex(pdu.Name, oidIfAlias)
		if iface, ok := ifMap[idx]; ok {
			iface.Description = extractStringValue(pdu)
		}
		return nil
	})

	// Convert map to sorted slice
	result := make([]SNMPInterface, 0, len(ifMap))
	for i := 1; i <= len(ifMap)+100; i++ {
		if iface, ok := ifMap[i]; ok {
			result = append(result, *iface)
		}
	}

	return result, nil
}

// extractStringValue extracts a string from an SNMP PDU value
func extractStringValue(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// extractIfIndex extracts the interface index from an OID like ".1.3.6.1.2.1.2.2.1.2.3"
func extractIfIndex(oid, baseOID string) int {
	prefix := "." + baseOID + "."
	if !strings.HasPrefix(oid, prefix) {
		return 0
	}
	suffix := oid[len(prefix):]
	var idx int
	fmt.Sscanf(suffix, "%d", &idx)
	return idx
}

// formatMAC converts raw bytes to a MAC address string
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

// parseDeviceType infers device type from sysObjectID and sysDescr
func parseDeviceType(sysObjectID, sysDescr string) int32 {
	lower := strings.ToLower(sysDescr)

	// Check sysObjectID enterprise prefixes
	switch {
	case strings.Contains(sysObjectID, "1.3.6.1.4.1.9."):
		// Cisco
		if strings.Contains(lower, "router") || strings.Contains(lower, "isr") || strings.Contains(lower, "asr") {
			return 3 // ROUTER
		}
		if strings.Contains(lower, "catalyst") || strings.Contains(lower, "switch") || strings.Contains(lower, "nexus") {
			return 4 // SWITCH
		}
		if strings.Contains(lower, "asa") || strings.Contains(lower, "firewall") || strings.Contains(lower, "firepower") {
			return 5 // FIREWALL
		}
	case strings.Contains(sysObjectID, "1.3.6.1.4.1.2636."):
		// Juniper
		if strings.Contains(lower, "mx") || strings.Contains(lower, "router") {
			return 3
		}
		if strings.Contains(lower, "ex") || strings.Contains(lower, "switch") || strings.Contains(lower, "qfx") {
			return 4
		}
		if strings.Contains(lower, "srx") || strings.Contains(lower, "firewall") {
			return 5
		}
	}

	// Fall back to description-based detection
	switch {
	case strings.Contains(lower, "router") || strings.Contains(lower, "routing"):
		return 3 // ROUTER
	case strings.Contains(lower, "switch") || strings.Contains(lower, "switching"):
		return 4 // SWITCH
	case strings.Contains(lower, "firewall") || strings.Contains(lower, "security"):
		return 5 // FIREWALL
	case strings.Contains(lower, "load balancer") || strings.Contains(lower, "f5") || strings.Contains(lower, "netscaler"):
		return 6 // LOAD_BALANCER
	case strings.Contains(lower, "access point") || strings.Contains(lower, "wireless") || strings.Contains(lower, " ap "):
		return 7 // ACCESS_POINT
	case strings.Contains(lower, "printer") || strings.Contains(lower, "laserjet") || strings.Contains(lower, "officejet"):
		return 9 // PRINTER
	case strings.Contains(lower, "linux") || strings.Contains(lower, "windows") || strings.Contains(lower, "vmware"):
		return 1 // SERVER
	}

	return 99 // OTHER
}

// parseManufacturer extracts manufacturer from sysDescr
func parseManufacturer(sysDescr string) string {
	lower := strings.ToLower(sysDescr)

	manufacturers := map[string]string{
		"cisco":       "Cisco",
		"juniper":     "Juniper",
		"arista":      "Arista",
		"huawei":      "Huawei",
		"hp ":         "HP",
		"hewlett":     "HP",
		"dell":        "Dell",
		"mikrotik":    "MikroTik",
		"ubiquiti":    "Ubiquiti",
		"fortinet":    "Fortinet",
		"fortigate":   "Fortinet",
		"palo alto":   "Palo Alto",
		"f5":          "F5",
		"netscaler":   "Citrix",
		"aruba":       "Aruba",
		"extreme":     "Extreme Networks",
		"brocade":     "Brocade",
		"vmware":      "VMware",
		"linux":       "Linux",
		"windows":     "Microsoft",
		"net-snmp":    "Net-SNMP",
		"synology":    "Synology",
		"qnap":        "QNAP",
	}

	for keyword, name := range manufacturers {
		if strings.Contains(lower, keyword) {
			return name
		}
	}

	return ""
}

// parseModelAndOS extracts model and OS version from sysDescr
func parseModelAndOS(sysDescr string) (model, osVersion string) {
	if sysDescr == "" {
		return "", ""
	}

	// Try to extract version info
	lower := strings.ToLower(sysDescr)

	// Common patterns: "Cisco IOS Software, Version 15.1(4)M"
	if strings.Contains(lower, "version") {
		parts := strings.SplitN(sysDescr, "Version", 2)
		if len(parts) == 2 {
			ver := strings.TrimSpace(parts[1])
			// Take until comma or newline
			for _, sep := range []string{",", "\n", "\r", "("} {
				if idx := strings.Index(ver, sep); idx > 0 {
					ver = ver[:idx]
				}
			}
			osVersion = strings.TrimSpace(ver)
		}
	}

	// Try to extract model from first line
	lines := strings.SplitN(sysDescr, "\n", 2)
	firstLine := strings.TrimSpace(lines[0])

	// Common patterns: "Cisco IOS XE Software (ASR1001-X)"
	if idx := strings.Index(firstLine, "("); idx >= 0 {
		end := strings.Index(firstLine[idx:], ")")
		if end > 0 {
			model = firstLine[idx+1 : idx+end]
		}
	}

	return model, osVersion
}

// ifTypeToString converts IANA ifType number to string
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
		return "ieee8023ad" // LAG
	case 71:
		return "ieee80211" // WiFi
	case 1:
		return "other"
	default:
		return fmt.Sprintf("type(%d)", ifType)
	}
}
