// Package icmp is the active ICMP liveness prober for the IPAM scan executor.
// It hides two capabilities behind interfaces — a single-host Pinger and a
// bounded, concurrent Sweeper — so the scan orchestration and its authz can be
// unit-tested against a Fake without ever opening a raw socket or touching the
// network.
//
// The real implementation (Raw) uses one shared raw ICMPv4 socket
// (icmp.ListenPacket("ip4:icmp","0.0.0.0"), which needs CAP_NET_RAW) with a
// single receiver goroutine that keys echo replies by their source address and
// wakes the matching sender. Opening the socket may fail when the process lacks
// CAP_NET_RAW; NewRaw returns the client anyway and every Ping/Sweep then fails
// closed with ErrNoSocket rather than silently reporting hosts as down.
//
// The Raw path is exercised only in a real deployment; the module's unit tests
// use Fake. Raw structurally satisfies the addresses.Pinger prober interface.
package icmp

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	xnicmp "golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ianaProtocolICMP is the IP protocol number for ICMPv4, used when parsing
// received messages.
const ianaProtocolICMP = 1

// defaultTimeout bounds a single ping when the caller supplies no deadline.
const defaultTimeout = time.Second

// Pinger probes a single host for ICMP liveness.
type Pinger interface {
	// Ping reports whether ip answered an echo request and the round-trip time
	// in milliseconds. A silent (timed-out) host returns alive=false, err=nil; a
	// non-nil err signals a socket/setup failure, not host-down.
	Ping(ctx context.Context, ip string) (alive bool, rttMs int, err error)
}

// Sweeper probes many hosts concurrently and returns the alive set.
type Sweeper interface {
	// Sweep pings every ip with at most concurrency in flight, each bounded by
	// timeout, and returns the addresses that answered. A non-nil err signals a
	// socket/setup failure; individual silent hosts are simply absent from alive.
	Sweep(ctx context.Context, ips []string, concurrency int, timeout time.Duration) (alive map[string]bool, err error)
}

// ErrNoSocket is returned by Raw when the shared raw ICMP socket could not be
// opened (typically because the process lacks CAP_NET_RAW).
var ErrNoSocket = errors.New("icmp: raw socket unavailable (needs CAP_NET_RAW)")

// Raw is the shared-socket ICMP prober. It is safe for concurrent use.
type Raw struct {
	conn    *xnicmp.PacketConn
	openErr error
	id      int

	mu      sync.RWMutex
	waiters map[string]chan time.Time // dst IP -> reply notification (receive time)

	closeOnce sync.Once
	done      chan struct{}
}

// NewRaw opens the shared raw ICMP socket and starts the receiver goroutine.
// It always returns a usable *Raw: if the socket could not be opened the client
// records the failure and every Ping/Sweep returns ErrNoSocket.
func NewRaw() *Raw {
	r := &Raw{
		id:      os.Getpid() & 0xffff,
		waiters: make(map[string]chan time.Time),
		done:    make(chan struct{}),
	}
	conn, err := xnicmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		r.openErr = ErrNoSocket
		return r
	}
	r.conn = conn
	go r.receive()
	return r
}

// receive reads echo replies off the shared socket and wakes the waiter keyed
// by the reply's source address. It runs until Close.
func (r *Raw) receive() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-r.done:
			return
		default:
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, peer, err := r.conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			select {
			case <-r.done:
				return
			default:
				continue
			}
		}
		msg, err := xnicmp.ParseMessage(ianaProtocolICMP, buf[:n])
		if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*xnicmp.Echo)
		if !ok || echo.ID != r.id {
			continue
		}
		src := ipString(peer)
		at := time.Now()
		r.mu.RLock()
		ch := r.waiters[src]
		r.mu.RUnlock()
		if ch != nil {
			select {
			case ch <- at:
			default:
			}
		}
	}
}

// Ping sends one echo request to ip and waits for a reply. The wait is bounded
// by the context deadline when present, otherwise by defaultTimeout.
func (r *Raw) Ping(ctx context.Context, ip string) (bool, int, error) {
	if r.openErr != nil {
		return false, 0, r.openErr
	}
	timeout := defaultTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			timeout = d
		}
	}
	return r.pingOnce(ctx, ip, timeout)
}

// pingOnce registers a waiter, sends one echo request and waits for the reply,
// the timeout, or context cancellation.
func (r *Raw) pingOnce(ctx context.Context, ip string, timeout time.Duration) (bool, int, error) {
	dst, err := net.ResolveIPAddr("ip4", ip)
	if err != nil {
		return false, 0, err
	}
	key := dst.IP.String()
	ch := make(chan time.Time, 1)
	r.mu.Lock()
	r.waiters[key] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.waiters, key)
		r.mu.Unlock()
	}()

	msg := xnicmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &xnicmp.Echo{ID: r.id, Seq: 1, Data: []byte("freya-ipam")},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false, 0, err
	}
	start := time.Now()
	if _, err := r.conn.WriteTo(b, dst); err != nil {
		return false, 0, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case at := <-ch:
		rtt := int(at.Sub(start).Milliseconds())
		if rtt < 0 {
			rtt = 0
		}
		return true, rtt, nil
	case <-timer.C:
		return false, 0, nil
	case <-ctx.Done():
		return false, 0, ctx.Err()
	}
}

// Sweep pings ips with bounded concurrency and returns the alive set.
func (r *Raw) Sweep(ctx context.Context, ips []string, concurrency int, timeout time.Duration) (map[string]bool, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	alive := make(map[string]bool, len(ips))
	var mu sync.Mutex
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, ip := range ips {
		select {
		case <-ctx.Done():
			wg.Wait()
			return alive, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, _, err := r.pingOnce(ctx, ip, timeout)
			if err != nil {
				return
			}
			if ok {
				mu.Lock()
				alive[ip] = true
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	return alive, nil
}

// Close stops the receiver goroutine and closes the shared socket. It is safe
// to call more than once and on a Raw whose socket never opened.
func (r *Raw) Close() error {
	r.closeOnce.Do(func() {
		close(r.done)
		if r.conn != nil {
			_ = r.conn.Close()
		}
	})
	return nil
}

// ipString extracts the source IP string from a received packet's address.
func ipString(addr net.Addr) string {
	switch a := addr.(type) {
	case *net.IPAddr:
		return a.IP.String()
	case *net.UDPAddr:
		return a.IP.String()
	default:
		return addr.String()
	}
}

// Fake is an in-memory Pinger/Sweeper for tests: it reports the configured
// addresses as alive and never touches the network.
type Fake struct {
	Alive map[string]bool
	RTTms int
	Err   error // when set, Ping/Sweep return this error
}

// NewFake builds a Fake whose given addresses are alive.
func NewFake(alive ...string) *Fake {
	m := make(map[string]bool, len(alive))
	for _, a := range alive {
		m[a] = true
	}
	return &Fake{Alive: m, RTTms: 1}
}

// Ping reports the configured liveness for ip.
func (f *Fake) Ping(_ context.Context, ip string) (bool, int, error) {
	if f.Err != nil {
		return false, 0, f.Err
	}
	if f.Alive[ip] {
		return true, f.RTTms, nil
	}
	return false, 0, nil
}

// Sweep returns the configured alive subset of ips.
func (f *Fake) Sweep(_ context.Context, ips []string, _ int, _ time.Duration) (map[string]bool, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make(map[string]bool, len(ips))
	for _, ip := range ips {
		if f.Alive[ip] {
			out[ip] = true
		}
	}
	return out, nil
}

// interface conformance.
var (
	_ Pinger  = (*Raw)(nil)
	_ Sweeper = (*Raw)(nil)
	_ Pinger  = (*Fake)(nil)
	_ Sweeper = (*Fake)(nil)
)
