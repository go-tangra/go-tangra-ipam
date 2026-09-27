package snmpcred

import (
	"fmt"
	"testing"
)

func TestResolve(t *testing.T) {
	// root -> child -> grandchild; lab -> labchild; orphan's parent is unknown.
	parents := map[string]string{
		"root": "", "child": "root", "grand": "child",
		"lab": "root", "labchild": "lab",
		"orphan": "gone", "loneroot": "",
		"cycA": "cycB", "cycB": "cycA",
	}
	own := map[string]bool{"root": true, "lab": true}
	cases := []struct {
		id, want string
		ok       bool
	}{
		{"root", "root", true},    // own wins
		{"child", "root", true},   // nearest ancestor
		{"grand", "root", true},   // two levels up
		{"labchild", "lab", true}, // own on child beats the root
		{"loneroot", "", false},   // no credentials anywhere
		{"orphan", "", false},     // parent missing from the map
		{"cycA", "", false},       // cycle guard
		{"unknown", "", false},    // subnet itself unknown
		{"", "", false},           // empty id
	}
	for _, c := range cases {
		got, ok := Resolve(c.id, parents, own)
		if got != c.want || ok != c.ok {
			t.Errorf("Resolve(%q) = %q,%v want %q,%v", c.id, got, ok, c.want, c.ok)
		}
	}
	// A grandchild under a child with its own credentials uses the child.
	own["child"] = true
	if got, _ := Resolve("grand", parents, own); got != "child" {
		t.Fatalf("grandchild source %q, want child", got)
	}
}

func TestResolveDepthLimit(t *testing.T) {
	parents := map[string]string{"n0": ""}
	for i := 1; i <= MaxDepth+1; i++ {
		parents[fmt.Sprintf("n%d", i)] = fmt.Sprintf("n%d", i-1)
	}
	own := map[string]bool{"n0": true}
	// n64 reaches n0 in exactly MaxDepth steps.
	if got, ok := Resolve(fmt.Sprintf("n%d", MaxDepth), parents, own); !ok || got != "n0" {
		t.Fatalf("depth %d: %q %v", MaxDepth, got, ok)
	}
	// n65 would need MaxDepth+1 steps: refused.
	if _, ok := Resolve(fmt.Sprintf("n%d", MaxDepth+1), parents, own); ok {
		t.Fatal("walk beyond the depth limit must stop")
	}
}
