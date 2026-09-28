package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
)

// bmcWeb is a fake BMC web UI for the KVM console login: the Redfish session
// login answers status/body (201 = a session with an X-Auth-Token), the
// legacy form login answers without a SID. A nil error reply means the BMC
// did not answer.
func bmcWeb(status int, body string) http.RoundTripper {
	return rtFunc(func(r *http.Request) (*http.Response, error) {
		if status == 0 {
			return nil, errors.New("connection refused")
		}
		h := http.Header{}
		code := http.StatusOK
		if r.URL.Path == "/redfish/v1/SessionService/Sessions" {
			code = status
			if status == http.StatusCreated {
				h.Set("X-Auth-Token", "web-token")
			}
		}
		return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestKVMSessionLoginReasons: the KVM session logs in to the BMC web UI when
// it starts; a refused login answers its reason (with the BMC address) and
// is audited, and neither the password nor the BMC session token leaks.
func TestKVMSessionLoginReasons(t *testing.T) {
	cases := []struct {
		name   string
		rt     http.RoundTripper
		status int
		reason string
	}{
		{"two factor", bmcWeb(http.StatusCreated, `{"Id":"1","Oem":{"Supermicro":{"TwoFAEnabled":true}}}`), 409, "bmc_2fa_required"},
		{"session limit", bmcWeb(http.StatusBadRequest, `{"error":{"@Message.ExtendedInfo":[{"MessageId":"Base.1.10.SessionLimitExceeded"}]}}`), 502, "bmc_session_limit"},
		{"rejected", bmcWeb(http.StatusUnauthorized, ""), 502, "bmc_auth_failed"},
		{"no login flow", bmcWeb(http.StatusNotFound, ""), 502, "bmc_error"},
		{"unreachable", bmcWeb(0, ""), 504, "bmc_unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIMut(t, nil, func(d *Deps) { d.KVM = kvm.NewManager(nil, 0, kvm.WithTransport(tc.rt)) })
			did := f.newDeviceWithBMC(t)
			w := f.req(t, "POST", p+"/devices/"+did+"/kvm-session", "admin", "")
			checkReason(t, w.Code, w.Body.String(), tc.status, tc.reason)
			if !strings.Contains(w.Body.String(), `"address":"10.99.0.10"`) || strings.Contains(w.Body.String(), "web-token") {
				t.Fatalf("body %s", w.Body)
			}
			assertNoCreds(t, w.Body.String())
			rows := f.auditRows(string(audit.KVMSessionStarted))
			if len(rows) != 1 || rows[0].Reason != tc.reason {
				t.Fatalf("audit %+v", rows)
			}
		})
	}
}
