package ipammanifest

import (
	"reflect"
	"regexp"
	"testing"
)

func TestManifestBuilds(t *testing.T) {
	m, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.Module != "ipam" {
		t.Fatalf("module = %q", m.Module)
	}
	if len(m.Prefixes) != 1 || m.Prefixes[0] != "/api/ipam" {
		t.Fatalf("prefixes = %v", m.Prefixes)
	}
	if len(m.Routes) == 0 {
		t.Fatal("no routes derived from OpenAPI")
	}
}

func TestPermissionCount(t *testing.T) {
	if len(Permissions) != 14 {
		t.Fatalf("want 14 permissions, got %d", len(Permissions))
	}
	// PermissionRefs must be unique.
	seen := map[string]bool{}
	for _, r := range PermissionRefs() {
		if seen[r] {
			t.Fatalf("duplicate permission ref %q", r)
		}
		seen[r] = true
	}
	for _, want := range []string{
		"ipam:read", "subnets:manage", "addresses:manage", "addresses:allocate",
		"devices:manage", "vlans:manage", "locations:manage", "groups:manage",
		"scan:run", "dns:manage", "backup:manage", "power:control", "kvm:access", "hostsync:manage",
	} {
		if !seen[want] {
			t.Errorf("missing permission %q", want)
		}
	}
}

// TestEveryRouteMapsToDeclaredPermission is the core contract: Routes() rejects
// any route whose permission is not declared, and it must succeed here.
func TestEveryRouteMapsToDeclaredPermission(t *testing.T) {
	doc, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	routes, err := Routes(doc)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	known := map[string]bool{}
	for _, r := range PermissionRefs() {
		known[r] = true
	}
	var publicCount int
	for _, r := range routes {
		if r.Public {
			publicCount++
			continue
		}
		if !known[r.Permission] {
			t.Errorf("%s %s uses undeclared permission %q", r.Method, r.Path, r.Permission)
		}
	}
	if publicCount != 1 {
		t.Fatalf("want exactly 1 public route (health), got %d", publicCount)
	}
}

func TestOperatorExcludesPowerAndKvm(t *testing.T) {
	op := Grants["operator"]
	for _, p := range op {
		if p == "power:control" || p == "kvm:access" {
			t.Fatalf("operator must not hold %q", p)
		}
	}
	// operator must hold the manage set + scan + allocate.
	for _, want := range []string{"subnets:manage", "addresses:allocate", "scan:run", "dns:manage", "backup:manage"} {
		if !contains(op, want) {
			t.Errorf("operator missing %q", want)
		}
	}
	// owner/admin hold everything including power/kvm.
	for _, role := range []string{"owner", "admin"} {
		if !contains(Grants[role], "power:control") || !contains(Grants[role], "kvm:access") {
			t.Errorf("%s must hold power:control and kvm:access", role)
		}
	}
}

func TestNavEntries(t *testing.T) {
	if len(Nav) != 9 {
		t.Fatalf("want 9 nav entries, got %d", len(Nav))
	}
	wantPaths := map[string]bool{
		"/ipam": false, "/ipam/addresses": false, "/ipam/devices": false,
		"/ipam/vlans": false, "/ipam/locations": false, "/ipam/groups": false,
		"/ipam/scans": false, "/ipam/dashboard": false, "/ipam/host-sync": false,
	}
	for _, n := range Nav {
		if _, ok := wantPaths[n.Path]; !ok {
			t.Errorf("unexpected nav path %q", n.Path)
		}
		wantPaths[n.Path] = true
		if n.Requires == "" {
			t.Errorf("nav %q has no Requires", n.Title)
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Errorf("missing nav path %q", p)
		}
	}
}

// TestRoles pins the module role set (feature 019, research D9).
func TestRoles(t *testing.T) {
	want := map[string][]string{
		"administrator": PermissionRefs(),
		"operator":      {"ipam:read", "addresses:allocate", "scan:run"},
		"viewer":        {"ipam:read"},
	}
	names := map[string]string{"administrator": "IPAM administrator", "operator": "IPAM operator", "viewer": "IPAM viewer"}
	if len(Roles) != len(want) {
		t.Fatalf("want %d roles, got %d", len(want), len(Roles))
	}
	own := map[string]bool{}
	for _, r := range PermissionRefs() {
		own[r] = true
	}
	slug := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	for _, r := range Roles {
		if !slug.MatchString(r.Slug) {
			t.Errorf("role slug %q", r.Slug)
		}
		if r.DisplayName != names[r.Slug] || r.Description == "" {
			t.Errorf("role %q: display name %q, description %q", r.Slug, r.DisplayName, r.Description)
		}
		if !reflect.DeepEqual(r.Permissions, want[r.Slug]) {
			t.Errorf("role %q: permissions %v, want %v", r.Slug, r.Permissions, want[r.Slug])
		}
		for _, p := range r.Permissions {
			if !own[p] {
				t.Errorf("role %q names %q, not an IPAM permission", r.Slug, p)
			}
		}
	}
	admin := Roles[0].Permissions
	if !contains(admin, "power:control") || !contains(admin, "kvm:access") {
		t.Error("administrator must hold power:control and kvm:access")
	}
}

// TestBuiltinGrantsUnchanged: the built-in grants are those registered before
// module roles existed (auth now scopes them to the module), plus
// hostsync:manage for owner/admin (feature 020).
func TestBuiltinGrantsUnchanged(t *testing.T) {
	all := []string{
		"ipam:read", "subnets:manage", "addresses:manage", "addresses:allocate",
		"devices:manage", "vlans:manage", "locations:manage", "groups:manage",
		"scan:run", "dns:manage", "backup:manage", "power:control", "kvm:access", "hostsync:manage",
	}
	want := map[string][]string{
		"owner": all,
		"admin": all,
		"operator": {
			"ipam:read", "subnets:manage", "addresses:manage", "addresses:allocate",
			"devices:manage", "vlans:manage", "locations:manage", "groups:manage",
			"scan:run", "dns:manage", "backup:manage",
		},
		"member":  {"ipam:read"},
		"auditor": {"ipam:read"},
	}
	if !reflect.DeepEqual(Grants, want) {
		t.Fatalf("built-in grants changed:\n got %v\nwant %v", Grants, want)
	}
}

// TestRegistration: the auth registration carries the module identity, every
// permission, the complete role set and the built-in grants, and is valid.
func TestRegistration(t *testing.T) {
	reg := Registration()
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	req := reg.Request()
	if req.GetModule() != "ipam" || req.GetModuleDisplayName() != "IPAM" || !req.GetDeclaresRoles() {
		t.Fatalf("module %q display %q declares_roles %v", req.GetModule(), req.GetModuleDisplayName(), req.GetDeclaresRoles())
	}
	if len(req.GetPermissions()) != len(Permissions) || len(req.GetRoles()) != len(Roles) {
		t.Fatalf("%d permissions, %d roles", len(req.GetPermissions()), len(req.GetRoles()))
	}
	got := map[string][]string{}
	for _, g := range req.GetBuiltinGrants() {
		got[g.GetRole()] = g.GetPermissions()
	}
	if !reflect.DeepEqual(got, Grants) {
		t.Fatalf("builtin grants %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestHostSyncPermission (T066): hostsync:manage is held by owner, admin and
// the IPAM administrator module role only; the new routes use the permissions
// of contracts/ipam-http.md.
func TestHostSyncPermission(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		if !contains(Grants[role], "hostsync:manage") {
			t.Errorf("%s must hold hostsync:manage", role)
		}
	}
	for _, role := range []string{"operator", "member", "auditor"} {
		if contains(Grants[role], "hostsync:manage") {
			t.Errorf("%s must not hold hostsync:manage", role)
		}
	}
	for _, r := range Roles {
		has := contains(r.Permissions, "hostsync:manage")
		if (r.Slug == "administrator") != has {
			t.Errorf("module role %s hostsync:manage = %v", r.Slug, has)
		}
	}
	reg := Registration()
	found := false
	for _, p := range reg.Permissions {
		found = found || (p.Resource == "hostsync" && p.Action == "manage")
	}
	if !found {
		t.Fatal("hostsync:manage registered with auth")
	}
	ability := false
	for _, a := range Abilities {
		ability = ability || (reflect.DeepEqual(a.Subject, []string{"HostSync"}) && reflect.DeepEqual(a.Action, []string{"manage"}) && a.Requires == "hostsync:manage")
	}
	if !ability {
		t.Fatal("CASL {manage, HostSync}")
	}
	for _, want := range [][3]string{{"resync", "HostSync", "devices:manage"}, {"clear", "AddressConflict", "addresses:manage"}, {"configure", "ArpSettings", "subnets:manage"}} {
		ok := false
		for _, a := range Abilities {
			ok = ok || (reflect.DeepEqual(a.Action, []string{want[0]}) && reflect.DeepEqual(a.Subject, []string{want[1]}) && a.Requires == want[2])
		}
		if !ok {
			t.Errorf("CASL %v", want)
		}
	}
	nav := false
	for _, n := range Nav {
		nav = nav || (n.Path == "/ipam/host-sync" && n.Requires == "ipam:read" && n.Order == 765 && n.Icon == "mdi-sync")
	}
	if !nav {
		t.Fatal("host sync nav entry")
	}
	doc, _ := Load()
	routes, _ := Routes(doc)
	want := map[string]string{
		"GET /api/ipam/v1/host-sync/settings":                "ipam:read",
		"PUT /api/ipam/v1/host-sync/settings":                "hostsync:manage",
		"GET /api/ipam/v1/host-sync/status":                  "ipam:read",
		"POST /api/ipam/v1/host-sync/resync":                 "devices:manage",
		"GET /api/ipam/v1/devices/{id}/host-sync":            "ipam:read",
		"POST /api/ipam/v1/devices/{id}/host-sync":           "devices:manage",
		"GET /api/ipam/v1/devices/{id}/guests":               "ipam:read",
		"POST /api/ipam/v1/ip-addresses/{id}/clear-conflict": "addresses:manage",
	}
	limits := map[string]uint64{"PUT /api/ipam/v1/host-sync/settings": 16384, "POST /api/ipam/v1/host-sync/resync": 1024,
		"POST /api/ipam/v1/devices/{id}/host-sync": 1024, "POST /api/ipam/v1/ip-addresses/{id}/clear-conflict": 1024}
	got := map[string]bool{}
	for _, r := range routes {
		k := r.Method + " " + r.Path
		if p, ok := want[k]; ok {
			got[k] = true
			if r.Permission != p {
				t.Errorf("%s permission %s want %s", k, r.Permission, p)
			}
			if l, ok := limits[k]; ok && r.MaxBodyBytes != l {
				t.Errorf("%s body limit %d want %d", k, r.MaxBodyBytes, l)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("routes %v", got)
	}
}

// TestSNMPCredentialPermissions (021): read-only roles see SNMP status but
// cannot set, clear or test credentials; operators can.
func TestSNMPCredentialPermissions(t *testing.T) {
	has := func(role, perm string) bool {
		for _, p := range Grants[role] {
			if p == perm {
				return true
			}
		}
		return false
	}
	for _, role := range []string{"member", "auditor"} {
		if !has(role, "ipam:read") || has(role, "subnets:manage") || has(role, "scan:run") {
			t.Errorf("%s must read SNMP status only", role)
		}
	}
	if !has("operator", "subnets:manage") || !has("operator", "scan:run") {
		t.Error("operator manages and tests SNMP credentials")
	}
}

// TestAbilitiesFollowRoutePermissions: ipam:read grants only "read" on the
// record types; create/update/delete follow the manage permission of the
// matching API routes, so read-only callers see no edit controls. Every
// ability names a declared permission.
func TestAbilitiesFollowRoutePermissions(t *testing.T) {
	known := map[string]bool{}
	for _, p := range PermissionRefs() {
		known[p] = true
	}
	// grants[subject][action] = permissions granting it.
	grants := map[string]map[string][]string{}
	for _, a := range Abilities {
		if !known[a.Requires] {
			t.Errorf("ability %v %v requires undeclared permission %q", a.Action, a.Subject, a.Requires)
		}
		for _, s := range a.Subject {
			if grants[s] == nil {
				grants[s] = map[string][]string{}
			}
			for _, act := range a.Action {
				grants[s][act] = append(grants[s][act], a.Requires)
			}
		}
	}
	write := map[string]string{
		"Subnet": "subnets:manage", "IpAddress": "addresses:manage", "Device": "devices:manage",
		"Vlan": "vlans:manage", "Location": "locations:manage", "IpGroup": "groups:manage",
		"HostGroup": "groups:manage", "IpScan": "scan:run",
	}
	for subject, perm := range write {
		if got := grants[subject]["read"]; !reflect.DeepEqual(got, []string{"ipam:read"}) {
			t.Errorf("read %s = %v, want [ipam:read]", subject, got)
		}
		for _, act := range []string{"create", "update", "delete"} {
			if got := grants[subject][act]; !reflect.DeepEqual(got, []string{perm}) {
				t.Errorf("%s %s = %v, want [%s]", act, subject, got, perm)
			}
		}
	}
	for _, a := range Abilities {
		if a.Requires != "ipam:read" {
			continue
		}
		if !reflect.DeepEqual(a.Action, []string{"read"}) {
			t.Errorf("ipam:read grants %v on %v; it must grant read only", a.Action, a.Subject)
		}
	}
}

// TestBMCReferencePermissions (024 T019/T020): viewers read the BMC status,
// device managers set and clear the reference (CASL configure DeviceBmc), and
// the retired warden-secrets routes are gone.
func TestBMCReferencePermissions(t *testing.T) {
	doc, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := Routes(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GET /api/ipam/v1/devices/{id}/bmc":    "ipam:read",
		"PUT /api/ipam/v1/devices/{id}/bmc":    "devices:manage",
		"DELETE /api/ipam/v1/devices/{id}/bmc": "devices:manage",
	}
	got := map[string]bool{}
	for _, r := range routes {
		k := r.Method + " " + r.Path
		if regexp.MustCompile(`warden-secrets`).MatchString(r.Path) {
			t.Errorf("retired route still declared: %s", k)
		}
		if p, ok := want[k]; ok {
			got[k] = true
			if r.Permission != p {
				t.Errorf("%s permission %s want %s", k, r.Permission, p)
			}
			if r.Method == "PUT" && r.MaxBodyBytes != 1024 {
				t.Errorf("%s body limit %d want 1024", k, r.MaxBodyBytes)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("routes %v", got)
	}
	ok := false
	for _, a := range Abilities {
		ok = ok || (reflect.DeepEqual(a.Action, []string{"configure"}) && reflect.DeepEqual(a.Subject, []string{"DeviceBmc"}) && a.Requires == "devices:manage")
	}
	if !ok {
		t.Fatal("CASL {configure, DeviceBmc} -> devices:manage")
	}
}
