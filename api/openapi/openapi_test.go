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
