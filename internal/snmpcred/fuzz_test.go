package snmpcred

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
)

// FuzzDecodeValidate: strict JSON decode + Validate never panic, and every
// accepted input round-trips through Seal/Open unchanged.
func FuzzDecodeValidate(f *testing.F) {
	for _, s := range []string{
		`{"version":2,"community":"public"}`,
		`{"version":3,"user":"lab","security_level":"authNoPriv","auth_protocol":"SHA256","auth_password":"authpass1"}`,
		`{"version":3,"user":"lab","security_level":"authPriv","auth_protocol":"SHA","auth_password":"authpass1","priv_protocol":"AES256","priv_password":"privpass1"}`,
		`{"version":2}`, `{"version":9,"x":1}`, `[]`, `null`, `{"version":"2"}`,
	} {
		f.Add([]byte(s))
	}
	env, err := sealed.NewEnvelope(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var in Input
		if dec.Decode(&in) != nil {
			return
		}
		if in.Validate() != nil {
			return
		}
		blob, err := Seal(env, "t", "s", in)
		if err != nil {
			t.Fatalf("seal accepted input: %v", err)
		}
		got, err := Open(env, "t", "s", blob)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got != secretOf(in) {
			t.Fatalf("round trip %+v != %+v", got, secretOf(in))
		}
	})
}
