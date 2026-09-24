// Package addresses is the IPAM address service: CRUD over the IP addresses of a
// subnet plus the allocation paths that hand out the next free address(es). It
// publishes a content-safe realtime event on every create/update/delete, and it
// redacts the sealed owner field out of every projection it returns.
//
// Allocation is the heart of the package. AllocateNext walks the subnet's free
// space through the pure ipnet library, excluding the already-allocated rows and
// any caller-supplied skips, and inserts the winning address under the store's
// unique-address guard; a lost race (repo.ErrConflict) is retried against a
// widened exclude set, and an exhausted range surfaces as ErrNoAvailable. The
// live-probe helpers (PingAddress, SuggestAvailableAddresses) call out to an
// injected Pinger/PortScanner when one is set and otherwise fall back to the
// database's own free/allocated knowledge.
package addresses

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/events"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipnet"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Sentinel errors. ErrNotFound and ErrConflict mask the store's equivalents;
// ErrNoAvailable is returned when a subnet has no free address left to allocate.
var (
	ErrNotFound    = errors.New("addresses: not found")
	ErrNoAvailable = errors.New("addresses: no available address")
	ErrConflict    = errors.New("addresses: conflict")
)

// maxAllocTries bounds how many times a single allocation retries after losing
// the unique-address race before giving up with ErrConflict.
const maxAllocTries = 20

// probeTimeout is the per-address dwell used by SuggestAvailableAddresses when a
// PortScanner is set.
const probeTimeout = time.Second

// defaultProbePorts is the small port set SuggestAvailableAddresses scans to tell
// an in-use address apart from a truly free one.
var defaultProbePorts = []int{22, 80, 443, 3389}

// Pinger reports whether an address answers ICMP (or an equivalent liveness
// probe). It is implemented by the active-ops module, not this package.
type Pinger interface {
	Ping(ctx context.Context, ip string) (alive bool, rttMs int, err error)
}

// PortScanner reports which of ports are open on ip within timeout. It is
// implemented by the active-ops module, not this package.
type PortScanner interface {
	Scan(ctx context.Context, ip string, ports []int, timeout time.Duration) (open []int, err error)
}

// PingResult is the content-safe result of PingAddress. Available reports whether
// a prober was configured; when false, Alive/RTTMs carry no meaning.
type PingResult struct {
	Address   string `json:"address"`
	Alive     bool   `json:"alive"`
	RTTMs     int    `json:"rtt_ms"`
	Available bool   `json:"available"`
}

// Service manages addresses and allocation.
type Service struct {
	st                  repo.Store
	pub                 events.Publisher
	now                 func() time.Time
	skipFirst, skipLast int
	ping                Pinger
	scan                PortScanner
}

// New builds the service. skipFirst/skipLast carve reserved addresses off the low
// and high ends of every subnet's usable range during allocation.
func New(st repo.Store, pub events.Publisher, skipFirst, skipLast int) *Service {
	return &Service{st: st, pub: pub, now: time.Now, skipFirst: skipFirst, skipLast: skipLast}
}

// SetClock injects the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetProbers injects the live-probe backends. Either may be nil; the probe paths
// fall back to the database when they are.
func (s *Service) SetProbers(p Pinger, sc PortScanner) { s.ping = p; s.scan = sc }

// Create inserts an address in the caller's tenant and publishes ip_address.created.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, in store.IPAddress) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	in.TenantID = subj.TenantID
	if in.ID == "" {
		in.ID = store.NewID()
	}
	if in.Status == "" {
		in.Status = store.IPActive
	}
	if in.AddressType == "" {
		in.AddressType = store.AddrHost
	}
	if in.CreatedBy == "" {
		in.CreatedBy = subj.ActorID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.now()
	}
	if err := s.st.CreateAddress(ctx, in); err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	created, err := s.st.GetAddress(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	s.publish(ctx, subj.TenantID, events.IPAddressCreated, "created", created)
	return redact(created), nil
}

// Get returns one address (owner redacted).
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	a, err := s.st.GetAddress(ctx, subj.TenantID, id)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	return redact(a), nil
}

// List returns the caller's addresses matching f (owner redacted from each).
func (s *Service) List(ctx context.Context, subj authz.Subjects, f store.AddressFilter) ([]store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	rows, err := s.st.ListAddresses(ctx, subj.TenantID, f)
	if err != nil {
		return nil, err
	}
	out := make([]store.IPAddress, 0, len(rows))
	for _, a := range rows {
		out = append(out, redact(a))
	}
	return out, nil
}

// Update replaces an address and publishes ip_address.updated. Empty fields are
// inherited from the stored row; created-by/created-at are preserved.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, in store.IPAddress) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	in.TenantID = subj.TenantID
	ex, err := s.st.GetAddress(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	if in.Address == "" {
		in.Address = ex.Address
	}
	if in.SubnetID == "" {
		in.SubnetID = ex.SubnetID
	}
	if in.Status == "" {
		in.Status = ex.Status
	}
	if in.AddressType == "" {
		in.AddressType = ex.AddressType
	}
	in.CreatedBy = ex.CreatedBy
	in.CreatedAt = ex.CreatedAt
	if err := s.st.UpdateAddress(ctx, in); err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	updated, err := s.st.GetAddress(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	s.publish(ctx, subj.TenantID, events.IPAddressUpdated, "updated", updated)
	return redact(updated), nil
}

// Delete removes an address and publishes ip_address.deleted.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	a, err := s.st.GetAddress(ctx, subj.TenantID, id)
	if err != nil {
		return mapErr(err)
	}
	if err := s.st.DeleteAddress(ctx, subj.TenantID, id); err != nil {
		return mapErr(err)
	}
	s.publish(ctx, subj.TenantID, events.IPAddressDeleted, "deleted", a)
	return nil
}

// Find returns the address with the given dotted/colon string in the caller's
// tenant (owner redacted).
func (s *Service) Find(ctx context.Context, subj authz.Subjects, address string) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	a, err := s.st.FindAddress(ctx, subj.TenantID, address)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	return redact(a), nil
}

// AllocateNext hands out the lowest free host in subnetID, skipping the gateway,
// the skipFirst/skipLast carve-outs, the already-allocated rows and skipAddresses,
// and (when startFrom is set) any address below startFrom. It returns the created
// host row, or ErrNoAvailable when the range is exhausted.
func (s *Service) AllocateNext(ctx context.Context, subj authz.Subjects, subnetID, startFrom string, skipAddresses []string) (store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.IPAddress{}, err
	}
	sub, err := s.st.GetSubnet(ctx, subj.TenantID, subnetID)
	if err != nil {
		return store.IPAddress{}, mapErr(err)
	}
	exclude, err := s.allocatedSet(ctx, subj.TenantID, subnetID, skipAddresses)
	if err != nil {
		return store.IPAddress{}, err
	}
	return s.allocInto(ctx, subj.TenantID, sub, exclude, startFrom, "")
}

// BulkAllocate hands out count consecutive free hosts in subnetID. When
// hostnamePrefix is set, the i-th address is named hostnamePrefix+i. On a partial
// failure it returns the addresses allocated so far alongside the error.
func (s *Service) BulkAllocate(ctx context.Context, subj authz.Subjects, subnetID string, count int, hostnamePrefix string) ([]store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	if count <= 0 {
		return nil, nil
	}
	sub, err := s.st.GetSubnet(ctx, subj.TenantID, subnetID)
	if err != nil {
		return nil, mapErr(err)
	}
	exclude, err := s.allocatedSet(ctx, subj.TenantID, subnetID, nil)
	if err != nil {
		return nil, err
	}
	out := make([]store.IPAddress, 0, count)
	for i := 0; i < count; i++ {
		hostname := ""
		if hostnamePrefix != "" {
			hostname = hostnamePrefix + strconv.Itoa(i)
		}
		a, aerr := s.allocInto(ctx, subj.TenantID, sub, exclude, "", hostname)
		if aerr != nil {
			return out, aerr
		}
		out = append(out, a)
	}
	return out, nil
}

// PingAddress probes one address through the injected Pinger. With no Pinger set
// it returns a clean not-available result rather than an error.
func (s *Service) PingAddress(ctx context.Context, subj authz.Subjects, id string) (PingResult, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return PingResult{}, err
	}
	a, err := s.st.GetAddress(ctx, subj.TenantID, id)
	if err != nil {
		return PingResult{}, mapErr(err)
	}
	if s.ping == nil {
		return PingResult{Address: a.Address, Available: false}, nil
	}
	alive, rtt, err := s.ping.Ping(ctx, a.Address)
	if err != nil {
		return PingResult{}, err
	}
	return PingResult{Address: a.Address, Alive: alive, RTTMs: rtt, Available: true}, nil
}

// SuggestAvailableAddresses returns up to count database-free candidates in
// subnetID. When a Pinger/PortScanner is set, each candidate is additionally
// verified quiet on the wire (not alive, no open port) before being suggested;
// otherwise the database-free candidates are returned directly.
func (s *Service) SuggestAvailableAddresses(ctx context.Context, subj authz.Subjects, subnetID string, count int, skip []string) ([]string, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	if count <= 0 {
		return nil, nil
	}
	sub, err := s.st.GetSubnet(ctx, subj.TenantID, subnetID)
	if err != nil {
		return nil, mapErr(err)
	}
	exclude, err := s.allocatedSet(ctx, subj.TenantID, subnetID, skip)
	if err != nil {
		return nil, err
	}
	probing := s.ping != nil || s.scan != nil
	sample := count
	if probing {
		sample = count * 4 // over-sample so probed-busy candidates can be dropped
	}
	cands := s.freeCandidates(sub, exclude, sample)
	if !probing {
		if len(cands) > count {
			cands = cands[:count]
		}
		return cands, nil
	}
	out := make([]string, 0, count)
	for _, ip := range cands {
		if len(out) >= count {
			break
		}
		free, verr := s.verifyQuiet(ctx, ip)
		if verr != nil {
			continue
		}
		if free {
			out = append(out, ip)
		}
	}
	return out, nil
}

// --- allocation helpers ---

// allocInto claims the lowest free host of sub not in exclude (and, when
// startFrom is set, not below it), inserting it and mutating exclude so repeat
// calls advance. It publishes ip_address.created for the winner.
func (s *Service) allocInto(ctx context.Context, tenantID string, sub store.Subnet, exclude map[string]bool, startFrom, hostname string) (store.IPAddress, error) {
	conflicts := 0
	for {
		ip, err := ipnet.FirstFree(sub.CIDR, exclude, sub.Gateway, s.skipFirst, s.skipLast)
		if errors.Is(err, ipnet.ErrExhausted) {
			return store.IPAddress{}, ErrNoAvailable
		}
		if err != nil {
			return store.IPAddress{}, err
		}
		ipStr := ip.String()
		if startFrom != "" && ipLess(ipStr, startFrom) {
			exclude[ipStr] = true
			continue
		}
		a := store.IPAddress{
			TenantID:    tenantID,
			Address:     ipStr,
			SubnetID:    sub.ID,
			Hostname:    hostname,
			Status:      store.IPActive,
			AddressType: store.AddrHost,
			CreatedAt:   s.now(),
		}
		err = s.st.CreateAddress(ctx, a)
		if errors.Is(err, repo.ErrConflict) {
			exclude[ipStr] = true // raced: someone else took it, retry
			conflicts++
			if conflicts >= maxAllocTries {
				return store.IPAddress{}, ErrConflict
			}
			continue
		}
		if err != nil {
			return store.IPAddress{}, mapErr(err)
		}
		exclude[ipStr] = true
		created, ferr := s.st.FindAddress(ctx, tenantID, ipStr)
		if ferr != nil {
			return store.IPAddress{}, mapErr(ferr)
		}
		s.publish(ctx, tenantID, events.IPAddressCreated, "created", created)
		return redact(created), nil
	}
}

// allocatedSet builds the exclude set from the subnet's allocated rows plus the
// caller-supplied extras.
func (s *Service) allocatedSet(ctx context.Context, tenantID, subnetID string, extra []string) (map[string]bool, error) {
	used, err := s.st.ListAllocatedAddresses(ctx, tenantID, subnetID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(used)+len(extra))
	for _, a := range used {
		set[a] = true
	}
	for _, a := range extra {
		if a != "" {
			set[a] = true
		}
	}
	return set, nil
}

// freeCandidates walks up to max lowest free hosts of sub through ipnet, never
// enumerating the whole range; it stops early (returning fewer) when the range is
// exhausted.
func (s *Service) freeCandidates(sub store.Subnet, exclude map[string]bool, max int) []string {
	local := make(map[string]bool, len(exclude))
	for k, v := range exclude {
		local[k] = v
	}
	out := make([]string, 0, max)
	for len(out) < max {
		ip, err := ipnet.FirstFree(sub.CIDR, local, sub.Gateway, s.skipFirst, s.skipLast)
		if err != nil {
			break
		}
		ss := ip.String()
		out = append(out, ss)
		local[ss] = true
	}
	return out
}

// verifyQuiet reports whether ip is quiet on the wire per the injected probers.
func (s *Service) verifyQuiet(ctx context.Context, ip string) (bool, error) {
	if s.ping != nil {
		alive, _, err := s.ping.Ping(ctx, ip)
		if err != nil {
			return false, err
		}
		if alive {
			return false, nil
		}
	}
	if s.scan != nil {
		open, err := s.scan.Scan(ctx, ip, defaultProbePorts, probeTimeout)
		if err != nil {
			return false, err
		}
		if len(open) > 0 {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) publish(ctx context.Context, tenantID, eventType, action string, a store.IPAddress) {
	if s.pub == nil {
		return
	}
	s.pub.Publish(ctx, tenantID, eventType, events.IPAddressPayload(action, a.ID, a.Address, a.SubnetID, a.Hostname, a.DeviceID))
}

// redact clears the sealed owner field so it never leaves the service.
func redact(a store.IPAddress) store.IPAddress {
	a.Owner = ""
	return a
}

// ipLess reports whether a sorts numerically before b (same address family).
func ipLess(a, b string) bool {
	ia := net.ParseIP(a)
	ib := net.ParseIP(b)
	if ia == nil || ib == nil {
		return false
	}
	return bytes.Compare(ia.To16(), ib.To16()) < 0
}

// mapErr masks the store's sentinels into this package's own.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, repo.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}
