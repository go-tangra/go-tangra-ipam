package openapi

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// loadDoc parses and validates the embedded document exactly as httpapi.Server
// does (uuid format registered, examples validation disabled).
func loadDoc(t *testing.T) *openapi3.T {
	t.Helper()
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(Ipam)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := doc.Validate(loader.Context, openapi3.DisableExamplesValidation()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return doc
}

func TestDocumentValidatesAndRoutes(t *testing.T) {
	doc := loadDoc(t)

	// The router must build (this is how the server mounts routes).
	servers := doc.Servers
	doc.Servers = nil
	if _, err := gorillamux.NewRouter(doc); err != nil {
		t.Fatalf("router: %v", err)
	}
	doc.Servers = servers

	if len(doc.Paths.Map()) == 0 {
		t.Fatal("no paths declared")
	}
}

// TestEveryRouteDeclaresPermissionOrPublic mirrors the manifest's route rule so
// a route without a permission cannot slip in.
func TestEveryRouteDeclaresPermissionOrPublic(t *testing.T) {
	doc := loadDoc(t)
	for p, item := range doc.Paths.Map() {
		if !strings.HasPrefix(p, "/api/ipam/v1/") {
			t.Errorf("path %s is not under /api/ipam/v1", p)
		}
		for m, op := range item.Operations() {
			perm, _ := op.Extensions["x-freya-permission"].(string)
			public, _ := op.Extensions["x-freya-public"].(bool)
			if !public && perm == "" {
				t.Errorf("%s %s declares neither permission nor public", m, p)
			}
			if public && perm != "" {
				t.Errorf("%s %s is both public and permissioned", m, p)
			}
		}
	}
}

// TestHealthIsPublic pins the one public route.
func TestHealthIsPublic(t *testing.T) {
	doc := loadDoc(t)
	item := doc.Paths.Find("/api/ipam/v1/health")
	if item == nil || item.Get == nil {
		t.Fatal("health route missing")
	}
	if public, _ := item.Get.Extensions["x-freya-public"].(bool); !public {
		t.Fatal("health must be public")
	}
}

// TestStreamHasNoTimeout guards the gateway's 5m MaxDurationRoute: the SSE route
// must not carry an x-freya-timeout-seconds larger than 300 (we omit it).
func TestStreamHasNoTimeout(t *testing.T) {
	doc := loadDoc(t)
	item := doc.Paths.Find("/api/ipam/v1/stream")
	if item == nil || item.Get == nil {
		t.Fatal("stream route missing")
	}
	if v, ok := item.Get.Extensions["x-freya-timeout-seconds"]; ok {
		n, _ := v.(float64)
		if n > 300 {
			t.Fatalf("stream timeout %v exceeds gateway MaxDurationRoute (300)", n)
		}
	}
}

// TestHostSyncContract (T068): the host-sync paths, schemas and bounds of
// contracts/ipam-http.md.
func TestHostSyncContract(t *testing.T) {
	doc := loadDoc(t)
	settings := doc.Components.Schemas["HostSyncSettings"].Value
	if settings == nil || settings.AdditionalProperties.Has == nil || *settings.AdditionalProperties.Has {
		t.Fatal("HostSyncSettings must forbid additional properties")
	}
	fim := settings.Properties["full_interval_minutes"].Value
	if *fim.Min != 15 || *fim.Max != 1440 {
		t.Fatal("interval bounds")
	}
	ex := settings.Properties["excluded_interfaces"].Value
	if *ex.MaxItems != 64 || ex.Items.Value.Pattern != `^[A-Za-z0-9*?._:-]{1,64}$` {
		t.Fatal("exclusion bounds")
	}
	for _, name := range []string{"HostSyncStatus", "DeviceHostSync", "ResyncResult", "HypervisorGuest", "HostSyncIssue"} {
		if doc.Components.Schemas[name] == nil {
			t.Errorf("schema %s missing", name)
		}
	}
	for path, methods := range map[string][]string{
		"/api/ipam/v1/host-sync/settings":               {"GET", "PUT"},
		"/api/ipam/v1/host-sync/status":                 {"GET"},
		"/api/ipam/v1/host-sync/resync":                 {"POST"},
		"/api/ipam/v1/devices/{id}/host-sync":           {"GET", "POST"},
		"/api/ipam/v1/devices/{id}/guests":              {"GET"},
		"/api/ipam/v1/ip-addresses/{id}/clear-conflict": {"POST"},
	} {
		item := doc.Paths.Find(path)
		if item == nil {
			t.Fatalf("%s missing", path)
		}
		for _, m := range methods {
			op := item.GetOperation(m)
			if op == nil {
				t.Fatalf("%s %s missing", m, path)
			}
			if m != "GET" {
				csrf := false
				for _, p := range op.Parameters {
					csrf = csrf || (p.Value != nil && p.Value.Name == "X-CSRF-Token" && p.Value.Required)
				}
				if !csrf {
					t.Errorf("%s %s must require the CSRF parameter", m, path)
				}
				if _, ok := op.Extensions["x-freya-max-body-bytes"]; !ok {
					t.Errorf("%s %s must bound its body", m, path)
				}
			}
		}
	}
	for path, params := range map[string][]string{"/api/ipam/v1/devices": {"source", "report_state"}, "/api/ipam/v1/ip-addresses": {"conflict", "report_state"}} {
		op := doc.Paths.Find(path).Get
		for _, want := range params {
			found := false
			for _, p := range op.Parameters {
				found = found || (p.Value != nil && p.Value.Name == want)
			}
			if !found {
				t.Errorf("%s filter %s", path, want)
			}
		}
	}
}

// TestSubnetSNMPContract (021 T022/T042/T049): the SNMP credential routes,
// their permissions, CSRF, body bounds and the write-only schemas.
func TestSubnetSNMPContract(t *testing.T) {
	doc := loadDoc(t)
	type want struct {
		perm  string
		limit float64
	}
	for path, ops := range map[string]map[string]want{
		"/api/ipam/v1/subnets/{id}/snmp": {
			"GET": {"ipam:read", 0}, "PUT": {"subnets:manage", 4096}, "DELETE": {"subnets:manage", 0},
		},
		"/api/ipam/v1/subnets/{id}/snmp/test": {"POST": {"scan:run", 1024}},
	} {
		item := doc.Paths.Find(path)
		if item == nil {
			t.Fatalf("%s missing", path)
		}
		for m, w := range ops {
			op := item.GetOperation(m)
			if op == nil {
				t.Fatalf("%s %s missing", m, path)
			}
			if perm, _ := op.Extensions["x-freya-permission"].(string); perm != w.perm {
				t.Errorf("%s %s permission %q want %q", m, path, perm, w.perm)
			}
			if w.limit > 0 {
				if n, _ := op.Extensions["x-freya-max-body-bytes"].(float64); n != w.limit {
					t.Errorf("%s %s body limit %v want %v", m, path, n, w.limit)
				}
			}
			if m != "GET" {
				csrf := false
				for _, p := range op.Parameters {
					csrf = csrf || (p.Value != nil && p.Value.Name == "X-CSRF-Token" && p.Value.Required)
				}
				if !csrf {
					t.Errorf("%s %s must require the CSRF parameter", m, path)
				}
			}
		}
	}
	in := doc.Components.Schemas["SubnetSNMPInput"].Value
	if in == nil || in.AdditionalProperties.Has == nil || *in.AdditionalProperties.Has {
		t.Fatal("SubnetSNMPInput must forbid additional properties")
	}
	for _, f := range []string{"community", "user", "auth_password", "priv_password"} {
		if p := in.Properties[f]; p == nil || p.Value.MaxLength == nil || *p.Value.MaxLength != 256 || !p.Value.WriteOnly {
			t.Errorf("input %s must be write-only with maxLength 256", f)
		}
	}
	if got := in.Properties["auth_protocol"].Value.Enum; len(got) != 6 {
		t.Errorf("auth protocols %v", got)
	}
	if got := in.Properties["priv_protocol"].Value.Enum; len(got) != 6 {
		t.Errorf("priv protocols %v", got)
	}
	for _, name := range []string{"SubnetSNMPStatus", "SNMPSummary", "SNMPTestResult"} {
		s := doc.Components.Schemas[name]
		if s == nil {
			t.Fatalf("schema %s missing", name)
		}
		for f := range s.Value.Properties {
			switch f {
			case "community", "user", "auth_password", "priv_password":
				t.Errorf("response schema %s exposes %s", name, f)
			}
		}
	}
}
