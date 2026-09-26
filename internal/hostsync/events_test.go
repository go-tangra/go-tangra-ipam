package hostsync

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestEventsAfterCommitOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, _ := f.st.EnsureHostSyncSettings(ctx, tA)
	// Moved address: another device owns it.
	_ = f.st.CreateSubnet(ctx, store.Subnet{ID: "s", TenantID: tA, Name: "lan", CIDR: "10.0.0.0/24"})
	_ = f.st.CreateDevice(ctx, store.Device{ID: "other", TenantID: tA, Name: "other"})
	_ = f.st.CreateAddress(ctx, store.IPAddress{ID: "a", TenantID: tA, Address: "10.0.0.5", SubnetID: "s", DeviceID: "other"})
	if _, err := f.r.Apply(ctx, s, rep(tA, host1, "web-01", "10.0.0.5", t0, 1), TriggerPoll, "run"); err != nil {
		t.Fatal(err)
	}
	var actions []any
	for _, e := range f.pub.evs {
		if e.typ == "ipam.ip_address.updated" {
			actions = append(actions, e.payload.(map[string]any)["action"])
		}
	}
	if len(actions) != 1 || actions[0] != "moved" {
		t.Fatalf("moved event: %v", actions)
	}
	// Released on the next report.
	if _, err := f.r.Apply(ctx, s, rep(tA, host1, "web-01", "10.0.0.7", t0.Add(time.Minute), 2), TriggerPoll, "run"); err != nil {
		t.Fatal(err)
	}
	if f.pub.count("ipam.ip_address.created") != 1 {
		t.Fatal("created event")
	}
	// Rollback: nothing published.
	n := len(f.pub.evs)
	f.st.FailNext("tx.SaveDeviceState")
	if _, err := f.r.Apply(ctx, s, rep(tA, host1, "web-01", "10.0.0.8", t0.Add(2*time.Minute), 3), TriggerPoll, "run"); err == nil {
		t.Fatal("injected failure")
	}
	if len(f.pub.evs) != n {
		t.Fatal("events published for a rolled back apply")
	}
	if _, err := f.st.FindAddress(ctx, tA, "10.0.0.8"); err == nil {
		t.Fatal("rolled back address exists")
	}
	for _, e := range f.pub.evs {
		for k := range e.payload.(map[string]any) {
			if k == "mac_address" || k == "mac" {
				t.Fatal("events never carry MACs")
			}
		}
	}
}
