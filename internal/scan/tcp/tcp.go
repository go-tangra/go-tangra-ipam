// Package tcp is the active TCP port prober for the IPAM scan executor. The real
// implementation dials each port with net.DialTimeout under a bounded worker
// pool; the Fake returns a configured open-port set so orchestration is
// unit-testable without touching the network. Dialer structurally satisfies the
// addresses.PortScanner prober interface.
package tcp

import (
	"context"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// maxConcurrency bounds the in-flight dials per Scan call.
const maxConcurrency = 64

// PortScanner probes a host's TCP ports for reachability.
type PortScanner interface {
	// Scan returns the subset of ports that accepted a connection within
	// timeout. A non-nil err signals a setup failure, not a closed port.
	Scan(ctx context.Context, ip string, ports []int, timeout time.Duration) (open []int, err error)
}

// Dialer is the real net.DialTimeout-backed PortScanner.
type Dialer struct{}

// NewDialer builds a real TCP port scanner.
func NewDialer() *Dialer { return &Dialer{} }

// Scan dials each port with bounded concurrency and returns the open ports
// sorted ascending.
func (d *Dialer) Scan(ctx context.Context, ip string, ports []int, timeout time.Duration) ([]int, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	conc := maxConcurrency
	if len(ports) < conc {
		conc = len(ports)
	}
	if conc <= 0 {
		return nil, nil
	}
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, conc)
	var dialer net.Dialer
	for _, port := range ports {
		select {
		case <-ctx.Done():
			wg.Wait()
			return sortedCopy(open), ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			defer func() { <-sem }()
			dctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			addr := net.JoinHostPort(ip, strconv.Itoa(port))
			conn, err := dialer.DialContext(dctx, "tcp", addr)
			if err != nil {
				return
			}
			_ = conn.Close()
			mu.Lock()
			open = append(open, port)
			mu.Unlock()
		}(port)
	}
	wg.Wait()
	return sortedCopy(open), nil
}

func sortedCopy(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := append([]int(nil), in...)
	sort.Ints(out)
	return out
}

// Fake is an in-memory PortScanner for tests, keyed by host IP.
type Fake struct {
	Open map[string][]int
	Err  error
}

// NewFake builds an empty Fake.
func NewFake() *Fake { return &Fake{Open: map[string][]int{}} }

// Set records the open ports for an IP.
func (f *Fake) Set(ip string, ports ...int) { f.Open[ip] = ports }

// Scan returns the configured open ports for ip intersected with the requested
// ports.
func (f *Fake) Scan(_ context.Context, ip string, ports []int, _ time.Duration) ([]int, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	want := make(map[int]bool, len(ports))
	for _, p := range ports {
		want[p] = true
	}
	var out []int
	for _, p := range f.Open[ip] {
		if len(ports) == 0 || want[p] {
			out = append(out, p)
		}
	}
	return sortedCopy(out), nil
}

// interface conformance.
var (
	_ PortScanner = (*Dialer)(nil)
	_ PortScanner = (*Fake)(nil)
)
