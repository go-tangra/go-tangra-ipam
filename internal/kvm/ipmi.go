package kvm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-tangra/go-tangra-ipam/internal/bmc"
)

// ipmiOpTimeout bounds a full IPMI LAN exchange (connect + command + close).
// Sensor and SEL reads walk the SDR/log repositories over UDP 623 and can take
// several seconds, so this is generous relative to the web-login timeouts.
const ipmiOpTimeout = 30 * time.Second

// handleIpmiInfo returns BMC identity (Get Device ID + FRU product details).
func (s *Service) handleIpmiInfo(w http.ResponseWriter, r *http.Request) {
	s.withBmcClient(w, r, func(ctx context.Context, c *bmc.Client) (any, error) {
		return c.Info(ctx)
	})
}

// handleIpmiSensors returns all decoded sensor readings.
func (s *Service) handleIpmiSensors(w http.ResponseWriter, r *http.Request) {
	s.withBmcClient(w, r, func(ctx context.Context, c *bmc.Client) (any, error) {
		readings, err := c.Sensors(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sensors": readings}, nil
	})
}

// handleIpmiSel returns the decoded System Event Log.
func (s *Service) handleIpmiSel(w http.ResponseWriter, r *http.Request) {
	s.withBmcClient(w, r, func(ctx context.Context, c *bmc.Client) (any, error) {
		entries, err := c.SEL(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil
	})
}

// handleIpmiPower reads chassis power state (GET) or applies a power action
// (POST {"action":"on|off|cycle|reset|soft|diag"}). Power changes are gated by
// the same platform-admin-minted token as the console.
func (s *Service) handleIpmiPower(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.withBmcClient(w, r, func(ctx context.Context, c *bmc.Client) (any, error) {
			return c.PowerStatus(ctx)
		})
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		action := bmc.PowerAction(body.Action)
		s.withBmcClient(w, r, func(ctx context.Context, c *bmc.Client) (any, error) {
			if err := c.Power(ctx, action); err != nil {
				return nil, err
			}
			return map[string]any{"action": string(action), "ok": true}, nil
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// withBmcClient resolves the request token, opens an IPMI LAN session to the
// bound BMC, runs op, and writes the result as JSON. It centralizes auth,
// connection lifecycle, and error handling for the IPMI management endpoints.
func (s *Service) withBmcClient(w http.ResponseWriter, r *http.Request, op func(context.Context, *bmc.Client) (any, error)) {
	kt, ok := s.tokenFor(r)
	if !ok {
		http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
		return
	}

	// Detach from the inbound request deadline (the admin gateway caps requests
	// at 10s, shorter than a full sensor/SEL read) but keep request values, then
	// bound the op with our own timeout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ipmiOpTimeout)
	defer cancel()

	c, err := bmc.Connect(ctx, bmc.Target{
		Host:     kt.target.Host,
		Username: kt.target.Username,
		Password: kt.target.Password,
		Protocol: "auto",
	})
	if err != nil {
		s.log.Warnf("ipmi connect %s: %v", kt.target.Host, err)
		writeJSONError(w, http.StatusBadGateway, "could not reach BMC over IPMI")
		return
	}
	defer func() { _ = c.Close(ctx) }()

	result, err := op(ctx, c)
	if err != nil {
		s.log.Warnf("ipmi op %s: %v", kt.target.Host, err)
		writeJSONError(w, http.StatusBadGateway, "IPMI command failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
