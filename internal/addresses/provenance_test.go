package addresses

import (
	"context"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestManualMACProvenance (022 FR-005/D4): a user-provided MAC is "manual"
// and clears a MAC conflict; an unchanged MAC keeps its provenance; an empty
// one clears the MAC and its source; server-owned fields in the body are
// ignored.
func TestManualMACProvenance(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newSvc(t)
	sid := makeSubnet(t, st, "10.0.0.0/24", "")
	seen := time.Now().UTC()
	a, err := svc.Create(ctx, subj(), store.IPAddress{SubnetID: sid, Address: "10.0.0.5", MACAddress: "0A-5C-D2-F1-00-05",
		MACSource: store.MACSourceARP, MACSourceDeviceID: "r1", MACSeenAt: &seen, MACConflict: "x", Origin: store.OriginARP,
		Link: &store.AddressLink{SwitchID: "sw", PortID: "p1"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.MACAddress != "0a:5c:d2:f1:00:05" || a.MACSource != store.MACSourceManual || a.MACSourceDeviceID != "" ||
		a.MACConflict != "" || a.Origin != "" || a.Link != nil || a.MACSeenAt == nil {
		t.Fatalf("create %+v", a)
	}
	b, err := svc.Create(ctx, subj(), store.IPAddress{SubnetID: sid, Address: "10.0.0.6", MACSource: store.MACSourceManual})
	if err != nil || b.MACSource != "" || b.MACSeenAt != nil {
		t.Fatalf("no mac %+v %v", b, err)
	}

	// An ARP-learned MAC with a conflict note, edited by a user.
	if err := st.ApplyARP(ctx, "t1", []store.ARPOp{{Kind: store.ARPFill, AddressID: b.ID, MAC: "0a:5c:d2:f1:00:06", SourceDeviceID: "r1", At: seen}}, nil); err != nil {
		t.Fatal(err)
	}
	// Same MAC in another notation: provenance kept (editing the description
	// does not turn an ARP MAC into a manual one).
	in := b
	in.MACAddress, in.Description, in.MACSource = "0a5c.d2f1.0006", "printer", store.MACSourceManual
	u, err := svc.Update(ctx, subj(), in)
	if err != nil || u.MACSource != store.MACSourceARP || u.MACSourceDeviceID != "r1" || u.MACAddress != "0a:5c:d2:f1:00:06" || u.Description != "printer" {
		t.Fatalf("unchanged mac %+v %v", u, err)
	}
	// A new MAC: manual, conflict and ARP device cleared.
	in = u
	in.MACAddress = "0a:5c:d2:f1:00:99"
	u, err = svc.Update(ctx, subj(), in)
	if err != nil || u.MACSource != store.MACSourceManual || u.MACSourceDeviceID != "" || u.MACConflict != "" {
		t.Fatalf("new mac %+v %v", u, err)
	}
	// Cleared.
	in = u
	in.MACAddress = ""
	u, err = svc.Update(ctx, subj(), in)
	if err != nil || u.MACSource != "" || u.MACAddress != "" || u.MACSeenAt != nil {
		t.Fatalf("cleared %+v %v", u, err)
	}
	// An unparsable MAC is stored as typed (legacy behaviour) and is manual.
	in = u
	in.MACAddress = "not-a-mac"
	u, err = svc.Update(ctx, subj(), in)
	if err != nil || u.MACSource != store.MACSourceManual || u.MACAddress != "not-a-mac" {
		t.Fatalf("free-form mac %+v %v", u, err)
	}
}
