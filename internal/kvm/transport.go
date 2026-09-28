package kvm

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// Console transport limits (ported from v3). Older Supermicro BMC web servers
// service about one request at a time, while the HTML5 viewer loads a dozen
// scripts at once: without a per-host bound the BMC drops all but a few and
// the viewer never starts. Idempotent loads are retried on transient 5xx.
const (
	maxConcurrentPerHost = 2
	maxAttempts          = 4
	retryBackoff         = 120 * time.Millisecond
)

// limitTransport bounds concurrent requests per BMC host and retries failed
// GET/HEAD requests. Safe for concurrent use.
type limitTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	sems map[string]chan struct{}
}

func newLimitTransport(base http.RoundTripper) *limitTransport {
	return &limitTransport{base: base, sems: map[string]chan struct{}{}}
}

func (t *limitTransport) hostSem(host string) chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sems[host]
	if s == nil {
		s = make(chan struct{}, maxConcurrentPerHost)
		t.sems[host] = s
	}
	return s
}

// RoundTrip implements http.RoundTripper.
func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sem := t.hostSem(req.URL.Host)
	select {
	case sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	defer func() { <-sem }()

	idempotent := req.Method == http.MethodGet || req.Method == http.MethodHead
	for attempt := 1; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if (err == nil && resp.StatusCode < 500) || !idempotent || attempt == maxAttempts {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		select {
		case <-time.After(time.Duration(attempt) * retryBackoff):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}
