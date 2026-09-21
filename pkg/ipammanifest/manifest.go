// Package ipammanifest declares what the IPAM module registers with the
// application gateway: routes derived from the embedded OpenAPI document, the
// API permissions, the CASL abilities and the navigation entries. The IPAM
// gRPC surface (ipam.v1) is service-to-service and is NOT proxied by the
// gateway; the KVM console proxy is token-gated on its own path and is not a
// permissioned route here.
package ipammanifest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"google.golang.org/grpc"

	authv1 "github.com/go-freya/freya/services/auth/api/proto/auth/v1"
	"github.com/go-freya/freya/services/gateway/pkg/gatewayclient"
	"github.com/go-freya/freya/services/ipam/api/openapi"
)

// Module identity.
const (
	Module       = "ipam"
	DisplayName  = "IPAM"
	Version      = "1.0.0"
	RemotePrefix = "/ui"
)

// OpenAPI operation extensions.
const (
	PermissionExtension = "x-freya-permission"
	PublicExtension     = "x-freya-public"
	BodyLimitExtension  = "x-freya-max-body-bytes"
	TimeoutExtension    = "x-freya-timeout-seconds"
)

// Permissions the module registers (the 13 IPAM permissions).
var Permissions = []gatewayclient.Permission{
	{Resource: "ipam", Action: "read", Description: "List and read subnets, addresses, devices, groups, scans and the live stream"},
	{Resource: "subnets", Action: "manage", Description: "Create, update and delete subnets"},
	{Resource: "addresses", Action: "manage", Description: "Create, update and delete IP addresses"},
	{Resource: "addresses", Action: "allocate", Description: "Allocate next-free and bulk IP addresses"},
	{Resource: "devices", Action: "manage", Description: "Create, update and delete devices, interfaces and packages"},
	{Resource: "vlans", Action: "manage", Description: "Create, update and delete VLANs"},
	{Resource: "locations", Action: "manage", Description: "Create, update and delete locations"},
	{Resource: "groups", Action: "manage", Description: "Create, update and delete IP and host groups and their members"},
	{Resource: "scan", Action: "run", Description: "Start subnet and address discovery scans and ping"},
	{Resource: "dns", Action: "manage", Description: "Read and update the per-tenant DNS configuration"},
	{Resource: "backup", Action: "manage", Description: "Export and import tenant IPAM data"},
	{Resource: "power", Action: "control", Description: "Out-of-band power status and actions via device BMC (platform-admin)"},
	{Resource: "kvm", Action: "access", Description: "Start token-gated KVM console sessions (platform-admin)"},
}

// Grants maps built-in role slugs to the permissions they hold. Owner and admin
// hold everything (including power:control and kvm:access); operator holds the
// manage set plus scan:run and allocate but NOT power/kvm; member and auditor
// read only.
var Grants = map[string][]string{
	"owner": PermissionRefs(),
	"admin": PermissionRefs(),
	"operator": {
		"ipam:read", "subnets:manage", "addresses:manage", "addresses:allocate",
		"devices:manage", "vlans:manage", "locations:manage", "groups:manage",
		"scan:run", "dns:manage", "backup:manage",
	},
	"member":  {"ipam:read"},
	"auditor": {"ipam:read"},
}

// Methods proxied by the gateway: none (IPAM gRPC is service to service and the
// KVM proxy is token-gated on its own path).
var Methods []gatewayclient.Method

// Abilities are the CASL rules bound to the permissions.
var Abilities = []gatewayclient.Ability{
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"Subnet"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"IpAddress"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"Device"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"Vlan"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"Location"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"IpGroup"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"HostGroup"}, Requires: "ipam:read"},
	{Action: []string{"read", "create", "update", "delete"}, Subject: []string{"IpScan"}, Requires: "ipam:read"},
}

// Nav lists the navigation contributions.
var Nav = []gatewayclient.NavEntry{
	{Title: "Subnets", Path: "/ipam", Icon: "mdi-ip-network", Order: 700, Requires: "ipam:read"},
	{Title: "IP Addresses", Path: "/ipam/addresses", Icon: "mdi-ip", Order: 710, Requires: "ipam:read"},
	{Title: "Devices", Path: "/ipam/devices", Icon: "mdi-server-network", Order: 720, Requires: "ipam:read"},
	{Title: "VLANs", Path: "/ipam/vlans", Icon: "mdi-lan", Order: 730, Requires: "ipam:read"},
	{Title: "Locations", Path: "/ipam/locations", Icon: "mdi-map-marker-outline", Order: 740, Requires: "ipam:read"},
	{Title: "Groups", Path: "/ipam/groups", Icon: "mdi-group", Order: 750, Requires: "ipam:read"},
	{Title: "Scans", Path: "/ipam/scans", Icon: "mdi-radar", Order: 760, Requires: "ipam:read"},
	{Title: "Dashboard", Path: "/ipam/dashboard", Icon: "mdi-view-dashboard-outline", Order: 770, Requires: "ipam:read"},
}

// PermissionRefs lists "resource:action" for every declared permission.
func PermissionRefs() []string {
	out := make([]string, 0, len(Permissions))
	for _, p := range Permissions {
		out = append(out, p.Resource+":"+p.Action)
	}
	return out
}

// Routes derives the gateway routes from the OpenAPI document.
func Routes(doc *openapi3.T) ([]gatewayclient.Route, error) {
	known := map[string]bool{}
	for _, p := range PermissionRefs() {
		known[p] = true
	}
	var routes []gatewayclient.Route
	for p, item := range doc.Paths.Map() {
		for m, op := range item.Operations() {
			r := gatewayclient.Route{Method: strings.ToUpper(m), Path: p}
			perm, _ := op.Extensions[PermissionExtension].(string)
			public, _ := op.Extensions[PublicExtension].(bool)
			switch {
			case public:
				r.Public = true
			case perm == "":
				return nil, fmt.Errorf("ipammanifest: %s %s declares no permission", r.Method, p)
			case !known[perm]:
				return nil, fmt.Errorf("ipammanifest: %s %s uses undeclared permission %q", r.Method, p, perm)
			default:
				r.Permission = perm
			}
			if v, ok := op.Extensions[BodyLimitExtension]; ok {
				n, ok := v.(float64)
				if !ok || n <= 0 {
					return nil, fmt.Errorf("ipammanifest: %s %s has a bad body limit", r.Method, p)
				}
				r.MaxBodyBytes = uint64(n)
			}
			if v, ok := op.Extensions[TimeoutExtension]; ok {
				n, ok := v.(float64)
				if !ok || n <= 0 || n > 600 {
					return nil, fmt.Errorf("ipammanifest: %s %s has a bad timeout", r.Method, p)
				}
				r.Timeout = time.Duration(n) * time.Second
			}
			routes = append(routes, r)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Method+" "+routes[i].Path < routes[j].Method+" "+routes[j].Path })
	return routes, nil
}

// Load parses the embedded document.
func Load() (*openapi3.T, error) {
	return openapi3.NewLoader().LoadFromData(openapi.Ipam)
}

// Manifest builds the gateway manifest from the embedded OpenAPI document.
func Manifest() (gatewayclient.Manifest, error) {
	doc, err := Load()
	if err != nil {
		return gatewayclient.Manifest{}, err
	}
	routes, err := Routes(doc)
	if err != nil {
		return gatewayclient.Manifest{}, err
	}
	return gatewayclient.Manifest{
		Module: Module, DisplayName: DisplayName, Version: Version,
		Prefixes:    []string{"/api/ipam"},
		Routes:      routes,
		Methods:     Methods,
		Permissions: Permissions,
		Abilities:   Abilities,
		Exposes:     []string{"./routes", "./nav"},
		Nav:         Nav,
	}, nil
}

// SeedRequest builds the auth registration request: every module permission plus
// the built-in role grants.
func SeedRequest() *authv1.RegisterPermissionsRequest {
	req := &authv1.RegisterPermissionsRequest{}
	for _, p := range Permissions {
		req.Permissions = append(req.Permissions, &authv1.PermissionDef{Resource: p.Resource, Action: p.Action, Description: p.Description})
	}
	for _, slug := range []string{"owner", "admin", "member", "auditor", "operator"} {
		req.BuiltinGrants = append(req.BuiltinGrants, &authv1.BuiltinGrant{Role: slug, Permissions: Grants[slug]})
	}
	return req
}

// SeedPermissions registers the module's permissions with the auth service and
// grants them to the built-in roles (idempotent). The gateway registers the
// permissions from the manifest for routing; only the IPAM module knows the role
// grants, so it pushes them to auth here.
func SeedPermissions(ctx context.Context, cc grpc.ClientConnInterface) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := authv1.NewAuthorizationClient(cc).RegisterPermissions(ctx, SeedRequest())
	return err
}
