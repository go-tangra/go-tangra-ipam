package httpapi

import (
	"context"
	"net/http"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/bmc"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// userContext carries the caller's platform token so warden acts on behalf
// of the signed-in user (feature 024).
func userContext(r *http.Request) context.Context {
	return warden.WithUserToken(r.Context(), authclient.BearerToken(r.Header.Get("Authorization")))
}

// registerPower mounts the privileged out-of-band routes: chassis power
// status/control, sensor and SEL reads, and the KVM console session. Every one
// requires platform-admin (a tenant user is refused with 403), then bmc.Resolve
// decides — device, warden reference, BMC address, and the credentials warden
// releases for the signed-in user at this moment (feature 024). The BMC is
// contacted only after all of that passed. Failures answer the documented
// reasons; credentials never reach a response or a log. Power actions and KVM
// sessions are audit rows; reads are logged.
func (s *Server) registerPower(d Deps, p string) {
	s.MustHandle("GET", p+"/devices/{id}/power", func(w http.ResponseWriter, r *http.Request) {
		subj, tg, ok := s.resolveBMC(w, r, d)
		if !ok {
			return
		}
		state, err := d.BMC.PowerStatus(r.Context(), tg.Address, tg.IPMI())
		if err != nil {
			failBMCCall(w, err, tg.Address)
			return
		}
		s.logOOB(r, subj, "power.status", tg.Device.ID)
		WriteJSON(w, http.StatusOK, state)
	})
	s.MustHandle("POST", p+"/devices/{id}/power", func(w http.ResponseWriter, r *http.Request) {
		subj, ok := s.platformAdmin(w, r, d)
		if !ok {
			return
		}
		var in struct {
			Action string `json:"action"`
		}
		if err := DecodeJSON(r, &in, 0); err != nil {
			Fail(w, r, nil, err)
			return
		}
		id := r.PathValue("id")
		if !ipmi.ValidAction(in.Action) {
			d.BMCRefs.AuditAction(r.Context(), subj, audit.PowerAction, id, "", in.Action, "bad_request")
			WriteError(w, http.StatusBadRequest, "bad_request")
			return
		}
		tg, err := d.BMCRefs.Resolve(userContext(r), subj, id)
		if err != nil {
			if reason := bmc.Reason(err); reason != "" {
				d.BMCRefs.AuditAction(r.Context(), subj, audit.PowerAction, id, tg.Address, in.Action, reason)
			}
			failResolve(w, err, tg.Address)
			return
		}
		if err := d.BMC.Power(r.Context(), tg.Address, tg.IPMI(), in.Action); err != nil {
			d.BMCRefs.AuditAction(r.Context(), subj, audit.PowerAction, id, tg.Address, in.Action, bmc.BMCReason(err))
			failBMCCall(w, err, tg.Address)
			return
		}
		d.BMCRefs.AuditAction(r.Context(), subj, audit.PowerAction, id, tg.Address, in.Action, "")
		WriteJSON(w, http.StatusOK, map[string]any{"accepted": true, "action": in.Action})
	})
	s.MustHandle("GET", p+"/devices/{id}/sensors", func(w http.ResponseWriter, r *http.Request) {
		subj, tg, ok := s.resolveBMC(w, r, d)
		if !ok {
			return
		}
		readings, err := d.BMC.Sensors(r.Context(), tg.Address, tg.IPMI())
		if err != nil {
			failBMCCall(w, err, tg.Address)
			return
		}
		if readings == nil {
			readings = []ipmi.SensorReading{}
		}
		s.logOOB(r, subj, "power.sensors", tg.Device.ID)
		WriteJSON(w, http.StatusOK, map[string]any{"items": readings})
	})
	s.MustHandle("GET", p+"/devices/{id}/sel", func(w http.ResponseWriter, r *http.Request) {
		subj, tg, ok := s.resolveBMC(w, r, d)
		if !ok {
			return
		}
		entries, err := d.BMC.SEL(r.Context(), tg.Address, tg.IPMI())
		if err != nil {
			failBMCCall(w, err, tg.Address)
			return
		}
		if entries == nil {
			entries = []ipmi.SELEntry{}
		}
		s.logOOB(r, subj, "power.sel", tg.Device.ID)
		WriteJSON(w, http.StatusOK, map[string]any{"items": entries})
	})
	s.MustHandle("POST", p+"/devices/{id}/kvm-session", func(w http.ResponseWriter, r *http.Request) {
		subj, ok := s.platformAdmin(w, r, d)
		if !ok {
			return
		}
		if d.KVM == nil {
			WriteError(w, http.StatusNotImplemented, "not_implemented")
			return
		}
		id := r.PathValue("id")
		tg, err := d.BMCRefs.Resolve(userContext(r), subj, id)
		if err != nil {
			if reason := bmc.Reason(err); reason != "" {
				d.BMCRefs.AuditAction(r.Context(), subj, audit.KVMSessionStarted, id, tg.Address, "", reason)
			}
			failResolve(w, err, tg.Address)
			return
		}
		// The credentials only bootstrap the BMC web login of this one-time
		// token (held in memory until the token expires, never persisted).
		token, consoleURL, err := d.KVM.StartSession(r.Context(), tg.Device.ID, tg.Address, tg.KVM())
		if err != nil {
			d.BMCRefs.AuditAction(r.Context(), subj, audit.KVMSessionStarted, id, tg.Address, "", "internal")
			failSvc(w, err)
			return
		}
		d.BMCRefs.AuditAction(r.Context(), subj, audit.KVMSessionStarted, id, tg.Address, "", "")
		WriteJSON(w, http.StatusCreated, map[string]any{"token": token, "console_url": consoleURL})
	})
}

// platformAdmin authenticates the caller and requires platform-admin before
// anything else happens; it also refuses when the BMC service is not wired.
func (s *Server) platformAdmin(w http.ResponseWriter, r *http.Request, d Deps) (authz.Subjects, bool) {
	subj, err := subjects(r)
	if err != nil {
		failSvc(w, err)
		return authz.Subjects{}, false
	}
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		failSvc(w, err)
		return authz.Subjects{}, false
	}
	if d.BMCRefs == nil || d.BMC == nil {
		WriteError(w, ErrUnavailable.Status, ErrUnavailable.Reason)
		return authz.Subjects{}, false
	}
	return subj, true
}

// resolveBMC is platformAdmin + bmc.Resolve for the read routes.
func (s *Server) resolveBMC(w http.ResponseWriter, r *http.Request, d Deps) (authz.Subjects, bmc.Target, bool) {
	subj, ok := s.platformAdmin(w, r, d)
	if !ok {
		return authz.Subjects{}, bmc.Target{}, false
	}
	tg, err := d.BMCRefs.Resolve(userContext(r), subj, r.PathValue("id"))
	if err != nil {
		failResolve(w, err, tg.Address)
		return authz.Subjects{}, bmc.Target{}, false
	}
	return subj, tg, true
}

// failResolve answers a refusal of bmc.Resolve: a reason, or the generic
// mapping (forbidden, not found) for everything else.
func failResolve(w http.ResponseWriter, err error, address string) {
	if reason := bmc.Reason(err); reason != "" {
		writeBMCReason(w, reason, address)
		return
	}
	failSvc(w, err)
}

// failBMCCall answers a failed BMC call with its reason (the BMC's own error
// text never reaches the response).
func failBMCCall(w http.ResponseWriter, err error, address string) {
	if reason := bmc.BMCReason(err); reason != "" {
		writeBMCReason(w, reason, address)
		return
	}
	failSvc(w, err)
}

// logOOB records an out-of-band read to the module log. It carries the actor,
// tenant, device and action only — never any credential or secret ref.
func (s *Server) logOOB(r *http.Request, subj authz.Subjects, action, deviceID string) {
	log := s.rt.Logger()
	if log == nil {
		return
	}
	log.InfoContext(r.Context(), "ipam oob operation",
		"action", action,
		"actor", subj.ActorID(),
		"tenant", subj.TenantID,
		"device_id", deviceID,
		"request_id", RequestID(r),
	)
}

// RegisterKVM mounts the KVM reverse-proxy handler at /bmc/ on mux when a KVM
// manager is wired. This proxy is NOT a gateway-proxied OpenAPI route: it is
// gated by the short-lived session token minted by POST .../kvm-session, not by
// the platform token, so the app mounts it on the outer mux alongside (not
// inside) the gateway API handler, e.g.:
//
//	mux := http.NewServeMux()
//	mux.Handle("/", srv.Handler())     // gateway-proxied IPAM API + SSE
//	srv.RegisterKVM(mux, deps)         // /bmc/ KVM console proxy (token-gated)
//
// It is a no-op when d.KVM is nil.
func (s *Server) RegisterKVM(mux *http.ServeMux, d Deps) {
	if d.KVM == nil {
		return
	}
	mux.Handle("/bmc/", d.KVM.Handler())
}
