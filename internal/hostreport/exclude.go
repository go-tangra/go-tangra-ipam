package hostreport

import (
	"errors"
	"fmt"
	"path"
	"regexp"
)

// Exclusion bounds (settings, research D10).
const (
	MaxPatterns   = 64
	MaxPatternLen = 64
)

// DefaultExclusions are the container/virtual bridge interfaces a tenant does
// not record unless it edits its settings (FR-022).
var DefaultExclusions = []string{
	"docker*", "br-*", "veth*", "virbr*", "cni*", "flannel*", "cali*", "weave*", "vxlan*",
	"kube-*", "cilium*", "podman*", "fwbr*", "fwpr*", "fwln*", "tap*", "vnet*",
}

// ErrPattern is an invalid exclusion list.
var ErrPattern = errors.New("hostreport: invalid exclusion pattern")

// patternRe is the allowed charset. It contains no '[' or '\', so every
// accepted pattern is also a syntactically valid path.Match pattern.
var patternRe = regexp.MustCompile(`^[A-Za-z0-9*?._:-]{1,64}$`)

// ValidatePatterns checks an exclusion list: at most MaxPatterns patterns of
// the allowed charset and length.
func ValidatePatterns(patterns []string) error {
	if len(patterns) > MaxPatterns {
		return fmt.Errorf("%w: more than %d patterns", ErrPattern, MaxPatterns)
	}
	for _, p := range patterns {
		if !patternRe.MatchString(p) {
			return fmt.Errorf("%w: %q", ErrPattern, p)
		}
	}
	return nil
}

// Excluded reports whether an interface is not recorded: loopback interfaces
// always, others when their name matches one of the tenant's patterns (Go
// path.Match semantics).
func Excluded(name, kind string, patterns []string) bool {
	if kind == KindLoopback {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}
