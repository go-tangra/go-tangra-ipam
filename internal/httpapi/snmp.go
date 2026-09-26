package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
)

// Body bounds of the subnet SNMP credential writes (contracts/ipam-http.md).
const (
	snmpSetBytes  = 4 << 10
	snmpTestBytes = 1 << 10
)

// failSNMP maps credential errors: a rejected field is 422 naming the field
// (detail.field, plus detail.fields in the UI kit's form shape) and never the
// value; a missing envelope is 503; the rest go through failSvc.
func failSNMP(w http.ResponseWriter, err error) {
	var fe *snmpcred.FieldError
	switch {
	case errors.As(err, &fe):
		WriteDetail(w, ErrValidation, map[string]any{"field": fe.Field, "message": fe.Msg, "fields": map[string]string{fe.Field: fe.Msg}})
	case errors.Is(err, snmpcred.ErrNoEnvelope):
		WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
	default:
		failSvc(w, err)
	}
}

// registerSNMP mounts the write-only subnet SNMP credential routes (feature
// 021). Permissions are enforced by the gateway from the OpenAPI
// extensions; every handler scopes to the caller's tenant. No response ever
// carries a community, v3 user or password.
func (s *Server) registerSNMP(d Deps) {
	p := ipamBase + "/subnets/{id}/snmp"
	s.MustHandle("GET", p, func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := d.Subnets.GetSNMP(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSNMP(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	s.MustHandle("PUT", p, func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in snmpcred.Input
		if err := DecodeJSON(r, &in, snmpSetBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Subnets.SetSNMP(r.Context(), subj, r.PathValue("id"), in)
		if err != nil {
			failSNMP(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
}
