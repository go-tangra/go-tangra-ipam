package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostsync"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Body bounds of the host-sync writes (contracts/ipam-http.md).
const (
	hostSyncSettingsBytes = 16 << 10
	hostSyncActionBytes   = 1 << 10
)

// failHostSync maps host-sync errors to the contract's refusals; anything
// else goes through failSvc.
func failHostSync(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hostsync.ErrNotHostReported):
		WriteError(w, http.StatusConflict, "not_host_reported")
	case errors.Is(err, repo.ErrSyncDisabled):
		WriteError(w, http.StatusConflict, "host_sync_disabled")
	case errors.Is(err, invclient.ErrUnavailable):
		WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
	case errors.Is(err, invclient.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, hostsync.ErrValidation):
		WriteError(w, http.StatusBadRequest, "validation")
	default:
		failSvc(w, err)
	}
}

// decodeAction accepts an empty body or an empty JSON object (strict).
func decodeAction(r *http.Request) error {
	if r.ContentLength == 0 {
		return nil
	}
	var in struct{}
	return DecodeJSON(r, &in, hostSyncActionBytes)
}

// registerHostSync mounts the host-sync routes (US6). Permissions are
// enforced by the gateway from the OpenAPI extensions; every handler still
// scopes to the caller's tenant.
func (s *Server) registerHostSync(d Deps) {
	p := ipamBase
	h := func(fn func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin)) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			if d.HostSync == nil {
				WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
				return
			}
			fn(w, r, d.HostSync)
		}
	}
	s.MustHandle("GET", p+"/host-sync/settings", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := a.Settings(r.Context(), subj)
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("PUT", p+"/host-sync/settings", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		var in hostsync.SettingsInput
		if err := DecodeJSON(r, &in, hostSyncSettingsBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := a.UpdateSettings(r.Context(), subj, in)
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("GET", p+"/host-sync/status", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := a.Status(r.Context(), subj)
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("POST", p+"/host-sync/resync", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := decodeAction(r); err != nil {
			Fail(w, r, nil, err)
			return
		}
		if err := a.ResyncAll(r.Context(), subj); err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, map[string]any{"scheduled": true})
	}))
	s.MustHandle("GET", p+"/devices/{id}/host-sync", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		v, err := a.DeviceHostSync(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("POST", p+"/devices/{id}/host-sync", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := decodeAction(r); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := a.ResyncDevice(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
	s.MustHandle("GET", p+"/devices/{id}/guests", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		req, err := listquery.Parse(r.URL.Query(), store.GuestList)
		var le *listquery.Error
		if errors.As(err, &le) {
			WriteDetail(w, ErrValidation, map[string]any{"param": le.Param})
			return
		}
		v, total, applied, err := a.PageGuests(r.Context(), subj, r.PathValue("id"), req)
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, listquery.NewPage(v, total, applied))
	}))
	s.MustHandle("POST", p+"/ip-addresses/{id}/clear-conflict", h(func(w http.ResponseWriter, r *http.Request, a *hostsync.Admin) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := decodeAction(r); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := a.ClearConflict(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failHostSync(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}))
}
