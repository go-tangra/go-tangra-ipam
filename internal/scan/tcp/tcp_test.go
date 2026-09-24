package tcp

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestFakeScanIntersectsRequestedPorts(t *testing.T) {
	f := NewFake()
	f.Set("10.0.0.1", 22, 443, 8080)
	open, err := f.Scan(context.Background(), "10.0.0.1", []int{22, 80, 443}, time.Second)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(open) != 2 || open[0] != 22 || open[1] != 443 {
		t.Fatalf("open = %v, want [22 443]", open)
	}
}

func TestDialerFindsOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	_, _ = fmtSscan(portStr, &port)

	d := NewDialer()
	open, err := d.Scan(context.Background(), "127.0.0.1", []int{port}, time.Second)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(open) != 1 || open[0] != port {
		t.Fatalf("open = %v, want [%d]", open, port)
	}
}

// fmtSscan is a tiny helper to avoid importing fmt only for one parse.
func fmtSscan(s string, p *int) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	*p = n
	return 1, nil
}
