package biz

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
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

// ScanSubnet scans all IPs in the given CIDR range using a shared ICMP connection.
func (s *Scanner) ScanSubnet(ctx context.Context, cidr string, progressCb ProgressCallback) ([]ScanResult, error) {
	ips, err := GenerateIPs(cidr)
	if err != nil {
		return nil, err
	}

	totalAddresses := int64(len(ips))
	if totalAddresses == 0 {
		return []ScanResult{}, nil
	}

	timeout := time.Duration(s.config.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	concurrency := int(s.config.Concurrency)
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > len(ips) {
		concurrency = len(ips)
	}

	// Open a single shared ICMP connection
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("failed to open ICMP socket: %w", err)
	}
	defer conn.Close()

	// Track which IPs are alive — receiver writes, main reads after done
	alive := make(map[string]bool, len(ips))
	aliveMu := sync.Mutex{}

	// Set up waiters: each sender registers a channel keyed by target IP
	waiters := make(map[string]chan struct{}, len(ips))
	waitersMu := sync.RWMutex{}

	// Receiver goroutine: reads all ICMP replies and notifies waiters
	receiverCtx, cancelReceiver := context.WithCancel(ctx)
	defer cancelReceiver()

	go func() {
		buf := make([]byte, 1500)
		for {
			select {
			case <-receiverCtx.Done():
				return
			default:
			}

			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				continue
			}

			reply, err := icmp.ParseMessage(1, buf[:n])
			if err != nil {
				continue
			}

			if reply.Type != ipv4.ICMPTypeEchoReply {
				continue
			}

			// Extract source IP from peer address
			var srcIP string
			switch addr := peer.(type) {
			case *net.IPAddr:
				srcIP = addr.IP.String()
			case *net.UDPAddr:
				srcIP = addr.IP.String()
			default:
				continue
			}

			aliveMu.Lock()
			alive[srcIP] = true
			aliveMu.Unlock()

			// Notify waiter if one exists
			waitersMu.RLock()
			if ch, ok := waiters[srcIP]; ok {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			waitersMu.RUnlock()
		}
	}()

	// Progress tracking
	var scannedCount int64
	var aliveCounter int64

	// Results collection
	results := make([]ScanResult, 0, len(ips))
	resultsMu := sync.Mutex{}

	// Build ICMP echo template
	echoID := 1

	// Worker pool: sends pings and waits for reply or timeout
	ipChan := make(chan net.IP, len(ips))
	for _, ip := range ips {
		ipChan <- ip
	}
	close(ipChan)

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

					address := ip.String()

					// Register waiter
					ch := make(chan struct{}, 1)
					waitersMu.Lock()
					waiters[address] = ch
					waitersMu.Unlock()

					// Send ICMP echo
					msg := icmp.Message{
						Type: ipv4.ICMPTypeEcho,
						Code: 0,
						Body: &icmp.Echo{
							ID:   echoID,
							Seq:  0,
							Data: []byte("ping"),
						},
					}
					msgBytes, _ := msg.Marshal(nil)
					dst := &net.IPAddr{IP: ip}
					_, _ = conn.WriteTo(msgBytes, dst)

					// Wait for reply or timeout
					timer := time.NewTimer(timeout)
					isAlive := false
					select {
					case <-ch:
						isAlive = true
					case <-timer.C:
					case <-ctx.Done():
					}
					timer.Stop()

					// Clean up waiter
					waitersMu.Lock()
					delete(waiters, address)
					waitersMu.Unlock()

					// Build result
					result := ScanResult{
						Address: address,
						Alive:   isAlive,
					}

					if isAlive && !s.config.SkipReverseDNS {
						result.Hostname = s.reverseDNS(address)
					}

					atomic.AddInt64(&scannedCount, 1)
					if isAlive {
						atomic.AddInt64(&aliveCounter, 1)
					}

					resultsMu.Lock()
					results = append(results, result)
					resultsMu.Unlock()

					if progressCb != nil {
						current := atomic.LoadInt64(&scannedCount)
						progress := int32(float64(current) / float64(totalAddresses) * 100)
						progressCb(ScanProgress{
							TotalAddresses: totalAddresses,
							ScannedCount:   current,
							AliveCount:     atomic.LoadInt64(&aliveCounter),
							Progress:       progress,
						})
					}
				}
			}
		}()
	}

	wg.Wait()
	cancelReceiver()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return results, nil
}

// pingICMP sends a single ICMP echo request and waits for a reply.
// Requires CAP_NET_RAW capability (set via setcap + cap_add in docker-compose).
func pingICMP(ctx context.Context, address string, timeout time.Duration) bool {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false
	}
	defer conn.Close()

	dst, err := net.ResolveIPAddr("ip4", address)
	if err != nil {
		return false
	}

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   1,
			Seq:  0,
			Data: []byte("ping"),
		},
	}

	msgBytes, err := msg.Marshal(nil)
	if err != nil {
		return false
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.WriteTo(msgBytes, dst); err != nil {
		return false
	}

	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return false
		}

		// Check source IP matches target
		var srcIP string
		switch addr := peer.(type) {
		case *net.IPAddr:
			srcIP = addr.IP.String()
		case *net.UDPAddr:
			srcIP = addr.IP.String()
		}
		if srcIP != address {
			continue
		}

		reply, err := icmp.ParseMessage(1, buf[:n])
		if err != nil {
			continue
		}

		if reply.Type == ipv4.ICMPTypeEchoReply {
			return true
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

// CommonPorts are commonly used TCP ports to check for host presence
var CommonPorts = []int{22, 80, 443, 3389, 8080}

// QuickPortScan checks if any common TCP ports are open on the given address.
// Returns true if at least one port is open (address in use).
func QuickPortScan(ctx context.Context, address string, timeout time.Duration) bool {
	for _, port := range CommonPorts {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", address, port), timeout)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
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
