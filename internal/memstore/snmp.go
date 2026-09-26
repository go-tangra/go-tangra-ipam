package memstore

import (
	"context"
	"slices"
	"sort"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// GetSubnetSNMP implements repo.Store (feature 021).
func (m *Mem) GetSubnetSNMP(_ context.Context, tenantID, subnetID string) (store.SubnetSNMP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("GetSubnetSNMP"); err != nil {
		return store.SubnetSNMP{}, err
	}
	r, ok := m.snmp[subnetID]
	if !ok || r.TenantID != tenantID {
		return store.SubnetSNMP{}, repo.ErrNotFound
	}
	r.Sealed = slices.Clone(r.Sealed)
	return r, nil
}

// PutSubnetSNMP implements repo.Store: insert or replace the subnet's row and
// append its audit row atomically.
func (m *Mem) PutSubnetSNMP(_ context.Context, row store.SubnetSNMP, audit store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("PutSubnetSNMP"); err != nil {
		return err
	}
	s, ok := m.subnets[row.SubnetID]
	if !ok || s.TenantID != row.TenantID {
		return repo.ErrNotFound
	}
	row.Sealed = slices.Clone(row.Sealed)
	row.UpdatedAt = now(m)
	m.snmp[row.SubnetID] = row
	m.appendAuditLocked(audit)
	return nil
}

// DeleteSubnetSNMP implements repo.Store.
func (m *Mem) DeleteSubnetSNMP(_ context.Context, tenantID, subnetID string, audit store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("DeleteSubnetSNMP"); err != nil {
		return err
	}
	r, ok := m.snmp[subnetID]
	if !ok || r.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.snmp, subnetID)
	m.appendAuditLocked(audit)
	return nil
}

// ListSubnetSNMP implements repo.Store (metadata only).
func (m *Mem) ListSubnetSNMP(_ context.Context, tenantID string) ([]store.SubnetSNMP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("ListSubnetSNMP"); err != nil {
		return nil, err
	}
	var out []store.SubnetSNMP
	for _, r := range m.snmp {
		if r.TenantID == tenantID {
			r.Sealed = nil
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubnetID < out[j].SubnetID })
	return out, nil
}

// LegacySNMPRefCount implements repo.Store (system scope).
func (m *Mem) LegacySNMPRefCount(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("LegacySNMPRefCount"); err != nil {
		return 0, err
	}
	var n int64
	for _, s := range m.subnets {
		if s.SNMPSecretRef != "" {
			n++
		}
	}
	return n, nil
}
