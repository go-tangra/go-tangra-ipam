package snmpcred

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func v3(level string) Input {
	in := Input{Version: 3, User: "lab", SecurityLevel: level, AuthProtocol: "SHA256", AuthPassword: "authpass1"}
	if level == LevelAuthPriv {
		in.PrivProtocol, in.PrivPassword = "AES256", "privpass1"
	}
	return in
}

func TestValidate(t *testing.T) {
	long := strings.Repeat("x", MaxValueLen+1)
	max := strings.Repeat("é", MaxValueLen) // 256 runes, 512 bytes: allowed
	with := func(in Input, f func(*Input)) Input { f(&in); return in }
	cases := []struct {
		name  string
		in    Input
		field string // "" = valid
	}{
		{"v2c ok", Input{Version: 2, Community: "pub lic 'q' ü"}, ""},
		{"v2c max runes", Input{Version: 2, Community: max}, ""},
		{"v2c empty community", Input{Version: 2}, "community"},
		{"v2c too long", Input{Version: 2, Community: long}, "community"},
		{"v2c user", Input{Version: 2, Community: "c", User: "u"}, "user"},
		{"v2c level", Input{Version: 2, Community: "c", SecurityLevel: LevelAuthPriv}, "security_level"},
		{"v2c auth protocol", Input{Version: 2, Community: "c", AuthProtocol: "SHA"}, "auth_protocol"},
		{"v2c auth password", Input{Version: 2, Community: "c", AuthPassword: "x"}, "auth_password"},
		{"v2c priv protocol", Input{Version: 2, Community: "c", PrivProtocol: "AES"}, "priv_protocol"},
		{"v2c priv password", Input{Version: 2, Community: "c", PrivPassword: "x"}, "priv_password"},
		{"v3 authNoPriv ok", v3(LevelAuthNoPriv), ""},
		{"v3 authPriv ok", v3(LevelAuthPriv), ""},
		{"v3 community", with(v3(LevelAuthNoPriv), func(i *Input) { i.Community = "c" }), "community"},
		{"v3 missing user", with(v3(LevelAuthNoPriv), func(i *Input) { i.User = "" }), "user"},
		{"v3 user too long", with(v3(LevelAuthNoPriv), func(i *Input) { i.User = long }), "user"},
		{"v3 bad level", with(v3(LevelAuthNoPriv), func(i *Input) { i.SecurityLevel = "noAuthNoPriv" }), "security_level"},
		{"v3 unknown auth protocol", with(v3(LevelAuthNoPriv), func(i *Input) { i.AuthProtocol = "SHA1" }), "auth_protocol"},
		{"v3 short auth password", with(v3(LevelAuthNoPriv), func(i *Input) { i.AuthPassword = "1234567" }), "auth_password"},
		{"v3 long auth password", with(v3(LevelAuthNoPriv), func(i *Input) { i.AuthPassword = long }), "auth_password"},
		{"v3 authNoPriv with priv protocol", with(v3(LevelAuthNoPriv), func(i *Input) { i.PrivProtocol = "AES" }), "priv_protocol"},
		{"v3 authNoPriv with priv password", with(v3(LevelAuthNoPriv), func(i *Input) { i.PrivPassword = "privpass1" }), "priv_password"},
		{"v3 authPriv unknown priv protocol", with(v3(LevelAuthPriv), func(i *Input) { i.PrivProtocol = "3DES" }), "priv_protocol"},
		{"v3 authPriv short priv password", with(v3(LevelAuthPriv), func(i *Input) { i.PrivPassword = "short" }), "priv_password"},
		{"v3 authPriv long priv password", with(v3(LevelAuthPriv), func(i *Input) { i.PrivPassword = long }), "priv_password"},
		{"version 1", Input{Version: 1, Community: "c"}, "version"},
		{"version 4", Input{Version: 4}, "version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.field == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != c.field {
				t.Fatalf("want field %q, got %v", c.field, err)
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Fatalf("error must name the field: %q", err.Error())
			}
		})
	}
}

func TestProtocolSetsAndWeak(t *testing.T) {
	for _, p := range []string{"MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512"} {
		if !validAuth(p) {
			t.Errorf("auth %s must be supported", p)
		}
	}
	for _, p := range []string{"DES", "AES", "AES192", "AES256"} {
		if !validPriv(p) {
			t.Errorf("priv %s must be supported", p)
		}
	}
	for p, want := range map[string]bool{"MD5": true, "SHA": true, "DES": true, "SHA256": false, "AES": false, "AES256": false, "": false} {
		if Weak(p) != want {
			t.Errorf("Weak(%q) = %v", p, !want)
		}
	}
	if !(Meta{Version: 3, AuthProtocol: "SHA256", PrivProtocol: "DES"}).Weak() {
		t.Error("DES privacy makes the set weak")
	}
	if !(Meta{Version: 3, AuthProtocol: "MD5"}).Weak() {
		t.Error("MD5 makes the set weak")
	}
	if (Meta{Version: 3, AuthProtocol: "SHA512", PrivProtocol: "AES256"}).Weak() {
		t.Error("SHA-512/AES-256 is not weak")
	}
	if (Meta{Version: 2}).Weak() {
		t.Error("v2c carries no protocol flags")
	}
}

func TestMeta(t *testing.T) {
	m := v3(LevelAuthPriv).Meta()
	if m != (Meta{Version: 3, SecurityLevel: LevelAuthPriv, AuthProtocol: "SHA256", PrivProtocol: "AES256"}) {
		t.Fatalf("meta %+v", m)
	}
	if (Input{Version: 2, Community: "c"}).Meta() != (Meta{Version: 2}) {
		t.Fatal("v2c meta carries only the version")
	}
}

// TestInputNeverPrints: formatting or logging an Input or a Secret never
// yields a credential value (SR-004).
func TestInputNeverPrints(t *testing.T) {
	in := Input{Version: 3, User: "labuser", SecurityLevel: LevelAuthPriv, AuthProtocol: "SHA", AuthPassword: "authsecret", PrivProtocol: "AES", PrivPassword: "privsecret", Community: "commsecret"}
	sec := Secret{Community: "commsecret", User: "labuser", AuthPassword: "authsecret", PrivPassword: "privsecret"}
	var b strings.Builder
	log := slog.New(slog.NewTextHandler(&b, nil))
	log.Info("x", "in", in, "sec", sec)
	out := fmt.Sprintf("%v %+v %#v %s %v %+v %#v %s", in, in, in, in, sec, sec, sec, sec) + b.String()
	for _, v := range []string{"labuser", "authsecret", "privsecret", "commsecret"} {
		if strings.Contains(out, v) {
			t.Fatalf("value %q printed: %s", v, out)
		}
	}
}
