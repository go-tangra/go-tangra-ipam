package snmp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/gosnmp/gosnmp"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestClassify(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("snmp: get system oids 10.0.0.1: %w", e) }
	cases := []struct {
		err  error
		want Outcome
	}{
		{nil, OutcomeOK},
		{wrap(errors.New("request timeout (after 1 retries)")), OutcomeNoResponse},
		{wrap(context.DeadlineExceeded), OutcomeNoResponse},
		{wrap(timeoutErr{}), OutcomeNoResponse},
		{wrap(errors.New("read udp 10.0.0.9:1->10.0.0.1:161: connection refused")), OutcomeNoResponse},
		{fmt.Errorf("snmp: no response from 10.0.0.1"), OutcomeNoResponse},
		{wrap(gosnmp.ErrUnknownUsername), OutcomeUnknownUser},
		{wrap(gosnmp.ErrWrongDigest), OutcomeAuthFailed},
		{wrap(gosnmp.ErrUnknownSecurityLevel), OutcomeAuthFailed},
		{wrap(gosnmp.ErrDecryption), OutcomePrivacyFailed},
		{wrap(errors.New("error decrypting ScopedPDU: truncated packet")), OutcomePrivacyFailed},
		{wrap(errors.New("something odd")), OutcomeError},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %q want %q", c.err, got, c.want)
		}
	}
	for o, want := range map[Outcome]bool{OutcomeAuthFailed: true, OutcomeUnknownUser: true, OutcomePrivacyFailed: true,
		OutcomeNoResponse: false, OutcomeError: false, OutcomeOK: false} {
		if o.Rejected() != want {
			t.Errorf("%s.Rejected() = %v", o, !want)
		}
	}
}

func TestFakeOutcomesAndProbe(t *testing.T) {
	f := NewFake()
	f.Set("10.0.0.1", DiscoveredDevice{SysName: "sw1", SysDescr: "Cisco IOS"})
	f.Fail("10.0.0.2", gosnmp.ErrWrongDigest)
	ctx := context.Background()
	creds := Creds{Version: 2, Community: "c"}
	name, descr, err := f.Probe(ctx, "10.0.0.1", creds)
	if err != nil || name != "sw1" || descr != "Cisco IOS" {
		t.Fatalf("probe ok: %q %q %v", name, descr, err)
	}
	if _, _, err := f.Probe(ctx, "10.0.0.2", creds); Classify(err) != OutcomeAuthFailed {
		t.Fatalf("probe auth failure: %v", err)
	}
	if _, err := f.Discover(ctx, "10.0.0.2", creds); Classify(err) != OutcomeAuthFailed {
		t.Fatalf("discover auth failure: %v", err)
	}
	if _, _, err := f.Probe(ctx, "10.0.0.3", creds); Classify(err) != OutcomeNoResponse {
		t.Fatalf("silent host: %v", err)
	}
	if got := f.Seen(); len(got) != 4 || got[0] != creds {
		t.Fatalf("recorded creds %v", got)
	}
	f.Err = errors.New("down")
	if _, _, err := f.Probe(ctx, "10.0.0.1", creds); err == nil {
		t.Fatal("global error")
	}
}
