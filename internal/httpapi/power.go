package httpapi

import (
	"net/http"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// registerPower mounts the privileged out-of-band routes: chassis power
// status/control, sensor and SEL reads, and the KVM console session. Every one
// requires platform-admin (a mere tenant admin is refused with 403), loads the
// tenant-scoped device to resolve its BMC host and warden secret ref, fetches
// the BMC credentials from warden at call time, and invokes the BMC/KVM client.
// Credentials are never written to the response or logged; only the resulting
// status, readings, or a short-lived KVM token/console URL are returned.
func (s *Server) registerPower(d Deps, p string) {
	s.MustHandle("GET", p+"/devices/{id}/power", func(w http.ResponseWriter, r *http.Request) {
		subj, host, creds, ok := s.bmcSetup(w, r, d)
		if !ok {
			return
		}
		state, err := d.BMC.PowerStatus(r.Context(), host, creds)
		if err != nil {
			failSvc(w, err)
			return
		}
		s.auditOOB(r, subj, "power.status", r.PathValue("id"))
		WriteJSON(w, http.StatusOK, state)
	})
	s.MustHandle("POST", p+"/devices/{id}/power", func(w http.ResponseWriter, r *http.Request) {
		subj, host, creds, ok := s.bmcSetup(w, r, d)
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
		if err := d.BMC.Power(r.Context(), host, creds, in.Action); err != nil {
			failSvc(w, err)
			return
		}
		s.auditOOB(r, subj, "power.action:"+in.Action, r.PathValue("id"))
		WriteJSON(w, http.StatusOK, map[string]any{"accepted": true, "action": in.Action})
	})
	s.MustHandle("GET", p+"/devices/{id}/sensors", func(w http.ResponseWriter, r *http.Request) {
		subj, host, creds, ok := s.bmcSetup(w, r, d)
		if !ok {
			return
		}
		readings, err := d.BMC.Sensors(r.Context(), host, creds)
		if err != nil {
			failSvc(w, err)
			return
		}
		if readings == nil {
			readings = []ipmi.SensorReading{}
		}
		s.auditOOB(r, subj, "power.sensors", r.PathValue("id"))
		WriteJSON(w, http.StatusOK, map[string]any{"items": readings})
	})
	s.MustHandle("GET", p+"/devices/{id}/sel", func(w http.ResponseWriter, r *http.Request) {
		subj, host, creds, ok := s.bmcSetup(w, r, d)
		if !ok {
			return
		}
		entries, err := d.BMC.SEL(r.Context(), host, creds)
		if err != nil {
			failSvc(w, err)
			return
		}
		if entries == nil {
			entries = []ipmi.SELEntry{}
		}
		s.auditOOB(r, subj, "power.sel", r.PathValue("id"))
		WriteJSON(w, http.StatusOK, map[string]any{"items": entries})
	})
	s.MustHandle("POST", p+"/devices/{id}/kvm-session", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if err := authz.RequirePlatformAdmin(subj); err != nil {
			failSvc(w, err)
			return
		}
		if d.KVM == nil {
			WriteError(w, http.StatusNotImplemented, "not_implemented")
			return
		}
		dev, host, secret, ok := s.loadBMC(w, r, d, subj)
		if !ok {
			return
		}
		token, consoleURL, err := d.KVM.StartSession(r.Context(), dev.ID, host, kvm.Creds{
			Username: secret["username"],
			Password: secret["password"],
		})
		if err != nil {
			failSvc(w, err)
			return
		}
		s.auditOOB(r, subj, "kvm.session", dev.ID)
		WriteJSON(w, http.StatusCreated, map[string]any{"token": token, "console_url": consoleURL})
	})
}

// bmcSetup authenticates the caller, enforces platform-admin, loads the device
// and resolves its BMC host and IPMI credentials. It writes the error response
// itself and returns ok=false when any step fails.
func (s *Server) bmcSetup(w http.ResponseWriter, r *http.Request, d Deps) (subj authz.Subjects, host string, creds ipmi.Creds, ok bool) {
	subj, err := subjects(r)
	if err != nil {
		failSvc(w, err)
		return authz.Subjects{}, "", ipmi.Creds{}, false
	}
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		failSvc(w, err)
		return authz.Subjects{}, "", ipmi.Creds{}, false
	}
	_, host, secret, ok := s.loadBMC(w, r, d, subj)
	if !ok {
		return authz.Subjects{}, "", ipmi.Creds{}, false
	}
	creds = ipmi.Creds{
		Username: secret["username"],
		Password: secret["password"],
		Protocol: secret["protocol"],
		Port:     atoiDefault(secret["port"], 0),
	}
	return subj, host, creds, true
}

// loadBMC loads the device and fetches its BMC secret from warden. It writes the
// error response and returns ok=false on any failure (missing device, no
// configured secret ref, or a warden fetch error). The returned secret map is
// used immediately by the caller and never persisted or logged.
func (s *Server) loadBMC(w http.ResponseWriter, r *http.Request, d Deps, subj authz.Subjects) (dev store.Device, host string, secret map[string]string, ok bool) {
	dev, err := d.Devices.Get(r.Context(), subj, r.PathValue("id"))
	if err != nil {
		failSvc(w, err)
		return store.Device{}, "", nil, false
	}
	host = dev.ManagementIP
	if host == "" {
		host = dev.PrimaryIP
	}
	if dev.IPMISecretRef == "" || host == "" {
		WriteError(w, http.StatusUnprocessableEntity, "validation_failed")
		return store.Device{}, "", nil, false
	}
	secret, err = d.Warden.GetSecret(r.Context(), dev.IPMISecretRef)
	if err != nil {
		failSvc(w, err)
		return store.Device{}, "", nil, false
	}
	return dev, host, secret, true
}

// auditOOB records an out-of-band operation to the module log. It carries the
// actor, tenant, device and action only — never any credential or secret ref.
func (s *Server) auditOOB(r *http.Request, subj authz.Subjects, action, deviceID string) {
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
