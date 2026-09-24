// Package config loads and validates the IPAM service configuration: the Freya
// framework config plus the module's own sections. Every value is explicit;
// insecure opt-outs are named and surfaced at start (Constitution I/VII). The
// subnet/address store, the Valkey event bus, the scan worker pool, the IPMI
// power controller, the KVM token minter and the module's own mesh enrollment
// all read from here.
//
// The module's yaml keys are chosen to NOT collide with the framework sections
// the embedded config already owns (server, admin, discovery, limits, identity,
// authz): the module's request/backup bounds live under "limits_ipam", never
// the framework "limits".
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	fconfig "github.com/go-tangra/go-tangra/v4/config"
	"gopkg.in/yaml.v3"
)

// Config is the IPAM service configuration. The embedded framework config
// (inline) already carries service_name, trust_domain, env, identity, authz,
// limits, admin, discovery and server (grpc_addr/http_addr); the fields below
// are the module's own.
type Config struct {
	fconfig.Config `yaml:",inline"`

	DB         DB         `yaml:"db"`
	Valkey     Valkey     `yaml:"valkey"`
	KEK        KEK        `yaml:"kek"`
	Warden     Warden     `yaml:"warden"`
	Scan       Scan       `yaml:"scan"`
	Allocation Allocation `yaml:"allocation"`
	IPMI       IPMI       `yaml:"ipmi"`
	KVM        KVM        `yaml:"kvm"`
	Events     Events     `yaml:"events"`
	Gateway    Gateway    `yaml:"gateway"`
	MeshEnroll MeshEnroll `yaml:"mesh_enroll"`
	Limits     Limits     `yaml:"limits_ipam"`
}

// DB configures the PostgreSQL/TimescaleDB store.
type DB struct {
	DSN        string `yaml:"dsn"`
	MigrateDSN string `yaml:"migrate_dsn"`
	MaxConns   int32  `yaml:"max_conns"`
}

// Valkey configures the platform event bus and any shared cache.
type Valkey struct {
	Addresses      []string `yaml:"addresses"`
	Username       string   `yaml:"username"`
	Password       string   `yaml:"password"`
	AllowPlaintext bool     `yaml:"allow_plaintext"`
	CAFile         string   `yaml:"ca_file"`
}

// KEK names where the 32-byte key-encryption key (sealed owner/contact fields,
// SNMP/IPMI secret material sealed before hand-off to warden) comes from.
type KEK struct {
	Source string `yaml:"source"` // file | env
	Path   string `yaml:"path"`
	Env    string `yaml:"env"`
}

// Warden names the secrets service the module fetches SNMP/IPMI credentials
// from by reference (the module stores only warden ids, never the secret).
type Warden struct {
	Endpoint string `yaml:"endpoint"`
	Service  string `yaml:"service"`
}

// Scan bounds the async subnet-discovery worker pool.
type Scan struct {
	MaxHosts    int `yaml:"max_hosts"`   // largest subnet a single job may enumerate
	Concurrency int `yaml:"concurrency"` // in-flight probes per job
	TimeoutMs   int `yaml:"timeout_ms"`  // per-host probe timeout
	Workers     int `yaml:"workers"`     // job executor goroutines
	MaxRetries  int `yaml:"max_retries"` // per-job retry budget
}

// Allocation carries the default reservation policy applied when a subnet does
// not override it. ReservedRanges are "a-b" specs excluded from FirstFree.
type Allocation struct {
	SkipFirst      int      `yaml:"skip_first"`
	SkipLast       int      `yaml:"skip_last"`
	ReservedRanges []string `yaml:"reserved_ranges"`
}

// IPMI bounds the out-of-band power controller.
type IPMI struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// KVM bounds the console-session token minter.
type KVM struct {
	TokenTTLSeconds int `yaml:"token_ttl_seconds"`
	SessionSeconds  int `yaml:"session_seconds"`
}

// Events toggles the realtime publisher.
type Events struct {
	Enabled bool `yaml:"enabled"`
}

// Gateway names the application gateway and the platform token issuer.
type Gateway struct {
	Service string `yaml:"service"`
	Issuer  string `yaml:"issuer"`
}

// MeshEnroll configures how the IPAM SERVER obtains its own mesh SPIFFE SVID by
// enrolling with lcm over the network (identity.provider=provided). This is the
// module's own SVID enrollment; it is unrelated to any endpoint/agent concept.
type MeshEnroll struct {
	Enabled       bool   `yaml:"enabled"`
	EnrollURL     string `yaml:"enroll_url"`
	LCMGRPCTarget string `yaml:"lcm_grpc"`
	TenantID      string `yaml:"tenant_id"`
	TokenFile     string `yaml:"token_file"`
	StateFile     string `yaml:"state_file"`
	Insecure      bool   `yaml:"insecure"`
}

// Limits bound the module's request shapes. They live under "limits_ipam" so
// they never collide with the framework's own "limits" section.
type Limits struct {
	MaxRequestBytes int64 `yaml:"max_request_bytes"`
	MaxBackupBytes  int64 `yaml:"max_backup_bytes"`
}

// Default returns secure defaults on top of the Freya defaults.
func Default() Config {
	return Config{
		Config:     fconfig.Default(),
		DB:         DB{MaxConns: 16},
		KEK:        KEK{Source: "file"},
		Warden:     Warden{Service: "warden"},
		Scan:       Scan{MaxHosts: 1024, Concurrency: 50, TimeoutMs: 1000, Workers: 3, MaxRetries: 3},
		Allocation: Allocation{SkipFirst: 0, SkipLast: 0},
		IPMI:       IPMI{TimeoutSeconds: 15},
		KVM:        KVM{TokenTTLSeconds: 60, SessionSeconds: 3600},
		Events:     Events{Enabled: true},
		Gateway:    Gateway{Service: "gateway"},
		Limits:     Limits{MaxRequestBytes: 1 << 20, MaxBackupBytes: 32 << 20},
	}
}

// Load reads YAML over Default(); unknown fields are rejected.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied config path
	if err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks the Freya config and every module section. It refuses a
// missing db or kek outright and enforces the production TLS guards.
func (c Config) Validate() error {
	if err := c.Config.Validate(); err != nil {
		return err
	}
	prod := c.IsProduction()
	if c.DB.DSN == "" {
		return errors.New("config: db.dsn is required")
	}
	if prod && !strings.Contains(c.DB.DSN, "sslmode=verify-full") && !strings.Contains(c.DB.DSN, "sslmode=verify-ca") {
		return errors.New("config: db.dsn must use sslmode=verify-full (or verify-ca) in production")
	}
	if len(c.Valkey.Addresses) == 0 {
		return errors.New("config: valkey.addresses is required")
	}
	if prod && c.Valkey.AllowPlaintext {
		return errors.New("config: valkey.allow_plaintext is not permitted in production")
	}
	switch c.KEK.Source {
	case "file":
		if c.KEK.Path == "" {
			return errors.New("config: kek.path is required for kek.source file")
		}
	case "env":
		if c.KEK.Env == "" {
			return errors.New("config: kek.env is required for kek.source env")
		}
	default:
		return errors.New("config: kek.source must be file or env")
	}
	if c.Scan.MaxHosts < 1 || c.Scan.MaxHosts > 1<<20 {
		return errors.New("config: scan.max_hosts must be within [1, 1048576]")
	}
	if c.Scan.Concurrency < 1 || c.Scan.Concurrency > 4096 {
		return errors.New("config: scan.concurrency must be within [1, 4096]")
	}
	if c.Scan.TimeoutMs < 10 || c.Scan.TimeoutMs > 60000 {
		return errors.New("config: scan.timeout_ms must be within [10, 60000]")
	}
	if c.Scan.Workers < 1 || c.Scan.Workers > 256 {
		return errors.New("config: scan.workers must be within [1, 256]")
	}
	if c.Scan.MaxRetries < 0 || c.Scan.MaxRetries > 100 {
		return errors.New("config: scan.max_retries must be within [0, 100]")
	}
	if c.Allocation.SkipFirst < 0 || c.Allocation.SkipFirst > 1<<16 {
		return errors.New("config: allocation.skip_first must be within [0, 65536]")
	}
	if c.Allocation.SkipLast < 0 || c.Allocation.SkipLast > 1<<16 {
		return errors.New("config: allocation.skip_last must be within [0, 65536]")
	}
	if c.IPMI.TimeoutSeconds < 1 || c.IPMI.TimeoutSeconds > 300 {
		return errors.New("config: ipmi.timeout_seconds must be within [1, 300]")
	}
	if c.KVM.TokenTTLSeconds < 5 || c.KVM.TokenTTLSeconds > 3600 {
		return errors.New("config: kvm.token_ttl_seconds must be within [5, 3600]")
	}
	if c.KVM.SessionSeconds < 60 || c.KVM.SessionSeconds > 86400 {
		return errors.New("config: kvm.session_seconds must be within [60, 86400]")
	}
	if c.Gateway.Service == "" {
		return errors.New("config: gateway.service is required")
	}
	if iu, err := url.Parse(c.Gateway.Issuer); err != nil || iu.Scheme != "https" || iu.Host == "" {
		return errors.New("config: gateway.issuer must be an https origin")
	}
	if prod && c.MeshEnroll.Enabled && c.MeshEnroll.Insecure {
		return errors.New("config: mesh_enroll.insecure is not permitted in production")
	}
	if c.Limits.MaxRequestBytes < 1<<10 || c.Limits.MaxRequestBytes > 64<<20 {
		return errors.New("config: limits_ipam.max_request_bytes must be within [1 KiB, 64 MiB]")
	}
	if c.Limits.MaxBackupBytes < 1<<10 || c.Limits.MaxBackupBytes > 256<<20 {
		return errors.New("config: limits_ipam.max_backup_bytes must be within [1 KiB, 256 MiB]")
	}
	return nil
}

// Warnings lists accepted insecure opt-outs (surfaced at start).
func (c Config) Warnings() []string {
	w := c.Config.Warnings()
	if c.Valkey.AllowPlaintext {
		w = append(w, "valkey.allow_plaintext: event-bus traffic without TLS (development only)")
	}
	if c.MeshEnroll.Enabled && c.MeshEnroll.Insecure {
		w = append(w, "mesh_enroll.insecure: SVID enrollment without TLS (development only)")
	}
	return w
}

// GRPCAddr is the mesh gRPC listener (framework server section).
func (c Config) GRPCAddr() string { return c.Config.Server.GRPCAddr }

// HTTPAddr is the mesh HTTP listener (framework server section).
func (c Config) HTTPAddr() string { return c.Config.Server.HTTPAddr }

// AdminAddr is the framework admin/operations listener.
func (c Config) AdminAddr() string { return c.Config.Admin.Addr }

// ScanTimeout is the per-host probe timeout.
func (c Config) ScanTimeout() time.Duration {
	return time.Duration(c.Scan.TimeoutMs) * time.Millisecond
}

// IPMITimeout is the out-of-band power-controller call timeout.
func (c Config) IPMITimeout() time.Duration {
	return time.Duration(c.IPMI.TimeoutSeconds) * time.Second
}

// KVMTokenTTL is the lifetime of a minted console-session token.
func (c Config) KVMTokenTTL() time.Duration {
	return time.Duration(c.KVM.TokenTTLSeconds) * time.Second
}

// KVMSession is the maximum console-session duration.
func (c Config) KVMSession() time.Duration {
	return time.Duration(c.KVM.SessionSeconds) * time.Second
}
