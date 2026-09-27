// Package warden is IPAM's client for the warden module's secrets. A device
// stores only an opaque warden secret id (ipmi_secret_ref); the BMC username
// and password are fetched from warden at USE TIME, immediately before a power
// action, a sensor/SEL read or a KVM login, and are never persisted, cached
// or logged.
//
// Every call is made ON BEHALF OF THE SIGNED-IN USER (feature 024): the
// caller puts the user's platform token into the context with WithUserToken
// and the client forwards it as "authorization" metadata over the Freya
// SPIFFE mesh, so warden applies its own per-secret authorization and audit.
// Without a user token no call is made.
package warden

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	wardenv1 "github.com/go-tangra/go-tangra-warden/sdk/v4/api/proto/warden/v1"
)

// SecretMeta is metadata about a secret. It NEVER carries secret material and
// is safe to return to the browser and to log.
type SecretMeta struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Username   string `json:"username,omitempty"`
	FolderPath string `json:"folder_path,omitempty"`
	HostURL    string `json:"host_url,omitempty"`
}

// Credentials are a secret's login material. They are used immediately and
// must never be stored or logged; every textual form is redacted.
type Credentials struct {
	Username string
	Password string
	HostURL  string
}

const redacted = "[REDACTED]"

// String implements fmt.Stringer (redacted).
func (Credentials) String() string { return redacted }

// GoString implements fmt.GoStringer (redacted).
func (Credentials) GoString() string { return redacted }

// Format redacts every fmt verb (%v, %+v, %#v, %s, %q, …).
func (Credentials) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// LogValue implements slog.LogValuer (redacted).
func (Credentials) LogValue() slog.Value { return slog.StringValue(redacted) }

// Client reads secrets for the user carried in the context.
type Client interface {
	// Meta returns the secret's metadata (warden Secrets/Get).
	Meta(ctx context.Context, ref string) (SecretMeta, error)
	// Credentials returns username, password and host URL (Secrets/Get +
	// Secrets/GetPassword). The result is used immediately and never stored.
	Credentials(ctx context.Context, ref string) (Credentials, error)
}

// Errors. None of them carries warden's message or any secret value.
var (
	ErrEmptyRef    = errors.New("warden: empty secret reference")
	ErrNotFound    = errors.New("warden: secret not found")
	ErrForbidden   = errors.New("warden: access to the secret refused")
	ErrUnavailable = errors.New("warden: unavailable")
	ErrNoUserToken = fmt.Errorf("%w: no user token to act on behalf of", ErrForbidden)
	// ErrPolicyDenied is the Freya mesh refusing this module (a missing
	// warden policy rule): it reads as unavailable, never as "no access".
	ErrPolicyDenied = fmt.Errorf("%w: mesh policy refused the ipam service", ErrUnavailable)
)

// ---- user token

type tokenKey struct{}

// WithUserToken returns ctx carrying the signed-in user's platform token.
func WithUserToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, strings.TrimSpace(token))
}

// UserToken returns the user token carried by ctx ("" when none).
func UserToken(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey{}).(string)
	return t
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidRef reports whether ref has the shape of a warden secret id (UUID).
func ValidRef(ref string) bool { return uuidRe.MatchString(ref) }

// precheck refuses a call that must not reach warden.
func precheck(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", ErrEmptyRef
	}
	if !ValidRef(ref) {
		return "", ErrNotFound
	}
	tok := UserToken(ctx)
	if tok == "" {
		return "", ErrNoUserToken
	}
	return tok, nil
}

// ---- real gRPC-backed client

// DefaultTimeout bounds one warden call.
const DefaultTimeout = 5 * time.Second

type grpcClient struct {
	sc      wardenv1.SecretsClient
	timeout time.Duration
}

// New builds a Client over a Freya mesh connection to the warden module.
func New(cc grpc.ClientConnInterface) Client {
	return &grpcClient{sc: wardenv1.NewSecretsClient(cc), timeout: DefaultTimeout}
}

func (c *grpcClient) outgoing(ctx context.Context, tok string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok), cancel
}

func (c *grpcClient) get(ctx context.Context, ref, tok string) (SecretMeta, error) {
	ctx, cancel := c.outgoing(ctx, tok)
	defer cancel()
	res, err := c.sc.Get(ctx, &wardenv1.GetRequest{Id: ref})
	if err != nil {
		return SecretMeta{}, mapErr(err)
	}
	s := res.GetSecret()
	return SecretMeta{ID: s.GetId(), Name: s.GetName(), Username: s.GetUsername(), FolderPath: s.GetFolderPath(), HostURL: s.GetHostUrl()}, nil
}

// Meta implements Client.
func (c *grpcClient) Meta(ctx context.Context, ref string) (SecretMeta, error) {
	tok, err := precheck(ctx, ref)
	if err != nil {
		return SecretMeta{}, err
	}
	return c.get(ctx, ref, tok)
}

// Credentials implements Client.
func (c *grpcClient) Credentials(ctx context.Context, ref string) (Credentials, error) {
	tok, err := precheck(ctx, ref)
	if err != nil {
		return Credentials{}, err
	}
	m, err := c.get(ctx, ref, tok)
	if err != nil {
		return Credentials{}, err
	}
	pctx, cancel := c.outgoing(ctx, tok)
	defer cancel()
	pw, err := c.sc.GetPassword(pctx, &wardenv1.GetPasswordRequest{Id: ref})
	if err != nil {
		return Credentials{}, mapErr(err)
	}
	return Credentials{Username: m.Username, Password: pw.GetPassword(), HostURL: m.HostURL}, nil
}

// wardenForbidden is the message warden's Secrets service uses for its own
// per-secret refusal; the Freya policy middleware refuses with another one.
const wardenForbidden = "forbidden"

// mapErr keeps only the status code of a warden failure.
func mapErr(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w (%s)", ErrUnavailable, codes.Unknown)
	}
	switch st.Code() {
	case codes.NotFound, codes.InvalidArgument:
		return ErrNotFound
	case codes.Unauthenticated:
		return ErrForbidden
	case codes.PermissionDenied:
		if st.Message() == wardenForbidden {
			return ErrForbidden
		}
		return ErrPolicyDenied
	}
	return fmt.Errorf("%w (%s)", ErrUnavailable, st.Code())
}

// ---- unavailable client

// Unavailable is the client wired when the warden connection cannot be
// created: every call fails closed with ErrUnavailable.
type Unavailable struct{}

// Meta implements Client.
func (Unavailable) Meta(context.Context, string) (SecretMeta, error) { return SecretMeta{}, ErrUnavailable }

// Credentials implements Client.
func (Unavailable) Credentials(context.Context, string) (Credentials, error) {
	return Credentials{}, ErrUnavailable
}

// ---- in-memory fake for tests

// Operations recorded by Fake.
const (
	OpMeta        = "meta"
	OpCredentials = "credentials"
)

// Call is one recorded Fake call.
type Call struct {
	Op, Ref, Token string
}

type fakeSecret struct {
	meta     SecretMeta
	password string
}

// Fake is an in-memory Client for tests with warden's semantics: a user
// token is required, access can be denied per token and secret, and the
// service can be switched unavailable. Values are test fixtures only.
type Fake struct {
	mu          sync.Mutex
	secrets     map[string]fakeSecret
	deny        map[string]map[string]bool
	unavailable bool
	calls       []Call
}

// NewFake builds an empty Fake.
func NewFake() *Fake {
	return &Fake{secrets: map[string]fakeSecret{}, deny: map[string]map[string]bool{}}
}

// Put stores a secret under ref (meta.ID is set to ref).
func (f *Fake) Put(ref string, meta SecretMeta, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	meta.ID = ref
	f.secrets[strings.ToLower(ref)] = fakeSecret{meta: meta, password: password}
}

// SetPassword rotates a stored secret's password (no-op when unknown).
func (f *Fake) SetPassword(ref, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.secrets[strings.ToLower(ref)]; ok {
		s.password = password
		f.secrets[strings.ToLower(ref)] = s
	}
}

// Remove deletes a secret.
func (f *Fake) Remove(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.secrets, strings.ToLower(ref))
}

// Deny refuses ref to the user holding token.
func (f *Fake) Deny(token, ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deny[token] == nil {
		f.deny[token] = map[string]bool{}
	}
	f.deny[token][strings.ToLower(ref)] = true
}

// SetUnavailable switches every call to ErrUnavailable.
func (f *Fake) SetUnavailable(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = v
}

// Calls returns the recorded calls (only those that passed the local checks).
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CredentialCalls counts the recorded Credentials calls.
func (f *Fake) CredentialCalls() int {
	n := 0
	for _, c := range f.Calls() {
		if c.Op == OpCredentials {
			n++
		}
	}
	return n
}

// ResetCalls clears the call log.
func (f *Fake) ResetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *Fake) lookup(ctx context.Context, op, ref string) (fakeSecret, error) {
	tok, err := precheck(ctx, ref)
	if err != nil {
		return fakeSecret{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Op: op, Ref: ref, Token: tok})
	if f.unavailable {
		return fakeSecret{}, ErrUnavailable
	}
	s, ok := f.secrets[strings.ToLower(ref)]
	if !ok {
		return fakeSecret{}, ErrNotFound
	}
	if f.deny[tok][strings.ToLower(ref)] {
		return fakeSecret{}, ErrForbidden
	}
	return s, nil
}

// Meta implements Client.
func (f *Fake) Meta(ctx context.Context, ref string) (SecretMeta, error) {
	s, err := f.lookup(ctx, OpMeta, ref)
	return s.meta, err
}

// Credentials implements Client.
func (f *Fake) Credentials(ctx context.Context, ref string) (Credentials, error) {
	s, err := f.lookup(ctx, OpCredentials, ref)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Username: s.meta.Username, Password: s.password, HostURL: s.meta.HostURL}, nil
}

// interface conformance checks.
var (
	_ Client = (*grpcClient)(nil)
	_ Client = (*Fake)(nil)
	_ Client = Unavailable{}
)
