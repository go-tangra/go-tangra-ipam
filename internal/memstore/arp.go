package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// GetARPSettings implements repo.ARPStore.
func (m *Mem) GetARPSettings(_ context.Context, tenantID string) (store.ARPSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("GetARPSettings"); err != nil {
		return store.ARPSettings{}, err
	}
	s, ok := m.arp[tenantID]
	if !ok {
		return store.DefaultARPSettings(tenantID), nil
	}
	s.ExcludedDevices = slices.Clone(s.ExcludedDevices)
	return s, nil
}

// PutARPSettings implements repo.ARPStore.
func (m *Mem) PutARPSettings(_ context.Context, s store.ARPSettings, audit store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("PutARPSettings"); err != nil {
		return err
	}
	s.ExcludedDevices = slices.Clone(s.ExcludedDevices)
	if s.ExcludedDevices == nil {
		s.ExcludedDevices = []string{}
	}
	s.UpdatedAt = now(m)
	m.arp[s.TenantID] = s
	audit.TenantID = s.TenantID
	m.appendAuditLocked(audit)
	return nil
}

// NetworkMACs implements repo.ARPStore.
func (m *Mem) NetworkMACs(_ context.Context, tenantID string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("NetworkMACs"); err != nil {
		return nil, err
	}
	return m.networkMACsLocked(tenantID), nil
}

func (m *Mem) networkMACsLocked(tenantID string) map[string]bool {
	out := map[string]bool{}
	for _, i := range m.ifaces {
		d, ok := m.devices[i.DeviceID]
		if i.TenantID != tenantID || !ok || !store.IsNetworkDevice(d.DeviceType) {
			continue
		}
		if mac, ok := hostreport.NormalizeMAC(i.MACAddress); ok {
			out[mac] = true
		}
	}
	return out
}

// ApplyARP implements repo.ARPStore with the same guards as the SQL store.
func (m *Mem) ApplyARP(_ context.Context, tenantID string, ops []store.ARPOp, summary []store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ApplyARP"); err != nil {
		return err
	}
	for _, op := range ops {
		if !m.applyARPOpLocked(tenantID, op) {
			continue
		}
		for _, row := range op.Audit {
			row.TenantID = tenantID
			m.appendAuditLocked(row)
		}
	}
	for _, row := range summary {
		row.TenantID = tenantID
		m.appendAuditLocked(row)
	}
	return nil
}

// arpOwned reports whether ARP data may write the address MAC: it is empty
// or was itself learned from ARP (agent, manual and pre-022 MACs are not).
func arpOwned(a store.IPAddress) bool { return a.MACAddress == "" || a.MACSource == store.MACSourceARP }

// applyARPOpLocked executes one op and reports whether its guard let it run.
func (m *Mem) applyARPOpLocked(tenantID string, op store.ARPOp) bool {
	at := op.At
	if op.Kind == store.ARPCreate {
		sub, ok := m.subnets[op.SubnetID]
		if _, _, dup := m.findAddrLocked(tenantID, op.Address); dup || !ok || sub.TenantID != tenantID {
			return false
		}
		if _, taken := m.addrs[op.AddressID]; taken {
			return false
		}
		m.addrs[op.AddressID] = store.IPAddress{ID: op.AddressID, TenantID: tenantID, Address: op.Address, SubnetID: op.SubnetID,
			MACAddress: op.MAC, Status: store.IPActive, AddressType: store.AddrHost, LastSeen: &at, CreatedBy: "arp",
			MACSource: store.MACSourceARP, MACSourceDeviceID: op.SourceDeviceID, MACSeenAt: &at, Origin: store.OriginARP,
			CreatedAt: now(m), UpdatedAt: now(m)}
		return true
	}
	a, ok := m.addrs[op.AddressID]
	if !ok || a.TenantID != tenantID {
		return false
	}
	switch op.Kind {
	case store.ARPFill, store.ARPUpdate:
		if !arpOwned(a) {
			return false
		}
		a.MACAddress, a.MACSource, a.MACSourceDeviceID, a.MACSeenAt, a.MACConflict = op.MAC, store.MACSourceARP, op.SourceDeviceID, &at, ""
	case store.ARPConflict:
		if arpOwned(a) {
			return false
		}
		a.MACConflict = op.MAC
	case store.ARPClearConflict:
		a.MACConflict, a.MACSeenAt = "", &at
	case store.ARPTouch:
		a.MACSeenAt = &at
		if a.MACSource == store.MACSourceARP {
			a.MACSourceDeviceID = op.SourceDeviceID
		}
	default:
		return false
	}
	a.UpdatedAt = now(m)
	m.addrs[a.ID] = a
	return true
}

// SetAddressLinks implements repo.PortLinkStore.
func (m *Mem) SetAddressLinks(_ context.Context, tenantID string, addrs []store.IPAddress, audit []store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("SetAddressLinks"); err != nil {
		return err
	}
	for _, a := range addrs {
		if cur, ok := m.addrs[a.ID]; !ok || cur.TenantID != tenantID {
			return repo.ErrNotFound
		}
	}
	for _, a := range addrs {
		cur := m.addrs[a.ID]
		cur.Link = cloneLink(a.Link)
		if cur.Link != nil {
			cur.Link.SwitchName = ""
		}
		cur.UpdatedAt = now(m)
		m.addrs[a.ID] = cur
	}
	for _, row := range audit {
		row.TenantID = tenantID
		m.appendAuditLocked(row)
	}
	return nil
}

func cloneLink(l *store.AddressLink) *store.AddressLink {
	if l == nil {
		return nil
	}
	c := *l
	return &c
}

// decorateAddrLocked returns a copy of a whose link carries the switch name.
func (m *Mem) decorateAddrLocked(a store.IPAddress) store.IPAddress {
	a.Link = cloneLink(a.Link)
	if a.Link != nil {
		if d, ok := m.devices[a.Link.SwitchID]; ok && d.TenantID == a.TenantID {
			a.Link.SwitchName = d.Name
		}
	}
	return a
}

// behindAddressesLocked lists the addresses linked to a switch port.
func (m *Mem) behindAddressesLocked(tenantID, portID string) []store.BehindAddress {
	var out []store.BehindAddress
	for _, a := range m.addrs {
		if a.TenantID == tenantID && a.Link != nil && a.Link.PortID == portID {
			out = append(out, store.BehindAddress{AddressID: a.ID, Address: a.Address, Hostname: a.Hostname})
		}
	}
	slices.SortFunc(out, func(x, y store.BehindAddress) int { return strings.Compare(x.Address, y.Address) })
	return out
}
