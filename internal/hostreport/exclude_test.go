package hostreport

import (
	"errors"
	"strings"
	"testing"
)

func TestExcluded(t *testing.T) {
	cases := []struct {
		name, kind string
		want       bool
	}{
		{"docker0", KindBridge, true},
		{"br-1234", "", true},
		{"veth9a", KindVirtual, true},
		{"eth0", KindEthernet, false},
		{"lo", KindLoopback, true},
		{"loopback-ish", KindLoopback, true},
		{"vmbr0", KindBridge, false},
		{"tap100i0", "", true},
	}
	for _, c := range cases {
		if got := Excluded(c.name, c.kind, DefaultExclusions); got != c.want {
			t.Errorf("Excluded(%q,%q) = %v", c.name, c.kind, got)
		}
	}
	if !Excluded("eth9", KindEthernet, []string{"eth?"}) || Excluded("eth10", KindEthernet, []string{"eth?"}) {
		t.Fatal("path.Match semantics")
	}
	if Excluded("x", "", []string{"[bad"}) {
		t.Fatal("a bad pattern never matches")
	}
}

func TestValidatePatterns(t *testing.T) {
	if err := ValidatePatterns(DefaultExclusions); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePatterns(nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{""}, {"a b"}, {"[x]"}, {`a\b`}, {strings.Repeat("a", MaxPatternLen+1)}, {"eth0;rm"}} {
		if err := ValidatePatterns(bad); !errors.Is(err, ErrPattern) {
			t.Errorf("ValidatePatterns(%q) = %v", bad, err)
		}
	}
	many := make([]string, MaxPatterns+1)
	for i := range many {
		many[i] = "x"
	}
	if err := ValidatePatterns(many); !errors.Is(err, ErrPattern) {
		t.Fatal("too many patterns")
	}
}
