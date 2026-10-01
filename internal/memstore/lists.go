package memstore

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// The paged lists (repo.PagedLists) reuse the keyset lists for filtering and
// computed fields, then sort and window the matches with listquery using the
// same public sort fields as the SQL Specs (store.*List).

// failPage reports an injected failure for a paged method.
func (m *Mem) failPage(method string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fail(method)
}

// pageOf sorts all by the request (completed with spec's defaults) and returns
// the requested page, the total and the applied request.
func pageOf[T any](all []T, spec listquery.Spec, req listquery.Request, key func(T, string) any, id func(T) string) ([]T, int, listquery.Request) {
	req = store.ListRequest(req, spec)
	listquery.SortSlice(all, req, key, id)
	page, total, applied := listquery.Window(all, req)
	out := make([]T, len(page))
	copy(out, page)
	return out, total, applied
}

// inetKey is a sortable form of an address or prefix in inet order (family,
// address, prefix length); nil for a value that does not parse (sorted last,
// like the SQL expression).
func inetKey(s string) any {
	var addr netip.Addr
	bits := -1
	if p, err := netip.ParsePrefix(s); err == nil {
		addr, bits = p.Addr(), p.Bits()
	} else if a, err := netip.ParseAddr(s); err == nil {
		addr = a
	} else {
		return nil
	}
	addr = addr.WithZone("")
	if bits < 0 {
		bits = addr.BitLen()
	}
	family := "6"
	if addr.Is4() {
		family = "4"
	}
	b := addr.As16()
	return fmt.Sprintf("%s%x%03d", family, b[:], bits)
}

// optional returns nil for an empty id (SQL NULL) so it sorts last.
func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PageSubnets implements repo.PagedLists.
func (m *Mem) PageSubnets(ctx context.Context, tenantID string, f store.SubnetFilter, req listquery.Request) ([]store.Subnet, int, listquery.Request, error) {
	if err := m.failPage("PageSubnets"); err != nil {
		return nil, 0, req, err
	}
	f.CursorID, f.Limit = "", 0
	all, err := m.ListSubnets(ctx, tenantID, f)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.SubnetList, req, func(s store.Subnet, field string) any {
		switch field {
		case "name":
			return s.Name
		case "vlan":
			return optional(s.VlanID)
		case "location":
			return optional(s.LocationID)
		case "status":
			return s.Status
		default:
			return inetKey(s.CIDR)
		}
	}, func(s store.Subnet) string { return s.ID })
	return out, total, applied, nil
}

// addressKey is the value of a store.AddressList sort field.
func addressKey(a store.IPAddress, field string) any {
	switch field {
	case "hostname":
		return a.Hostname
	case "mac":
		return a.MACAddress
	case "status":
		return a.Status
	case "address_type":
		return a.AddressType
	case "last_seen":
		if a.LastSeen == nil {
			return nil
		}
		return *a.LastSeen
	case "created_at":
		return a.CreatedAt
	default:
		return inetKey(a.Address)
	}
}

// PageAddresses implements repo.PagedLists.
func (m *Mem) PageAddresses(ctx context.Context, tenantID string, f store.AddressFilter, req listquery.Request) ([]store.IPAddress, int, listquery.Request, error) {
	if err := m.failPage("PageAddresses"); err != nil {
		return nil, 0, req, err
	}
	f.CursorID, f.Limit = "", 0
	all, err := m.ListAddresses(ctx, tenantID, f)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.AddressList, req, addressKey, func(a store.IPAddress) string { return a.ID })
	return out, total, applied, nil
}

// PageDevices implements repo.PagedLists.
func (m *Mem) PageDevices(ctx context.Context, tenantID string, f store.DeviceFilter, req listquery.Request) ([]store.Device, int, listquery.Request, error) {
	if err := m.failPage("PageDevices"); err != nil {
		return nil, 0, req, err
	}
	f.CursorID, f.Limit = "", 0
	all, err := m.ListDevices(ctx, tenantID, f)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.DeviceList, req, func(d store.Device, field string) any {
		switch field {
		case "device_type":
			return d.DeviceType
		case "status":
			return d.Status
		case "manufacturer":
			return d.Manufacturer
		case "location":
			return optional(d.LocationID)
		case "created_at":
			return d.CreatedAt
		default:
			return d.Name
		}
	}, func(d store.Device) string { return d.ID })
	return out, total, applied, nil
}

// PageVlans implements repo.PagedLists.
func (m *Mem) PageVlans(ctx context.Context, tenantID string, f store.VlanFilter, req listquery.Request) ([]store.Vlan, int, listquery.Request, error) {
	if err := m.failPage("PageVlans"); err != nil {
		return nil, 0, req, err
	}
	f.CursorID, f.Limit = "", 0
	all, err := m.ListVlans(ctx, tenantID, f)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.VlanList, req, func(v store.Vlan, field string) any {
		switch field {
		case "name":
			return v.Name
		case "domain":
			return v.Domain
		case "status":
			return v.Status
		default:
			return int64(v.VlanID)
		}
	}, func(v store.Vlan) string { return v.ID })
	return out, total, applied, nil
}

// PageScanJobs implements repo.PagedLists.
func (m *Mem) PageScanJobs(ctx context.Context, tenantID string, f store.ScanFilter, req listquery.Request) ([]store.IPScanJob, int, listquery.Request, error) {
	if err := m.failPage("PageScanJobs"); err != nil {
		return nil, 0, req, err
	}
	f.CursorID, f.Limit = "", 0
	all, err := m.ListScanJobs(ctx, tenantID, f)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.ScanList, req, func(j store.IPScanJob, field string) any {
		switch field {
		case "status":
			return j.Status
		case "subnet":
			return optional(j.SubnetID)
		default:
			return j.CreatedAt
		}
	}, func(j store.IPScanJob) string { return j.ID })
	return out, total, applied, nil
}

// PageIPGroupMembers implements repo.PagedLists.
func (m *Mem) PageIPGroupMembers(ctx context.Context, tenantID, groupID string, req listquery.Request) ([]store.IPGroupMember, int, listquery.Request, error) {
	if err := m.failPage("PageIPGroupMembers"); err != nil {
		return nil, 0, req, err
	}
	all, err := m.ListIPGroupMembers(ctx, tenantID, groupID)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.IPMemberList, req, func(g store.IPGroupMember, field string) any {
		if field == "name" {
			return g.Value
		}
		return int64(g.Sequence)
	}, func(g store.IPGroupMember) string { return g.ID })
	return out, total, applied, nil
}

// PageHostGroupMembers implements repo.PagedLists.
func (m *Mem) PageHostGroupMembers(ctx context.Context, tenantID, groupID string, req listquery.Request) ([]store.HostGroupMember, int, listquery.Request, error) {
	if err := m.failPage("PageHostGroupMembers"); err != nil {
		return nil, 0, req, err
	}
	all, err := m.ListHostGroupMembers(ctx, tenantID, groupID)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.HostMemberList, req, func(g store.HostGroupMember, field string) any {
		if field == "name" {
			return g.DeviceName
		}
		return int64(g.Sequence)
	}, func(g store.HostGroupMember) string { return g.ID })
	return out, total, applied, nil
}

// PageInterfaces implements repo.PagedLists.
func (m *Mem) PageInterfaces(ctx context.Context, tenantID, deviceID string, req listquery.Request) ([]store.DeviceInterface, int, listquery.Request, error) {
	if err := m.failPage("PageInterfaces"); err != nil {
		return nil, 0, req, err
	}
	all, err := m.ListInterfaces(ctx, tenantID, deviceID)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.InterfaceList, req, func(i store.DeviceInterface, _ string) any { return i.Name },
		func(i store.DeviceInterface) string { return i.ID })
	return out, total, applied, nil
}

// PageDevicePackages implements repo.PagedLists.
func (m *Mem) PageDevicePackages(ctx context.Context, tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string, req listquery.Request) ([]store.DevicePackage, int, listquery.Request, error) {
	if err := m.failPage("PageDevicePackages"); err != nil {
		return nil, 0, req, err
	}
	all, err := m.ListDevicePackages(ctx, tenantID, deviceID, needsUpdate, securityOnly, manager)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.PackageList, req, func(p store.DevicePackage, field string) any {
		if field == "version" {
			return p.CurrentVersion
		}
		return p.Name
	}, func(p store.DevicePackage) string { return p.ID })
	return out, total, applied, nil
}

// PageGuests implements repo.PagedLists.
func (m *Mem) PageGuests(ctx context.Context, tenantID, hostDeviceID string, req listquery.Request) ([]store.HypervisorGuest, int, listquery.Request, error) {
	if err := m.failPage("PageGuests"); err != nil {
		return nil, 0, req, err
	}
	all, err := m.ListGuests(ctx, tenantID, hostDeviceID)
	if err != nil {
		return nil, 0, req, err
	}
	out, total, applied := pageOf(all, store.GuestList, req, func(g store.HypervisorGuest, _ string) any { return g.Name },
		func(g store.HypervisorGuest) string { return g.ID })
	return out, total, applied, nil
}
