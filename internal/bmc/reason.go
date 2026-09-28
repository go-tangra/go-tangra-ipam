package bmc

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// Failure reasons (closed set, contracts/ipam-http.md).
const (
	ReasonNotConfigured     = "bmc_not_configured"
	ReasonNoAddress         = "bmc_no_address"
	ReasonForbidden         = "bmc_secret_forbidden"
	ReasonSecretNotFound    = "bmc_secret_not_found" // #nosec G101 -- a reason code, not a credential
	ReasonWardenUnavailable = "warden_unavailable"
	ReasonBMCUnreachable    = "bmc_unreachable"
	ReasonBMCAuthFailed     = "bmc_auth_failed"
	ReasonBMCError          = "bmc_error"
	// KVM console logins (the BMC web UI): a user with two-factor login
	// enabled cannot be logged in automatically; the BMC's web session
	// slots can be exhausted by other sessions.
	ReasonBMC2FARequired  = "bmc_2fa_required"
	ReasonBMCSessionLimit = "bmc_session_limit"
)

// Reason names a failure of the decision flow (reference, address, warden)
// or a classified BMC failure. It returns "" for nil and for every other
// error (authorization, missing device, storage), which the caller maps
// through its generic error path.
func Reason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotConfigured):
		return ReasonNotConfigured
	case errors.Is(err, ErrNoAddress):
		return ReasonNoAddress
	case errors.Is(err, warden.ErrForbidden):
		return ReasonForbidden
	case errors.Is(err, warden.ErrNotFound), errors.Is(err, warden.ErrEmptyRef):
		return ReasonSecretNotFound
	case errors.Is(err, warden.ErrUnavailable):
		return ReasonWardenUnavailable
	case errors.Is(err, ipmi.ErrUnreachable), errors.Is(err, kvm.ErrUnreachable):
		return ReasonBMCUnreachable
	case errors.Is(err, ipmi.ErrAuthFailed), errors.Is(err, kvm.ErrAuthFailed):
		return ReasonBMCAuthFailed
	case errors.Is(err, kvm.ErrTwoFactor):
		return ReasonBMC2FARequired
	case errors.Is(err, kvm.ErrSessionLimit):
		return ReasonBMCSessionLimit
	case errors.Is(err, kvm.ErrLogin):
		return ReasonBMCError
	}
	return ""
}

// BMCReason names the failure of a call to the BMC itself (IPMI or the KVM
// console's web login): unreachable, authentication failed, two-factor login
// required, session limit, or any other BMC error. An unknown power verb is
// the caller's input error ("").
func BMCReason(err error) string {
	switch {
	case err == nil, errors.Is(err, ipmi.ErrUnknownAction):
		return ""
	case errors.Is(err, ipmi.ErrUnreachable), errors.Is(err, ipmi.ErrAuthFailed),
		errors.Is(err, kvm.ErrUnreachable), errors.Is(err, kvm.ErrAuthFailed),
		errors.Is(err, kvm.ErrTwoFactor), errors.Is(err, kvm.ErrSessionLimit):
		return Reason(err)
	}
	return ReasonBMCError
}

// HTTPStatus is the response status of a reason (500 for "").
func HTTPStatus(reason string) int {
	switch reason {
	case ReasonNotConfigured, ReasonNoAddress, ReasonSecretNotFound, ReasonBMC2FARequired:
		return http.StatusConflict
	case ReasonForbidden:
		return http.StatusForbidden
	case ReasonWardenUnavailable:
		return http.StatusServiceUnavailable
	case ReasonBMCUnreachable:
		return http.StatusGatewayTimeout
	case ReasonBMCAuthFailed, ReasonBMCError, ReasonBMCSessionLimit:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// BMCSide reports whether the reason comes from the BMC itself (the response
// then names the BMC address).
func BMCSide(reason string) bool {
	switch reason {
	case ReasonBMCUnreachable, ReasonBMCAuthFailed, ReasonBMCError, ReasonBMC2FARequired, ReasonBMCSessionLimit:
		return true
	}
	return false
}

// Outcome is the audit outcome of a reason: ok, refused (a precondition, the
// BMC's two-factor policy or warden said no) or error (something failed).
func Outcome(reason string) string {
	switch reason {
	case "":
		return audit.OutcomeOK
	case ReasonNotConfigured, ReasonNoAddress, ReasonForbidden, ReasonSecretNotFound, ReasonBMC2FARequired:
		return audit.OutcomeRefused
	}
	return audit.OutcomeError
}
