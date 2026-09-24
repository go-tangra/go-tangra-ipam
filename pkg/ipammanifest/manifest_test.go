package ipammanifest

import "testing"

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

func TestSeedRequest(t *testing.T) {
	req := SeedRequest()
	if len(req.Permissions) != len(Permissions) {
		t.Fatalf("seed permissions = %d, want %d", len(req.Permissions), len(Permissions))
	}
	if len(req.BuiltinGrants) != 5 {
		t.Fatalf("want 5 builtin grants, got %d", len(req.BuiltinGrants))
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
