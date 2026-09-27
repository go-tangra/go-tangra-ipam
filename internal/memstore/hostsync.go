package memstore

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// defaultSettings mirrors the column defaults of ipam_hostsync_settings.
func (m *Mem) defaultSettings(tenantID string) store.HostSyncSettings {
	return store.HostSyncSettings{
		TenantID: tenantID, Enabled: true, FullIntervalMinutes: 60,
		ExcludedInterfaces: slices.Clone(hostreport.DefaultExclusions),
		Status:             store.HostSyncOK, UpdatedAt: now(m),
	}
}

func (m *Mem) ensureSettingsLocked(tenantID string) store.HostSyncSettings {
	s, ok := m.hsSettings[tenantID]
	if !ok {
		s = m.defaultSettings(tenantID)
		m.hsSettings[tenantID] = s
	}
	return s
}

func cloneSettings(s store.HostSyncSettings) store.HostSyncSettings {
	s.ExcludedInterfaces = slices.Clone(s.ExcludedInterfaces)
	return s
}

// EnsureHostSyncSettings implements repo.HostSyncStore.
func (m *Mem) EnsureHostSyncSettings(_ context.Context, tenantID string) (store.HostSyncSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("EnsureHostSyncSettings"); err != nil {
		return store.HostSyncSettings{}, err
	}
	return cloneSettings(m.ensureSettingsLocked(tenantID)), nil
}

// GetHostSyncSettings implements repo.HostSyncStore.
func (m *Mem) GetHostSyncSettings(_ context.Context, tenantID string) (store.HostSyncSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("GetHostSyncSettings"); err != nil {
		return store.HostSyncSettings{}, err
	}
	s, ok := m.hsSettings[tenantID]
	if !ok {
		return store.HostSyncSettings{}, repo.ErrNotFound
	}
	return cloneSettings(s), nil
}

// UpdateHostSyncSettings implements repo.HostSyncStore.
func (m *Mem) UpdateHostSyncSettings(_ context.Context, in store.HostSyncSettings, audit store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("UpdateHostSyncSettings"); err != nil {
		return err
	}
	cur := m.ensureSettingsLocked(in.TenantID)
	if in.Enabled && !cur.Enabled {
		cur.ReconcileRequested = true // re-enabling applies the latest reports
	}
	cur.Enabled, cur.FullIntervalMinutes = in.Enabled, in.FullIntervalMinutes
	cur.ExcludedInterfaces = slices.Clone(in.ExcludedInterfaces)
	cur.UpdatedBy, cur.UpdatedAt = in.UpdatedBy, now(m)
	if !cur.Enabled {
		cur.Status = store.HostSyncDisabled
	} else if cur.Status == store.HostSyncDisabled {
		cur.Status = store.HostSyncOK
	}
	m.hsSettings[in.TenantID] = cur
	m.appendAuditLocked(audit)
	return nil
}

// RequestReconcile implements repo.HostSyncStore.
func (m *Mem) RequestReconcile(_ context.Context, tenantID string, audit store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("RequestReconcile"); err != nil {
		return err
	}
	cur := m.ensureSettingsLocked(tenantID)
	cur.ReconcileRequested = true
	m.hsSettings[tenantID] = cur
	m.appendAuditLocked(audit)
	return nil
}

// ListHostSyncSettings implements repo.HostSyncStore (system scope).
func (m *Mem) ListHostSyncSettings(_ context.Context) ([]store.HostSyncSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ListHostSyncSettings"); err != nil {
		return nil, err
	}
	out := make([]store.HostSyncSettings, 0, len(m.hsSettings))
	for _, s := range m.hsSettings {
		out = append(out, cloneSettings(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// SaveHostSyncState implements repo.HostSyncStore.
func (m *Mem) SaveHostSyncState(_ context.Context, tenantID string, st store.HostSyncStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("SaveHostSyncState"); err != nil {
		return err
	}
	cur := m.ensureSettingsLocked(tenantID)
	cur.Status, cur.LastError = st.Status, st.LastError
	if st.ChangedSince != nil {
		cur.ChangedSince = st.ChangedSince
	}
	if st.LastPollAt != nil {
		cur.LastPollAt = st.LastPollAt
	}
	if st.LastReconcileAt != nil {
		cur.LastReconcileAt = st.LastReconcileAt
	}
	if st.ClearReconcile {
		cur.ReconcileRequested = false
	}
	cur.HostsReported, cur.HostsFailed = st.HostsReported, st.HostsFailed
	m.hsSettings[tenantID] = cur
	return nil
}

// HostDevices implements repo.HostSyncStore.
func (m *Mem) HostDevices(_ context.Context, tenantID string) ([]store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("HostDevices"); err != nil {
		return nil, err
	}
	var out []store.Device
	for _, d := range m.devices {
		if d.TenantID == tenantID && d.InventoryHostID != "" {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetHostSyncDeviceState implements repo.HostSyncStore.
func (m *Mem) GetHostSyncDeviceState(_ context.Context, tenantID, deviceID string) (store.HostSyncDeviceState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.devState[deviceID]
	if !ok || st.TenantID != tenantID {
		return store.HostSyncDeviceState{}, repo.ErrNotFound
	}
	st.Issues = slices.Clone(st.Issues)
	return st, nil
}

// HostSyncCounts implements repo.HostSyncStore.
func (m *Mem) HostSyncCounts(_ context.Context, tenantID string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("HostSyncCounts"); err != nil {
		return 0, 0, err
	}
	var nr, cf int64
	for _, d := range m.devices {
		if d.TenantID == tenantID && d.ReportState == store.RepNotReported {
			nr++
		}
	}
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.Conflict {
			cf++
		}
	}
	return nr, cf, nil
}

// ListGuests implements repo.HostSyncStore.
func (m *Mem) ListGuests(_ context.Context, tenantID, hostDeviceID string) ([]store.HypervisorGuest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ListGuests"); err != nil {
		return nil, err
	}
	out := m.guestsOfLocked(tenantID, hostDeviceID)
	for i := range out {
		if d, ok := m.devices[out[i].GuestDeviceID]; ok && d.TenantID == tenantID {
			out[i].GuestDeviceName = d.Name
		}
	}
	return out, nil
}

// ClearAddressConflict implements repo.HostSyncStore.
func (m *Mem) ClearAddressConflict(_ context.Context, tenantID, addressID string, audit store.AuditRow) (store.IPAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ClearAddressConflict"); err != nil {
		return store.IPAddress{}, err
	}
	a, ok := m.addrs[addressID]
	if !ok || a.TenantID != tenantID {
		return store.IPAddress{}, repo.ErrNotFound
	}
	a.Conflict, a.MoveCount, a.MoveWindowStart = false, 0, nil
	a.UpdatedAt = now(m)
	m.addrs[addressID] = a
	m.appendAuditLocked(audit)
	return a, nil
}

func (m *Mem) appendAuditLocked(row store.AuditRow) {
	if row.ID == "" {
		row.ID = store.NewID()
	}
	if row.At.IsZero() {
		row.At = now(m)
	}
	m.audit = append(m.audit, row)
}

// Audit returns a copy of the audit rows (tests).
func (m *Mem) Audit() []store.AuditRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.audit)
}

func (m *Mem) guestsOfLocked(tenantID, hostDeviceID string) []store.HypervisorGuest {
	var out []store.HypervisorGuest
	for _, g := range m.guests {
		if g.TenantID == tenantID && g.HostDeviceID == hostDeviceID {
			g.MACs = slices.Clone(g.MACs)
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GuestRef < out[j].GuestRef })
	return out
}

// deleteDeviceHostSyncLocked mirrors the FK actions of a device delete:
// its guest rows and device state cascade; other rows' references are nulled.
func (m *Mem) deleteDeviceHostSyncLocked(id string) {
	delete(m.devState, id)
	delete(m.hardware, id)
	for gid, g := range m.guests {
		switch {
		case g.HostDeviceID == id:
			delete(m.guests, gid)
		case g.GuestDeviceID == id:
			g.GuestDeviceID = ""
			m.guests[gid] = g
		}
	}
	for did, d := range m.devices {
		if d.HypervisorDeviceID == id {
			d.HypervisorDeviceID = ""
			m.devices[did] = d
		}
	}
}

// ---- apply (one host, all-or-nothing)

type memSnapshot struct {
	devices  map[string]store.Device
	ifaces   map[string]store.DeviceInterface
	addrs    map[string]store.IPAddress
	subnets  map[string]store.Subnet
	pkgs     map[string][]store.DevicePackage
	guests   map[string]store.HypervisorGuest
	devState map[string]store.HostSyncDeviceState
	hardware map[string]store.DeviceHardware
	audit    int
}

func (m *Mem) snapshotLocked() memSnapshot {
	return memSnapshot{
		devices: clone(m.devices), ifaces: clone(m.ifaces), addrs: clone(m.addrs), subnets: clone(m.subnets),
		pkgs: clone(m.pkgs), guests: clone(m.guests), devState: clone(m.devState), hardware: clone(m.hardware), audit: len(m.audit),
	}
}

func (m *Mem) restoreLocked(s memSnapshot) {
	m.devices, m.ifaces, m.addrs, m.subnets = s.devices, s.ifaces, s.addrs, s.subnets
	m.pkgs, m.guests, m.devState, m.audit = s.pkgs, s.guests, s.devState, m.audit[:s.audit]
	m.hardware = s.hardware
}

func clone[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ApplyHostReport implements repo.HostSyncStore: the whole callback runs under
// the store lock (the in-memory advisory lock); a disabled tenant or any error
// restores the state from before the call, audit rows included.
func (m *Mem) ApplyHostReport(_ context.Context, tenantID string, fn func(tx repo.HostTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ApplyHostReport"); err != nil {
		return err
	}
	if !m.ensureSettingsLocked(tenantID).Enabled {
		return repo.ErrSyncDisabled
	}
	snap := m.snapshotLocked()
	if err := fn(&memTx{m: m, tid: tenantID}); err != nil {
		m.restoreLocked(snap)
		return err
	}
	return nil
}

// memTx is the in-memory HostTx; its methods run with m.mu held.
type memTx struct {
	m   *Mem
	tid string
}

func (t *memTx) fail(method string) error { return t.m.fail("tx." + method) }

func (t *memTx) DeviceByInventoryHost(hostID string) (store.Device, bool, error) {
	if err := t.fail("DeviceByInventoryHost"); err != nil {
		return store.Device{}, false, err
	}
	for _, d := range t.m.devices {
		if d.TenantID == t.tid && d.InventoryHostID != "" && strings.EqualFold(d.InventoryHostID, hostID) {
			return d, true, nil
		}
	}
	return store.Device{}, false, nil
}

func (t *memTx) devicesWhere(pred func(store.Device) bool) []store.Device {
	var out []store.Device
	for _, d := range t.m.devices {
		if d.TenantID == t.tid && pred(d) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (t *memTx) DevicesBySerial(serial string) ([]store.Device, error) {
	serial = strings.TrimSpace(serial)
	return t.devicesWhere(func(d store.Device) bool {
		return serial != "" && strings.EqualFold(strings.TrimSpace(d.SerialNumber), serial)
	}), nil
}

func (t *memTx) DevicesByNames(names []string) ([]store.Device, error) {
	return t.devicesWhere(func(d store.Device) bool {
		for _, n := range names {
			if strings.EqualFold(d.Name, n) {
				return true
			}
		}
		return false
	}), nil
}

func (t *memTx) Interfaces(deviceID string) ([]store.DeviceInterface, error) {
	var out []store.DeviceInterface
	for _, i := range t.m.ifaces {
		if i.TenantID == t.tid && i.DeviceID == deviceID {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (t *memTx) addrsWhere(pred func(store.IPAddress) bool) []store.IPAddress {
	var out []store.IPAddress
	for _, a := range t.m.addrs {
		if a.TenantID == t.tid && pred(a) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func (t *memTx) AddressesByValue(addrs []string) ([]store.IPAddress, error) {
	return t.addrsWhere(func(a store.IPAddress) bool { return slices.Contains(addrs, a.Address) }), nil
}

func (t *memTx) AddressesOfDevice(deviceID string) ([]store.IPAddress, error) {
	return t.addrsWhere(func(a store.IPAddress) bool { return a.DeviceID == deviceID }), nil
}

func (t *memTx) Subnets() ([]store.Subnet, error) {
	var out []store.Subnet
	for _, s := range t.m.subnets {
		if s.TenantID == t.tid {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *memTx) Packages(deviceID string) ([]store.DevicePackage, error) {
	var out []store.DevicePackage
	for _, p := range t.m.pkgs[deviceID] {
		if p.TenantID == t.tid {
			out = append(out, p)
		}
	}
	return out, nil
}

func (t *memTx) Guests(hostDeviceID string) ([]store.HypervisorGuest, error) {
	return t.m.guestsOfLocked(t.tid, hostDeviceID), nil
}

func (t *memTx) DevicesByMAC(macs []string) ([]repo.MACOwner, error) {
	var out []repo.MACOwner
	for _, i := range t.m.ifaces {
		if i.TenantID != t.tid || i.MACAddress == "" || !slices.Contains(macs, strings.ToLower(i.MACAddress)) {
			continue
		}
		d := t.m.devices[i.DeviceID]
		out = append(out, repo.MACOwner{MAC: strings.ToLower(i.MACAddress), DeviceID: d.ID, HypervisorDeviceID: d.HypervisorDeviceID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MAC != out[j].MAC {
			return out[i].MAC < out[j].MAC
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out, nil
}

func (t *memTx) GuestRowsByMAC(macs []string) ([]store.HypervisorGuest, error) {
	var out []store.HypervisorGuest
	for _, g := range t.m.guests {
		if g.TenantID != t.tid {
			continue
		}
		for _, mac := range g.MACs {
			if slices.Contains(macs, mac) {
				g.MACs = slices.Clone(g.MACs)
				out = append(out, g)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t *memTx) GuestDevicesOf(hostDeviceID string) ([]store.Device, error) {
	return t.devicesWhere(func(d store.Device) bool { return d.HypervisorDeviceID == hostDeviceID }), nil
}

func (t *memTx) nameTaken(id, name string) bool {
	for _, d := range t.m.devices {
		if d.TenantID == t.tid && d.ID != id && d.Name == name {
			return true
		}
	}
	return false
}

func (t *memTx) InsertDevice(d store.Device) error {
	if err := t.fail("InsertDevice"); err != nil {
		return err
	}
	if t.nameTaken(d.ID, d.Name) {
		return repo.ErrConflict
	}
	for _, o := range t.m.devices {
		if o.TenantID == t.tid && d.InventoryHostID != "" && o.InventoryHostID == d.InventoryHostID {
			return repo.ErrConflict // partial UNIQUE (tenant_id, inventory_host_id)
		}
	}
	d.TenantID = t.tid
	ts := now(t.m)
	d.CreatedAt, d.UpdatedAt = ts, ts
	t.m.devices[d.ID] = d
	return nil
}

func (t *memTx) UpdateDeviceReported(d store.Device) error {
	if err := t.fail("UpdateDeviceReported"); err != nil {
		return err
	}
	cur, ok := t.m.devices[d.ID]
	if !ok || cur.TenantID != t.tid {
		return repo.ErrNotFound
	}
	if t.nameTaken(d.ID, d.Name) {
		return repo.ErrConflict
	}
	// Exactly the D8 column set; administrator fields keep their values.
	cur.Name, cur.DeviceType, cur.VirtualizationKind = d.Name, d.DeviceType, d.VirtualizationKind
	cur.OSType, cur.OSVersion, cur.Manufacturer, cur.Model, cur.SerialNumber = d.OSType, d.OSVersion, d.Manufacturer, d.Model, d.SerialNumber
	cur.PrimaryIP, cur.PrimaryIPv6, cur.ManagementIP = d.PrimaryIP, d.PrimaryIPv6, d.ManagementIP
	cur.RebootRequired, cur.UnattendedUpgrades, cur.UpdateStatus = d.RebootRequired, d.UnattendedUpgrades, d.UpdateStatus
	cur.LastSeen, cur.Source, cur.InventoryHostID, cur.ReportState = d.LastSeen, d.Source, d.InventoryHostID, d.ReportState
	cur.LastReportAt, cur.ReportDigest = d.LastReportAt, d.ReportDigest
	cur.UpdatedAt = now(t.m)
	t.m.devices[d.ID] = cur
	return nil
}

func (t *memTx) UpsertInterfaceReported(i store.DeviceInterface, create bool) error {
	if err := t.fail("UpsertInterfaceReported"); err != nil {
		return err
	}
	ts := now(t.m)
	if create {
		for _, ex := range t.m.ifaces {
			if ex.DeviceID == i.DeviceID && ex.Name == i.Name {
				return repo.ErrConflict
			}
		}
		i.TenantID, i.CreatedAt, i.UpdatedAt = t.tid, ts, ts
		t.m.ifaces[i.ID] = i
		return nil
	}
	cur, ok := t.m.ifaces[i.ID]
	if !ok || cur.TenantID != t.tid {
		return repo.ErrNotFound
	}
	cur.MACAddress, cur.InterfaceType, cur.SpeedMbps, cur.Enabled, cur.ReportState = i.MACAddress, i.InterfaceType, i.SpeedMbps, i.Enabled, i.ReportState
	cur.UpdatedAt = ts
	t.m.ifaces[i.ID] = cur
	return nil
}

func (t *memTx) CreateSubnetAuto(s store.Subnet) error {
	if err := t.fail("CreateSubnetAuto"); err != nil {
		return err
	}
	for _, ex := range t.m.subnets {
		if ex.TenantID == t.tid && ex.Name == s.Name {
			return repo.ErrConflict
		}
	}
	ts := now(t.m)
	s.TenantID, s.CreatedAt, s.UpdatedAt = t.tid, ts, ts
	t.m.subnets[s.ID] = s
	return nil
}

func (t *memTx) InsertAddressReported(a store.IPAddress) error {
	if err := t.fail("InsertAddressReported"); err != nil {
		return err
	}
	if _, _, dup := t.m.findAddrLocked(t.tid, a.Address); dup {
		return repo.ErrConflict
	}
	ts := now(t.m)
	a.TenantID, a.CreatedAt, a.UpdatedAt = t.tid, ts, ts
	a.MACSource, a.MACSourceDeviceID, a.MACSeenAt, a.MACConflict, a.Origin, a.Link = "", "", nil, "", "", nil
	if a.MACAddress != "" {
		a.MACSource, a.MACSeenAt = store.MACSourceAgent, &ts
	}
	t.m.addrs[a.ID] = a
	return nil
}

func (t *memTx) UpdateAddressReported(a store.IPAddress) error {
	if err := t.fail("UpdateAddressReported"); err != nil {
		return err
	}
	cur, ok := t.m.addrs[a.ID]
	if !ok || cur.TenantID != t.tid {
		return repo.ErrNotFound
	}
	cur.DeviceID, cur.InterfaceName, cur.MACAddress, cur.Hostname, cur.IsPrimary = a.DeviceID, a.InterfaceName, a.MACAddress, a.Hostname, a.IsPrimary
	cur.LastSeen, cur.ReportState, cur.PreviousDeviceID, cur.MovedAt = a.LastSeen, a.ReportState, a.PreviousDeviceID, a.MovedAt
	cur.MoveCount, cur.MoveWindowStart, cur.Conflict = a.MoveCount, a.MoveWindowStart, a.Conflict
	cur.UpdatedAt = now(t.m)
	if a.ReportState == store.RepReported {
		// 022: a reported MAC is the agent's (none: no source).
		cur.MACSource, cur.MACSourceDeviceID, cur.MACSeenAt = "", "", nil
		if a.MACAddress != "" {
			ts := cur.UpdatedAt
			cur.MACSource, cur.MACSeenAt = store.MACSourceAgent, &ts
		}
	}
	t.m.addrs[a.ID] = cur
	return nil
}

func (t *memTx) ReplacePendingPackages(deviceID string, pkgs []store.DevicePackage) error {
	if err := t.fail("ReplacePendingPackages"); err != nil {
		return err
	}
	ts := now(t.m)
	cp := make([]store.DevicePackage, 0, len(pkgs))
	for _, p := range pkgs {
		p.TenantID, p.DeviceID, p.CreatedAt, p.UpdatedAt = t.tid, deviceID, ts, ts
		if p.ID == "" {
			p.ID = store.NewID()
		}
		cp = append(cp, p)
	}
	t.m.pkgs[deviceID] = cp
	return nil
}

func (t *memTx) ReplaceGuests(hostDeviceID string, guests []store.HypervisorGuest) error {
	if err := t.fail("ReplaceGuests"); err != nil {
		return err
	}
	for id, g := range t.m.guests {
		if g.TenantID == t.tid && g.HostDeviceID == hostDeviceID {
			delete(t.m.guests, id)
		}
	}
	for _, g := range guests {
		g.TenantID, g.HostDeviceID, g.MACs = t.tid, hostDeviceID, slices.Clone(g.MACs)
		t.m.guests[g.ID] = g
	}
	return nil
}

func (t *memTx) SetHypervisor(deviceID, hypervisorID string) error {
	if err := t.fail("SetHypervisor"); err != nil {
		return err
	}
	d, ok := t.m.devices[deviceID]
	if !ok || d.TenantID != t.tid || deviceID == hypervisorID {
		return repo.ErrNotFound
	}
	d.HypervisorDeviceID = hypervisorID
	t.m.devices[deviceID] = d
	return nil
}

func (t *memTx) SetGuestDevice(guestRowID, deviceID string) error {
	if err := t.fail("SetGuestDevice"); err != nil {
		return err
	}
	g, ok := t.m.guests[guestRowID]
	if !ok || g.TenantID != t.tid {
		return repo.ErrNotFound
	}
	g.GuestDeviceID = deviceID
	t.m.guests[guestRowID] = g
	return nil
}

func (t *memTx) MarkDeviceNotReported(deviceID string) error {
	if err := t.fail("MarkDeviceNotReported"); err != nil {
		return err
	}
	d, ok := t.m.devices[deviceID]
	if !ok || d.TenantID != t.tid {
		return repo.ErrNotFound
	}
	d.ReportState = store.RepNotReported
	t.m.devices[deviceID] = d
	for id, i := range t.m.ifaces {
		if i.DeviceID == deviceID && i.ReportState == store.RepReported {
			i.ReportState = store.RepNotReported
			t.m.ifaces[id] = i
		}
	}
	for id, a := range t.m.addrs {
		if a.TenantID == t.tid && a.DeviceID == deviceID && a.ReportState == store.RepReported {
			a.ReportState = store.RepNotReported
			t.m.addrs[id] = a
		}
	}
	return nil
}

func (t *memTx) SaveDeviceState(st store.HostSyncDeviceState) error {
	if err := t.fail("SaveDeviceState"); err != nil {
		return err
	}
	st.TenantID = t.tid
	st.Issues = slices.Clone(st.Issues)
	t.m.devState[st.DeviceID] = st
	return nil
}

func (t *memTx) AppendAudit(row store.AuditRow) error {
	if err := t.fail("AppendAudit"); err != nil {
		return err
	}
	row.TenantID = t.tid
	t.m.appendAuditLocked(row)
	return nil
}

// GetHardware implements repo.HostTx.
func (t *memTx) GetHardware(deviceID string) (*store.DeviceHardware, error) {
	if err := t.fail("GetHardware"); err != nil {
		return nil, err
	}
	h, ok := t.m.hardware[deviceID]
	if !ok || h.TenantID != t.tid {
		return nil, nil
	}
	return &h, nil
}

// ReplaceHardware implements repo.HostTx.
func (t *memTx) ReplaceHardware(h store.DeviceHardware) error {
	if err := t.fail("ReplaceHardware"); err != nil {
		return err
	}
	if d, ok := t.m.devices[h.DeviceID]; !ok || d.TenantID != t.tid {
		return repo.ErrNotFound
	}
	h.TenantID, h.UpdatedAt = t.tid, now(t.m)
	t.m.hardware[h.DeviceID] = h
	return nil
}

// GetDeviceHardware implements repo.Store.
func (m *Mem) GetDeviceHardware(_ context.Context, tenantID, deviceID string) (store.DeviceHardware, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("GetDeviceHardware"); err != nil {
		return store.DeviceHardware{}, err
	}
	h, ok := m.hardware[deviceID]
	if !ok || h.TenantID != tenantID {
		return store.DeviceHardware{}, repo.ErrNotFound
	}
	return h, nil
}

var _ repo.HostTx = (*memTx)(nil)
