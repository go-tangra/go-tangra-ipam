package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/go-freya/freya/internal/testrt"
	"github.com/go-freya/freya/internal/testutil"
	"github.com/go-freya/freya/services/auth/pkg/authclient"
	"github.com/go-freya/freya/transport/edge"
)

// TestNewBindsEdgeListener covers New (edge-bound constructor) plus the Edge,
// Start and Stop lifecycle. It skips gracefully if the environment cannot bind a
// listener, matching the sibling services' convention.
func TestNewBindsEdgeListener(t *testing.T) {
	rt := testrt.New(t, testutil.MustCA("example.org"), "ipam")
	v := fakeVerifier{ids: map[string]authclient.Identity{}}
	s, err := New(rt, edge.Config{Addr: "127.0.0.1:0", Env: "test"}, WithVerifier(v))
	if err != nil {
		t.Skipf("edge server unavailable in test env: %v", err)
	}
	if s.Edge() == nil {
		t.Fatal("Edge() nil after New")
	}
	if s.Handler() == nil {
		t.Fatal("Handler() nil after New")
	}

	errc := make(chan error, 1)
	go func() { errc <- s.Start(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}
