package snmpcred

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
)

func testEnv(t *testing.T, b byte) *sealed.Envelope {
	t.Helper()
	env, err := sealed.NewEnvelope(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestSealOpenRoundTrip(t *testing.T) {
	env := testEnv(t, 7)
	cases := map[string]struct {
		in   Input
		want Secret
	}{
		"v2c":        {Input{Version: 2, Community: "c0mm 'ü'"}, Secret{Community: "c0mm 'ü'"}},
		"authNoPriv": {v3(LevelAuthNoPriv), Secret{User: "lab", AuthPassword: "authpass1"}},
		"authPriv":   {v3(LevelAuthPriv), Secret{User: "lab", AuthPassword: "authpass1", PrivPassword: "privpass1"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			blob, err := Seal(env, "t1", "s1", c.in)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []string{c.want.Community, c.want.User, c.want.AuthPassword, c.want.PrivPassword} {
				if v != "" && bytes.Contains(blob, []byte(v)) {
					t.Fatalf("blob carries %q in clear", v)
				}
			}
			got, err := Open(env, "t1", "s1", blob)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
			// The sealed document holds only the secret values, never metadata.
			raw, err := env.Open(blob, sealed.ADSNMP("t1", "s1"))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for k := range doc {
				switch k {
				case "community", "user", "auth_password", "priv_password":
				default:
					t.Fatalf("metadata key %q in the sealed document", k)
				}
			}
		})
	}
}

func TestOpenRejectsMovedOrTamperedBlob(t *testing.T) {
	env := testEnv(t, 7)
	blob, err := Seal(env, "t1", "s1", Input{Version: 2, Community: "c"})
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() error{
		"other subnet": func() error { _, e := Open(env, "t1", "s2", blob); return e },
		"other tenant": func() error { _, e := Open(env, "t2", "s1", blob); return e },
		"other KEK":    func() error { _, e := Open(testEnv(t, 8), "t1", "s1", blob); return e },
		"tampered": func() error {
			bad := append([]byte{}, blob...)
			bad[len(bad)-1] ^= 1
			_, e := Open(env, "t1", "s1", bad)
			return e
		},
	} {
		if err := open(); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: want ErrUnreadable, got %v", name, err)
		}
	}
}

// fakeSealer drives the error branches the real envelope cannot reach.
type fakeSealer struct {
	sealErr error
	opened  []byte
}

func (f fakeSealer) Seal(_, _ []byte) ([]byte, error) { return nil, f.sealErr }
func (f fakeSealer) Open(_, _ []byte) ([]byte, error) { return f.opened, nil }

func TestSealOpenErrorBranches(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Seal(fakeSealer{sealErr: boom}, "t", "s", Input{Version: 2, Community: "c"}); !errors.Is(err, boom) {
		t.Fatalf("seal error: %v", err)
	}
	if _, err := Open(fakeSealer{opened: []byte("not json")}, "t", "s", []byte{1}); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("garbage document: %v", err)
	}
	if _, err := Open(nil, "t", "s", []byte{1}); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("no envelope: %v", err)
	}
	if _, err := Seal(nil, "t", "s", Input{Version: 2, Community: "c"}); !errors.Is(err, ErrNoEnvelope) {
		t.Fatalf("seal without envelope: %v", err)
	}
}
