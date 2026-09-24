// Package memstore is an in-memory repo.Store for the IPAM service, used by tests
// and local dev. It filters by tenant (mirroring RLS), implements the full IPAM
// surface (subnets/addresses with the duplicate-address allocation guard,
// devices/interfaces/links/packages, vlans, the location tree, ip/host groups +
// members, the scan work queue with due-job claiming, DNS config, tenant stats
// and audit), fills the computed counts on Get/List, and offers per-method error
// injection via FailNext.
package memstore

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// injectedErr is the error FailNext arms for a given method.
type injectedErr struct{ method string }

func (e injectedErr) Error() string { return "memstore: injected failure in " + e.method }

// Mem is an in-memory store. It is safe for concurrent use.
type Mem struct {
	mu sync.Mutex

	subnets     map[string]store.Subnet
	addrs       map[string]store.IPAddress
	devices     map[string]store.Device
	ifaces      map[string]store.DeviceInterface
	links       map[string][]store.DeviceInterfaceLink // keyed by interface id
	pkgs        map[string][]store.DevicePackage       // keyed by device id
	vlans       map[string]store.Vlan
	locs        map[string]store.Location
	ipGroups    map[string]store.IPGroup
	ipMembers   map[string]store.IPGroupMember // keyed by member id
	hostGroups  map[string]store.HostGroup
	hostMembers map[string]store.HostGroupMember // keyed by member id
	scans       map[string]store.IPScanJob
	dns         map[string]store.DNSConfig // keyed by tenant id
	audit       []store.AuditRow

	failNext map[string]bool
	Now      func() time.Time
}

// New builds an empty store.
func New() *Mem {
	return &Mem{
		subnets:     map[string]store.Subnet{},
		addrs:       map[string]store.IPAddress{},
		devices:     map[string]store.Device{},
		ifaces:      map[string]store.DeviceInterface{},
		links:       map[string][]store.DeviceInterfaceLink{},
		pkgs:        map[string][]store.DevicePackage{},
		vlans:       map[string]store.Vlan{},
		locs:        map[string]store.Location{},
		ipGroups:    map[string]store.IPGroup{},
		ipMembers:   map[string]store.IPGroupMember{},
		hostGroups:  map[string]store.HostGroup{},
		hostMembers: map[string]store.HostGroupMember{},
		scans:       map[string]store.IPScanJob{},
		dns:         map[string]store.DNSConfig{},
		failNext:    map[string]bool{},
		Now:         func() time.Time { return time.Now().UTC() },
	}
}

// FailNext arms the next call to the named method to return an injected error.
func (m *Mem) FailNext(method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext[method] = true
}

// fail reports (and disarms) an injected failure for method, if armed.
func (m *Mem) fail(method string) error {
	if m.failNext[method] {
		delete(m.failNext, method)
		return injectedErr{method}
	}
	return nil
}

// Close is a no-op for the in-memory store.
func (m *Mem) Close() {}

// ---- helpers

// paginate sorts items newest-first by id (uuid v7 ids are time-ordered), applies
// the keyset cursor (last id of the previous page) and the limit.
func paginate[T any](items []T, id func(T) string, cursor string, limit int) []T {
	sort.Slice(items, func(i, j int) bool { return id(items[i]) > id(items[j]) })
	if cursor != "" {
		filtered := items[:0]
		for _, it := range items {
			if id(it) < cursor {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// subnetTotal returns the address capacity of a CIDR. Prefixes too large to
// represent (very wide IPv6) return 0 (treated as "unbounded", utilization 0).
func subnetTotal(cidr string) int64 {
	if cidr == "" {
		return 0
	}
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0
	}
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits <= 0 {
		return 1
	}
	if hostBits > 62 {
		return 0
	}
	return int64(1) << uint(hostBits)
}

func now(m *Mem) time.Time { return m.Now() }

// ---- subnets

// fillSubnet computes the used/available/utilization fields from allocated rows.
// Caller holds the lock.
func (m *Mem) fillSubnet(s *store.Subnet) {
	var used int64
	for _, a := range m.addrs {
		if a.TenantID == s.TenantID && a.SubnetID == s.ID {
			used++
		}
	}
	total := subnetTotal(s.CIDR)
	s.TotalAddresses = total
	s.UsedAddresses = used
	if total > 0 {
		s.AvailableAddresses = total - used
		s.Utilization = float64(used) / float64(total)
	} else {
		s.AvailableAddresses = 0
		s.Utilization = 0
	}
}

func (m *Mem) CreateSubnet(_ context.Context, s store.Subnet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateSubnet"); err != nil {
		return err
	}
	for _, ex := range m.subnets {
		if ex.TenantID == s.TenantID && ex.Name == s.Name {
			return repo.ErrConflict
		}
	}
	if s.ID == "" {
		s.ID = store.NewID()
	}
	if s.Status == "" {
		s.Status = store.SubnetActive
	}
	if s.IPVersion == 0 {
		s.IPVersion = 4
	}
	t := now(m)
	if s.CreatedAt.IsZero() {
		s.CreatedAt = t
	}
	s.UpdatedAt = t
	m.subnets[s.ID] = s
	return nil
}

func (m *Mem) GetSubnet(_ context.Context, tenantID, id string) (store.Subnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subnets[id]
	if !ok || s.TenantID != tenantID {
		return store.Subnet{}, repo.ErrNotFound
	}
	m.fillSubnet(&s)
	return s, nil
}

func (m *Mem) ListSubnets(_ context.Context, tenantID string, f store.SubnetFilter) ([]store.Subnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Subnet
	for _, s := range m.subnets {
		if s.TenantID != tenantID {
			continue
		}
		if f.VlanID != "" && s.VlanID != f.VlanID {
			continue
		}
		if f.ParentID != "" && s.ParentID != f.ParentID {
			continue
		}
		if f.LocationID != "" && s.LocationID != f.LocationID {
			continue
		}
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		if f.IPVersion != 0 && s.IPVersion != f.IPVersion {
			continue
		}
		if f.Query != "" {
			q := strings.ToLower(f.Query)
			if !strings.Contains(strings.ToLower(s.Name), q) && !strings.Contains(strings.ToLower(s.CIDR), q) {
				continue
			}
		}
		out = append(out, s)
	}
	out = paginate(out, func(s store.Subnet) string { return s.ID }, f.CursorID, f.Limit)
	for i := range out {
		m.fillSubnet(&out[i])
	}
	return out, nil
}

func (m *Mem) UpdateSubnet(_ context.Context, s store.Subnet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateSubnet"); err != nil {
		return err
	}
	ex, ok := m.subnets[s.ID]
	if !ok || ex.TenantID != s.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.subnets {
		if o.ID != s.ID && o.TenantID == s.TenantID && o.Name == s.Name {
			return repo.ErrConflict
		}
	}
	s.CreatedAt = ex.CreatedAt
	s.UpdatedAt = now(m)
	m.subnets[s.ID] = s
	return nil
}

func (m *Mem) DeleteSubnet(_ context.Context, tenantID, id string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteSubnet"); err != nil {
		return err
	}
	s, ok := m.subnets[id]
	if !ok || s.TenantID != tenantID {
		return repo.ErrNotFound
	}
	var addrIDs []string
	for aid, a := range m.addrs {
		if a.TenantID == tenantID && a.SubnetID == id {
			addrIDs = append(addrIDs, aid)
		}
	}
	if len(addrIDs) > 0 && !force {
		return repo.ErrNotEmpty
	}
	for _, aid := range addrIDs {
		delete(m.addrs, aid)
	}
	for jid, j := range m.scans {
		if j.TenantID == tenantID && j.SubnetID == id {
			delete(m.scans, jid)
		}
	}
	for cid, c := range m.subnets {
		if c.ParentID == id {
			c.ParentID = ""
			m.subnets[cid] = c
		}
	}
	delete(m.subnets, id)
	return nil
}

func (m *Mem) CountAddressesInSubnet(_ context.Context, tenantID, subnetID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.SubnetID == subnetID {
			n++
		}
	}
	return n, nil
}

func (m *Mem) ListAllocatedAddresses(_ context.Context, tenantID, subnetID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.SubnetID == subnetID {
			out = append(out, a.Address)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Mem) SubnetsForVlan(_ context.Context, tenantID, vlanID string) ([]store.Subnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Subnet
	for _, s := range m.subnets {
		if s.TenantID == tenantID && s.VlanID == vlanID {
			m.fillSubnet(&s)
			out = append(out, s)
		}
	}
	out = paginate(out, func(s store.Subnet) string { return s.ID }, "", 0)
	return out, nil
}

func (m *Mem) AllSubnetCIDRs(_ context.Context, tenantID string) ([]store.Subnet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Subnet
	for _, s := range m.subnets {
		if s.TenantID == tenantID {
			out = append(out, s)
		}
	}
	out = paginate(out, func(s store.Subnet) string { return s.ID }, "", 0)
	return out, nil
}

// ---- ip addresses

func (m *Mem) findAddrLocked(tenantID, address string) (store.IPAddress, string, bool) {
	for id, a := range m.addrs {
		if a.TenantID == tenantID && a.Address == address {
			return a, id, true
		}
	}
	return store.IPAddress{}, "", false
}

func (m *Mem) CreateAddress(_ context.Context, a store.IPAddress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateAddress"); err != nil {
		return err
	}
	if _, _, ok := m.findAddrLocked(a.TenantID, a.Address); ok {
		return repo.ErrConflict // duplicate-IP / allocation guard
	}
	if a.ID == "" {
		a.ID = store.NewID()
	}
	if a.Status == "" {
		a.Status = store.IPActive
	}
	if a.AddressType == "" {
		a.AddressType = store.AddrHost
	}
	t := now(m)
	if a.CreatedAt.IsZero() {
		a.CreatedAt = t
	}
	a.UpdatedAt = t
	m.addrs[a.ID] = a
	return nil
}

func (m *Mem) GetAddress(_ context.Context, tenantID, id string) (store.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addrs[id]
	if !ok || a.TenantID != tenantID {
		return store.IPAddress{}, repo.ErrNotFound
	}
	return a, nil
}

func (m *Mem) FindAddress(_ context.Context, tenantID, address string) (store.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, _, ok := m.findAddrLocked(tenantID, address)
	if !ok {
		return store.IPAddress{}, repo.ErrNotFound
	}
	return a, nil
}

func (m *Mem) ListAddresses(_ context.Context, tenantID string, f store.AddressFilter) ([]store.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.IPAddress
	for _, a := range m.addrs {
		if a.TenantID != tenantID {
			continue
		}
		if f.SubnetID != "" && a.SubnetID != f.SubnetID {
			continue
		}
		if f.DeviceID != "" && a.DeviceID != f.DeviceID {
			continue
		}
		if f.Status != "" && a.Status != f.Status {
			continue
		}
		if f.AddressType != "" && a.AddressType != f.AddressType {
			continue
		}
		if f.AddressPrefix != "" && !strings.HasPrefix(a.Address, f.AddressPrefix) {
			continue
		}
		if f.HostnamePattern != "" && !strings.Contains(strings.ToLower(a.Hostname), strings.ToLower(f.HostnamePattern)) {
			continue
		}
		out = append(out, a)
	}
	out = paginate(out, func(a store.IPAddress) string { return a.ID }, f.CursorID, f.Limit)
	return out, nil
}

func (m *Mem) UpdateAddress(_ context.Context, a store.IPAddress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateAddress"); err != nil {
		return err
	}
	ex, ok := m.addrs[a.ID]
	if !ok || ex.TenantID != a.TenantID {
		return repo.ErrNotFound
	}
	if a.Address != ex.Address {
		if _, _, dup := m.findAddrLocked(a.TenantID, a.Address); dup {
			return repo.ErrConflict
		}
	}
	a.CreatedAt = ex.CreatedAt
	a.UpdatedAt = now(m)
	m.addrs[a.ID] = a
	return nil
}

func (m *Mem) DeleteAddress(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteAddress"); err != nil {
		return err
	}
	a, ok := m.addrs[id]
	if !ok || a.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.addrs, id)
	return nil
}

func (m *Mem) UpsertAddressByAddress(_ context.Context, a store.IPAddress) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpsertAddressByAddress"); err != nil {
		return false, err
	}
	t := now(m)
	if ex, id, ok := m.findAddrLocked(a.TenantID, a.Address); ok {
		a.ID = id
		a.CreatedAt = ex.CreatedAt
		if a.Status == "" {
			a.Status = ex.Status
		}
		if a.AddressType == "" {
			a.AddressType = ex.AddressType
		}
		a.UpdatedAt = t
		m.addrs[id] = a
		return false, nil
	}
	if a.ID == "" {
		a.ID = store.NewID()
	}
	if a.Status == "" {
		a.Status = store.IPActive
	}
	if a.AddressType == "" {
		a.AddressType = store.AddrHost
	}
	a.CreatedAt = t
	a.UpdatedAt = t
	m.addrs[a.ID] = a
	return true, nil
}

func (m *Mem) AddressesForDevice(_ context.Context, tenantID, deviceID string) ([]store.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.IPAddress
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.DeviceID == deviceID {
			out = append(out, a)
		}
	}
	out = paginate(out, func(a store.IPAddress) string { return a.ID }, "", 0)
	return out, nil
}

// ---- devices

// fillDevice computes the interface/address/package counts. Caller holds the lock.
func (m *Mem) fillDevice(d *store.Device) {
	var ifc, ac, pu, su int64
	for _, i := range m.ifaces {
		if i.TenantID == d.TenantID && i.DeviceID == d.ID {
			ifc++
		}
	}
	for _, a := range m.addrs {
		if a.TenantID == d.TenantID && a.DeviceID == d.ID {
			ac++
		}
	}
	for _, p := range m.pkgs[d.ID] {
		if p.TenantID != d.TenantID {
			continue
		}
		if p.NeedsUpdate {
			pu++
		}
		if p.IsSecurityUpdate {
			su++
		}
	}
	d.InterfaceCount = ifc
	d.AddressCount = ac
	d.PackageUpdateCount = pu
	d.SecurityUpdateCount = su
}

// mergeDevice overlays the non-empty/non-zero summary fields of src onto dst (the
// scan-path merge: a discovery update never clobbers a known field with a blank).
func mergeDevice(dst, src store.Device) store.Device {
	set := func(d *string, s string) {
		if s != "" {
			*d = s
		}
	}
	set(&dst.Name, src.Name)
	set(&dst.DeviceType, src.DeviceType)
	set(&dst.Description, src.Description)
	set(&dst.Manufacturer, src.Manufacturer)
	set(&dst.Model, src.Model)
	set(&dst.SerialNumber, src.SerialNumber)
	set(&dst.AssetTag, src.AssetTag)
	set(&dst.LocationID, src.LocationID)
	set(&dst.RackID, src.RackID)
	set(&dst.Status, src.Status)
	set(&dst.PrimaryIP, src.PrimaryIP)
	set(&dst.PrimaryIPv6, src.PrimaryIPv6)
	set(&dst.ManagementIP, src.ManagementIP)
	set(&dst.OSType, src.OSType)
	set(&dst.OSVersion, src.OSVersion)
	set(&dst.FirmwareVersion, src.FirmwareVersion)
	set(&dst.Contact, src.Contact)
	set(&dst.IPMISecretRef, src.IPMISecretRef)
	if src.RackPosition != 0 {
		dst.RackPosition = src.RackPosition
	}
	if src.DeviceHeightU != 0 {
		dst.DeviceHeightU = src.DeviceHeightU
	}
	if src.LastSeen != nil {
		dst.LastSeen = src.LastSeen
	}
	if src.Tags != nil {
		dst.Tags = src.Tags
	}
	dst.RebootRequired = src.RebootRequired
	dst.UnattendedUpgrades = src.UnattendedUpgrades
	return dst
}

func (m *Mem) CreateDevice(_ context.Context, d store.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateDevice"); err != nil {
		return err
	}
	for _, ex := range m.devices {
		if ex.TenantID == d.TenantID && ex.Name == d.Name {
			return repo.ErrConflict
		}
	}
	if d.ID == "" {
		d.ID = store.NewID()
	}
	if d.Status == "" {
		d.Status = store.DevStActive
	}
	if d.DeviceType == "" {
		d.DeviceType = store.DevOther
	}
	t := now(m)
	if d.CreatedAt.IsZero() {
		d.CreatedAt = t
	}
	d.UpdatedAt = t
	m.devices[d.ID] = d
	return nil
}

func (m *Mem) GetDevice(_ context.Context, tenantID, id string) (store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok || d.TenantID != tenantID {
		return store.Device{}, repo.ErrNotFound
	}
	m.fillDevice(&d)
	return d, nil
}

func (m *Mem) ListDevices(_ context.Context, tenantID string, f store.DeviceFilter) ([]store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Device
	for _, d := range m.devices {
		if d.TenantID != tenantID {
			continue
		}
		if f.DeviceType != "" && d.DeviceType != f.DeviceType {
			continue
		}
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		if f.LocationID != "" && d.LocationID != f.LocationID {
			continue
		}
		if f.Manufacturer != "" && d.Manufacturer != f.Manufacturer {
			continue
		}
		if f.RackID != "" && d.RackID != f.RackID {
			continue
		}
		if f.Query != "" {
			q := strings.ToLower(f.Query)
			if !strings.Contains(strings.ToLower(d.Name), q) && !strings.Contains(strings.ToLower(d.PrimaryIP), q) {
				continue
			}
		}
		out = append(out, d)
	}
	out = paginate(out, func(d store.Device) string { return d.ID }, f.CursorID, f.Limit)
	for i := range out {
		m.fillDevice(&out[i])
	}
	return out, nil
}

func (m *Mem) UpdateDevice(_ context.Context, d store.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateDevice"); err != nil {
		return err
	}
	ex, ok := m.devices[d.ID]
	if !ok || ex.TenantID != d.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.devices {
		if o.ID != d.ID && o.TenantID == d.TenantID && o.Name == d.Name {
			return repo.ErrConflict
		}
	}
	d.CreatedAt = ex.CreatedAt
	d.UpdatedAt = now(m)
	m.devices[d.ID] = d
	return nil
}

func (m *Mem) DeleteDevice(_ context.Context, tenantID, id string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteDevice"); err != nil {
		return err
	}
	d, ok := m.devices[id]
	if !ok || d.TenantID != tenantID {
		return repo.ErrNotFound
	}
	var ifaceIDs []string
	for iid, i := range m.ifaces {
		if i.TenantID == tenantID && i.DeviceID == id {
			ifaceIDs = append(ifaceIDs, iid)
		}
	}
	var boundAddrs int
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.DeviceID == id {
			boundAddrs++
		}
	}
	if (len(ifaceIDs) > 0 || boundAddrs > 0) && !force {
		return repo.ErrNotEmpty
	}
	for _, iid := range ifaceIDs {
		delete(m.ifaces, iid)
		delete(m.links, iid)
	}
	delete(m.pkgs, id)
	for aid, a := range m.addrs {
		if a.TenantID == tenantID && a.DeviceID == id {
			a.DeviceID = ""
			m.addrs[aid] = a
		}
	}
	for mid, hm := range m.hostMembers {
		if hm.TenantID == tenantID && hm.DeviceID == id {
			delete(m.hostMembers, mid)
		}
	}
	delete(m.devices, id)
	return nil
}

func (m *Mem) UpsertDeviceByName(_ context.Context, d store.Device) (store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpsertDeviceByName"); err != nil {
		return store.Device{}, err
	}
	t := now(m)
	for id, ex := range m.devices {
		if ex.TenantID == d.TenantID && ex.Name == d.Name {
			merged := mergeDevice(ex, d)
			merged.ID = id
			merged.TenantID = ex.TenantID
			merged.CreatedAt = ex.CreatedAt
			merged.UpdatedAt = t
			m.devices[id] = merged
			out := merged
			m.fillDevice(&out)
			return out, nil
		}
	}
	if d.ID == "" {
		d.ID = store.NewID()
	}
	if d.Status == "" {
		d.Status = store.DevStActive
	}
	if d.DeviceType == "" {
		d.DeviceType = store.DevOther
	}
	d.CreatedAt = t
	d.UpdatedAt = t
	m.devices[d.ID] = d
	out := d
	m.fillDevice(&out)
	return out, nil
}

// ---- device interfaces + links

func (m *Mem) CreateInterface(_ context.Context, i store.DeviceInterface) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateInterface"); err != nil {
		return err
	}
	for _, ex := range m.ifaces {
		if ex.DeviceID == i.DeviceID && ex.Name == i.Name {
			return repo.ErrConflict
		}
	}
	if i.ID == "" {
		i.ID = store.NewID()
	}
	t := now(m)
	if i.CreatedAt.IsZero() {
		i.CreatedAt = t
	}
	i.UpdatedAt = t
	m.ifaces[i.ID] = i
	return nil
}

func (m *Mem) GetInterface(_ context.Context, tenantID, id string) (store.DeviceInterface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ifaces[id]
	if !ok || i.TenantID != tenantID {
		return store.DeviceInterface{}, repo.ErrNotFound
	}
	return i, nil
}

func (m *Mem) ListInterfaces(_ context.Context, tenantID, deviceID string) ([]store.DeviceInterface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.DeviceInterface
	for _, i := range m.ifaces {
		if i.TenantID == tenantID && i.DeviceID == deviceID {
			out = append(out, i)
		}
	}
	out = paginate(out, func(i store.DeviceInterface) string { return i.ID }, "", 0)
	return out, nil
}

func (m *Mem) UpsertInterfaceByName(_ context.Context, i store.DeviceInterface) (store.DeviceInterface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpsertInterfaceByName"); err != nil {
		return store.DeviceInterface{}, err
	}
	t := now(m)
	for id, ex := range m.ifaces {
		if ex.DeviceID == i.DeviceID && ex.Name == i.Name {
			i.ID = id
			i.CreatedAt = ex.CreatedAt
			i.UpdatedAt = t
			m.ifaces[id] = i
			return i, nil
		}
	}
	if i.ID == "" {
		i.ID = store.NewID()
	}
	i.CreatedAt = t
	i.UpdatedAt = t
	m.ifaces[i.ID] = i
	return i, nil
}

func (m *Mem) DeleteInterface(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteInterface"); err != nil {
		return err
	}
	i, ok := m.ifaces[id]
	if !ok || i.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.ifaces, id)
	delete(m.links, id)
	return nil
}

func (m *Mem) ReplaceInterfaceLinks(_ context.Context, tenantID, interfaceID string, links []store.DeviceInterfaceLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ReplaceInterfaceLinks"); err != nil {
		return err
	}
	i, ok := m.ifaces[interfaceID]
	if !ok || i.TenantID != tenantID {
		return repo.ErrNotFound
	}
	cp := make([]store.DeviceInterfaceLink, 0, len(links))
	for _, l := range links {
		if l.ID == "" {
			l.ID = store.NewID()
		}
		l.TenantID = tenantID
		l.InterfaceID = interfaceID
		cp = append(cp, l)
	}
	m.links[interfaceID] = cp
	return nil
}

func (m *Mem) ListInterfaceLinks(_ context.Context, tenantID, interfaceID string) ([]store.DeviceInterfaceLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.DeviceInterfaceLink
	for _, l := range m.links[interfaceID] {
		if l.TenantID == tenantID {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ---- device packages

func (m *Mem) ReplaceDevicePackages(_ context.Context, tenantID, deviceID string, pkgs []store.DevicePackage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ReplaceDevicePackages"); err != nil {
		return err
	}
	t := now(m)
	cp := make([]store.DevicePackage, 0, len(pkgs))
	seen := map[string]bool{}
	for _, p := range pkgs {
		if seen[p.Name] {
			return repo.ErrConflict
		}
		seen[p.Name] = true
		if p.ID == "" {
			p.ID = store.NewID()
		}
		p.TenantID = tenantID
		p.DeviceID = deviceID
		if p.CreatedAt.IsZero() {
			p.CreatedAt = t
		}
		p.UpdatedAt = t
		cp = append(cp, p)
	}
	m.pkgs[deviceID] = cp
	return nil
}

func (m *Mem) ListDevicePackages(_ context.Context, tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string) ([]store.DevicePackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.DevicePackage
	for _, p := range m.pkgs[deviceID] {
		if p.TenantID != tenantID {
			continue
		}
		if needsUpdate != nil && p.NeedsUpdate != *needsUpdate {
			continue
		}
		if securityOnly != nil && p.IsSecurityUpdate != *securityOnly {
			continue
		}
		if manager != "" && p.PackageManager != manager {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Mem) DeleteDevicePackages(_ context.Context, tenantID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteDevicePackages"); err != nil {
		return err
	}
	delete(m.pkgs, deviceID)
	return nil
}

// ---- vlans

func (m *Mem) fillVlan(v *store.Vlan) {
	var n int64
	for _, s := range m.subnets {
		if s.TenantID == v.TenantID && s.VlanID == v.ID {
			n++
		}
	}
	v.SubnetCount = n
}

func (m *Mem) CreateVlan(_ context.Context, v store.Vlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateVlan"); err != nil {
		return err
	}
	for _, ex := range m.vlans {
		if ex.TenantID == v.TenantID && (ex.VlanID == v.VlanID || ex.Name == v.Name) {
			return repo.ErrConflict
		}
	}
	if v.ID == "" {
		v.ID = store.NewID()
	}
	if v.Status == "" {
		v.Status = store.VlanActive
	}
	t := now(m)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = t
	}
	v.UpdatedAt = t
	m.vlans[v.ID] = v
	return nil
}

func (m *Mem) GetVlan(_ context.Context, tenantID, id string) (store.Vlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vlans[id]
	if !ok || v.TenantID != tenantID {
		return store.Vlan{}, repo.ErrNotFound
	}
	m.fillVlan(&v)
	return v, nil
}

func (m *Mem) ListVlans(_ context.Context, tenantID string, f store.VlanFilter) ([]store.Vlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Vlan
	for _, v := range m.vlans {
		if v.TenantID != tenantID {
			continue
		}
		if f.LocationID != "" && v.LocationID != f.LocationID {
			continue
		}
		if f.Domain != "" && v.Domain != f.Domain {
			continue
		}
		if f.Status != "" && v.Status != f.Status {
			continue
		}
		if f.VlanIDMin != 0 && v.VlanID < f.VlanIDMin {
			continue
		}
		if f.VlanIDMax != 0 && v.VlanID > f.VlanIDMax {
			continue
		}
		out = append(out, v)
	}
	out = paginate(out, func(v store.Vlan) string { return v.ID }, f.CursorID, f.Limit)
	for i := range out {
		m.fillVlan(&out[i])
	}
	return out, nil
}

func (m *Mem) UpdateVlan(_ context.Context, v store.Vlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateVlan"); err != nil {
		return err
	}
	ex, ok := m.vlans[v.ID]
	if !ok || ex.TenantID != v.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.vlans {
		if o.ID != v.ID && o.TenantID == v.TenantID && (o.VlanID == v.VlanID || o.Name == v.Name) {
			return repo.ErrConflict
		}
	}
	v.CreatedAt = ex.CreatedAt
	v.UpdatedAt = now(m)
	m.vlans[v.ID] = v
	return nil
}

func (m *Mem) DeleteVlan(_ context.Context, tenantID, id string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteVlan"); err != nil {
		return err
	}
	v, ok := m.vlans[id]
	if !ok || v.TenantID != tenantID {
		return repo.ErrNotFound
	}
	var refs []string
	for sid, s := range m.subnets {
		if s.TenantID == tenantID && s.VlanID == id {
			refs = append(refs, sid)
		}
	}
	if len(refs) > 0 && !force {
		return repo.ErrNotEmpty
	}
	for _, sid := range refs {
		s := m.subnets[sid]
		s.VlanID = ""
		m.subnets[sid] = s
	}
	delete(m.vlans, id)
	return nil
}

// ---- locations

func (m *Mem) fillLocation(l *store.Location) {
	var child, dev, sub, vl int64
	for _, c := range m.locs {
		if c.TenantID == l.TenantID && c.ParentID == l.ID {
			child++
		}
	}
	for _, d := range m.devices {
		if d.TenantID == l.TenantID && d.LocationID == l.ID {
			dev++
		}
	}
	for _, s := range m.subnets {
		if s.TenantID == l.TenantID && s.LocationID == l.ID {
			sub++
		}
	}
	for _, v := range m.vlans {
		if v.TenantID == l.TenantID && v.LocationID == l.ID {
			vl++
		}
	}
	l.ChildCount = child
	l.DeviceCount = dev
	l.SubnetCount = sub
	l.VlanCount = vl
}

func (m *Mem) CreateLocation(_ context.Context, l store.Location) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateLocation"); err != nil {
		return err
	}
	for _, ex := range m.locs {
		if ex.TenantID != l.TenantID {
			continue
		}
		if ex.Name == l.Name {
			return repo.ErrConflict
		}
		if l.Code != "" && ex.Code == l.Code {
			return repo.ErrConflict
		}
	}
	if l.ID == "" {
		l.ID = store.NewID()
	}
	if l.Status == "" {
		l.Status = store.LocStActive
	}
	t := now(m)
	if l.CreatedAt.IsZero() {
		l.CreatedAt = t
	}
	l.UpdatedAt = t
	m.locs[l.ID] = l
	return nil
}

func (m *Mem) GetLocation(_ context.Context, tenantID, id string) (store.Location, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locs[id]
	if !ok || l.TenantID != tenantID {
		return store.Location{}, repo.ErrNotFound
	}
	m.fillLocation(&l)
	return l, nil
}

func (m *Mem) ListLocations(_ context.Context, tenantID string, f store.LocationFilter) ([]store.Location, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Location
	for _, l := range m.locs {
		if l.TenantID != tenantID {
			continue
		}
		if f.ParentID != "" && l.ParentID != f.ParentID {
			continue
		}
		if f.LocationType != "" && l.LocationType != f.LocationType {
			continue
		}
		if f.Country != "" && l.Country != f.Country {
			continue
		}
		if f.Status != "" && l.Status != f.Status {
			continue
		}
		out = append(out, l)
	}
	out = paginate(out, func(l store.Location) string { return l.ID }, f.CursorID, f.Limit)
	for i := range out {
		m.fillLocation(&out[i])
	}
	return out, nil
}

func (m *Mem) UpdateLocation(_ context.Context, l store.Location) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateLocation"); err != nil {
		return err
	}
	ex, ok := m.locs[l.ID]
	if !ok || ex.TenantID != l.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.locs {
		if o.ID == l.ID || o.TenantID != l.TenantID {
			continue
		}
		if o.Name == l.Name {
			return repo.ErrConflict
		}
		if l.Code != "" && o.Code == l.Code {
			return repo.ErrConflict
		}
	}
	l.CreatedAt = ex.CreatedAt
	l.UpdatedAt = now(m)
	m.locs[l.ID] = l
	return nil
}

func (m *Mem) DeleteLocation(_ context.Context, tenantID, id string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteLocation"); err != nil {
		return err
	}
	l, ok := m.locs[id]
	if !ok || l.TenantID != tenantID {
		return repo.ErrNotFound
	}
	referenced := false
	for _, c := range m.locs {
		if c.TenantID == tenantID && c.ParentID == id {
			referenced = true
		}
	}
	for _, d := range m.devices {
		if d.TenantID == tenantID && d.LocationID == id {
			referenced = true
		}
	}
	for _, s := range m.subnets {
		if s.TenantID == tenantID && s.LocationID == id {
			referenced = true
		}
	}
	for _, v := range m.vlans {
		if v.TenantID == tenantID && v.LocationID == id {
			referenced = true
		}
	}
	if referenced && !force {
		return repo.ErrNotEmpty
	}
	// Null out references.
	for cid, c := range m.locs {
		if c.TenantID == tenantID && c.ParentID == id {
			c.ParentID = ""
			m.locs[cid] = c
		}
	}
	for did, d := range m.devices {
		if d.TenantID == tenantID && d.LocationID == id {
			d.LocationID = ""
			m.devices[did] = d
		}
	}
	for sid, s := range m.subnets {
		if s.TenantID == tenantID && s.LocationID == id {
			s.LocationID = ""
			m.subnets[sid] = s
		}
	}
	for vid, v := range m.vlans {
		if v.TenantID == tenantID && v.LocationID == id {
			v.LocationID = ""
			m.vlans[vid] = v
		}
	}
	delete(m.locs, id)
	return nil
}

// ---- ip groups

func (m *Mem) ipGroupMemberCount(tenantID, groupID string) int64 {
	var n int64
	for _, mem := range m.ipMembers {
		if mem.TenantID == tenantID && mem.IPGroupID == groupID {
			n++
		}
	}
	return n
}

func (m *Mem) CreateIPGroup(_ context.Context, g store.IPGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateIPGroup"); err != nil {
		return err
	}
	for _, ex := range m.ipGroups {
		if ex.TenantID == g.TenantID && ex.Name == g.Name {
			return repo.ErrConflict
		}
	}
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	t := now(m)
	if g.CreatedAt.IsZero() {
		g.CreatedAt = t
	}
	g.UpdatedAt = t
	m.ipGroups[g.ID] = g
	return nil
}

func (m *Mem) GetIPGroup(_ context.Context, tenantID, id string) (store.IPGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.ipGroups[id]
	if !ok || g.TenantID != tenantID {
		return store.IPGroup{}, repo.ErrNotFound
	}
	g.MemberCount = m.ipGroupMemberCount(tenantID, id)
	return g, nil
}

func (m *Mem) ListIPGroups(_ context.Context, tenantID string, limit int, cursorID string) ([]store.IPGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.IPGroup
	for _, g := range m.ipGroups {
		if g.TenantID == tenantID {
			out = append(out, g)
		}
	}
	out = paginate(out, func(g store.IPGroup) string { return g.ID }, cursorID, limit)
	for i := range out {
		out[i].MemberCount = m.ipGroupMemberCount(tenantID, out[i].ID)
	}
	return out, nil
}

func (m *Mem) UpdateIPGroup(_ context.Context, g store.IPGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateIPGroup"); err != nil {
		return err
	}
	ex, ok := m.ipGroups[g.ID]
	if !ok || ex.TenantID != g.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.ipGroups {
		if o.ID != g.ID && o.TenantID == g.TenantID && o.Name == g.Name {
			return repo.ErrConflict
		}
	}
	g.CreatedAt = ex.CreatedAt
	g.UpdatedAt = now(m)
	m.ipGroups[g.ID] = g
	return nil
}

func (m *Mem) DeleteIPGroup(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteIPGroup"); err != nil {
		return err
	}
	g, ok := m.ipGroups[id]
	if !ok || g.TenantID != tenantID {
		return repo.ErrNotFound
	}
	for mid, mem := range m.ipMembers {
		if mem.IPGroupID == id {
			delete(m.ipMembers, mid)
		}
	}
	delete(m.ipGroups, id)
	return nil
}

func (m *Mem) AddIPGroupMember(_ context.Context, mem store.IPGroupMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("AddIPGroupMember"); err != nil {
		return err
	}
	g, ok := m.ipGroups[mem.IPGroupID]
	if !ok || g.TenantID != mem.TenantID {
		return repo.ErrNotFound
	}
	for _, ex := range m.ipMembers {
		if ex.IPGroupID == mem.IPGroupID && ex.Value == mem.Value {
			return repo.ErrConflict
		}
	}
	if mem.ID == "" {
		mem.ID = store.NewID()
	}
	if mem.MemberType == "" {
		mem.MemberType = store.MemberAddress
	}
	m.ipMembers[mem.ID] = mem
	return nil
}

func (m *Mem) RemoveIPGroupMember(_ context.Context, tenantID, memberID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("RemoveIPGroupMember"); err != nil {
		return err
	}
	mem, ok := m.ipMembers[memberID]
	if !ok || mem.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.ipMembers, memberID)
	return nil
}

func (m *Mem) UpdateIPGroupMember(_ context.Context, mem store.IPGroupMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateIPGroupMember"); err != nil {
		return err
	}
	ex, ok := m.ipMembers[mem.ID]
	if !ok || ex.TenantID != mem.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.ipMembers {
		if o.ID != mem.ID && o.IPGroupID == ex.IPGroupID && o.Value == mem.Value {
			return repo.ErrConflict
		}
	}
	mem.IPGroupID = ex.IPGroupID
	m.ipMembers[mem.ID] = mem
	return nil
}

func (m *Mem) ListIPGroupMembers(_ context.Context, tenantID, groupID string) ([]store.IPGroupMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.IPGroupMember
	for _, mem := range m.ipMembers {
		if mem.TenantID == tenantID && mem.IPGroupID == groupID {
			out = append(out, mem)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Mem) AllIPGroupsWithMembers(_ context.Context, tenantID string, groupIDs []string) ([]store.IPGroup, map[string][]store.IPGroupMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]bool{}
	for _, id := range groupIDs {
		want[id] = true
	}
	var groups []store.IPGroup
	for _, g := range m.ipGroups {
		if g.TenantID != tenantID {
			continue
		}
		if len(want) > 0 && !want[g.ID] {
			continue
		}
		g.MemberCount = m.ipGroupMemberCount(tenantID, g.ID)
		groups = append(groups, g)
	}
	groups = paginate(groups, func(g store.IPGroup) string { return g.ID }, "", 0)
	members := map[string][]store.IPGroupMember{}
	for _, mem := range m.ipMembers {
		if mem.TenantID != tenantID {
			continue
		}
		if len(want) > 0 && !want[mem.IPGroupID] {
			continue
		}
		members[mem.IPGroupID] = append(members[mem.IPGroupID], mem)
	}
	for k := range members {
		list := members[k]
		sort.Slice(list, func(i, j int) bool {
			if list[i].Sequence != list[j].Sequence {
				return list[i].Sequence < list[j].Sequence
			}
			return list[i].ID < list[j].ID
		})
		members[k] = list
	}
	return groups, members, nil
}

// ---- host groups

func (m *Mem) hostGroupMemberCount(tenantID, groupID string) int64 {
	var n int64
	for _, mem := range m.hostMembers {
		if mem.TenantID == tenantID && mem.HostGroupID == groupID {
			n++
		}
	}
	return n
}

func (m *Mem) CreateHostGroup(_ context.Context, g store.HostGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateHostGroup"); err != nil {
		return err
	}
	for _, ex := range m.hostGroups {
		if ex.TenantID == g.TenantID && ex.Name == g.Name {
			return repo.ErrConflict
		}
	}
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	t := now(m)
	if g.CreatedAt.IsZero() {
		g.CreatedAt = t
	}
	g.UpdatedAt = t
	m.hostGroups[g.ID] = g
	return nil
}

func (m *Mem) GetHostGroup(_ context.Context, tenantID, id string) (store.HostGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.hostGroups[id]
	if !ok || g.TenantID != tenantID {
		return store.HostGroup{}, repo.ErrNotFound
	}
	g.MemberCount = m.hostGroupMemberCount(tenantID, id)
	return g, nil
}

func (m *Mem) ListHostGroups(_ context.Context, tenantID string, limit int, cursorID string) ([]store.HostGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.HostGroup
	for _, g := range m.hostGroups {
		if g.TenantID == tenantID {
			out = append(out, g)
		}
	}
	out = paginate(out, func(g store.HostGroup) string { return g.ID }, cursorID, limit)
	for i := range out {
		out[i].MemberCount = m.hostGroupMemberCount(tenantID, out[i].ID)
	}
	return out, nil
}

func (m *Mem) UpdateHostGroup(_ context.Context, g store.HostGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateHostGroup"); err != nil {
		return err
	}
	ex, ok := m.hostGroups[g.ID]
	if !ok || ex.TenantID != g.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.hostGroups {
		if o.ID != g.ID && o.TenantID == g.TenantID && o.Name == g.Name {
			return repo.ErrConflict
		}
	}
	g.CreatedAt = ex.CreatedAt
	g.UpdatedAt = now(m)
	m.hostGroups[g.ID] = g
	return nil
}

func (m *Mem) DeleteHostGroup(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteHostGroup"); err != nil {
		return err
	}
	g, ok := m.hostGroups[id]
	if !ok || g.TenantID != tenantID {
		return repo.ErrNotFound
	}
	for mid, mem := range m.hostMembers {
		if mem.HostGroupID == id {
			delete(m.hostMembers, mid)
		}
	}
	delete(m.hostGroups, id)
	return nil
}

func (m *Mem) AddHostGroupMember(_ context.Context, mem store.HostGroupMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("AddHostGroupMember"); err != nil {
		return err
	}
	g, ok := m.hostGroups[mem.HostGroupID]
	if !ok || g.TenantID != mem.TenantID {
		return repo.ErrNotFound
	}
	for _, ex := range m.hostMembers {
		if ex.HostGroupID == mem.HostGroupID && ex.DeviceID == mem.DeviceID {
			return repo.ErrConflict
		}
	}
	if mem.ID == "" {
		mem.ID = store.NewID()
	}
	m.hostMembers[mem.ID] = mem
	return nil
}

func (m *Mem) RemoveHostGroupMember(_ context.Context, tenantID, memberID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("RemoveHostGroupMember"); err != nil {
		return err
	}
	mem, ok := m.hostMembers[memberID]
	if !ok || mem.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.hostMembers, memberID)
	return nil
}

func (m *Mem) UpdateHostGroupMember(_ context.Context, mem store.HostGroupMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateHostGroupMember"); err != nil {
		return err
	}
	ex, ok := m.hostMembers[mem.ID]
	if !ok || ex.TenantID != mem.TenantID {
		return repo.ErrNotFound
	}
	for _, o := range m.hostMembers {
		if o.ID != mem.ID && o.HostGroupID == ex.HostGroupID && o.DeviceID == mem.DeviceID {
			return repo.ErrConflict
		}
	}
	mem.HostGroupID = ex.HostGroupID
	m.hostMembers[mem.ID] = mem
	return nil
}

// enrichHostMember fills the device summary fields. Caller holds the lock.
func (m *Mem) enrichHostMember(mem *store.HostGroupMember) {
	if d, ok := m.devices[mem.DeviceID]; ok {
		mem.DeviceName = d.Name
		mem.DeviceType = d.DeviceType
		mem.DeviceStatus = d.Status
		mem.DevicePrimaryIP = d.PrimaryIP
	}
}

func (m *Mem) ListHostGroupMembers(_ context.Context, tenantID, groupID string) ([]store.HostGroupMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.HostGroupMember
	for _, mem := range m.hostMembers {
		if mem.TenantID == tenantID && mem.HostGroupID == groupID {
			m.enrichHostMember(&mem)
			out = append(out, mem)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Mem) ListDeviceHostGroups(_ context.Context, tenantID, deviceID string) ([]store.HostGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := map[string]bool{}
	for _, mem := range m.hostMembers {
		if mem.TenantID == tenantID && mem.DeviceID == deviceID {
			ids[mem.HostGroupID] = true
		}
	}
	var out []store.HostGroup
	for id := range ids {
		if g, ok := m.hostGroups[id]; ok && g.TenantID == tenantID {
			g.MemberCount = m.hostGroupMemberCount(tenantID, id)
			out = append(out, g)
		}
	}
	out = paginate(out, func(g store.HostGroup) string { return g.ID }, "", 0)
	return out, nil
}

// ---- scan jobs

func (m *Mem) CreateScanJob(_ context.Context, j store.IPScanJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateScanJob"); err != nil {
		return err
	}
	if j.ID == "" {
		j.ID = store.NewID()
	}
	if j.Status == "" {
		j.Status = store.ScanPending
	}
	if j.TriggeredBy == "" {
		j.TriggeredBy = store.TriggerManual
	}
	if j.MaxRetries == 0 {
		j.MaxRetries = 3
	}
	if j.TimeoutMs == 0 {
		j.TimeoutMs = 1000
	}
	if j.Concurrency == 0 {
		j.Concurrency = 50
	}
	t := now(m)
	if j.CreatedAt.IsZero() {
		j.CreatedAt = t
	}
	j.UpdatedAt = t
	m.scans[j.ID] = j
	return nil
}

func (m *Mem) GetScanJob(_ context.Context, tenantID, id string) (store.IPScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.scans[id]
	if !ok || j.TenantID != tenantID {
		return store.IPScanJob{}, repo.ErrNotFound
	}
	return j, nil
}

func (m *Mem) ListScanJobs(_ context.Context, tenantID string, f store.ScanFilter) ([]store.IPScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.IPScanJob
	for _, j := range m.scans {
		if j.TenantID != tenantID {
			continue
		}
		if f.SubnetID != "" && j.SubnetID != f.SubnetID {
			continue
		}
		if f.Status != "" && j.Status != f.Status {
			continue
		}
		out = append(out, j)
	}
	out = paginate(out, func(j store.IPScanJob) string { return j.ID }, f.CursorID, f.Limit)
	return out, nil
}

func (m *Mem) UpdateScanJob(_ context.Context, j store.IPScanJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateScanJob"); err != nil {
		return err
	}
	ex, ok := m.scans[j.ID]
	if !ok || ex.TenantID != j.TenantID {
		return repo.ErrNotFound
	}
	j.CreatedAt = ex.CreatedAt
	j.UpdatedAt = now(m)
	m.scans[j.ID] = j
	return nil
}

func (m *Mem) ClaimDueScanJobs(_ context.Context, at time.Time, limit int) ([]store.IPScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ClaimDueScanJobs"); err != nil {
		return nil, err
	}
	var due []store.IPScanJob
	for _, j := range m.scans {
		if j.Status != store.ScanPending {
			continue
		}
		if j.NextRetryAt != nil && j.NextRetryAt.After(at) {
			continue
		}
		due = append(due, j)
	}
	sort.Slice(due, func(i, j int) bool {
		ki := dueKey(due[i])
		kj := dueKey(due[j])
		if !ki.Equal(kj) {
			return ki.Before(kj)
		}
		return due[i].ID < due[j].ID
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	t := now(m)
	out := make([]store.IPScanJob, 0, len(due))
	for _, j := range due {
		j.Status = store.ScanScanning
		started := t
		j.StartedAt = &started
		j.UpdatedAt = t
		m.scans[j.ID] = j
		out = append(out, j)
	}
	return out, nil
}

func dueKey(j store.IPScanJob) time.Time {
	if j.NextRetryAt != nil {
		return *j.NextRetryAt
	}
	return j.CreatedAt
}

func (m *Mem) ActiveScanForSubnet(_ context.Context, tenantID, subnetID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.scans {
		if j.TenantID == tenantID && j.SubnetID == subnetID &&
			(j.Status == store.ScanPending || j.Status == store.ScanScanning) {
			return true, nil
		}
	}
	return false, nil
}

// ---- dns config

func (m *Mem) GetDNSConfig(_ context.Context, tenantID string) (store.DNSConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.dns[tenantID]
	if !ok {
		return store.DNSConfig{}, repo.ErrNotFound
	}
	return c, nil
}

func (m *Mem) UpsertDNSConfig(_ context.Context, c store.DNSConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpsertDNSConfig"); err != nil {
		return err
	}
	t := now(m)
	if ex, ok := m.dns[c.TenantID]; ok {
		c.ID = ex.ID
		c.CreatedAt = ex.CreatedAt
	} else {
		if c.ID == "" {
			c.ID = store.NewID()
		}
		c.CreatedAt = t
	}
	if c.TimeoutMs == 0 {
		c.TimeoutMs = 5000
	}
	c.UpdatedAt = t
	m.dns[c.TenantID] = c
	return nil
}

// ---- statistics

func (m *Mem) TenantStats(_ context.Context, tenantID string) (repo.Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := repo.Stats{DevicesByType: map[string]int64{}}
	var totalCap, usedTotal int64
	for _, s := range m.subnets {
		if s.TenantID != tenantID {
			continue
		}
		st.TotalSubnets++
		cap := subnetTotal(s.CIDR)
		var used int64
		for _, a := range m.addrs {
			if a.TenantID == tenantID && a.SubnetID == s.ID {
				used++
			}
		}
		totalCap += cap
		usedTotal += used
	}
	st.TotalAddresses = totalCap
	st.UsedAddresses = usedTotal
	if totalCap > 0 {
		st.AvailableAddresses = totalCap - usedTotal
		st.OverallUtilization = float64(usedTotal) / float64(totalCap)
	}
	for _, v := range m.vlans {
		if v.TenantID == tenantID {
			st.TotalVlans++
		}
	}
	for _, d := range m.devices {
		if d.TenantID == tenantID {
			st.TotalDevices++
			st.DevicesByType[d.DeviceType]++
		}
	}
	for _, l := range m.locs {
		if l.TenantID == tenantID {
			st.TotalLocations++
		}
	}
	return st, nil
}

func (m *Mem) TenantIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for _, s := range m.subnets {
		set[s.TenantID] = true
	}
	for _, d := range m.devices {
		set[d.TenantID] = true
	}
	for _, v := range m.vlans {
		set[v.TenantID] = true
	}
	for _, l := range m.locs {
		set[l.TenantID] = true
	}
	for _, j := range m.scans {
		set[j.TenantID] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// ---- audit

func (m *Mem) AppendAudit(_ context.Context, row store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("AppendAudit"); err != nil {
		return err
	}
	if row.ID == "" {
		row.ID = store.NewID()
	}
	if row.At.IsZero() {
		row.At = now(m)
	}
	m.audit = append(m.audit, row)
	return nil
}

var _ repo.Store = (*Mem)(nil)
