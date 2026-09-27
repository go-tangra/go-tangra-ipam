package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/arpcfg"
)

// arpSettingsBytes bounds the ARP settings write (contracts/ipam-http.md).
const arpSettingsBytes = 16 << 10

// failARP maps a rejected settings field to 422 naming the field (never the
// value); the rest go through failSvc.
func failARP(w http.ResponseWriter, err error) {
	var fe *arpcfg.FieldError
	if errors.As(err, &fe) {
		WriteDetail(w, ErrValidation, map[string]any{"field": fe.Field, "message": fe.Msg, "fields": map[string]string{fe.Field: fe.Msg}})
		return
	}
	failSvc(w, err)
}

// registerARP mounts the per-tenant ARP settings routes (feature 022, US3).
// Permissions are enforced by the gateway from the OpenAPI extensions (read
// ipam:read, write subnets:manage); every handler scopes to the caller's
// tenant.
func (s *Server) registerARP(d Deps) {
	p := ipamBase
	h := func(fn func(w http.ResponseWriter, r *http.Request, a *arpcfg.Service)) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			if d.ARP == nil {
				WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
				return
			}
			fn(w, r, d.ARP)
		}
	}
	s.MustHandle("GET", p+"/arp/settings", h(func(w http.ResponseWriter, r *http.Request, a *arpcfg.Service) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := a.Get(r.Context(), subj)
		if err != nil {
			failARP(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("PUT", p+"/arp/settings", h(func(w http.ResponseWriter, r *http.Request, a *arpcfg.Service) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in arpcfg.Input
		if err := DecodeJSON(r, &in, arpSettingsBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := a.Update(r.Context(), subj, in)
		if err != nil {
			failARP(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
}

// parseMACQuery turns the address list's mac parameter into the lower-case
// hex search form (022 D9): separators ":-." and spaces are dropped and 2-12
// hex digits must remain. An empty parameter means no filter.
func parseMACQuery(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == ':' || c == '-' || c == '.' || c == ' ':
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			b.WriteRune(c)
		case c >= 'A' && c <= 'F':
			b.WriteRune(c + ('a' - 'A'))
		default:
			return "", false
		}
	}
	q := b.String()
	return q, len(q) >= 2 && len(q) <= 12
}
