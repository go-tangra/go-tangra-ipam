// Package backup exports and imports a tenant's IPAM data (subnets, addresses,
// devices, vlans, locations, and ip/host groups with their members) for backup
// or tenant migration. Secrets are NEVER exported: the SNMP and IPMI credential
// references and the sealed contact/owner PII fields are stripped from the
// export, so a restored tenant carries only non-sensitive inventory. Entity ids
// are preserved on import so cross-references (parent/vlan/location/device) stay
// valid. Duplicate handling is per id: skip or overwrite.
package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

// SchemaVersion is the export format version.
const SchemaVersion = 1

// Import modes.
const (
	ModeSkip      = "skip"
	ModeOverwrite = "overwrite"
)

// ErrBadSchema is returned when importing an unsupported schema version.
var ErrBadSchema = errors.New("backup: unsupported schema version")

// pageSize bounds each keyset page while collecting a tenant's rows.
const pageSize = 500

// IPGroupExport is an IP group together with its members.
type IPGroupExport struct {
	Group   store.IPGroup         `json:"group"`
	Members []store.IPGroupMember `json:"members,omitempty"`
}

// HostGroupExport is a host group together with its members.
type HostGroupExport struct {
	Group   store.HostGroup         `json:"group"`
	Members []store.HostGroupMember `json:"members,omitempty"`
}

// Backup is the export document. Every secret reference and sealed PII field has
// been cleared from the contained rows.
type Backup struct {
	SchemaVersion int               `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	Subnets       []store.Subnet    `json:"subnets,omitempty"`
	Addresses     []store.IPAddress `json:"addresses,omitempty"`
	Devices       []store.Device    `json:"devices,omitempty"`
	Vlans         []store.Vlan      `json:"vlans,omitempty"`
	Locations     []store.Location  `json:"locations,omitempty"`
	IPGroups      []IPGroupExport   `json:"ip_groups,omitempty"`
	HostGroups    []HostGroupExport `json:"host_groups,omitempty"`
}

// Result reports what an import did.
type Result struct {
	SubnetsImported    int `json:"subnets_imported"`
	SubnetsSkipped     int `json:"subnets_skipped"`
	AddressesImported  int `json:"addresses_imported"`
	AddressesSkipped   int `json:"addresses_skipped"`
	DevicesImported    int `json:"devices_imported"`
	DevicesSkipped     int `json:"devices_skipped"`
	VlansImported      int `json:"vlans_imported"`
	VlansSkipped       int `json:"vlans_skipped"`
	LocationsImported  int `json:"locations_imported"`
	LocationsSkipped   int `json:"locations_skipped"`
	IPGroupsImported   int `json:"ip_groups_imported"`
	IPGroupsSkipped    int `json:"ip_groups_skipped"`
	HostGroupsImported int `json:"host_groups_imported"`
	HostGroupsSkipped  int `json:"host_groups_skipped"`
}

// Service exports and imports tenant data.
type Service struct {
	st  repo.Store
	now func() time.Time
}

// New builds the service.
func New(st repo.Store) *Service { return &Service{st: st, now: time.Now} }

// SetClock injects the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Export builds a backup of the caller's tenant. includeSecrets is accepted for
// interface symmetry but has no effect: secrets are never exported regardless of
// its value. SNMP/IPMI credential references and sealed contact/owner PII are
// stripped from every row.
func (s *Service) Export(ctx context.Context, subj authz.Subjects, includeSecrets bool) (Backup, error) {
	_ = includeSecrets // secrets are never exported; parameter kept for symmetry.
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return Backup{}, err
	}
	tenant := subj.TenantID
	b := Backup{SchemaVersion: SchemaVersion, ExportedAt: s.now().UTC()}

	subnets, err := s.st.AllSubnetCIDRs(ctx, tenant)
	if err != nil {
		return Backup{}, err
	}
	for _, sn := range subnets {
		b.Subnets = append(b.Subnets, sanitizeSubnet(sn))
	}

	// Addresses (page through every subnet's rows via the address filter).
	var addrCursor string
	for {
		page, err := s.st.ListAddresses(ctx, tenant, store.AddressFilter{Limit: pageSize, CursorID: addrCursor})
		if err != nil {
			return Backup{}, err
		}
		for _, a := range page {
			b.Addresses = append(b.Addresses, sanitizeAddress(a))
		}
		if len(page) < pageSize {
			break
		}
		addrCursor = page[len(page)-1].ID
	}

	var devCursor string
	for {
		page, err := s.st.ListDevices(ctx, tenant, store.DeviceFilter{Limit: pageSize, CursorID: devCursor})
		if err != nil {
			return Backup{}, err
		}
		for _, d := range page {
			b.Devices = append(b.Devices, sanitizeDevice(d))
		}
		if len(page) < pageSize {
			break
		}
		devCursor = page[len(page)-1].ID
	}

	var vlanCursor string
	for {
		page, err := s.st.ListVlans(ctx, tenant, store.VlanFilter{Limit: pageSize, CursorID: vlanCursor})
		if err != nil {
			return Backup{}, err
		}
		b.Vlans = append(b.Vlans, page...)
		if len(page) < pageSize {
			break
		}
		vlanCursor = page[len(page)-1].ID
	}

	var locCursor string
	for {
		page, err := s.st.ListLocations(ctx, tenant, store.LocationFilter{Limit: pageSize, CursorID: locCursor})
		if err != nil {
			return Backup{}, err
		}
		for _, l := range page {
			b.Locations = append(b.Locations, sanitizeLocation(l))
		}
		if len(page) < pageSize {
			break
		}
		locCursor = page[len(page)-1].ID
	}

	ipGroups, ipMembers, err := s.st.AllIPGroupsWithMembers(ctx, tenant, nil)
	if err != nil {
		return Backup{}, err
	}
	for _, g := range ipGroups {
		b.IPGroups = append(b.IPGroups, IPGroupExport{Group: g, Members: ipMembers[g.ID]})
	}

	var hgCursor string
	for {
		page, err := s.st.ListHostGroups(ctx, tenant, pageSize, hgCursor)
		if err != nil {
			return Backup{}, err
		}
		for _, g := range page {
			members, err := s.st.ListHostGroupMembers(ctx, tenant, g.ID)
			if err != nil {
				return Backup{}, err
			}
			b.HostGroups = append(b.HostGroups, HostGroupExport{Group: g, Members: members})
		}
		if len(page) < pageSize {
			break
		}
		hgCursor = page[len(page)-1].ID
	}

	return b, nil
}

// Import recreates the backup's entities in the caller's tenant, preserving ids.
// mode is skip (default) or overwrite for id collisions. An unsupported schema
// version returns ErrBadSchema. Locations, vlans and subnets are imported before
// the rows that reference them.
func (s *Service) Import(ctx context.Context, subj authz.Subjects, b Backup, mode string) (Result, error) {
	var res Result
	if err := authz.RequireTenant(subj, subj.TenantID); err != nil {
		return res, err
	}
	if b.SchemaVersion != SchemaVersion {
		return res, fmt.Errorf("%w: %d", ErrBadSchema, b.SchemaVersion)
	}
	if mode != ModeOverwrite {
		mode = ModeSkip
	}
	tenant := subj.TenantID

	for _, l := range b.Locations {
		l.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetLocation(ctx, tenant, l.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.LocationsSkipped++
				continue
			}
			if err := s.st.DeleteLocation(ctx, tenant, l.ID, true); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateLocation(ctx, l); err != nil {
			return res, err
		}
		res.LocationsImported++
	}

	for _, v := range b.Vlans {
		v.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetVlan(ctx, tenant, v.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.VlansSkipped++
				continue
			}
			if err := s.st.DeleteVlan(ctx, tenant, v.ID, true); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateVlan(ctx, v); err != nil {
			return res, err
		}
		res.VlansImported++
	}

	for _, sn := range b.Subnets {
		sn.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetSubnet(ctx, tenant, sn.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.SubnetsSkipped++
				continue
			}
			if err := s.st.DeleteSubnet(ctx, tenant, sn.ID, true); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateSubnet(ctx, sn); err != nil {
			return res, err
		}
		res.SubnetsImported++
	}

	for _, d := range b.Devices {
		d.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetDevice(ctx, tenant, d.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.DevicesSkipped++
				continue
			}
			if err := s.st.DeleteDevice(ctx, tenant, d.ID, true); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateDevice(ctx, d); err != nil {
			return res, err
		}
		res.DevicesImported++
	}

	for _, a := range b.Addresses {
		a.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetAddress(ctx, tenant, a.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.AddressesSkipped++
				continue
			}
			if err := s.st.DeleteAddress(ctx, tenant, a.ID); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateAddress(ctx, a); err != nil {
			return res, err
		}
		res.AddressesImported++
	}

	for _, ge := range b.IPGroups {
		g := ge.Group
		g.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetIPGroup(ctx, tenant, g.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.IPGroupsSkipped++
				continue
			}
			if err := s.st.DeleteIPGroup(ctx, tenant, g.ID); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateIPGroup(ctx, g); err != nil {
			return res, err
		}
		for _, m := range ge.Members {
			m.TenantID = tenant
			m.IPGroupID = g.ID
			if err := s.st.AddIPGroupMember(ctx, m); err != nil {
				return res, err
			}
		}
		res.IPGroupsImported++
	}

	for _, ge := range b.HostGroups {
		g := ge.Group
		g.TenantID = tenant
		exists, err := s.exists(func() error { _, e := s.st.GetHostGroup(ctx, tenant, g.ID); return e })
		if err != nil {
			return res, err
		}
		if exists {
			if mode == ModeSkip {
				res.HostGroupsSkipped++
				continue
			}
			if err := s.st.DeleteHostGroup(ctx, tenant, g.ID); err != nil {
				return res, err
			}
		}
		if err := s.st.CreateHostGroup(ctx, g); err != nil {
			return res, err
		}
		for _, m := range ge.Members {
			m.TenantID = tenant
			m.HostGroupID = g.ID
			if err := s.st.AddHostGroupMember(ctx, m); err != nil {
				return res, err
			}
		}
		res.HostGroupsImported++
	}

	return res, nil
}

// exists reports whether a Get returned a row; repo.ErrNotFound means "no", any
// other error is propagated.
func (s *Service) exists(get func() error) (bool, error) {
	err := get()
	if err == nil {
		return true, nil
	}
	if errors.Is(err, repo.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// ---- sanitizers: clear every secret reference and sealed PII field.

func sanitizeSubnet(s store.Subnet) store.Subnet {
	s.SNMPSecretRef = ""
	return s
}

func sanitizeAddress(a store.IPAddress) store.IPAddress {
	a.Owner = "" // sealed/redacted
	return a
}

func sanitizeDevice(d store.Device) store.Device {
	d.IPMISecretRef = ""
	d.Contact = "" // sealed/redacted
	return d
}

func sanitizeLocation(l store.Location) store.Location {
	l.Contact = "" // sealed/redacted
	l.Phone = ""   // sealed/redacted
	l.Email = ""   // sealed/redacted
	return l
}
