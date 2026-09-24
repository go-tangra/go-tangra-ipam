package warden

import (
	"context"
	"errors"
	"testing"
)

func TestFakeGetSecret(t *testing.T) {
	f := NewFake()
	f.Put("ref-1", map[string]string{"username": "admin", "password": "s3cr3t"},
		SecretMeta{Name: "bmc-a", Description: "rack A BMC", Username: "admin"})

	got, err := f.GetSecret(context.Background(), "ref-1")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got["username"] != "admin" || got["password"] != "s3cr3t" {
		t.Fatalf("unexpected secret map: %v", got)
	}

	// Mutating the returned map must not affect the stored copy.
	got["password"] = "tampered"
	again, _ := f.GetSecret(context.Background(), "ref-1")
	if again["password"] != "s3cr3t" {
		t.Fatalf("stored secret was mutated: %v", again)
	}
}

func TestFakeGetSecretErrors(t *testing.T) {
	f := NewFake()
	if _, err := f.GetSecret(context.Background(), ""); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("empty ref: want ErrEmptyRef, got %v", err)
	}
	if _, err := f.GetSecret(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ref: want ErrNotFound, got %v", err)
	}
}

func TestFakeListSecretsMetadataOnly(t *testing.T) {
	f := NewFake()
	f.Put("ref-a", map[string]string{"password": "p1"}, SecretMeta{Name: "snmp-core", Description: "core switch"})
	f.Put("ref-b", map[string]string{"password": "p2"}, SecretMeta{Name: "bmc-edge", Description: "edge BMC"})

	all, err := f.ListSecrets(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 metas, got %d", len(all))
	}
	for _, m := range all {
		// Metadata must never carry values; SecretMeta has no value field, and
		// the id must be set to the ref.
		if m.ID == "" {
			t.Fatalf("meta missing id: %+v", m)
		}
	}

	// Filtered query is case-insensitive substring over name/id/description.
	hits, _ := f.ListSecrets(context.Background(), "BMC")
	if len(hits) != 1 || hits[0].ID != "ref-b" {
		t.Fatalf("filtered query mismatch: %+v", hits)
	}
}

func TestGrpcClientFailsClosed(t *testing.T) {
	// The real client is inert until wired; it must fail closed, never fabricate.
	c := New(nil)
	if _, err := c.GetSecret(context.Background(), ""); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("empty ref should be rejected first, got %v", err)
	}
	if _, err := c.GetSecret(context.Background(), "ref"); err == nil {
		t.Fatalf("GetSecret must error while unwired")
	}
	if _, err := c.ListSecrets(context.Background(), ""); err == nil {
		t.Fatalf("ListSecrets must error while unwired")
	}
}

func TestInterfaceConformance(t *testing.T) {
	var _ Client = NewFake()
	var _ Client = New(nil)
}
