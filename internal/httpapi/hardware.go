package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// deviceBody is the device create/update body: a device whose server-owned
// hardware fields are refused (FR-008). The shadowing fields win over the
// embedded ones in encoding/json, so a body carrying them fails the strict
// decode with 400.
type deviceBody struct {
	store.Device
	HardwareSummary serverOwned `json:"hardware_summary"`
	Hardware        serverOwned `json:"hardware"`
}

// serverOwned refuses any JSON value.
type serverOwned struct{}

// UnmarshalJSON implements json.Unmarshaler.
func (serverOwned) UnmarshalJSON([]byte) error { return errServerOwned }

var errServerOwned = errors.New("httpapi: server-owned field")

// deviceHardwareView is GET /devices/{id}/hardware (contracts/ipam-http.md
// DeviceHardware): the stored profile flattened with the device id, the
// report time and the summary.
type deviceHardwareView struct {
	DeviceID   string                `json:"device_id"`
	ReportedAt time.Time             `json:"reported_at"`
	Summary    store.HardwareSummary `json:"summary"`
	store.HardwareProfile
}

// registerHardware mounts the read-only device hardware route.
func (s *Server) registerHardware(p string, d Deps) {
	s.MustHandle("GET", p+"/devices/{id}/hardware", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		h, err := d.Devices.GetHardware(r.Context(), subj, r.PathValue("id"))
		if err != nil {
			failSvc(w, err)
			return
		}
		sum := h.Summary
		sum.ReportedAt = h.ReportedAt
		WriteJSON(w, http.StatusOK, deviceHardwareView{DeviceID: h.DeviceID, ReportedAt: h.ReportedAt, Summary: sum, HardwareProfile: h.Profile})
	})
}
