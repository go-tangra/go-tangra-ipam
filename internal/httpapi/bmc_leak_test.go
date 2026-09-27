package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra/v4/freyatest/testrt"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// leakToken is a platform token distinctive enough to search for.
const leakToken = "tok-LEAK-7f3c9a"

// TestBMCCredentialsNeverLeak (024 T043, SR-002, SC-003): attaching a secret,
// the status, every out-of-band route on success and on every failure (with
// BMC errors that echo the password), backup export, events, audit rows and
// the module log never contain the BMC password or the user's token.
func TestBMCCredentialsNeverLeak(t *testing.T) {
	const pw = "BMC-pw-LEAK-4411"
	f := newAPI(t)
	var logs bytes.Buffer
	f.s.rt.(*testrt.Runtime).Log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.warden.Put(bmcRef, warden.SecretMeta{Name: "zax-5 IPMI", Username: "ADMIN", HostURL: "lanplus://bmc:623"}, pw)
	did := f.newDevice(t, `{"name":"node-1","device_type":"server","management_ip":"10.1.112.14"}`)

	var bodies []string
	record := func(method, path, body string) {
		t.Helper()
		w := f.req(t, method, p+path, leakToken, body)
		bodies = append(bodies, method+" "+path+" -> "+w.Body.String())
	}
	record("PUT", "/devices/"+did+"/bmc", `{"reference":"`+bmcRef+`"}`)
	record("GET", "/devices/"+did+"/bmc", "")
	record("GET", "/devices/"+did, "")
	record("GET", "/devices", "")
	oob := func() {
		for _, rt := range oobRoutes {
			record(rt.method, "/devices/"+did+rt.suffix, rt.body)
		}
	}
	oob()
	for _, e := range []error{
		fmt.Errorf("%w: rakp2 authcode mismatch for password %s", ipmi.ErrAuthFailed, pw),
		fmt.Errorf("%w: read udp 10.1.112.14:623 (%s)", ipmi.ErrUnreachable, pw),
		errors.New("completion code 0xc1 user ADMIN pw " + pw),
	} {
		f.bmc.Err = e
		oob()
	}
	f.bmc.Err = nil
	f.warden.Deny(leakToken, bmcRef)
	oob()
	f.warden.SetUnavailable(true)
	oob()
	f.warden.SetUnavailable(false)
	record("POST", "/backup/export", `{"include_secrets":true}`)
	record("DELETE", "/devices/"+did+"/bmc", "")

	if f.bmc.Calls == 0 || f.bmc.LastCreds.Password != pw {
		t.Fatal("the BMC never received the warden password (test would be vacuous)")
	}
	f.pub.mu.Lock()
	events := append([]string(nil), f.pub.payloads...)
	f.pub.mu.Unlock()
	var audit []string
	for _, a := range f.mem.Audit() {
		b, _ := json.Marshal(a)
		audit = append(audit, string(b))
	}
	if len(audit) < 4 {
		t.Fatalf("expected reference and power audit rows, got %d", len(audit))
	}
	if !strings.Contains(logs.String(), "ipam oob operation") {
		t.Fatal("no module log captured (test would be vacuous)")
	}
	for name, texts := range map[string][]string{"response": bodies, "event": events, "audit": audit, "log": {logs.String()}} {
		for _, txt := range texts {
			for _, secret := range []string{pw, leakToken} {
				if strings.Contains(txt, secret) {
					t.Fatalf("%s leaks %q: %s", name, secret, txt)
				}
			}
		}
	}
}
