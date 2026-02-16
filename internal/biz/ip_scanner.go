package biz

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	// MaxScanAddresses is the maximum number of addresses that can be scanned
	MaxScanAddresses = 1024

	// DefaultTimeout is the default ICMP ping timeout
	DefaultTimeout = 1000 * time.Millisecond

	// DefaultConcurrency is the default number of parallel probes
	DefaultConcurrency = 50
)

// ScanConfig holds configuration for a scan
type ScanConfig struct {
	TimeoutMs            int32
	Concurrency          int32
	SkipReverseDNS       bool
	TCPProbePorts        string   // Deprecated: kept for backward compatibility, ignored
	DNSServers           []string // Custom DNS servers for reverse lookup
	DNSTimeoutMs         int32    // Timeout for DNS queries
	UseSystemDNSFallback bool     // Whether to use system DNS as fallback
}

// ScanResult represents the result of scanning a single IP
type ScanResult struct {
	Address  string
	Alive    bool
	Hostname string
	Ports    []int // Deprecated: kept for backward compatibility, always empty
}

// ScanProgress represents the current progress of a scan
type ScanProgress struct {
	TotalAddresses int64
	ScannedCount   int64
	AliveCount     int64
	NewCount       int64
	UpdatedCount   int64
	Progress       int32
}

// ProgressCallback is called during scanning to report progress
type ProgressCallback func(progress ScanProgress)

// Scanner performs network scanning using ICMP ping
type Scanner struct {
	config ScanConfig
}

// NewScanner creates a new Scanner with the given config
func NewScanner(config ScanConfig) *Scanner {
	return &Scanner{
		config: config,
	}
}

// GenerateIPs generates all host IPs in a CIDR range
// Excludes network and broadcast addresses for IPv4
func GenerateIPs(cidr string) ([]net.IP, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}

	// Check if IPv6
	if ipNet.IP.To4() == nil {
		return nil, fmt.Errorf("IPv6 subnets not supported for scanning")
	}

	// Calculate the number of addresses
	ones, bits := ipNet.Mask.Size()
	numAddresses := 1 << (bits - ones)

	// Check size limit
	if numAddresses > MaxScanAddresses+2 { // +2 for network and broadcast
		return nil, fmt.Errorf("subnet too large: %d addresses (max %d)", numAddresses-2, MaxScanAddresses)
	}

	// Special case for /31 and /32
	if ones >= 31 {
		ips := make([]net.IP, 0, numAddresses)
		ip := ipNet.IP.To4()
		start := binary.BigEndian.Uint32(ip)
		for i := 0; i < numAddresses; i++ {
			newIP := make(net.IP, 4)
			binary.BigEndian.PutUint32(newIP, start+uint32(i))
			ips = append(ips, newIP)
		}
		return ips, nil
	}

	// For /30 and larger, exclude network and broadcast
	ips := make([]net.IP, 0, numAddresses-2)
	ip := ipNet.IP.To4()
	start := binary.BigEndian.Uint32(ip)

	for i := 1; i < numAddresses-1; i++ {
		newIP := make(net.IP, 4)
		binary.BigEndian.PutUint32(newIP, start+uint32(i))
		ips = append(ips, newIP)
	}

	return ips, nil
}

// ValidateCIDRForScanning validates if a CIDR can be scanned
func ValidateCIDRForScanning(cidr string) error {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}

	// Check if IPv6
	if ipNet.IP.To4() == nil {
		return fmt.Errorf("IPv6 subnets not supported for scanning")
	}

	// Calculate the number of addresses
	ones, bits := ipNet.Mask.Size()
	numAddresses := 1 << (bits - ones)

	// Exclude network and broadcast for standard subnets
	hostCount := numAddresses
	if ones < 31 {
		hostCount = numAddresses - 2
	}

	if hostCount > MaxScanAddresses {
		return fmt.Errorf("subnet too large: %d addresses (max %d)", hostCount, MaxScanAddresses)
	}

	return nil
}

// ScanSubnet scans all IPs in the given CIDR range
func (s *Scanner) ScanSubnet(ctx context.Context, cidr string, progressCb ProgressCallback) ([]ScanResult, error) {
	// Generate IPs
	ips, err := GenerateIPs(cidr)
	if err != nil {
		return nil, err
	}

	totalAddresses := int64(len(ips))
	if totalAddresses == 0 {
		return []ScanResult{}, nil
	}

	// Configure concurrency
	concurrency := int(s.config.Concurrency)
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > len(ips) {
		concurrency = len(ips)
	}

	// Configure timeout
	timeout := time.Duration(s.config.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// Results collection
	results := make([]ScanResult, 0, len(ips))
	resultsMu := sync.Mutex{}

	// Progress tracking
	var scannedCount int64
	var aliveCount int64

	// Work queue
	ipChan := make(chan net.IP, len(ips))
	for _, ip := range ips {
		ipChan <- ip
	}
	close(ipChan)

	// Worker pool
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ip, ok := <-ipChan:
					if !ok {
						return
					}

					result := s.scanIP(ctx, ip, timeout)

					// Update counters
					atomic.AddInt64(&scannedCount, 1)
					if result.Alive {
						atomic.AddInt64(&aliveCount, 1)
					}

					// Collect result
					resultsMu.Lock()
					results = append(results, result)
					resultsMu.Unlock()

					// Report progress
					if progressCb != nil {
						current := atomic.LoadInt64(&scannedCount)
						progress := int32(float64(current) / float64(totalAddresses) * 100)
						progressCb(ScanProgress{
							TotalAddresses: totalAddresses,
							ScannedCount:   current,
							AliveCount:     atomic.LoadInt64(&aliveCount),
							Progress:       progress,
						})
					}
				}
			}
		}()
	}

	// Wait for completion
	wg.Wait()

	// Check if cancelled
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return results, nil
}

// scanIP scans a single IP address using ICMP ping
func (s *Scanner) scanIP(ctx context.Context, ip net.IP, timeout time.Duration) ScanResult {
	address := ip.String()
	result := ScanResult{
		Address: address,
		Alive:   false,
	}

	result.Alive = pingICMP(ctx, address, timeout)

	// If alive and not skipping DNS, do reverse lookup
	if result.Alive && !s.config.SkipReverseDNS {
		result.Hostname = s.reverseDNS(address)
	}

	return result
}

// pingICMP sends an ICMP echo request and waits for a reply.
// Tries unprivileged UDP datagram sockets first, falls back to raw ICMP.
func pingICMP(ctx context.Context, address string, timeout time.Duration) bool {
	// Try unprivileged ICMP first (udp4 = SOCK_DGRAM, no root needed)
	conn, err := icmp.ListenPacket("udp4", "")
	privileged := false
	if err != nil {
		// Fallback to privileged raw ICMP (requires CAP_NET_RAW)
		conn, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			return false
		}
		privileged = true
	}
	defer conn.Close()

	dst, err := net.ResolveIPAddr("ip4", address)
	if err != nil {
		return false
	}

	// Build ICMP echo request
	id := os.Getpid() & 0xffff
	seq := rand.IntN(0xffff)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq,
			Data: []byte("ping"),
		},
	}

	msgBytes, err := msg.Marshal(nil)
	if err != nil {
		return false
	}

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	if dl := time.Now().Add(timeout); dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	// Send — udp4 needs UDPAddr, raw needs IPAddr
	var dstAddr net.Addr
	if privileged {
		dstAddr = dst
	} else {
		dstAddr = &net.UDPAddr{IP: dst.IP}
	}

	if _, err := conn.WriteTo(msgBytes, dstAddr); err != nil {
		return false
	}

	// Read reply
	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return false
		}

		reply, err := icmp.ParseMessage(1, buf[:n]) // proto 1 = ICMPv4
		if err != nil {
			continue
		}

		if reply.Type == ipv4.ICMPTypeEchoReply {
			// In unprivileged mode (udp4), the kernel mangles the echo ID
			// to an internal port number and demuxes replies per-socket,
			// so any EchoReply on this socket is ours.
			if !privileged {
				return true
			}
			// In privileged mode, verify ID/Seq since all replies come to one socket
			if echo, ok := reply.Body.(*icmp.Echo); ok {
				if echo.ID == id && echo.Seq == seq {
					return true
				}
			}
		}
	}
}

// reverseDNS performs a reverse DNS lookup using custom DNS servers if configured
func (s *Scanner) reverseDNS(address string) string {
	// Determine timeout
	timeout := time.Duration(s.config.DNSTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var names []string
	var err error

	if len(s.config.DNSServers) > 0 {
		// Use custom DNS servers
		names, err = s.reverseDNSWithServers(address, s.config.DNSServers, timeout)

		// Fall back to system DNS if configured and custom lookup failed
		if (err != nil || len(names) == 0) && s.config.UseSystemDNSFallback {
			names, err = net.LookupAddr(address)
		}
	} else {
		// Use system default resolver
		names, err = net.LookupAddr(address)
	}

	if err != nil || len(names) == 0 {
		return ""
	}

	// Return the first hostname, stripping trailing dot
	hostname := names[0]
	return strings.TrimSuffix(hostname, ".")
}

// reverseDNSWithServers performs reverse DNS using specified servers
func (s *Scanner) reverseDNSWithServers(address string, servers []string, timeout time.Duration) ([]string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: timeout,
			}
			// Try each DNS server
			for _, server := range servers {
				serverAddr := server
				if _, _, err := net.SplitHostPort(server); err != nil {
					serverAddr = net.JoinHostPort(server, "53")
				}
				conn, err := d.DialContext(ctx, "udp", serverAddr)
				if err == nil {
					return conn, nil
				}
			}
			return nil, net.UnknownNetworkError("no DNS server available")
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return resolver.LookupAddr(ctx, address)
}

// QuickScan performs a quick scan to just check if an IP is alive using ICMP ping
func QuickScan(ctx context.Context, address string, timeout time.Duration) bool {
	return pingICMP(ctx, address, timeout)
}

// GetHostAddressCount returns the number of host addresses in a CIDR
func GetHostAddressCount(cidr string) (int64, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("invalid CIDR: %w", err)
	}

	// Check if IPv6
	if ipNet.IP.To4() == nil {
		return 0, fmt.Errorf("IPv6 subnets not supported")
	}

	ones, bits := ipNet.Mask.Size()
	numAddresses := int64(1 << (bits - ones))

	// Exclude network and broadcast for standard subnets
	if ones < 31 {
		numAddresses -= 2
	}

	return numAddresses, nil
}
