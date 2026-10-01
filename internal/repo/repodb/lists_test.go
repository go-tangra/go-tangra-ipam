package repodb

import (
	"strings"
	"testing"
)

func TestLikeTerm(t *testing.T) {
	for in, want := range map[string]string{
		"web":     "web",
		"10%":     `10\%`,
		"a_b":     `a\_b`,
		`c:\x`:    `c:\\x`,
		"":        "",
		"Ünï_%\\": `Ünï\_\%\\`,
	} {
		if got := likeTerm(in); got != want {
			t.Fatalf("likeTerm(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", maxSearchLen+50)
	if got := likeTerm(long); len([]rune(got)) != maxSearchLen {
		t.Fatalf("capped to %d runes", len([]rune(got)))
	}
}
