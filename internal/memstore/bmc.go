package memstore

import (
	"context"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// SetDeviceBMCRef implements repo.Store (feature 024).
func (m *Mem) SetDeviceBMCRef(_ context.Context, tenantID, deviceID, ref string, audit store.AuditRow) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("SetDeviceBMCRef"); err != nil {
		return "", err
	}
	d, ok := m.devices[deviceID]
	if !ok || d.TenantID != tenantID {
		return "", repo.ErrNotFound
	}
	prev := d.IPMISecretRef
	d.IPMISecretRef = ref
	d.UpdatedAt = now(m)
	m.devices[deviceID] = d
	audit.TenantID = tenantID
	m.appendAuditLocked(audit)
	return prev, nil
}
