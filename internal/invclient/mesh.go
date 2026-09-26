package invclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/go-tangra/go-tangra-inventory/sdk/v4/pkg/inventoryclient"
)

// MaxRecvBytes bounds one HostReportService response (inventory bounds a
// page to 3 MiB; the IPAM client accepts up to 8 MiB).
const MaxRecvBytes = 8 << 20

// Dialer returns the mesh connection to the inventory service (e.g. the
// Freya client pool); it is called lazily on first use.
type Dialer func(ctx context.Context) (grpc.ClientConnInterface, error)

// reportAPI is the subset of the inventory SDK the host sync uses.
type reportAPI interface {
	ListReportTenants(ctx context.Context, changedSince time.Time) ([]string, time.Time, error)
	ListHostReports(ctx context.Context, tenantID string, f inventoryclient.ReportFilter) ([]inventoryclient.HostReport, string, error)
	GetHostReport(ctx context.Context, tenantID, hostID string) (inventoryclient.HostReport, error)
}

// Mesh is the Client over inventory's HostReportService (SPIFFE mTLS through
// the Freya client pool). The connection is dialled once, on first use,
// under a mutex; every call has its own timeout.
type Mesh struct {
	dial     Dialer
	timeout  time.Duration
	pageSize int

	mu  sync.Mutex
	api reportAPI
	// newAPI builds the SDK client on a connection (tests replace it).
	newAPI func(grpc.ClientConnInterface) reportAPI
}

// NewMesh builds the mesh client. timeout bounds each call; pageSize is the
// ListHostReports page (1–200).
func NewMesh(dial Dialer, timeout time.Duration, pageSize int) *Mesh {
	return &Mesh{dial: dial, timeout: timeout, pageSize: pageSize,
		newAPI: func(c grpc.ClientConnInterface) reportAPI {
			return inventoryclient.New(optConn{ClientConnInterface: c, opts: CallOptions()})
		}}
}

// optConn adds the IPAM call options (receive bound) to every call made on a
// pooled connection the SDK uses.
type optConn struct {
	grpc.ClientConnInterface
	opts []grpc.CallOption
}

func (c optConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	return c.ClientConnInterface.Invoke(ctx, method, args, reply, append(append([]grpc.CallOption(nil), c.opts...), opts...)...)
}

// CallOptions are the gRPC options the IPAM dial must use for inventory.
func CallOptions() []grpc.CallOption { return []grpc.CallOption{grpc.MaxCallRecvMsgSize(MaxRecvBytes)} }

func (m *Mesh) client(ctx context.Context) (reportAPI, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.api != nil {
		return m.api, nil
	}
	conn, err := m.dial(ctx)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Err: err}
	}
	m.api = m.newAPI(conn)
	return m.api, nil
}

// wrap maps an inventory error to a coded ErrUnavailable. NotFound stays
// ErrNotFound (a host without report is not an outage).
func wrap(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return ErrNotFound
	case codes.PermissionDenied, codes.Unauthenticated:
		return &Error{Code: CodePermissionDenied, Err: err}
	case codes.Unimplemented:
		return &Error{Code: CodeOutdated, Err: err}
	}
	return &Error{Code: CodeUnavailable, Err: err}
}

// ListReportTenants implements Client.
func (m *Mesh) ListReportTenants(ctx context.Context, since time.Time) ([]string, time.Time, error) {
	api, err := m.client(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	ids, maxAt, err := api.ListReportTenants(cctx, since)
	if err != nil {
		return nil, time.Time{}, wrap(err)
	}
	return ids, maxAt, nil
}

// maxPages bounds one listing (defence against a cursor that never ends).
const maxPages = 10000

// ListHostReports implements Client: pages until the last page.
func (m *Mesh) ListHostReports(ctx context.Context, tenantID string, f Filter, fn func([]Report) error) error {
	api, err := m.client(ctx)
	if err != nil {
		return err
	}
	cursor := ""
	for page := 0; page < maxPages; page++ {
		cctx, cancel := context.WithTimeout(ctx, m.timeout)
		list, next, err := api.ListHostReports(cctx, tenantID, inventoryclient.ReportFilter{
			ChangedSince: f.ChangedSince, Digest: f.Digest, Limit: m.pageSize, Cursor: cursor})
		cancel()
		if err != nil {
			return wrap(err)
		}
		if len(list) > m.pageSize && m.pageSize > 0 {
			return &Error{Code: CodeUnavailable, Err: errors.New("page larger than requested")}
		}
		out := make([]Report, len(list))
		for i, r := range list {
			out[i] = fromSDK(r)
		}
		if err := fn(out); err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
	return &Error{Code: CodeUnavailable, Err: errors.New("too many pages")}
}

// GetHostReport implements Client.
func (m *Mesh) GetHostReport(ctx context.Context, tenantID, hostID string) (Report, error) {
	api, err := m.client(ctx)
	if err != nil {
		return Report{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	r, err := api.GetHostReport(cctx, tenantID, hostID)
	if err != nil {
		return Report{}, wrap(err)
	}
	return fromSDK(r), nil
}

func fromSDK(r inventoryclient.HostReport) Report {
	out := Report{
		TenantID: r.TenantID, SnapshotID: r.SnapshotID, CollectedAt: r.CollectedAt, ChangedAt: r.ChangedAt,
		Digest: r.Digest, AgentVersion: r.AgentVersion, OSFamily: r.OSFamily,
		PrimaryIPv4: r.PrimaryIPv4, PrimaryIPv6: r.PrimaryIPv6,
		Host: Host{ID: r.Host.ID, Hostname: r.Host.Hostname, SystemSerial: r.Host.SystemSerial, Manufacturer: r.Host.Manufacturer,
			Model: r.Host.Model, OSName: r.Host.OSName, OSVersion: r.Host.OSVersion, Status: r.Host.Status, LastSeen: r.Host.LastSeen},
		Virtualization: Virtualization{Role: r.Virtualization.Role, Kind: r.Virtualization.Kind, Source: r.Virtualization.Source},
		Updates: UpdateState{PackageManager: r.Updates.PackageManager, Status: r.Updates.Status, RebootRequired: r.Updates.RebootRequired,
			AutomaticUpdates: r.Updates.AutomaticUpdates, SecurityClassified: r.Updates.SecurityClassified, CheckedAt: r.Updates.CheckedAt,
			PendingCount: r.Updates.PendingCount, SecurityCount: r.Updates.SecurityCount},
		Truncated: Limits{Interfaces: r.Truncated.Interfaces, Addresses: r.Truncated.Addresses, Guests: r.Truncated.Guests,
			Packages: r.Truncated.Packages, BMCPorts: r.Truncated.BMCPorts},
	}
	for _, i := range r.Interfaces {
		ni := Interface{Name: i.Name, MAC: i.MAC, IPAddresses: i.IPAddresses, Gateway: i.Gateway, DHCP: i.DHCP, SpeedBps: i.SpeedBps,
			Type: i.Type, Up: i.Up, DefaultRoute: i.DefaultRoute, Master: i.Master, VLANID: i.VLANID}
		for _, a := range i.Addresses {
			ni.Addresses = append(ni.Addresses, Address{Address: a.Address, PrefixLength: a.PrefixLength, Family: a.Family,
				DHCP: a.DHCP, Temporary: a.Temporary, Deprecated: a.Deprecated, Scope: a.Scope})
		}
		out.Interfaces = append(out.Interfaces, ni)
	}
	if b := r.BMC; b != nil {
		nb := &BMC{Address: b.Address, PrefixLength: b.PrefixLength, Gateway: b.Gateway, IPSource: b.IPSource, VLANID: b.VLANID}
		for _, p := range b.Ports {
			nb.Ports = append(nb.Ports, BMCPort{Channel: p.Channel, MAC: p.MAC, Address: p.Address})
		}
		out.BMC = nb
	}
	for _, g := range r.Guests {
		out.Guests = append(out.Guests, Guest{ID: g.ID, Name: g.Name, Kind: g.Kind, Platform: g.Platform, MACs: g.MACs})
	}
	for _, p := range r.PendingUpdates {
		out.PendingUpdates = append(out.PendingUpdates, PendingUpdate{Name: p.Name, InstalledVersion: p.InstalledVersion,
			AvailableVersion: p.AvailableVersion, Security: p.Security})
	}
	return out
}

var _ Client = (*Mesh)(nil)
