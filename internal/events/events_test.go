package events

import (
	"context"
	"testing"

	"github.com/go-freya/freya/services/ipam/internal/stream"
)

func TestNilHubPublisherIsNoOp(t *testing.T) {
	// A zero-value publisher (nil hub) must not panic.
	var p HubPublisher
	p.Publish(context.Background(), "t1", IPAddressCreated, map[string]any{"x": 1})

	var pub Publisher = HubPublisher{}
	pub.Publish(context.Background(), "t1", ScanStarted, nil)
}

func TestHubPublisherPublishes(t *testing.T) {
	hub := stream.NewHub(stream.NewMemory(), stream.Config{}, nil)
	defer hub.Close()
	p := HubPublisher{Hub: hub}
	// Non-nil hub path: publish must reach the bus without error/panic.
	p.Publish(context.Background(), "t1", IPAddressScanned,
		IPAddressPayload(IPAddressScanned, "a1", "10.0.0.5", "s1", "host-1", "d1"))
}

func TestIPAddressPayload(t *testing.T) {
	p := IPAddressPayload(IPAddressCreated, "a1", "10.0.0.5", "s1", "host-1", "d1")
	if p["action"] != IPAddressCreated || p["id"] != "a1" || p["address"] != "10.0.0.5" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if p["subnet_id"] != "s1" || p["hostname"] != "host-1" || p["device_id"] != "d1" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	// The scanned action must survive so consumers can distinguish it.
	scanned := IPAddressPayload(IPAddressScanned, "a1", "10.0.0.5", "s1", "", "")
	if scanned["action"] != IPAddressScanned {
		t.Fatalf("scanned distinction lost: %+v", scanned)
	}
	assertNoSecrets(t, p)
}

func TestScanPayload(t *testing.T) {
	p := ScanPayload("j1", "s1", 12, 3)
	if p["job_id"] != "j1" || p["subnet_id"] != "s1" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if p["alive_count"] != int64(12) || p["new_count"] != int64(3) {
		t.Fatalf("counts wrong: %+v", p)
	}
	assertNoSecrets(t, p)
}

// assertNoSecrets guards the "payloads carry no credentials or secret refs"
// contract at the key level.
func assertNoSecrets(t *testing.T, p map[string]any) {
	t.Helper()
	for k := range p {
		switch k {
		case "credential", "secret", "snmp_secret_ref", "ipmi_secret_ref", "password", "owner", "contact":
			t.Errorf("payload leaks sensitive key %q", k)
		}
	}
}
