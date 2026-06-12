package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/grpc"
	grpcMD "google.golang.org/grpc/metadata"

	"github.com/go-tangra/go-tangra-common/grpcx"
	wardenV1 "github.com/go-tangra/go-tangra-warden/gen/go/warden/service/v1"
)

// SecretRef is the minimal Warden secret metadata used to pick and display a
// reference. It deliberately never carries the secret value.
type SecretRef struct {
	ID         string
	Name       string
	FolderPath string
	Username   string
}

// WardenClient calls the Warden module's secret service over the mTLS mesh,
// resolving the endpoint lazily via ModuleDialer on first use. It exposes only
// metadata reads (search/get) so IPAM can reference a secret without ever
// touching its value.
type WardenClient struct {
	dialer *grpcx.ModuleDialer
	log    *log.Helper

	once    sync.Once
	conn    *grpc.ClientConn
	initErr error
	secrets wardenV1.WardenSecretServiceClient
}

// NewWardenClient creates a lazily-resolving Warden client.
func NewWardenClient(ctx *bootstrap.Context, dialer *grpcx.ModuleDialer) (*WardenClient, func(), error) {
	c := &WardenClient{
		dialer: dialer,
		log:    ctx.NewLoggerHelper("warden/client/ipam-service"),
	}
	cleanup := func() {
		if c.conn != nil {
			if err := c.conn.Close(); err != nil {
				c.log.Errorf("Failed to close Warden connection: %v", err)
			}
		}
	}
	return c, cleanup, nil
}

func (c *WardenClient) resolve() error {
	if c.dialer == nil {
		return fmt.Errorf("warden client unavailable (no admin endpoint configured)")
	}
	c.once.Do(func() {
		conn, err := c.dialer.DialModule(context.Background(), "warden", 30, 5*time.Second)
		if err != nil {
			c.initErr = fmt.Errorf("resolve warden: %w", err)
			c.log.Errorf("Failed to resolve warden: %v", err)
			return
		}
		c.conn = conn
		c.secrets = wardenV1.NewWardenSecretServiceClient(conn)
		c.log.Info("Warden client connected via ModuleDialer")
	})
	return c.initErr
}

// propagate forwards the gateway-injected user context (tenant, user, roles)
// onto the outgoing call so Warden enforces the calling user's permissions.
func propagate(ctx context.Context) context.Context {
	var pairs []string
	for _, k := range []string{grpcx.MDTenantID, grpcx.MDUserID, grpcx.MDUsername, grpcx.MDRoles} {
		if v := grpcx.GetMetadataValue(ctx, k); v != "" {
			pairs = append(pairs, k, v)
		}
	}
	if len(pairs) == 0 {
		return ctx
	}
	return grpcMD.AppendToOutgoingContext(ctx, pairs...)
}

// SearchSecrets returns up to limit secrets whose name matches query (metadata
// only). The calling user's permissions are enforced by Warden.
func (c *WardenClient) SearchSecrets(ctx context.Context, query string, limit uint32) ([]SecretRef, error) {
	if err := c.resolve(); err != nil {
		return nil, err
	}
	req := &wardenV1.ListSecretsRequest{}
	if query != "" {
		req.NameFilter = &query
	}
	if limit > 0 {
		req.PageSize = &limit
	}
	resp, err := c.secrets.ListSecrets(propagate(ctx), req)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	out := make([]SecretRef, 0, len(resp.GetSecrets()))
	for _, s := range resp.GetSecrets() {
		out = append(out, toRef(s))
	}
	return out, nil
}

// GetSecret resolves a single secret's metadata by id (for display). Returns
// (nil, nil) when not found.
func (c *WardenClient) GetSecret(ctx context.Context, id string) (*SecretRef, error) {
	if err := c.resolve(); err != nil {
		return nil, err
	}
	resp, err := c.secrets.GetSecret(propagate(ctx), &wardenV1.GetSecretRequest{Id: id})
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}
	if resp.GetSecret() == nil {
		return nil, nil
	}
	ref := toRef(resp.GetSecret())
	return &ref, nil
}

// GetCredentials returns the username + password of a secret, for server-side
// use only (e.g. logging into a BMC). NEVER return the password to a browser.
// The username comes from the secret metadata; the password from the dedicated
// password RPC. The caller's permissions are enforced by Warden.
func (c *WardenClient) GetCredentials(ctx context.Context, secretID string) (username, password string, err error) {
	if err := c.resolve(); err != nil {
		return "", "", err
	}
	metaResp, err := c.secrets.GetSecret(propagate(ctx), &wardenV1.GetSecretRequest{Id: secretID})
	if err != nil {
		return "", "", fmt.Errorf("get secret: %w", err)
	}
	if metaResp.GetSecret() != nil {
		username = metaResp.GetSecret().GetUsername()
	}
	pwResp, err := c.secrets.GetSecretPassword(propagate(ctx), &wardenV1.GetSecretPasswordRequest{Id: secretID})
	if err != nil {
		return "", "", fmt.Errorf("get secret password: %w", err)
	}
	return username, pwResp.GetPassword(), nil
}

func toRef(s *wardenV1.Secret) SecretRef {
	return SecretRef{
		ID:         s.GetId(),
		Name:       s.GetName(),
		FolderPath: s.GetFolderPath(),
		Username:   s.GetUsername(),
	}
}
