package kvm

import (
	"testing"
	"time"
)

func TestTokenStoreMintResolve(t *testing.T) {
	s := newTokenStore()
	tgt := bmcTarget{Host: "10.1.112.18", Username: "ADMIN", Password: "secret"}

	tok, err := s.mint("dev-1", tgt)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(tok) < 32 {
		t.Fatalf("token too short: %q", tok)
	}

	kt, ok := s.resolve(tok)
	if !ok {
		t.Fatal("expected token to resolve")
	}
	if kt.deviceID != "dev-1" || kt.target != tgt {
		t.Fatalf("resolved binding mismatch: %+v", kt)
	}

	if _, ok := s.resolve("nope"); ok {
		t.Fatal("unknown token should not resolve")
	}
	if _, ok := s.resolve(""); ok {
		t.Fatal("empty token should not resolve")
	}
}

func TestTokenStoreExpiry(t *testing.T) {
	now := time.Now()
	s := newTokenStore()
	s.now = func() time.Time { return now }

	tok, _ := s.mint("dev-1", bmcTarget{Host: "h", Username: "u", Password: "p"})
	if _, ok := s.resolve(tok); !ok {
		t.Fatal("token should be valid immediately")
	}

	now = now.Add(tokenTTL + time.Second)
	if _, ok := s.resolve(tok); ok {
		t.Fatal("token should be expired past TTL")
	}
}
