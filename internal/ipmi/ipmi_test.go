package ipmi

import (
	"context"
	"errors"
	"testing"
)

func TestFakePowerRecordsActionsAndState(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	creds := Creds{Username: "admin", Password: "secret"}

	if err := f.Power(ctx, "10.0.0.1", creds, ActionOff); err != nil {
		t.Fatalf("Power off: %v", err)
	}
	st, err := f.PowerStatus(ctx, "10.0.0.1", creds)
	if err != nil {
		t.Fatalf("PowerStatus: %v", err)
	}
	if st.On {
		t.Fatal("expected chassis off after ActionOff")
	}
	if err := f.Power(ctx, "10.0.0.1", creds, ActionOn); err != nil {
		t.Fatalf("Power on: %v", err)
	}
	if st, _ := f.PowerStatus(ctx, "10.0.0.1", creds); !st.On {
		t.Fatal("expected chassis on after ActionOn")
	}
	if len(f.Actions) != 2 || f.Actions[0] != ActionOff || f.Actions[1] != ActionOn {
		t.Fatalf("actions = %v", f.Actions)
	}
	// The fake captures the host/creds it was called with (for authz assertions
	// in higher layers) but never exposes them to the browser.
	if f.LastHost != "10.0.0.1" || f.LastCreds.Password != "secret" {
		t.Fatalf("last call not captured: %s %+v", f.LastHost, f.LastCreds)
	}
}

func TestUnknownActionRejected(t *testing.T) {
	f := NewFake()
	if err := f.Power(context.Background(), "h", Creds{}, "explode"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("err = %v, want ErrUnknownAction", err)
	}
}

func TestControlForCoversAllVerbs(t *testing.T) {
	for _, a := range []string{ActionOn, ActionOff, ActionCycle, ActionReset, ActionSoft, ActionDiag} {
		if _, ok := controlFor(a); !ok {
			t.Errorf("controlFor(%q) not mapped", a)
		}
	}
	if _, ok := controlFor("nope"); ok {
		t.Error("controlFor(nope) should be unmapped")
	}
}
