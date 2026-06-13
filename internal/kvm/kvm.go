package kvm

import (
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
)

// kvmCookie carries the session token across the BMC viewer's relative asset
// and WebSocket requests (which can't carry a query param).
const kvmCookie = "ipam_kvm"

// Service is the BMC KVM reverse-proxy. The authenticated gRPC layer calls
// StartSession (after a platform-admin check) to mint a token bound to a
// device's BMC + credentials; the HTTP handler (mounted at /bmc/) validates the
// token and proxies the BMC's HTML5 console under our origin.
type Service struct {
	log      *log.Helper
	sessions *sessionManager
	tokens   *tokenStore
	mux      *http.ServeMux
}

// NewService constructs the KVM service. Single instance shared between the
// gRPC RPC (mint) and the HTTP server (proxy).
func NewService(ctx *bootstrap.Context) *Service {
	logger := ctx.NewLoggerHelper("ipam/kvm")
	s := &Service{
		log:      logger,
		sessions: newSessionManager(logger),
		tokens:   newTokenStore(),
		mux:      http.NewServeMux(),
	}
	s.mux.HandleFunc("/bmc/{id}/__kvmws", s.handleConsoleWS)
	// IPMI LAN (UDP 623) management endpoints — distinct __ipmi/ prefix so they
	// are not proxied to the BMC web UI by the catch-all below.
	s.mux.HandleFunc("/bmc/{id}/__ipmi/info", s.handleIpmiInfo)
	s.mux.HandleFunc("/bmc/{id}/__ipmi/power", s.handleIpmiPower)
	s.mux.HandleFunc("/bmc/{id}/__ipmi/sensors", s.handleIpmiSensors)
	s.mux.HandleFunc("/bmc/{id}/__ipmi/sel", s.handleIpmiSel)
	s.mux.HandleFunc("/bmc/{id}/{path...}", s.handleConsoleProxy)
	return s
}

// StartSession mints a short-lived KVM token bound to the device's BMC host and
// credentials, returning the token. The caller must have verified authorization
// (platform admin) before calling.
func (s *Service) StartSession(deviceID, host, username, password string) (string, error) {
	return s.tokens.mint(deviceID, bmcTarget{Host: host, Username: username, Password: password})
}

// Handler returns the http.Handler for the /bmc/ proxy routes. Mount it on the
// module's HTTP server (bypasses gRPC transcoding); access is gated by the
// session token, not the gateway.
func (s *Service) Handler() http.Handler { return s.mux }

// tokenFor resolves the request's KVM token (query param on the bootstrap, then
// cookie for subsequent asset/WS requests) and checks it matches the path id.
func (s *Service) tokenFor(r *http.Request) (kvmToken, bool) {
	tok := r.URL.Query().Get("kvmtoken")
	if tok == "" {
		if c, err := r.Cookie(kvmCookie); err == nil {
			tok = c.Value
		}
	}
	kt, ok := s.tokens.resolve(tok)
	if !ok {
		return kvmToken{}, false
	}
	if kt.deviceID != r.PathValue("id") {
		return kvmToken{}, false
	}
	return kt, true
}
