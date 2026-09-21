// Package grpcapi serves ipam.v1 for other platform services on the Freya
// SPIFFE mTLS channel: the caller is an authenticated service acting for the
// tenant named in the request. Nothing here is proxied by the gateway. The
// tenant comes from the request and the actor identity from the verified SPIFFE
// peer. No response ever carries SNMP/BMC/IPMI credentials or sealed
// owner/contact fields; devices and subnets expose only opaque warden secret
// references. The privileged out-of-band device operations (power control, KVM
// console) require the platform-admin role and fetch their credentials from
// warden at use time — the credentials never touch a wire message.
package grpcapi

import (
	"context"
	"errors"
	"regexp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/go-freya/freya/authn"
	ipamv1 "github.com/go-freya/freya/services/ipam/api/proto/ipam/v1"
	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/backup"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/ipmi"
	"github.com/go-freya/freya/services/ipam/internal/kvm"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// callerFunc resolves the SPIFFE identity and any roles of a call. It is a test
// seam: tests override it to inject an identity and (for the platform-admin
// paths) the roles a real gateway/mesh would have resolved for the peer.
var callerFunc = func(ctx context.Context) (id string, roles []string, ok bool) {
	p, ok := authn.FromContext(ctx)
	if !ok {
		return "", nil, false
	}
	return p.ID.String(), nil, true
}

// caller returns the service subjects for the tenant named in the request; the
// tenant must be a uuid and the peer must present a SPIFFE identity. The actor
// kind is always "service" on this mesh channel.
func caller(ctx context.Context, tenantID string) (authz.Subjects, error) {
	id, roles, ok := callerFunc(ctx)
	if !ok {
		return authz.Subjects{}, status.Error(codes.Unauthenticated, "service identity required")
	}
	if !uuidRE.MatchString(tenantID) {
		return authz.Subjects{}, status.Error(codes.InvalidArgument, "tenant_id must be a uuid")
	}
	return authz.Subjects{TenantID: tenantID, UserID: id, Roles: roles, ActorKind: authz.ActorService}, nil
}

// isValidation reports whether err is any of the domain ValidationError types.
func isValidation(err error) bool {
	var se subnets.ValidationError
	var ve vlans.ValidationError
	var le locations.ValidationError
	var ge groups.ValidationError
	return errors.As(err, &se) || errors.As(err, &ve) || errors.As(err, &le) || errors.As(err, &ge)
}

// grpcError maps a service/domain error to a gRPC status. Detail is never
// surfaced beyond the stable reason string.
func grpcError(err error) error {
	switch {
	case err == nil:
		return nil
	case isValidation(err), errors.Is(err, scan.ErrIPv6), errors.Is(err, ipmi.ErrUnknownAction):
		return status.Error(codes.InvalidArgument, "validation_failed")
	case errors.Is(err, subnets.ErrNotFound), errors.Is(err, addresses.ErrNotFound),
		errors.Is(err, devices.ErrNotFound), errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, "not_found")
	case errors.Is(err, authz.ErrForbidden):
		return status.Error(codes.PermissionDenied, "forbidden")
	case errors.Is(err, addresses.ErrNoAvailable):
		return status.Error(codes.ResourceExhausted, "no_available_address")
	case errors.Is(err, subnets.ErrNotEmpty), errors.Is(err, devices.ErrNotEmpty),
		errors.Is(err, repo.ErrNotEmpty), errors.Is(err, addresses.ErrConflict),
		errors.Is(err, devices.ErrConflict), errors.Is(err, repo.ErrConflict),
		errors.Is(err, scan.ErrTooLarge), errors.Is(err, scan.ErrActiveScan),
		errors.Is(err, scan.ErrTerminal),
		errors.Is(err, warden.ErrEmptyRef), errors.Is(err, warden.ErrNotFound):
		return status.Error(codes.FailedPrecondition, "conflict")
	}
	return status.Error(codes.Unavailable, "temporarily_unavailable")
}

// Deps carries the services and out-of-band clients the ipam.v1 servers use.
// Backup is wired for parity with the app but has no mesh RPC of its own. BMC,
// KVM and Warden power the privileged device operations; when any is nil those
// RPCs report Unavailable.
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

	BMC    ipmi.BMC
	KVM    *kvm.Manager
	Warden warden.Client
}

// Register registers the nine ipam.v1 mesh servers on the gRPC server. Callers
// are authenticated services; nothing here is gateway-proxied.
func Register(gs grpc.ServiceRegistrar, d Deps) {
	if d.Subnets != nil {
		ipamv1.RegisterSubnetServiceServer(gs, &SubnetServer{subnets: d.Subnets, scan: d.Scan})
	}
	if d.Addresses != nil {
		ipamv1.RegisterIpAddressServiceServer(gs, &IpAddressServer{addresses: d.Addresses})
	}
	if d.Devices != nil {
		ipamv1.RegisterDeviceServiceServer(gs, &DeviceServer{devices: d.Devices, bmc: d.BMC, kvm: d.KVM, warden: d.Warden})
	}
	if d.Vlans != nil {
		ipamv1.RegisterVlanServiceServer(gs, &VlanServer{vlans: d.Vlans})
	}
	if d.Locations != nil {
		ipamv1.RegisterLocationServiceServer(gs, &LocationServer{locations: d.Locations})
	}
	if d.Groups != nil {
		ipamv1.RegisterIpGroupServiceServer(gs, &IpGroupServer{groups: d.Groups})
		ipamv1.RegisterHostGroupServiceServer(gs, &HostGroupServer{groups: d.Groups})
	}
	if d.Scan != nil {
		ipamv1.RegisterIpScanServiceServer(gs, &IpScanServer{scan: d.Scan})
	}
	if d.Stats != nil || d.DNS != nil {
		ipamv1.RegisterSystemServiceServer(gs, &SystemServer{stats: d.Stats, dns: d.DNS})
	}
}
