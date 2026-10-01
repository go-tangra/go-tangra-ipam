package repodb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// where accumulates a WHERE clause and its positional arguments. Conditions
// carry a single %d for the placeholder number of the value they bind.
type where struct {
	b    strings.Builder
	args []any
}

func newWhere(base string, args ...any) *where {
	w := &where{args: args}
	w.b.WriteString(base)
	return w
}

// add appends cond with val bound to the next placeholder.
func (w *where) add(cond string, val any) {
	w.args = append(w.args, val)
	fmt.Fprintf(&w.b, cond, len(w.args))
}

// raw appends a condition without arguments.
func (w *where) raw(cond string) { w.b.WriteString(cond) }

func (w *where) String() string { return w.b.String() }

// maxSearchLen caps a free-text search term (runes): longer terms are cut so
// a request cannot make every row run an unbounded pattern match.
const maxSearchLen = 200

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likeTerm returns s capped to maxSearchLen runes with the LIKE wildcards
// (and the escape character, backslash, LIKE's default) escaped, so user
// input matches literally inside the caller's own % anchors.
func likeTerm(s string) string {
	if r := []rune(s); len(r) > maxSearchLen {
		s = string(r[:maxSearchLen])
	}
	return likeEscaper.Replace(s)
}

// The filter builders below are shared by the keyset (cursor) lists the gRPC
// API, backup and internal callers use and by the paged lists of the HTTP
// list contract, so both apply exactly the same filters.

func subnetWhere(tenantID string, f store.SubnetFilter) *where {
	w := newWhere("tenant_id=$1", tenantID)
	if f.VlanID != "" {
		w.add(" AND vlan_id = $%d", f.VlanID)
	}
	if f.ParentID != "" {
		w.add(" AND parent_id = $%d", f.ParentID)
	}
	if f.LocationID != "" {
		w.add(" AND location_id = $%d", f.LocationID)
	}
	if f.Status != "" {
		w.add(" AND status = $%d", f.Status)
	}
	if f.IPVersion != 0 {
		w.add(" AND ip_version = $%d", f.IPVersion)
	}
	if f.Query != "" {
		w.args = append(w.args, "%"+likeTerm(f.Query)+"%")
		fmt.Fprintf(&w.b, " AND (name ILIKE $%d OR cidr ILIKE $%d)", len(w.args), len(w.args))
	}
	return w
}

func addressWhere(tenantID string, f store.AddressFilter) *where {
	w := newWhere("tenant_id=$1", tenantID)
	if f.SubnetID != "" {
		w.add(" AND subnet_id = $%d", f.SubnetID)
	}
	if f.DeviceID != "" {
		w.add(" AND device_id = $%d", f.DeviceID)
	}
	if f.Status != "" {
		w.add(" AND status = $%d", f.Status)
	}
	if f.AddressType != "" {
		w.add(" AND address_type = $%d", f.AddressType)
	}
	if f.AddressPrefix != "" {
		w.add(" AND address LIKE $%d", likeTerm(f.AddressPrefix)+"%")
	}
	if f.HostnamePattern != "" {
		w.add(" AND hostname ILIKE $%d", "%"+likeTerm(f.HostnamePattern)+"%")
	}
	if f.ReportState != "" {
		w.add(" AND report_state = $%d", f.ReportState)
	}
	if f.Conflict != nil {
		w.add(" AND conflict = $%d", *f.Conflict)
	}
	if f.MAC != "" {
		// Matches the addresses_mac_hex expression index.
		w.add(" AND regexp_replace(lower(mac_address), '[^0-9a-f]', '', 'g') LIKE $%d", "%"+likeTerm(f.MAC)+"%")
	}
	return w
}

func deviceWhere(tenantID string, f store.DeviceFilter) *where {
	w := newWhere("tenant_id=$1", tenantID)
	if f.DeviceType != "" {
		w.add(" AND device_type = $%d", f.DeviceType)
	}
	if f.Status != "" {
		w.add(" AND status = $%d", f.Status)
	}
	if f.LocationID != "" {
		w.add(" AND location_id = $%d", f.LocationID)
	}
	if f.Manufacturer != "" {
		w.add(" AND manufacturer = $%d", f.Manufacturer)
	}
	if f.RackID != "" {
		w.add(" AND rack_id = $%d", f.RackID)
	}
	if f.Source != "" {
		w.add(" AND source = $%d", f.Source)
	}
	if f.ReportState != "" {
		w.add(" AND report_state = $%d", f.ReportState)
	}
	if f.Query != "" {
		w.args = append(w.args, "%"+likeTerm(f.Query)+"%")
		fmt.Fprintf(&w.b, " AND (name ILIKE $%d OR primary_ip ILIKE $%d)", len(w.args), len(w.args))
	}
	switch f.HasHardware {
	case "true":
		w.raw(" AND EXISTS (SELECT 1 FROM ipam_device_hardware h WHERE h.device_id = ipam_devices.id)")
	case "false":
		w.raw(" AND NOT EXISTS (SELECT 1 FROM ipam_device_hardware h WHERE h.device_id = ipam_devices.id)")
	}
	return w
}

func vlanWhere(tenantID string, f store.VlanFilter) *where {
	w := newWhere("tenant_id=$1", tenantID)
	if f.LocationID != "" {
		w.add(" AND location_id = $%d", f.LocationID)
	}
	if f.Domain != "" {
		w.add(" AND domain = $%d", f.Domain)
	}
	if f.Status != "" {
		w.add(" AND status = $%d", f.Status)
	}
	if f.VlanIDMin != 0 {
		w.add(" AND vlan_id >= $%d", f.VlanIDMin)
	}
	if f.VlanIDMax != 0 {
		w.add(" AND vlan_id <= $%d", f.VlanIDMax)
	}
	return w
}

func scanJobWhere(tenantID string, f store.ScanFilter) *where {
	w := newWhere("tenant_id=$1", tenantID)
	if f.SubnetID != "" {
		w.add(" AND subnet_id = $%d", f.SubnetID)
	}
	if f.Status != "" {
		w.add(" AND status = $%d", f.Status)
	}
	return w
}

func packageWhere(tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string) *where {
	w := newWhere("tenant_id=$1 AND device_id=$2", tenantID, deviceID)
	if needsUpdate != nil {
		w.add(" AND needs_update = $%d", *needsUpdate)
	}
	if securityOnly != nil {
		w.add(" AND is_security_update = $%d", *securityOnly)
	}
	if manager != "" {
		w.add(" AND package_manager = $%d", manager)
	}
	return w
}

// pageRows runs one page of a list contract query in tx: it counts the rows of
// "FROM <from> WHERE <w>", clamps req to the last page and selects cols of the
// requested page in spec order (sort expression, NULLS LAST unless the field is
// NotNull, tie-breaker). The ORDER BY is built only from spec constants and the
// direction enum.
func pageRows[T any](ctx context.Context, tx pgx.Tx, cols, from string, w *where, spec listquery.Spec, req listquery.Request,
	scan func(scanner) (T, error)) ([]T, int, listquery.Request, error) {
	return runPage(ctx, tx, cols, from, w, spec, req, "", scan)
}

// pageRowsDeferred is pageRows for a single unaliased table whose cols carry
// per-row subqueries (the address list's switch name and links): the page's
// keys (spec.TieBreak) are picked by an inner query that sorts and skips
// narrow rows (filters, tenant predicate and RLS all inside it), and cols are
// evaluated only for those rows, re-sorted in the same order. Without this a
// deep page evaluated the subqueries for every skipped row.
func pageRowsDeferred[T any](ctx context.Context, tx pgx.Tx, cols, table string, w *where, spec listquery.Spec, req listquery.Request,
	scan func(scanner) (T, error)) ([]T, int, listquery.Request, error) {
	return runPage(ctx, tx, cols, table, w, spec, req, spec.TieBreak, scan)
}

func runPage[T any](ctx context.Context, tx pgx.Tx, cols, from string, w *where, spec listquery.Spec, req listquery.Request,
	deferKey string, scan func(scanner) (T, error)) ([]T, int, listquery.Request, error) {
	req = store.ListRequest(req, spec)
	var total int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+from+" WHERE "+w.String(), w.args...).Scan(&total); err != nil {
		return nil, 0, req, err
	}
	req = req.Clamp(total)
	orderBy := req.OrderBy(spec)
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT %d OFFSET %d",
		cols, from, w.String(), orderBy, req.Limit(), req.Offset())
	if deferKey != "" {
		q = fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT %d OFFSET %d) ORDER BY %s",
			cols, from, deferKey, deferKey, from, w.String(), orderBy, req.Limit(), req.Offset(), orderBy)
	}
	rows, err := tx.Query(ctx, q, w.args...)
	if err != nil {
		return nil, 0, req, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, 0, req, err
		}
		out = append(out, v)
	}
	return out, total, req, rows.Err()
}

// PageSubnets implements repo.Store (store.SubnetList order, counts computed
// for the page only).
func (d *DB) PageSubnets(ctx context.Context, tenantID string, f store.SubnetFilter, req listquery.Request) (out []store.Subnet, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if out, total, applied, e = pageRows(ctx, tx, subnetCols, "ipam_subnets", subnetWhere(tenantID, f), store.SubnetList, req, scanSubnet); e != nil {
			return e
		}
		for i := range out {
			if e := computeSubnet(ctx, tx, &out[i]); e != nil {
				return e
			}
		}
		return nil
	})
	return
}

// PageAddresses implements repo.Store (store.AddressList order).
func (d *DB) PageAddresses(ctx context.Context, tenantID string, f store.AddressFilter, req listquery.Request) (out []store.IPAddress, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRowsDeferred(ctx, tx, addrCols, "ipam_ip_addresses", addressWhere(tenantID, f), store.AddressList, req, scanAddress)
		return e
	})
	return
}

// PageDevices implements repo.Store (store.DeviceList order, computed counts
// and hardware summary for the page only).
func (d *DB) PageDevices(ctx context.Context, tenantID string, f store.DeviceFilter, req listquery.Request) (out []store.Device, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if out, total, applied, e = pageRows(ctx, tx, deviceCols, "ipam_devices", deviceWhere(tenantID, f), store.DeviceList, req, scanDevice); e != nil {
			return e
		}
		for i := range out {
			if e := computeDevice(ctx, tx, &out[i]); e != nil {
				return e
			}
		}
		return nil
	})
	return
}

// PageVlans implements repo.Store (store.VlanList order).
func (d *DB) PageVlans(ctx context.Context, tenantID string, f store.VlanFilter, req listquery.Request) (out []store.Vlan, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if out, total, applied, e = pageRows(ctx, tx, vlanCols, "ipam_vlans", vlanWhere(tenantID, f), store.VlanList, req, scanVlan); e != nil {
			return e
		}
		for i := range out {
			if e := computeVlan(ctx, tx, &out[i]); e != nil {
				return e
			}
		}
		return nil
	})
	return
}

// PageScanJobs implements repo.Store (store.ScanList order).
func (d *DB) PageScanJobs(ctx context.Context, tenantID string, f store.ScanFilter, req listquery.Request) (out []store.IPScanJob, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRows(ctx, tx, scanJobCols, "ipam_ip_scan_jobs", scanJobWhere(tenantID, f), store.ScanList, req, scanScanJob)
		return e
	})
	return
}

// PageIPGroupMembers implements repo.Store (store.IPMemberList order).
func (d *DB) PageIPGroupMembers(ctx context.Context, tenantID, groupID string, req listquery.Request) (out []store.IPGroupMember, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRows(ctx, tx, ipMemberCols, "ipam_ip_group_members",
			newWhere("tenant_id=$1 AND ip_group_id=$2", tenantID, groupID), store.IPMemberList, req, scanIPMember)
		return e
	})
	return
}

// PageHostGroupMembers implements repo.Store (store.HostMemberList order).
func (d *DB) PageHostGroupMembers(ctx context.Context, tenantID, groupID string, req listquery.Request) (out []store.HostGroupMember, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRows(ctx, tx, hostMemberCols, "ipam_host_group_members m LEFT JOIN ipam_devices d ON d.tenant_id = m.tenant_id AND d.id = m.device_id",
			newWhere("m.tenant_id=$1 AND m.host_group_id=$2", tenantID, groupID), store.HostMemberList, req, scanHostMember)
		return e
	})
	return
}

// PageInterfaces implements repo.Store (store.InterfaceList order); the
// remote / behind device names and per-switch links are computed for the page
// only.
func (d *DB) PageInterfaces(ctx context.Context, tenantID, deviceID string, req listquery.Request) (out []store.DeviceInterface, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if out, total, applied, e = pageRows(ctx, tx, ifaceCols, "ipam_device_interfaces",
			newWhere("tenant_id=$1 AND device_id=$2", tenantID, deviceID), store.InterfaceList, req, scanIface); e != nil {
			return e
		}
		return enrichIfaces(ctx, tx, tenantID, out)
	})
	return
}

// PageDevicePackages implements repo.Store (store.PackageList order).
func (d *DB) PageDevicePackages(ctx context.Context, tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string, req listquery.Request) (out []store.DevicePackage, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRows(ctx, tx, pkgCols, "ipam_device_packages",
			packageWhere(tenantID, deviceID, needsUpdate, securityOnly, manager), store.PackageList, req, scanPkg)
		return e
	})
	return
}

// PageGuests implements repo.HostSyncStore (store.GuestList order, with the
// matched guest device names).
func (d *DB) PageGuests(ctx context.Context, tenantID, hostDeviceID string, req listquery.Request) (out []store.HypervisorGuest, total int, applied listquery.Request, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, total, applied, e = pageRows(ctx, tx, guestCols+", coalesce(gd.name,'')",
			"ipam_hypervisor_guests g LEFT JOIN ipam_devices gd ON gd.tenant_id = g.tenant_id AND gd.id = g.guest_device_id",
			newWhere("g.tenant_id=$1 AND g.host_device_id=$2", tenantID, hostDeviceID), store.GuestList, req,
			func(sc scanner) (store.HypervisorGuest, error) { return scanGuest(sc, true) })
		return e
	})
	return
}
