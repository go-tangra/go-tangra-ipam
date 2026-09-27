package ipmi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "read udp: i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestClassify(t *testing.T) {
	if Classify(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	unreachable := []error{
		context.DeadlineExceeded,
		fmt.Errorf("ipmi: connect h: %w", timeoutErr{}),
		fmt.Errorf("exchange: %w", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}),
		errors.New("ipmi: connect 10.0.0.1: client exchange failed, err: read udp 10.0.0.2:5000->10.0.0.1:623: i/o timeout"),
		errors.New("dial udp: connect: no route to host"),
		errors.New("write udp: network is unreachable"),
		errors.New("recv: connection refused"),
	}
	for _, e := range unreachable {
		if got := Classify(e); !errors.Is(got, ErrUnreachable) {
			t.Errorf("Classify(%q) = %v, want unreachable", e, got)
		}
	}
	auth := []error{
		errors.New("ipmi: connect h: rakp status code error: (0x0d) Unauthorized name"),
		errors.New("validate rakp2 message failed, err: rakp2 authcode not equal"),
		errors.New("rakp4 returned integrity check not passed"),
		errors.New("the return status of rakp2 has error: Invalid role"),
		errors.New("activate session: invalid password"),
		errors.New("Unauthorized role or privilege level requested"),
	}
	for _, e := range auth {
		if got := Classify(e); !errors.Is(got, ErrAuthFailed) {
			t.Errorf("Classify(%q) = %v, want auth failed", e, got)
		}
	}
	other := errors.New("ipmi: get chassis status: completion code 0xc1")
	if got := Classify(other); got != other {
		t.Fatalf("other error changed: %v", got)
	}
	// Already classified errors pass through unchanged.
	if got := Classify(ErrAuthFailed); got != ErrAuthFailed {
		t.Fatalf("classified: %v", got)
	}
	if got := Classify(fmt.Errorf("x: %w", ErrUnreachable)); !errors.Is(got, ErrUnreachable) {
		t.Fatalf("classified wrap: %v", got)
	}
}

func TestFakeCountsCallsAndInjectsClasses(t *testing.T) {
	f := NewFake()
	if f.Calls != 0 {
		t.Fatal("fresh fake has calls")
	}
	f.Err = ErrAuthFailed
	if _, err := f.PowerStatus(context.Background(), "h", Creds{}); !errors.Is(err, ErrAuthFailed) {
		t.Fatal(err)
	}
	_, _ = f.Sensors(context.Background(), "h", Creds{})
	_, _ = f.SEL(context.Background(), "h", Creds{})
	_, _ = f.Info(context.Background(), "h", Creds{})
	_ = f.Power(context.Background(), "h", Creds{}, ActionOn)
	if f.Calls != 5 {
		t.Fatalf("calls = %d", f.Calls)
	}
}
