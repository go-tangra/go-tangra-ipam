// Package app wires the ipam service: configuration -> Freya runtime ->
// store/sealing/warden -> the mesh HTTP+gRPC surfaces (via the gateway), the
// active-network-operations subsystem (scan executor, ICMP/SNMP/TCP, IPMI, KVM
// proxy) and gateway registration.
package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-lcm/sdk/v4/pkg/lcmidentity"
	"github.com/go-tangra/go-tangra/v4"

	authv1 "github.com/go-tangra/go-tangra-auth/sdk/v4/api/proto/auth/v1"
	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra-portal/sdk/v4/pkg/gatewayclient"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/addresses"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/backup"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/config"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/devices"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/dnscfg"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/groups"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/grpcapi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/httpapi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/locations"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/icmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/tcp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stats"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stream"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/stream/valkeykv"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/subnets"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/vlans"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
	"github.com/go-tangra/go-tangra-ipam/v4/pkg/ipammanifest"
)

// Options override infrastructure (tests) and attach optional parts.
type Options struct {
	Logger   slog.Handler
	KEK      []byte
	Verifier httpapi.Verifier
	Freya    []freya.Option
	Migrate  bool
	Remote   fs.FS
}

// App is the wired service.
type App struct {
	Cfg      config.Config
	Log      *slog.Logger
	Freya    *freya.App
	Store    *store.Store
	Repo     repo.Store
	Env      *sealed.Envelope
	Verifier httpapi.Verifier
	HTTP     *httpapi.Server
	Hub      *stream.Hub

	closers []func()
	workers []func(context.Context)
}

// Build wires the service.
func Build(ctx context.Context, cfg config.Config, o Options) (a *App, err error) {
	a = &App{Cfg: cfg}
	handler := o.Logger
	if handler == nil {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	}
	a.Log = slog.New(handler)

	fopts := append([]freya.Option{freya.WithLogger(handler)}, o.Freya...)
	if cfg.MeshEnroll.Enabled {
		raw, rerr := os.ReadFile(cfg.MeshEnroll.TokenFile)
		if rerr != nil {
			return nil, fmt.Errorf("ipam: mesh enroll token: %w", rerr)
		}
		prov, perr := lcmidentity.NewNet(ctx, lcmidentity.NetConfig{
			EnrollURL: cfg.MeshEnroll.EnrollURL, LCMGRPCTarget: cfg.MeshEnroll.LCMGRPCTarget,
			TenantID: cfg.MeshEnroll.TenantID, TrustDomain: cfg.Config.TrustDomain, ServiceName: cfg.Config.ServiceName,
			EnrollmentToken: strings.TrimSpace(string(raw)), Insecure: cfg.MeshEnroll.Insecure, StateFile: cfg.MeshEnroll.StateFile,
		})
		if perr != nil {
			return nil, fmt.Errorf("ipam: mesh enroll: %w", perr)
		}
		a.closers = append(a.closers, func() { _ = prov.Close() })
		fopts = append(fopts, freya.WithIdentityProvider(prov))
	}
	if a.Freya, err = freya.New(cfg.Config, fopts...); err != nil {
		return nil, err
	}
	a.closers = append(a.closers, a.Freya.Close)

	// KEK + envelope.
	kek := o.KEK
	if len(kek) == 0 {
		if kek, err = sealed.LoadKEK(cfg.KEK.Source, cfg.KEK.Path, cfg.KEK.Env); err != nil {
			return nil, fmt.Errorf("kek: %w", err)
		}
	}
	if a.Env, err = sealed.NewEnvelope(kek); err != nil {
		return nil, err
	}

	// Store.
	if o.Migrate {
		mdsn := cfg.DB.MigrateDSN
		if mdsn == "" {
			mdsn = cfg.DB.DSN
		}
		if err = store.Migrate(ctx, mdsn); err != nil {
			return nil, err
		}
	}
	if a.Store, err = store.Open(ctx, cfg.DB.DSN, cfg.DB.MaxConns); err != nil {
		return nil, err
	}
	a.closers = append(a.closers, a.Store.Close)
	a.Repo = repodb.New(a.Store)

	// Verifier (platform token) from auth.
	a.Verifier = o.Verifier
	if a.Verifier == nil {
		conn, cerr := a.Freya.Client(ctx, "auth")
		if cerr != nil {
			return nil, fmt.Errorf("auth client: %w", cerr)
		}
		a.Verifier = authclient.New(authclient.Config{Issuer: cfg.Gateway.Issuer},
			authclient.GRPCKeys{Client: authv1.NewKeysClient(conn)},
			authclient.GRPCRevocations{Client: authv1.NewSessionsClient(conn)})
	}

	// Warden secret-reference client (BMC/SNMP creds fetched at use time).
	var wclient warden.Client = warden.NewFake()
	if wconn, werr := a.Freya.Client(ctx, cfg.Warden.Service); werr == nil {
		wclient = warden.New(wconn)
	} else {
		a.Log.Warn("warden client unavailable; power/KVM/SNMP credential fetch will fail", "err", werr)
	}

	// Event bus.
	sc := valkeykv.Config{Addresses: cfg.Valkey.Addresses, Username: cfg.Valkey.Username, Password: cfg.Valkey.Password, AllowPlaintext: cfg.Valkey.AllowPlaintext}
	if cfg.Valkey.CAFile != "" {
		if sc.CAPEM, err = os.ReadFile(cfg.Valkey.CAFile); err != nil {
			return nil, fmt.Errorf("valkey ca: %w", err)
		}
	}
	streamClient, serr := valkeykv.New(sc)
	if serr != nil {
		return nil, fmt.Errorf("event bus: %w", serr)
	}
	a.Hub = stream.NewHub(streamClient, stream.Config{}, a.Log)
	a.closers = append(a.closers, a.Hub.Close)
	pub := events.HubPublisher{Hub: a.Hub}

	// Active-operations clients (network-touching; behind interfaces).
	pinger := icmp.NewRaw()
	a.closers = append(a.closers, func() { _ = pinger.Close() })
	portScanner := tcp.NewDialer()
	snmpDisc := snmp.NewClient()
	bmc := ipmi.NewClient(cfg.IPMITimeout())
	kvmMgr := kvm.NewManager(a.Log, cfg.KVMTokenTTL())

	// Domain services.
	subnetsSvc := subnets.New(a.Repo)
	addressesSvc := addresses.New(a.Repo, pub, cfg.Allocation.SkipFirst, cfg.Allocation.SkipLast)
	addressesSvc.SetProbers(pinger, portScanner)
	devicesSvc := devices.New(a.Repo)
	vlansSvc := vlans.New(a.Repo)
	locationsSvc := locations.New(a.Repo)
	groupsSvc := groups.New(a.Repo)
	statsSvc := stats.New(a.Repo)
	backupSvc := backup.New(a.Repo)
	dnsSvc := dnscfg.New(a.Repo)
	scanSvc := scan.New(a.Repo, pinger, pinger, snmpDisc, wclient, pub, scan.Config{
		MaxHosts: cfg.Scan.MaxHosts, Concurrency: cfg.Scan.Concurrency, TimeoutMs: cfg.Scan.TimeoutMs,
		Workers: cfg.Scan.Workers, MaxRetries: cfg.Scan.MaxRetries,
	}, nil)

	// Mesh HTTP surface + the /bmc KVM console proxy on the outer mux.
	hopts := []httpapi.Option{httpapi.WithVerifier(a.Verifier)}
	if o.Remote != nil {
		hopts = append(hopts, httpapi.WithRemote(o.Remote))
	}
	if a.HTTP, err = httpapi.NewHandler(a.Freya, hopts...); err != nil {
		return nil, err
	}
	deps := httpapi.Deps{
		Subnets: subnetsSvc, Addresses: addressesSvc, Devices: devicesSvc, Vlans: vlansSvc,
		Locations: locationsSvc, Groups: groupsSvc, Stats: statsSvc, Backup: backupSvc,
		DNS: dnsSvc, Scan: scanSvc, BMC: bmc, KVM: kvmMgr, Warden: wclient, Hub: a.Hub,
	}
	a.HTTP.Register(deps)
	mux := http.NewServeMux()
	mux.Handle("/", a.HTTP.Handler())
	a.HTTP.RegisterKVM(mux, deps) // mounts /bmc/ (token-gated KVM proxy)
	a.Freya.HTTP().HandlePrefix("/", mux)

	// Service-to-service gRPC surface (ipam.v1).
	grpcapi.Register(a.Freya.GRPC(), grpcapi.Deps{
		Subnets: subnetsSvc, Addresses: addressesSvc, Devices: devicesSvc, Vlans: vlansSvc,
		Locations: locationsSvc, Groups: groupsSvc, Stats: statsSvc, Backup: backupSvc,
		DNS: dnsSvc, Scan: scanSvc, BMC: bmc, KVM: kvmMgr, Warden: wclient,
	})

	// Scan executor worker pool.
	a.workers = append(a.workers, func(c context.Context) { _ = scanSvc.Run(c, a.Log) })
	return a, nil
}

// Run starts the verifier, gateway registration, workers, and the Freya runtime.
func (a *App) Run(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if v, ok := a.Verifier.(*authclient.Verifier); ok {
		go func() {
			for wctx.Err() == nil {
				if err := v.Start(wctx, func(err error) { a.Log.Warn("verifier", "err", err) }); err == nil {
					return
				}
				select {
				case <-wctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}()
	}
	go a.register(wctx)
	for _, w := range a.workers {
		go w(wctx)
	}
	go func() {
		for wctx.Err() == nil && !a.Freya.Ready() {
			time.Sleep(100 * time.Millisecond)
		}
		a.seedLoop(wctx)
	}()
	return a.Freya.Run(ctx)
}

// Close releases resources.
func (a *App) Close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}

// register keeps the gateway lease for the manifest.
func (a *App) register(ctx context.Context) {
	for ctx.Err() == nil && !a.Freya.Ready() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	man, err := ipammanifest.Manifest()
	if err != nil {
		a.Log.Error("gateway manifest", "err", err)
		return
	}
	httpEP, err := a.Freya.HTTP().Endpoint()
	if err != nil {
		a.Log.Error("gateway registration: http endpoint", "err", err)
		return
	}
	grpcEP, err := a.Freya.GRPC().Endpoint()
	if err != nil {
		a.Log.Error("gateway registration: grpc endpoint", "err", err)
		return
	}
	var client *gatewayclient.Client
	for ctx.Err() == nil && client == nil {
		conn, cerr := a.Freya.Client(ctx, a.Cfg.Gateway.Service)
		if cerr != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		client, err = gatewayclient.New(conn, gatewayclient.Options{Manifest: man, HTTPURL: "https://" + httpEP.Host, GRPCTarget: grpcEP.Host, Logger: a.Log,
			OnState: func(s gatewayclient.State) {
				a.Log.Info("gateway lease", "registered", s.Registered, "lease", s.LeaseID, "err", s.Err)
			}})
		if err != nil {
			a.Log.Error("gateway client", "err", err)
			return
		}
	}
	if client != nil {
		if err := client.Run(ctx); err != nil {
			a.Log.Error("gateway registration", "err", err)
		}
	}
}
