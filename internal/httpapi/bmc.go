package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/bmc"
)

// bmcSetBytes bounds the body of PUT /devices/{id}/bmc.
const bmcSetBytes = 1 << 10

// writeBMCReason answers a BMC failure reason with its documented status; a
// BMC-side reason names the BMC address (never a credential).
func writeBMCReason(w http.ResponseWriter, reason, address string) {
	if bmc.BMCSide(reason) && address != "" {
		WriteDetail(w, &Error{bmc.HTTPStatus(reason), reason}, map[string]any{"address": address})
		return
	}
	WriteError(w, bmc.HTTPStatus(reason), reason)
}

// failBMCRef maps errors of the reference routes: a malformed reference is
// 422 naming the field, a secret warden does not know is 422 on save, the
// other warden outcomes use their reasons, the rest goes through failSvc.
func failBMCRef(w http.ResponseWriter, err error) {
	switch reason := bmc.Reason(err); {
	case errors.Is(err, bmc.ErrInvalidReference):
		WriteDetail(w, ErrValidation, map[string]any{"field": "reference", "message": "must be a warden secret id",
			"fields": map[string]string{"reference": "must be a warden secret id"}})
	case reason == bmc.ReasonSecretNotFound:
		WriteError(w, http.StatusUnprocessableEntity, reason)
	case reason != "":
		writeBMCReason(w, reason, "")
	default:
		failSvc(w, err)
	}
}

// registerBMC mounts the device BMC reference routes (feature 024). The
// status is computed for the viewer (warden metadata read as that user); set
// validates the secret against warden for the acting user before storing the
// pointer; both writes are audited. Permissions come from the OpenAPI
// extensions (ipam:read / devices:manage).
func (s *Server) registerBMC(d Deps) {
	path := ipamBase + "/devices/{id}/bmc"
	available := func(w http.ResponseWriter) bool {
		if d.BMCRefs == nil {
			WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
			return false
		}
		return true
	}
	s.MustHandle("GET", path, func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if !available(w) {
			return
		}
		st, err := d.BMCRefs.Status(userContext(r), subj, r.PathValue("id"))
		if err != nil {
			failBMCRef(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	s.MustHandle("PUT", path, func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if !available(w) {
			return
		}
		var in bmc.Input
		if err := DecodeJSON(r, &in, bmcSetBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		st, err := d.BMCRefs.Set(userContext(r), subj, r.PathValue("id"), in.Reference)
		if err != nil {
			failBMCRef(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	s.MustHandle("DELETE", path, func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if !available(w) {
			return
		}
		if err := d.BMCRefs.Clear(r.Context(), subj, r.PathValue("id")); err != nil {
			failBMCRef(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
