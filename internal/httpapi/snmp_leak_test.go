package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// TestSNMPCredentialsNeverLeak (T055, SC-003): after setting v2c and v3
// credentials, every subnet/status/test/scan/backup response, every event
// payload, every audit row and every log line of a scan run is free of the
// community, the v3 user and both passwords.
func TestSNMPCredentialsNeverLeak(t *testing.T) {
	const (
		community = "c0mm-LEAK-S3CRET"
		user      = "v3user-LEAK"
		authPass  = "v3auth-LEAK-pw"
		privPass  = "v3priv-LEAK-pw"
	)
	secrets := []string{community, user, authPass, privPass}
	f := newAPI(t)
	parent := f.createSubnet(t, "site", "10.1.0.0/16")
	w := f.req(t, "POST", p+"/subnets", "admin", `{"name":"mgmt","cidr":"10.1.112.0/28","parent_id":"`+parent+`"}`)
	child, _ := decodeBody(t, w)["id"].(string)
	core := f.createSubnet(t, "core", "10.2.0.0/28")

	var bodies []string
	record := func(method, path, tok, body string) {
		t.Helper()
		w := f.req(t, method, p+path, tok, body)
		if w.Code >= 500 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		bodies = append(bodies, method+" "+path+" -> "+w.Body.String())
	}
	record("PUT", "/subnets/"+parent+"/snmp", "admin", `{"version":2,"community":"`+community+`"}`)
	record("PUT", "/subnets/"+core+"/snmp", "admin", fmt.Sprintf(`{"version":3,"user":%q,"security_level":"authPriv","auth_protocol":"SHA256","auth_password":%q,"priv_protocol":"AES256","priv_password":%q}`, user, authPass, privPass))
	// Replace (audited with the previous version) and a rejected replace.
	record("PUT", "/subnets/"+core+"/snmp", "admin", fmt.Sprintf(`{"version":3,"user":%q,"security_level":"authPriv","auth_protocol":"SHA512","auth_password":%q,"priv_protocol":"AES256","priv_password":%q}`, user, authPass, privPass))
	record("PUT", "/subnets/"+core+"/snmp", "admin", fmt.Sprintf(`{"version":3,"user":%q,"security_level":"authNoPriv","auth_protocol":"SHA512","auth_password":%q,"priv_password":%q}`, user, authPass, privPass))
	for _, id := range []string{parent, child, core} {
		record("GET", "/subnets/"+id+"/snmp", "user", "")
		record("GET", "/subnets/"+id, "user", "")
	}
	record("GET", "/subnets", "user", "")
	record("GET", "/subnets/tree", "user", "")

	// Devices answer, reject, or echo a credential in their error text.
	f.snmp.Set("10.1.112.2", snmpDevice("sw-1", "Cisco IOS"))
	f.snmp.Fail("10.1.112.3", gosnmp.ErrWrongDigest)
	f.snmp.Fail("10.1.112.4", fmt.Errorf("agent echoed %s", community))
	f.snmp.Fail("10.2.0.4", fmt.Errorf("usm: user %s pass %s/%s", user, authPass, privPass))
	for _, a := range []string{"10.1.112.2", "10.1.112.3", "10.1.112.4", "10.2.0.4"} {
		f.sweeper.Alive[a] = true
	}
	record("POST", "/subnets/"+child+"/snmp/test", "admin", `{"address":"10.1.112.2"}`)
	record("POST", "/subnets/"+child+"/snmp/test", "admin", `{"address":"10.1.112.4"}`)
	record("POST", "/subnets/"+core+"/snmp/test", "admin", `{"address":"10.2.0.4"}`)
	record("POST", "/ip-scans", "admin", `{"subnet_id":"`+child+`","enable_snmp":true}`)
	record("POST", "/ip-scans", "admin", `{"subnet_id":"`+core+`","enable_snmp":true}`)

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if _, err := f.scan.RunOnce(context.Background(), log); err != nil {
		t.Fatal(err)
	}
	record("GET", "/ip-scans", "user", "")
	record("POST", "/backup/export", "admin", `{"include_secrets":true}`)
	record("DELETE", "/subnets/"+core+"/snmp", "admin", "")

	// The scans ran SNMP with the effective credentials (the sweep is real).
	if !strings.Contains(bodies[len(bodies)-3], `"snmp_status":"ran"`) {
		t.Fatalf("scans did not run SNMP: %s", bodies[len(bodies)-3])
	}
	seen := f.snmp.Seen()
	if len(seen) == 0 {
		t.Fatal("no SNMP call reached the client")
	}

	f.pub.mu.Lock()
	events := append([]string(nil), f.pub.payloads...)
	f.pub.mu.Unlock()
	var audit []string
	for _, a := range f.mem.Audit() {
		b, _ := json.Marshal(a)
		audit = append(audit, string(b))
	}
	if len(audit) < 6 {
		t.Fatalf("expected set/replace/test/clear audit rows, got %d", len(audit))
	}
	for name, texts := range map[string][]string{"response": bodies, "event": events, "audit": audit, "log": {logs.String()}} {
		for _, txt := range texts {
			for _, s := range secrets {
				if strings.Contains(txt, s) {
					t.Fatalf("%s leaks %q: %s", name, s, txt)
				}
			}
		}
	}
	if !strings.Contains(logs.String(), "[REDACTED]") {
		t.Fatalf("the echoed credential was not scrubbed from the log: %s", logs.String())
	}
}
