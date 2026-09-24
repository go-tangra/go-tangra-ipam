// Package devices is the IPAM device service: CRUD over a tenant's managed
// network devices/hosts, their NICs (interfaces) and their OS package inventory,
// plus the list of addresses bound to a device.
//
// A device name is unique within a tenant, so a duplicate surfaces as ErrConflict;
// deleting a device that still has addresses bound to it requires force, else
// ErrNotEmpty. Package sync replaces a device's whole package set in one shot and
// reports the total/updatable/security-update counts back to the caller.
// Authorization is coarse (the caller is scoped to its tenant); the concrete
// store enforces per-tenant RLS.
package devices

import (
	"context"
	"errors"
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/authz"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Sentinel errors masking the store's equivalents.
var (
	ErrNotFound = errors.New("devices: not found")
	ErrConflict = errors.New("devices: conflict")
	ErrNotEmpty = errors.New("devices: not empty")
)

// SyncResult reports the outcome of a package sync: the total package count and
// how many of them are updatable / carry a security update.
type SyncResult struct {
	Total    int `json:"total"`
	Updates  int `json:"updates"`
	Security int `json:"security"`
}

// Service manages devices, their interfaces and their packages.
type Service struct {
	st  repo.Store
	now func() time.Time
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st, now: time.Now} }

// SetClock injects the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Create inserts a device in the caller's tenant. A duplicate name is ErrConflict.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, in store.Device) (store.Device, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Device{}, err
	}
	in.TenantID = subj.TenantID
	if in.ID == "" {
		in.ID = store.NewID()
	}
	if in.Status == "" {
		in.Status = store.DevStActive
	}
	if in.DeviceType == "" {
		in.DeviceType = store.DevOther
	}
	if in.CreatedBy == "" {
		in.CreatedBy = subj.ActorID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.now()
	}
	if err := s.st.CreateDevice(ctx, in); err != nil {
		return store.Device{}, mapErr(err)
	}
	return s.Get(ctx, subj, in.ID)
}

// Get returns one device with its computed interface/address/package counts.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.Device, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Device{}, err
	}
	d, err := s.st.GetDevice(ctx, subj.TenantID, id)
	if err != nil {
		return store.Device{}, mapErr(err)
	}
	return d, nil
}

// List returns the caller's devices matching f.
func (s *Service) List(ctx context.Context, subj authz.Subjects, f store.DeviceFilter) ([]store.Device, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListDevices(ctx, subj.TenantID, f)
}

// Update replaces a device. Empty fields are inherited from the stored row;
// created-by/created-at are preserved.
func (s *Service) Update(ctx context.Context, subj authz.Subjects, in store.Device) (store.Device, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.Device{}, err
	}
	in.TenantID = subj.TenantID
	ex, err := s.st.GetDevice(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.Device{}, mapErr(err)
	}
	if in.Name == "" {
		in.Name = ex.Name
	}
	if in.Status == "" {
		in.Status = ex.Status
	}
	if in.DeviceType == "" {
		in.DeviceType = ex.DeviceType
	}
	in.CreatedBy = ex.CreatedBy
	in.CreatedAt = ex.CreatedAt
	if err := s.st.UpdateDevice(ctx, in); err != nil {
		return store.Device{}, mapErr(err)
	}
	return s.Get(ctx, subj, in.ID)
}

// Delete removes a device. If addresses are still bound to it, force is required;
// otherwise ErrNotEmpty. The address guard is explicit here.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string, force bool) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	if !force {
		addrs, err := s.st.AddressesForDevice(ctx, subj.TenantID, id)
		if err != nil {
			return err
		}
		if len(addrs) > 0 {
			return ErrNotEmpty
		}
	}
	return mapErr(s.st.DeleteDevice(ctx, subj.TenantID, id, force))
}

// GetAddresses returns the addresses bound to a device (sealed owner cleared).
func (s *Service) GetAddresses(ctx context.Context, subj authz.Subjects, deviceID string) ([]store.IPAddress, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	rows, err := s.st.AddressesForDevice(ctx, subj.TenantID, deviceID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Owner = ""
	}
	return rows, nil
}

// CreateInterface adds a NIC to a device. A duplicate interface name on the same
// device is ErrConflict.
func (s *Service) CreateInterface(ctx context.Context, subj authz.Subjects, deviceID string, in store.DeviceInterface) (store.DeviceInterface, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return store.DeviceInterface{}, err
	}
	if _, err := s.st.GetDevice(ctx, subj.TenantID, deviceID); err != nil {
		return store.DeviceInterface{}, mapErr(err)
	}
	in.TenantID = subj.TenantID
	in.DeviceID = deviceID
	if in.ID == "" {
		in.ID = store.NewID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = s.now()
	}
	if err := s.st.CreateInterface(ctx, in); err != nil {
		return store.DeviceInterface{}, mapErr(err)
	}
	i, err := s.st.GetInterface(ctx, subj.TenantID, in.ID)
	if err != nil {
		return store.DeviceInterface{}, mapErr(err)
	}
	return i, nil
}

// ListInterfaces returns a device's NICs.
func (s *Service) ListInterfaces(ctx context.Context, subj authz.Subjects, deviceID string) ([]store.DeviceInterface, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListInterfaces(ctx, subj.TenantID, deviceID)
}

// DeleteInterface removes a NIC by id.
func (s *Service) DeleteInterface(ctx context.Context, subj authz.Subjects, id string) error {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return err
	}
	return mapErr(s.st.DeleteInterface(ctx, subj.TenantID, id))
}

// SyncPackages replaces a device's whole package set and reports the counts.
func (s *Service) SyncPackages(ctx context.Context, subj authz.Subjects, deviceID string, pkgs []store.DevicePackage) (SyncResult, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return SyncResult{}, err
	}
	if _, err := s.st.GetDevice(ctx, subj.TenantID, deviceID); err != nil {
		return SyncResult{}, mapErr(err)
	}
	if err := s.st.ReplaceDevicePackages(ctx, subj.TenantID, deviceID, pkgs); err != nil {
		return SyncResult{}, mapErr(err)
	}
	res := SyncResult{Total: len(pkgs)}
	for _, p := range pkgs {
		if p.NeedsUpdate {
			res.Updates++
		}
		if p.IsSecurityUpdate {
			res.Security++
		}
	}
	return res, nil
}

// ListPackages returns a device's packages, filtered by the optional
// needs-update / security-only flags and package manager.
func (s *Service) ListPackages(ctx context.Context, subj authz.Subjects, deviceID string, needsUpdate, securityOnly *bool, manager string) ([]store.DevicePackage, error) {
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return nil, err
	}
	return s.st.ListDevicePackages(ctx, subj.TenantID, deviceID, needsUpdate, securityOnly, manager)
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
	case errors.Is(err, repo.ErrNotEmpty):
		return ErrNotEmpty
	default:
		return err
	}
}
