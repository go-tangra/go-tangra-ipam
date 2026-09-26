package httpapi

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
)

// Body bounds of the subnet SNMP credential writes (contracts/ipam-http.md).
const (
	snmpSetBytes  = 4 << 10
	snmpTestBytes = 1 << 10
)

// Credentials tests per user per minute (FR-020: the test endpoint must not
// become a network probe).
const (
	snmpTestsPerWindow = 10
	snmpTestWindow     = time.Minute
	maxLimiterKeys     = 10000
)

// rateLimiter is a per-key sliding window kept in process.
type rateLimiter struct {
	mu     sync.Mutex
	per    int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

func newRateLimiter(per int, window time.Duration, now func() time.Time) *rateLimiter {
	return &rateLimiter{per: per, window: window, now: now, hits: map[string][]time.Time{}}
}

// allow records one request for key and reports whether it is within the limit.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) >= maxLimiterKeys {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.per {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

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
	limiter := newRateLimiter(snmpTestsPerWindow, snmpTestWindow, time.Now)
	s.MustHandle("POST", p+"/test", func(w http.ResponseWriter, r *http.Request) {
		subj, err := subjects(r)
		if err != nil {
			failSvc(w, err)
			return
		}
		if !limiter.allow(subj.TenantID + "/" + subj.UserID) {
			WriteError(w, ErrRateLimited.Status, ErrRateLimited.Reason)
			return
		}
		var in struct {
			Address string `json:"address"`
		}
		if err := DecodeJSON(r, &in, snmpTestBytes); err != nil {
			Fail(w, r, nil, err)
			return
		}
		v, err := d.Scan.TestCredentials(r.Context(), subj, r.PathValue("id"), in.Address)
		if err != nil {
			failSNMP(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
}
