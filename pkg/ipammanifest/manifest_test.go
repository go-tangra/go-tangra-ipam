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
	if len(Permissions) != 13 {
		t.Fatalf("want 13 permissions, got %d", len(Permissions))
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
		"scan:run", "dns:manage", "backup:manage", "power:control", "kvm:access",
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
	if len(Nav) != 8 {
		t.Fatalf("want 8 nav entries, got %d", len(Nav))
	}
	wantPaths := map[string]bool{
		"/ipam": false, "/ipam/addresses": false, "/ipam/devices": false,
		"/ipam/vlans": false, "/ipam/locations": false, "/ipam/groups": false,
		"/ipam/scans": false, "/ipam/dashboard": false,
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
// module roles existed (auth now scopes them to the module).
func TestBuiltinGrantsUnchanged(t *testing.T) {
	all := []string{
		"ipam:read", "subnets:manage", "addresses:manage", "addresses:allocate",
		"devices:manage", "vlans:manage", "locations:manage", "groups:manage",
		"scan:run", "dns:manage", "backup:manage", "power:control", "kvm:access",
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
