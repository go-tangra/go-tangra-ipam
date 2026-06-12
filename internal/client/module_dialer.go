// Package client holds gRPC clients IPAM uses to call other modules over the
// mTLS service mesh (resolved via admin-service).
package client

import (
	"os"

	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-common/grpcx"
	"github.com/go-tangra/go-tangra-common/registration"
)

// NewRegistrationClient creates a registration client connected to admin-service.
// Its admin connection is shared with ModuleDialer for module-to-module
// resolution. Returns nil when ADMIN_GRPC_ENDPOINT is unset (e.g. local/dev).
func NewRegistrationClient(ctx *bootstrap.Context) (*registration.Client, error) {
	adminEndpoint := os.Getenv("ADMIN_GRPC_ENDPOINT")
	if adminEndpoint == "" {
		return nil, nil
	}
	return registration.NewClient(ctx.GetLogger(), &registration.Config{
		AdminEndpoint: adminEndpoint,
		MaxRetries:    60,
	})
}

// NewModuleDialer creates a ModuleDialer from the registration client's admin
// connection. Returns nil when there is no registration client.
func NewModuleDialer(ctx *bootstrap.Context, regClient *registration.Client) *grpcx.ModuleDialer {
	if regClient == nil {
		return nil
	}
	return grpcx.NewModuleDialer(ctx.GetLogger(), "ipam", regClient.AdminConn(), os.Getenv("CERTS_DIR"))
}

// RegistrationClientCleanup returns a cleanup function for the registration client.
func RegistrationClientCleanup(client *registration.Client) func() {
	return func() {
		if client != nil {
			_ = client.Close()
		}
	}
}
