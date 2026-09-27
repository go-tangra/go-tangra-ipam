package warden

import (
	"context"
	"errors"
	"testing"
)

func TestUserTokenContext(t *testing.T) {
	if UserToken(context.Background()) != "" {
		t.Fatal("empty context carries a token")
	}
	ctx := WithUserToken(context.Background(), " tok ")
	if UserToken(ctx) != "tok" {
		t.Fatalf("token = %q", UserToken(ctx))
	}
}

func TestFake(t *testing.T) {
	f := NewFake()
	f.Put(testRef, SecretMeta{Name: "bmc-a", Username: "admin", FolderPath: "/f", HostURL: "lan://h:1"}, "s3cr3t")
	ctx := WithUserToken(context.Background(), "alice")

	m, err := f.Meta(ctx, testRef)
	if err != nil || m.ID != testRef || m.Name != "bmc-a" || m.Username != "admin" {
		t.Fatalf("meta = %+v, %v", m, err)
	}
	cr, err := f.Credentials(ctx, testRef)
	if err != nil || cr.Username != "admin" || cr.Password != "s3cr3t" || cr.HostURL != "lan://h:1" {
		t.Fatalf("credentials: %v", err)
	}

	// Rotation is visible on the next fetch (no caching anywhere).
	f.SetPassword(testRef, "rotated")
	if cr, _ := f.Credentials(ctx, testRef); cr.Password != "rotated" {
		t.Fatal("rotation not visible")
	}
	f.SetPassword("01928f7e-3c1a-7b44-9d2e-111111111111", "ignored") // unknown ref: no-op

	// Per-token refusal.
	f.Deny("bob", testRef)
	bob := WithUserToken(context.Background(), "bob")
	if _, err := f.Meta(bob, testRef); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob meta: %v", err)
	}
	if _, err := f.Credentials(bob, testRef); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob credentials: %v", err)
	}

	// Missing token, empty ref, unknown secret.
	if _, err := f.Meta(context.Background(), testRef); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("no token: %v", err)
	}
	if _, err := f.Credentials(ctx, ""); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("empty ref: %v", err)
	}
	if _, err := f.Meta(ctx, "01928f7e-3c1a-7b44-9d2e-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	f.Remove(testRef)
	if _, err := f.Credentials(ctx, testRef); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed: %v", err)
	}

	// Availability switch.
	f.Put(testRef, SecretMeta{Name: "bmc-a"}, "x")
	f.SetUnavailable(true)
	if _, err := f.Meta(ctx, testRef); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable meta: %v", err)
	}
	if _, err := f.Credentials(ctx, testRef); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable credentials: %v", err)
	}
	f.SetUnavailable(false)

	calls := f.Calls()
	if len(calls) == 0 || calls[0] != (Call{Op: OpMeta, Ref: testRef, Token: "alice"}) {
		t.Fatalf("calls = %+v", calls)
	}
	var creds int
	for _, c := range calls {
		if c.Op == OpCredentials {
			creds++
		}
	}
	if creds == 0 || f.CredentialCalls() != creds {
		t.Fatalf("credential calls = %d / %d", creds, f.CredentialCalls())
	}
	f.ResetCalls()
	if len(f.Calls()) != 0 {
		t.Fatal("reset")
	}
}

func TestUnavailable(t *testing.T) {
	var c Client = Unavailable{}
	if _, err := c.Meta(context.Background(), testRef); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := c.Credentials(context.Background(), testRef); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
