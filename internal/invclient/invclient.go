package invclient

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-inventory/sdk/v4/pkg/inventoryclient"
)

// ErrUnavailable wraps every transport or inventory failure: the host sync
// reports "inventory unavailable" (or the sanitised code) and writes nothing.
var ErrUnavailable = errors.New("invclient: inventory unavailable")

// ErrNotFound is a host (or its latest snapshot) inventory does not have.
var ErrNotFound = errors.New("invclient: host report not found")

// Sanitised status codes stored as the tenant's last_error (never free text
// from inventory).
const (
	CodeUnavailable      = "inventory_unavailable"
	CodePermissionDenied = "permission_denied"
	CodeOutdated         = "inventory_outdated"
)

// Error is an inventory failure with its sanitised code. It matches
// ErrUnavailable with errors.Is.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "invclient: " + e.Code
	}
	return "invclient: " + e.Code + ": " + e.Err.Error()
}

// Is reports ErrUnavailable for every inventory failure.
func (e *Error) Is(target error) bool { return target == ErrUnavailable }

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

// Code returns the sanitised code of an inventory failure ("" for nil).
func Code(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeUnavailable
}

// Host is the inventory host identity carried by a report.
type Host struct {
	ID           string
	Hostname     string
	SystemSerial string
	Manufacturer string
	Model        string
	OSName       string
	OSVersion    string
	Status       string // active | stale | retired
	LastSeen     time.Time
}

// Address is one address of a reported interface.
type Address struct {
	Address      string
	PrefixLength uint32
	Family       string
	DHCP         bool
	Temporary    bool
	Deprecated   bool
	Scope        string
}

// Interface is one reported network interface.
type Interface struct {
	Name         string
	MAC          string
	IPAddresses  []string // CIDR strings (old agents)
	Gateway      string
	DHCP         bool
	SpeedBps     uint64
	Type         string // kind
	Up           bool
	Addresses    []Address
	DefaultRoute bool
	Master       string
	VLANID       uint32
}

// Virtualization is the reported virtualization role and kind.
type Virtualization struct {
	Role   string
	Kind   string
	Source string
}

// BMCPort is one BMC LAN channel.
type BMCPort struct {
	Channel uint32
	MAC     string
	Address string
}

// BMC is the out-of-band controller's LAN configuration (no credentials).
type BMC struct {
	Address      string
	PrefixLength uint32
	Gateway      string
	IPSource     string
	VLANID       uint32
	Ports        []BMCPort
}

// Guest is a guest a hypervisor host runs.
type Guest struct {
	ID       string
	Name     string
	Kind     string
	Platform string
	MACs     []string
}

// UpdateState is the host's package update state.
type UpdateState struct {
	PackageManager     string
	Status             string
	RebootRequired     string // unknown | true | false
	AutomaticUpdates   string // unknown | true | false
	SecurityClassified bool
	CheckedAt          time.Time
	PendingCount       uint32
	SecurityCount      uint32
}

// PendingUpdate is an installed package with a newer version available.
type PendingUpdate struct {
	Name             string
	InstalledVersion string
	AvailableVersion string
	Security         bool
}

// Limits counts entries the agent or inventory dropped at a bound.
type Limits struct {
	Interfaces, Addresses, Guests, Packages, BMCPorts uint32
	// Feature 023 hardware bounds.
	Disks, MemorySlots, MemoryArrays, Processors, Filesystems uint32
}

// Hardware and its parts are the inventory SDK's plain-Go hardware profile
// (feature 023; inventory SDK >= v4.2.0). IPAM re-validates it in
// internal/hostreport before anything is stored.
type (
	Hardware             = inventoryclient.Hardware
	BIOSInfo             = inventoryclient.BIOSInfo
	SystemInfo           = inventoryclient.SystemInfo
	BaseboardInfo        = inventoryclient.BaseboardInfo
	ChassisInfo          = inventoryclient.ChassisInfo
	Processor            = inventoryclient.Processor
	MemoryInfo           = inventoryclient.MemoryInfo
	MemoryArray          = inventoryclient.MemoryArray
	MemoryModule         = inventoryclient.MemoryModule
	Disk                 = inventoryclient.Disk
	Filesystem           = inventoryclient.Filesystem
	HardwareAvailability = inventoryclient.HardwareAvailability
)

// Report is inventory's projection of a host's latest snapshot for IPAM.
type Report struct {
	TenantID       string
	Host           Host
	SnapshotID     string
	CollectedAt    time.Time
	ChangedAt      time.Time
	Digest         string
	AgentVersion   string
	OSFamily       string
	Interfaces     []Interface
	PrimaryIPv4    string
	PrimaryIPv6    string
	Virtualization Virtualization
	BMC            *BMC
	Guests         []Guest
	Updates        UpdateState
	PendingUpdates []PendingUpdate
	Truncated      Limits
	// Hardware is nil for hosts whose agent predates the corrected hardware
	// collection and for inventory versions before 4.4.0 (feature 023).
	Hardware *Hardware
}

// Filter selects host reports of one tenant.
type Filter struct {
	ChangedSince time.Time // zero = every host (incl. retired)
	Digest       bool      // DIGEST view: host, status, digest and changed-at only
}

// Client reads host reports from inventory.
type Client interface {
	// ListReportTenants returns tenants with a report change after since
	// (zero = every tenant with hosts) and the watermark to use next.
	ListReportTenants(ctx context.Context, since time.Time) ([]string, time.Time, error)
	// ListHostReports pages every matching report of the tenant, ordered by
	// (changed-at, host id), and calls fn once per page.
	ListHostReports(ctx context.Context, tenantID string, f Filter, fn func([]Report) error) error
	// GetHostReport returns one host's latest report.
	GetHostReport(ctx context.Context, tenantID, hostID string) (Report, error)
}

// Fake is an in-memory Client for tests: reports per tenant, a Down switch,
// an injectable error and the calls it served.
type Fake struct {
	mu       sync.Mutex
	reports  map[string]map[string]Report // tenant -> host id -> report
	Down     bool
	Err      error // returned by every call when set (e.g. a coded *Error)
	PageSize int
	Calls    []string
}

// NewFake builds an empty fake.
func NewFake() *Fake { return &Fake{reports: map[string]map[string]Report{}, PageSize: 100} }

// Put stores (or replaces) a report under its tenant and host id.
func (f *Fake) Put(r Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reports[r.TenantID] == nil {
		f.reports[r.TenantID] = map[string]Report{}
	}
	f.reports[r.TenantID][r.Host.ID] = r
}

// Delete removes a host's report (host deleted in inventory).
func (f *Fake) Delete(tenantID, hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.reports[tenantID], hostID)
}

func (f *Fake) failure(call string) error {
	f.Calls = append(f.Calls, call)
	if f.Err != nil {
		return f.Err
	}
	if f.Down {
		return &Error{Code: CodeUnavailable}
	}
	return nil
}

// ListReportTenants implements Client.
func (f *Fake) ListReportTenants(_ context.Context, since time.Time) ([]string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failure("ListReportTenants"); err != nil {
		return nil, time.Time{}, err
	}
	var ids []string
	maxAt := since
	for tid, hosts := range f.reports {
		changed := false
		for _, r := range hosts {
			if since.IsZero() || r.ChangedAt.After(since) {
				changed = true
			}
			if r.ChangedAt.After(maxAt) {
				maxAt = r.ChangedAt
			}
		}
		if changed && len(hosts) > 0 {
			ids = append(ids, tid)
		}
	}
	sort.Strings(ids)
	return ids, maxAt, nil
}

// ListHostReports implements Client.
func (f *Fake) ListHostReports(_ context.Context, tenantID string, flt Filter, fn func([]Report) error) error {
	f.mu.Lock()
	if err := f.failure("ListHostReports"); err != nil {
		f.mu.Unlock()
		return err
	}
	var list []Report
	for _, r := range f.reports[tenantID] {
		if flt.ChangedSince.IsZero() || r.ChangedAt.After(flt.ChangedSince) {
			if flt.Digest {
				r = Report{TenantID: r.TenantID, Host: r.Host, Digest: r.Digest, ChangedAt: r.ChangedAt}
			}
			list = append(list, r)
		}
	}
	size := f.PageSize
	f.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if !list[i].ChangedAt.Equal(list[j].ChangedAt) {
			return list[i].ChangedAt.Before(list[j].ChangedAt)
		}
		return list[i].Host.ID < list[j].Host.ID
	})
	if size < 1 {
		size = 1
	}
	for len(list) > 0 {
		n := min(size, len(list))
		if err := fn(list[:n]); err != nil {
			return err
		}
		list = list[n:]
	}
	return nil
}

// GetHostReport implements Client.
func (f *Fake) GetHostReport(_ context.Context, tenantID, hostID string) (Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failure("GetHostReport"); err != nil {
		return Report{}, err
	}
	r, ok := f.reports[tenantID][hostID]
	if !ok {
		return Report{}, ErrNotFound
	}
	return r, nil
}

var _ Client = (*Fake)(nil)
