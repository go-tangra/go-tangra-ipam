package bmc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzReferenceInput (024 T010): the strict decode of the PUT body plus
// ValidReference never panics, and every accepted reference is a canonical
// 36-character UUID that survives a JSON round trip unchanged.
func FuzzReferenceInput(f *testing.F) {
	for _, s := range []string{`{"reference":"01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"}`, `{"reference":""}`, `{}`, `{"reference":1}`,
		`{"reference":"../x"}`, `{"reference":"01928F7E-3C1A-7B44-9D2E-5A6B7C8D9E0F","x":1}`, `null`, `[]`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		var in Input
		dec := json.NewDecoder(bytes.NewReader([]byte(body)))
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil {
			return
		}
		if !ValidReference(in.Reference) {
			return
		}
		ref := CanonicalReference(in.Reference)
		if len(ref) != 36 || ref != strings.ToLower(ref) || !ValidReference(ref) {
			t.Fatalf("accepted %q -> %q", in.Reference, ref)
		}
		b, _ := json.Marshal(Input{Reference: ref})
		var back Input
		if err := json.Unmarshal(b, &back); err != nil || back.Reference != ref {
			t.Fatalf("round trip %q", ref)
		}
	})
}
