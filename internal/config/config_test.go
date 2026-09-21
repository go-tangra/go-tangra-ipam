package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fconfig "github.com/go-freya/freya/config"
)

// valid returns a Config that passes both the framework and module Validate.
func valid() Config {
	c := Default()
	c.ServiceName = "ipam"
	c.TrustDomain = "example.org"
	c.Authz.Path = "/etc/ipam/policy.yaml"
	c.DB.DSN = "postgres://localhost/ipam"
	c.Valkey.Addresses = []string{"valkey:6379"}
	c.KEK = KEK{Source: "file", Path: "/etc/ipam/kek"}
	c.Gateway.Issuer = "https://gw.example.org"
	return c
}

func TestDefaultSecure(t *testing.T) {
	d := Default()
	if d.KEK.Source != "file" {
		t.Errorf("kek.source default = %q, want file", d.KEK.Source)
	}
	if d.Valkey.AllowPlaintext {
		t.Error("valkey.allow_plaintext must default to false")
	}
	if !d.Events.Enabled {
		t.Error("events.enabled must default to true")
	}
	if d.DB.MaxConns != 16 {
		t.Errorf("db.max_conns default = %d, want 16", d.DB.MaxConns)
	}
	if d.Scan.MaxHosts != 1024 || d.Scan.Concurrency != 50 || d.Scan.TimeoutMs != 1000 || d.Scan.Workers != 3 || d.Scan.MaxRetries != 3 {
		t.Errorf("unexpected scan defaults: %+v", d.Scan)
	}
	if d.Allocation.SkipFirst != 0 || d.Allocation.SkipLast != 0 {
		t.Errorf("unexpected allocation defaults: %+v", d.Allocation)
	}
	if d.Gateway.Service != "gateway" {
		t.Errorf("gateway.service default = %q, want gateway", d.Gateway.Service)
	}
	if d.Warden.Service != "warden" {
		t.Errorf("warden.service default = %q, want warden", d.Warden.Service)
	}
	if d.Limits.MaxRequestBytes != 1<<20 || d.Limits.MaxBackupBytes != 32<<20 {
		t.Errorf("unexpected limits defaults: %+v", d.Limits)
	}
	// A pristine Default() has no service_name/db and must not validate.
	if err := Default().Validate(); err == nil {
		t.Error("Default() must not validate without required fields")
	}
}

func TestValidateOK(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	// env source is also accepted.
	c := valid()
	c.KEK = KEK{Source: "env", Env: "IPAM_KEK"}
	if err := c.Validate(); err != nil {
		t.Fatalf("env kek rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"framework", func(c *Config) { c.ServiceName = "" }, "service_name"},
		{"db missing", func(c *Config) { c.DB.DSN = "" }, "db.dsn"},
		{"db prod tls", func(c *Config) { c.Env = "production" }, "sslmode"},
		{"valkey missing", func(c *Config) { c.Valkey.Addresses = nil }, "valkey.addresses"},
		{"valkey prod plaintext", func(c *Config) {
			c.Env = "production"
			c.DB.DSN = "postgres://h/db?sslmode=verify-full"
			c.Valkey.AllowPlaintext = true
		}, "allow_plaintext"},
		{"kek file no path", func(c *Config) { c.KEK = KEK{Source: "file"} }, "kek.path"},
		{"kek env no env", func(c *Config) { c.KEK = KEK{Source: "env"} }, "kek.env"},
		{"kek bad source", func(c *Config) { c.KEK = KEK{Source: "vault"} }, "kek.source"},
		{"scan max_hosts", func(c *Config) { c.Scan.MaxHosts = 0 }, "scan.max_hosts"},
		{"scan concurrency", func(c *Config) { c.Scan.Concurrency = 0 }, "scan.concurrency"},
		{"scan timeout", func(c *Config) { c.Scan.TimeoutMs = 1 }, "scan.timeout_ms"},
		{"scan workers", func(c *Config) { c.Scan.Workers = 0 }, "scan.workers"},
		{"scan retries", func(c *Config) { c.Scan.MaxRetries = -1 }, "scan.max_retries"},
		{"alloc skip_first", func(c *Config) { c.Allocation.SkipFirst = -1 }, "allocation.skip_first"},
		{"alloc skip_last", func(c *Config) { c.Allocation.SkipLast = -1 }, "allocation.skip_last"},
		{"ipmi timeout", func(c *Config) { c.IPMI.TimeoutSeconds = 0 }, "ipmi.timeout_seconds"},
		{"kvm token ttl", func(c *Config) { c.KVM.TokenTTLSeconds = 1 }, "kvm.token_ttl_seconds"},
		{"kvm session", func(c *Config) { c.KVM.SessionSeconds = 1 }, "kvm.session_seconds"},
		{"gateway service", func(c *Config) { c.Gateway.Service = "" }, "gateway.service"},
		{"gateway issuer", func(c *Config) { c.Gateway.Issuer = "http://gw" }, "gateway.issuer"},
		{"mesh prod insecure", func(c *Config) {
			c.Env = "production"
			c.DB.DSN = "postgres://h/db?sslmode=verify-full"
			c.MeshEnroll.Enabled = true
			c.MeshEnroll.Insecure = true
		}, "mesh_enroll.insecure"},
		{"max request", func(c *Config) { c.Limits.MaxRequestBytes = 1 }, "max_request_bytes"},
		{"max backup", func(c *Config) { c.Limits.MaxBackupBytes = 1 }, "max_backup_bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateProdOK(t *testing.T) {
	c := valid()
	c.Env = "production"
	c.DB.DSN = "postgres://h/db?sslmode=verify-full"
	c.MeshEnroll.Enabled = true // enabled + secure is fine
	if err := c.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestWarnings(t *testing.T) {
	c := valid()
	if w := c.Warnings(); len(w) != 0 {
		t.Fatalf("secure config produced warnings: %v", w)
	}
	c.Valkey.AllowPlaintext = true
	c.MeshEnroll.Enabled = true
	c.MeshEnroll.Insecure = true
	c.Admin.EnablePprof = true // framework warning path
	w := c.Warnings()
	joined := strings.Join(w, "\n")
	if !strings.Contains(joined, "valkey.allow_plaintext") {
		t.Errorf("missing valkey warning: %v", w)
	}
	if !strings.Contains(joined, "mesh_enroll.insecure") {
		t.Errorf("missing mesh_enroll warning: %v", w)
	}
	if !strings.Contains(joined, "pprof") {
		t.Errorf("framework warnings not surfaced: %v", w)
	}
}

func TestDurationHelpers(t *testing.T) {
	c := valid()
	c.Scan.TimeoutMs = 500
	c.IPMI.TimeoutSeconds = 20
	c.KVM.TokenTTLSeconds = 90
	c.KVM.SessionSeconds = 1800
	if c.ScanTimeout() != 500*time.Millisecond {
		t.Errorf("ScanTimeout=%v", c.ScanTimeout())
	}
	if c.IPMITimeout() != 20*time.Second {
		t.Errorf("IPMITimeout=%v", c.IPMITimeout())
	}
	if c.KVMTokenTTL() != 90*time.Second {
		t.Errorf("KVMTokenTTL=%v", c.KVMTokenTTL())
	}
	if c.KVMSession() != 30*time.Minute {
		t.Errorf("KVMSession=%v", c.KVMSession())
	}
}

func TestAddrHelpers(t *testing.T) {
	c := valid()
	c.Config.Server = fconfig.Server{GRPCAddr: ":1", HTTPAddr: ":2"}
	c.Config.Admin.Addr = ":3"
	if c.GRPCAddr() != ":1" || c.HTTPAddr() != ":2" || c.AdminAddr() != ":3" {
		t.Errorf("addr helpers: grpc=%q http=%q admin=%q", c.GRPCAddr(), c.HTTPAddr(), c.AdminAddr())
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ipam.yaml")
	yaml := `
service_name: ipam
trust_domain: example.org
authz:
  source: file
  path: /etc/ipam/policy.yaml
db:
  dsn: postgres://localhost/ipam
  max_conns: 8
valkey:
  addresses: ["valkey:6379"]
  allow_plaintext: true
kek:
  source: env
  env: IPAM_KEK
warden:
  endpoint: https://warden.example.org:9443
  service: warden
scan:
  max_hosts: 4096
  concurrency: 100
  timeout_ms: 750
  workers: 5
  max_retries: 2
allocation:
  skip_first: 1
  skip_last: 1
  reserved_ranges: ["10.0.0.10-10.0.0.20"]
ipmi:
  timeout_seconds: 30
kvm:
  token_ttl_seconds: 120
  session_seconds: 7200
events:
  enabled: false
gateway:
  service: gateway
  issuer: https://gw.example.org
mesh_enroll:
  enabled: true
  enroll_url: https://lcm.example.org/enroll
  lcm_grpc: lcm.example.org:9443
  tenant_id: t1
  token_file: /var/lib/ipam/token
  state_file: /var/lib/ipam/state
  insecure: true
limits_ipam:
  max_request_bytes: 2097152
  max_backup_bytes: 67108864
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DB.MaxConns != 8 || c.Scan.MaxHosts != 4096 || c.Scan.Workers != 5 {
		t.Errorf("unexpected loaded values: %+v %+v", c.DB, c.Scan)
	}
	if !c.Valkey.AllowPlaintext || !c.MeshEnroll.Insecure || c.Events.Enabled {
		t.Errorf("bool fields not loaded: %+v %+v %+v", c.Valkey, c.MeshEnroll, c.Events)
	}
	if len(c.Allocation.ReservedRanges) != 1 || c.Allocation.ReservedRanges[0] != "10.0.0.10-10.0.0.20" {
		t.Errorf("reserved_ranges not loaded: %+v", c.Allocation)
	}
	if c.Warden.Endpoint == "" || c.KVM.SessionSeconds != 7200 {
		t.Errorf("warden/kvm not loaded: %+v %+v", c.Warden, c.KVM)
	}
	// mesh_enroll.insecure is fine because Env is not production here.
	if err := c.Validate(); err != nil {
		t.Fatalf("loaded config invalid: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("Load of missing file must error")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("db:\n  unknown_field: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("Load with unknown field must error")
	}
}
