package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra/v4/authz"
	"github.com/go-tangra/go-tangra/v4/catalogue"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/config"
	"github.com/go-tangra/go-tangra-ipam/v4/pkg/ipammanifest"
)

var placeholderRE = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

// joinValues are values of the shape the gateway fills in: core values,
// generated secrets and the host inputs.
func joinValues(consoleOrigin string) map[string]string {
	return map[string]string{
		"TRUST_DOMAIN": "infra.example.org", "GATEWAY_ISSUER": "https://portal.example.org:8443",
		"LCM_ENROLL_URL": "https://portal.example.org:8443/api/lcm/v1/enroll", "LCM_GRPC": "portal.example.org:9945",
		"GATEWAY_GRPC": "portal.example.org:9643", "AUTH_GRPC": "portal.example.org:9543", "MESH_TENANT_ID": "00000000-0000-0000-0000-000000000001",
		"MODULE_IMAGE": "ghcr.io/go-tangra/go-tangra-ipam", "MODULE_VERSION": "4.5.0",
		"GEN_PASSWORD_1": strings.Repeat("a", 48), "GEN_PASSWORD_2": strings.Repeat("b", 48),
		"GEN_KEY_1":             "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"MODULE_ADVERTISE_HOST": "ipam.example.org", "MODULE_BIND_IP": "10.0.0.5",
		"VALKEY_ADDRESS": "valkey.example.org:6380", "VALKEY_PASSWORD": strings.Repeat("c", 32), "VALKEY_CA_FILE": "/etc/tangra/valkey-ca.pem",
		// Optional input: left empty it renders as "" (gateway 4.12.0).
		"KVM_CONSOLE_ORIGIN": consoleOrigin,
	}
}

func render(t *testing.T, name string, values map[string]string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bundle", name))
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	rendered := placeholderRE.ReplaceAllStringFunc(string(raw), func(m string) string {
		k := placeholderRE.FindStringSubmatch(m)[1]
		v, ok := values[k]
		if !ok {
			missing = append(missing, k)
		}
		return v
	})
	if len(missing) > 0 {
		t.Fatalf("%s uses placeholders the gateway does not fill: %v", name, missing)
	}
	return rendered
}

// tangra-module.yaml is a valid descriptor for this module: its name is the
// manifest's, its route scope covers every prefix the manifest registers, its
// host inputs accept values of the shape operators give, and it builds a valid
// catalogue entry with the packed bundle and the manifest's permissions.
func TestCatalogueDescriptorBuildsEntry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tangra-module.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := catalogue.ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ipammanifest.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if d.Module != m.Module {
		t.Fatalf("descriptor module %q, manifest %q", d.Module, m.Module)
	}
	for _, p := range m.Prefixes {
		if !strings.Contains(" "+strings.Join(d.Routes.Prefixes, " ")+" ", " "+p+" ") {
			t.Fatalf("manifest prefix %s is outside routes.prefixes %v", p, d.Routes.Prefixes)
		}
	}
	values := joinValues("https://portal.example.org:8444")
	for _, in := range d.HostInputs {
		if !regexp.MustCompile(in.Pattern).MatchString(values[in.Key]) {
			t.Fatalf("host input %s refuses %q", in.Key, values[in.Key])
		}
	}
	bundle, err := catalogue.PackBundle(os.DirFS(filepath.Join("..", "..", d.Bundle.Dir)))
	if err != nil {
		t.Fatal(err)
	}
	e, err := catalogue.BuildEntry(d, "4.5.0", "go-tangra/go-tangra-ipam", ipammanifest.PermissionRefs(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CheckBundle(bundle); err != nil {
		t.Fatal(err)
	}
	files, err := catalogue.ReadBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"compose.yaml", "init-db.sh"}, d.Bundle.Templates...) {
		if _, ok := files[name]; !ok {
			t.Fatalf("%s missing from the bundle", name)
		}
	}
}

// The join bundle's templates, rendered with values of the shape the gateway
// fills in, are a valid production configuration and policy.
func TestJoinBundleRendersToValidProductionConfig(t *testing.T) {
	for _, origin := range []string{"", "https://portal.example.org:8444"} {
		values := joinValues(origin)
		dir := t.TempDir()
		for _, name := range []string{"config.yaml", "policy.yaml"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(render(t, name, values)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// compose.yaml uses only values the gateway writes to .env.
		render(t, "compose.yaml", values)
		cfg, err := config.Load(filepath.Join(dir, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("rendered config invalid: %v", err)
		}
		if !cfg.IsProduction() || cfg.Gateway.Issuer != "https://portal.example.org:8443" || cfg.KVMConsoleOrigin() != origin ||
			cfg.KEK.Env != "IPAM_KEK" || !cfg.Events.Enabled {
			t.Fatalf("%+v %+v %+v", cfg.Gateway, cfg.KVM, cfg.KEK)
		}
		// Events go to the core's event bus over TLS, never a private one.
		if cfg.Valkey.AllowPlaintext || len(cfg.Valkey.Addresses) != 1 || cfg.Valkey.Addresses[0] != values["VALKEY_ADDRESS"] ||
			cfg.Valkey.Username != "ipam" || cfg.Valkey.Password != values["VALKEY_PASSWORD"] || cfg.Valkey.CAFile == "" {
			t.Fatalf("%+v", cfg.Valkey.Addresses)
		}
		f, err := os.Open(filepath.Join(dir, "policy.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		pol, err := authz.Load(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("rendered policy invalid: %v", err)
		}
		if pol == nil {
			t.Fatal("no policy")
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "policy.yaml"))
		if !strings.Contains(string(raw), "spiffe://infra.example.org/svc/gateway") {
			t.Fatal(string(raw))
		}
	}
}
