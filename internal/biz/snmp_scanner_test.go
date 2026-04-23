package biz

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
		// Legacy devices that don't support AES silently drop the request,
		// which the client sees as a gosnmp "request timeout". These MUST
		// trigger the DES fallback.
		{"request timeout", baseV3, errors.New("request timeout (after 1 retries)"), true},
		{"generic auth failure", baseV3, errors.New("wrong digest"), true},
		{"decryption error", baseV3, errors.New("decryption error"), true},
		{"not in time window", baseV3, errors.New("not in time window"), true},
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
