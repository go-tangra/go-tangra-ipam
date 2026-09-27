package snmpcred

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
)

func TestToCreds(t *testing.T) {
	c := ToCreds(Meta{Version: 2}, Secret{Community: "comm"}, 1500, 2)
	if c != (snmp.Creds{Version: 2, Community: "comm", TimeoutMs: 1500, Retries: 2}) {
		t.Fatalf("v2c creds %+v", c)
	}
	c = ToCreds(Meta{Version: 3, SecurityLevel: LevelAuthPriv, AuthProtocol: "SHA512", PrivProtocol: "AES192"},
		Secret{User: "u", AuthPassword: "a1234567", PrivPassword: "p1234567"}, 100, 0)
	want := snmp.Creds{Version: 3, SecurityLevel: LevelAuthPriv, User: "u", AuthProtocol: "SHA512", AuthPassword: "a1234567",
		PrivProtocol: "AES192", PrivPassword: "p1234567", TimeoutMs: 100}
	if c != want {
		t.Fatalf("v3 creds %+v", c)
	}
	c = ToCreds(Meta{Version: 3, SecurityLevel: LevelAuthNoPriv, AuthProtocol: "MD5"}, Secret{User: "u", AuthPassword: "a1234567"}, 0, 0)
	if c.SecurityLevel != LevelAuthNoPriv || c.PrivProtocol != "" || c.PrivPassword != "" {
		t.Fatalf("authNoPriv creds %+v", c)
	}
}

func TestScrub(t *testing.T) {
	sec := Secret{Community: "comm-XYZ", User: "labuser", AuthPassword: "authpass1", PrivPassword: "privpass1"}
	text := "snmp: labuser rejected comm-XYZ / authpass1 / privpass1 / " + base64.StdEncoding.EncodeToString([]byte("authpass1")) + "\nsecond line privpass1"
	out := Scrub(text, sec)
	for _, v := range []string{"comm-XYZ", "labuser", "authpass1", "privpass1", "second line", base64.StdEncoding.EncodeToString([]byte("authpass1"))} {
		if strings.Contains(out, v) {
			t.Fatalf("scrubbed text still has %q: %s", v, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") || !strings.HasPrefix(out, "snmp: ") {
		t.Fatalf("scrubbed text %q", out)
	}
	if got := Scrub("request timeout", Secret{}); got != "request timeout" {
		t.Fatalf("clean text changed: %q", got)
	}
}
