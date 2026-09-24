package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/addresses"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/backup"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/devices"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/dnscfg"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/groups"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/locations"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stats"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stream"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/subnets"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/vlans"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// Deps wire the IPAM HTTP handlers. Every domain service is required; the
// out-of-band surfaces (BMC, KVM, Warden) gate the privileged power/console
// routes; Hub is optional and, when set, enables the GET /stream SSE route
// (otherwise that route stays 501 not_implemented). Credentials fetched from
// Warden and passed to BMC/KVM are used at call time only and are never
// returned to, or persisted by, this layer.
type Deps struct {
	Subnets   *subnets.Service
	Addresses *addresses.Service
	Devices   *devices.Service
	Vlans     *vlans.Service
	Locations *locations.Service
	Groups    *groups.Service
	Stats     *stats.Service
	Backup    *backup.Service
	DNS       *dnscfg.Service
	Scan      *scan.Service
	BMC       ipmi.BMC
	KVM       *kvm.Manager
	Warden    warden.Client
	Hub       *stream.Hub // optional: enables GET /stream (SSE) when set
}

// subjects derives the authz subject from the verified platform identity. The
// gateway forwards a human/user caller on every gateway-proxied route.
func subjects(r *http.Request) (authz.Subjects, error) {
	id, err := Caller(r)
	if err != nil {
		return authz.Subjects{}, err
	}
	return authz.Subjects{
		TenantID:  id.TenantID,
		UserID:    id.UserID,
		Roles:     id.Roles,
		ActorKind: authz.ActorUser,
	}, nil
}

// failSvc maps a service/domain error to an HTTP response drawn from the
// OpenAPI closed vocabulary. Validation errors and a bad backup schema collapse
// to 422; not-found sentinels to 404; a forbidden scope to 403; not-empty /
// conflict / active-scan / terminal-job to 409; an exhausted subnet to 507; an
// IPv6 / too-large subnet or an unknown power verb to 400; a missing caller to
// 401; anything else to 500. Error detail is never leaked to the client.
func failSvc(w http.ResponseWriter, err error) {
	var subnetVE subnets.ValidationError
	var vlanVE vlans.ValidationError
	var locVE locations.ValidationError
	var grpVE groups.ValidationError
	switch {
	case errors.As(err, &subnetVE), errors.As(err, &vlanVE), errors.As(err, &locVE),
		errors.As(err, &grpVE), errors.Is(err, backup.ErrBadSchema):
		WriteError(w, http.StatusUnprocessableEntity, "validation_failed")
	case errors.Is(err, subnets.ErrNotFound), errors.Is(err, addresses.ErrNotFound),
		errors.Is(err, devices.ErrNotFound), errors.Is(err, warden.ErrNotFound),
		errors.Is(err, repo.ErrNotFound), errors.Is(err, store.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, authz.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, subnets.ErrNotEmpty), errors.Is(err, devices.ErrNotEmpty),
		errors.Is(err, devices.ErrConflict), errors.Is(err, addresses.ErrConflict),
		errors.Is(err, scan.ErrActiveScan), errors.Is(err, scan.ErrTerminal),
		errors.Is(err, repo.ErrConflict), errors.Is(err, store.ErrConflict):
		WriteError(w, http.StatusConflict, "conflict")
	case errors.Is(err, addresses.ErrNoAvailable):
		WriteError(w, http.StatusInsufficientStorage, "no_available_address")
	case errors.Is(err, scan.ErrIPv6), errors.Is(err, scan.ErrTooLarge),
		errors.Is(err, ipmi.ErrUnknownAction):
		WriteError(w, http.StatusBadRequest, "bad_request")
	case errors.Is(err, ErrUnauthenticated):
		WriteError(w, http.StatusUnauthorized, "unauthenticated")
	default:
		WriteError(w, http.StatusInternalServerError, "internal")
	}
}
