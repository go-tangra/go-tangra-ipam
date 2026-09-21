package icmp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeSweepReportsConfiguredAlive(t *testing.T) {
	f := NewFake("10.0.0.1", "10.0.0.3")
	alive, err := f.Sweep(context.Background(), []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, 4, time.Second)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !alive["10.0.0.1"] || alive["10.0.0.2"] || !alive["10.0.0.3"] {
		t.Fatalf("unexpected alive set: %v", alive)
	}
}

func TestFakePing(t *testing.T) {
	f := NewFake("10.0.0.1")
	if ok, _, err := f.Ping(context.Background(), "10.0.0.1"); err != nil || !ok {
		t.Fatalf("Ping alive = %v, %v", ok, err)
	}
	if ok, _, err := f.Ping(context.Background(), "10.0.0.2"); err != nil || ok {
		t.Fatalf("Ping dead = %v, %v", ok, err)
	}
}

func TestFakeErrorPropagates(t *testing.T) {
	f := &Fake{Err: errors.New("boom")}
	if _, err := f.Sweep(context.Background(), []string{"10.0.0.1"}, 1, time.Second); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRawNeverNilAndClosable(t *testing.T) {
	// Opening the raw socket may fail without CAP_NET_RAW; NewRaw must still
	// return a usable client whose probes fail closed.
	r := NewRaw()
	if r == nil {
		t.Fatal("NewRaw returned nil")
	}
	if r.openErr != nil {
		if _, _, err := r.Ping(context.Background(), "10.0.0.1"); !errors.Is(err, ErrNoSocket) {
			t.Fatalf("Ping without socket err = %v, want ErrNoSocket", err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = r.Close() // idempotent
}
