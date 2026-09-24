// Package warden is a secret-reference client. The IPAM module stores only
// opaque warden references (snmp_secret_ref, ipmi_secret_ref) on subnets and
// devices; the actual SNMP/BMC/IPMI credential material is fetched from the
// warden module at USE TIME (immediately before an SNMP walk, an IPMI power
// action or a KVM login) and is never persisted, cached to disk or logged.
//
// A secret is a map[string]string of named fields (for example "username",
// "password", "host_url"). Only GetSecret ever returns values; ListSecrets and
// the SecretMeta it returns are metadata only and are safe to log. Callers must
// never log the map returned by GetSecret.
package warden

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
)

// SecretMeta is metadata about a secret. It NEVER carries secret values (no
// password, key or token) and is safe to return to the browser and to log.
type SecretMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Optional, non-sensitive descriptors surfaced by warden.
	Username string `json:"username,omitempty"`
	HostURL  string `json:"host_url,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
}

// Client fetches secret material by opaque reference at use time and lists the
// non-sensitive metadata of the secrets the caller may reach.
type Client interface {
	// GetSecret resolves a warden reference to its named credential fields
	// (e.g. {"username":..., "password":...}). The result is used immediately
	// and MUST NOT be stored or logged. It is an error to call with an empty ref.
	GetSecret(ctx context.Context, ref string) (map[string]string, error)
	// ListSecrets returns metadata only (id/name/description) for the secrets
	// matching query. It never returns secret values.
	ListSecrets(ctx context.Context, query string) ([]SecretMeta, error)
}

// ErrEmptyRef is returned when a secret reference is empty.
var ErrEmptyRef = errors.New("warden: empty secret reference")

// ErrNotFound is returned when no secret matches the reference.
var ErrNotFound = errors.New("warden: secret not found")

// ---- real gRPC-backed client ----

// grpcClient talks to the warden module's Secrets gRPC service over the Freya
// SPIFFE mTLS channel. It combines the metadata (Get) and material (GetPassword)
// RPCs into the field map returned by GetSecret.
type grpcClient struct {
	cc grpc.ClientConnInterface
}

// New builds a Client backed by a gRPC connection to the warden module.
//
// The warden surface is warden.v1.Secrets:
//
//	rpc Get(GetRequest{id}) returns (GetResponse{Secret})                  // metadata only
//	rpc GetPassword(GetPasswordRequest{id,version}) returns (GetPasswordResponse{password}) // material; audited
//	rpc Check(CheckRequest) returns (CheckResponse)
//
// GetSecret(ref) should call Secrets/Get for the descriptor fields (name,
// username, host_url, ...) and Secrets/GetPassword for the material, then
// assemble the map {"username":..., "host_url":..., "password":...}.
//
// TODO(ipam): wire the concrete calls once the warden proto is a dependency of
// this module. That requires adding to go.mod (owned elsewhere):
//
//	require  github.com/go-tangra/go-tangra-warden/v4 v0.0.0-...
//	replace  github.com/go-tangra/go-tangra-warden/v4 => ../warden
//
// and then, with wardenv1 "github.com/go-tangra/go-tangra-warden/sdk/v4/api/proto/warden/v1":
//
//	sc := wardenv1.NewSecretsClient(c.cc)
//	meta, err := sc.Get(ctx, &wardenv1.GetRequest{Id: ref})
//	pw, err   := sc.GetPassword(ctx, &wardenv1.GetPasswordRequest{Id: ref})
//	return map[string]string{"username": meta.GetSecret().GetUsername(),
//	    "host_url": meta.GetSecret().GetHostUrl(), "password": pw.GetPassword()}, nil
//
// Until that dependency is present the real client is inert: GetSecret and
// ListSecrets return errNotWired so the module fails closed (no secret is ever
// fabricated). Tests use Fake, which is fully functional.
func New(cc grpc.ClientConnInterface) Client {
	return &grpcClient{cc: cc}
}

// errNotWired signals that the real warden RPCs are not yet bound (see New).
var errNotWired = errors.New("warden: gRPC client not wired (warden proto not a module dependency)")

func (c *grpcClient) GetSecret(_ context.Context, ref string) (map[string]string, error) {
	if ref == "" {
		return nil, ErrEmptyRef
	}
	// TODO(ipam): call warden.v1.Secrets/Get + /GetPassword (see New). Never log
	// the assembled map.
	return nil, errNotWired
}

func (c *grpcClient) ListSecrets(_ context.Context, _ string) ([]SecretMeta, error) {
	// TODO(ipam): warden has no List RPC; expose the caller-reachable secrets
	// via the appropriate warden metadata RPC once available (see New).
	return nil, errNotWired
}

// ---- in-memory fake for tests ----

// Fake is an in-memory Client for tests. It resolves refs to secret values and
// exposes metadata separately, so allocation, scan orchestration, IPMI/KVM and
// redaction can be tested without touching warden or the network. Values held
// by a Fake are test fixtures and never real credentials.
type Fake struct {
	secrets map[string]map[string]string
	meta    map[string]SecretMeta
}

// NewFake builds an empty Fake.
func NewFake() *Fake {
	return &Fake{secrets: map[string]map[string]string{}, meta: map[string]SecretMeta{}}
}

// Put registers a secret's value map and its metadata under ref. The metadata's
// ID is set to ref. The value map is copied.
func (f *Fake) Put(ref string, value map[string]string, meta SecretMeta) {
	cp := make(map[string]string, len(value))
	for k, v := range value {
		cp[k] = v
	}
	f.secrets[ref] = cp
	meta.ID = ref
	f.meta[ref] = meta
}

// GetSecret returns a copy of the stored value map for ref.
func (f *Fake) GetSecret(_ context.Context, ref string) (map[string]string, error) {
	if ref == "" {
		return nil, ErrEmptyRef
	}
	v, ok := f.secrets[ref]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make(map[string]string, len(v))
	for k, val := range v {
		cp[k] = val
	}
	return cp, nil
}

// ListSecrets returns metadata (never values) for secrets whose name, id or
// description contains query (empty query returns all).
func (f *Fake) ListSecrets(_ context.Context, query string) ([]SecretMeta, error) {
	var out []SecretMeta
	for _, m := range f.meta {
		if query == "" || contains(m.Name, query) || contains(m.ID, query) || contains(m.Description, query) {
			out = append(out, m)
		}
	}
	return out, nil
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// interface conformance checks.
var (
	_ Client = (*grpcClient)(nil)
	_ Client = (*Fake)(nil)
)
