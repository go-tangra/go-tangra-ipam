package app

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"

	"github.com/go-tangra/go-tangra-ipam/v4/pkg/ipammanifest"
)

// Registration cadence: retry until auth accepts the registration, then
// re-register periodically (new tenants, auth restarts).
const (
	registerRetry = 5 * time.Second
	registerEvery = 5 * time.Minute
)

// RegisterPermissions registers the module's permissions, module roles and
// built-in role grants with the auth service (idempotent).
func (a *App) RegisterPermissions(ctx context.Context) error {
	conn, err := a.Freya.Client(ctx, "auth")
	if err != nil {
		return err
	}
	return registerPermissions(ctx, conn, a.Log)
}

func registerPermissions(ctx context.Context, cc grpc.ClientConnInterface, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := ipammanifest.Registration().Register(ctx, cc, log)
	return err
}

func (a *App) seedLoop(ctx context.Context) {
	registrationLoop(ctx, a.Log, a.RegisterPermissions, registerRetry, registerEvery)
}

// registrationLoop calls register until it succeeds (every retry), then every
// period until ctx ends.
func registrationLoop(ctx context.Context, log *slog.Logger, register func(context.Context) error, retry, period time.Duration) {
	for ctx.Err() == nil {
		err := register(ctx)
		if err == nil {
			break
		}
		log.Warn("auth registration failed; retrying", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := register(ctx); err != nil {
				log.Warn("auth registration", "err", err)
			}
		}
	}
}
