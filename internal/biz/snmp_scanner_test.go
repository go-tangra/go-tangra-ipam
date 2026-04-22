package biz

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// fakeTimeout satisfies net.Error with Timeout() == true so we can exercise
// the timeout branch of shouldFallbackToDES without opening a real socket.
type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestShouldFallbackToDES(t *testing.T) {
	baseV3 := SNMPConfig{
		Version:      3,
		User:         "admin",
		AuthPassword: "authpass",
		PrivPassword: "privpass",
		AuthProtocol: "SHA",
		PrivProtocol: "AES",
	}

	tests := []struct {
		name string
		cfg  SNMPConfig
		err  error
		want bool
	}{
		{"nil error", baseV3, nil, false},
		{"v2c ignored", SNMPConfig{Version: 2, Community: "public"}, errors.New("boom"), false},
		{"v3 with DES already", func() SNMPConfig { c := baseV3; c.PrivProtocol = "DES"; return c }(), errors.New("boom"), false},
		{"v3 without priv password", func() SNMPConfig { c := baseV3; c.PrivPassword = ""; return c }(), errors.New("boom"), false},
		{"context cancelled", baseV3, context.Canceled, false},
		{"context deadline", baseV3, context.DeadlineExceeded, false},
		{"net timeout wrapped", baseV3, &net.OpError{Op: "read", Err: fakeTimeout{}}, false},
		{"timeout substring", baseV3, errors.New("request timeout after 3 retries"), false},
		{"generic auth failure", baseV3, errors.New("wrong digest"), true},
		{"decryption error", baseV3, errors.New("decryption error"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldFallbackToDES(tc.cfg, tc.err)
			if got != tc.want {
				t.Fatalf("shouldFallbackToDES(err=%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// Ensure resolveTimeout honours custom values — regression guard for the
// logging line that prints it alongside priv protocol.
func TestResolveTimeout(t *testing.T) {
	if got := resolveTimeout(SNMPConfig{TimeoutMs: 1500}); got != 1500*time.Millisecond {
		t.Fatalf("want 1.5s, got %v", got)
	}
	if got := resolveTimeout(SNMPConfig{}); got != defaultSNMPTimeout {
		t.Fatalf("want default, got %v", got)
	}
}
