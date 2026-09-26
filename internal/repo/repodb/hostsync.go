package repodb

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// ---- settings

const settingsCols = `tenant_id::text, enabled, full_interval_minutes, excluded_interfaces, changed_since,
	last_poll_at, last_reconcile_at, reconcile_requested, status, last_error, hosts_reported, hosts_failed,
	updated_by, updated_at`

func scanSettings(sc scanner) (store.HostSyncSettings, error) {
	var s store.HostSyncSettings
	err := sc.Scan(&s.TenantID, &s.Enabled, &s.FullIntervalMinutes, &s.ExcludedInterfaces, &s.ChangedSince,
		&s.LastPollAt, &s.LastReconcileAt, &s.ReconcileRequested, &s.Status, &s.LastError, &s.HostsReported,
		&s.HostsFailed, &s.UpdatedBy, &s.UpdatedAt)
	return s, err
}

func ensureSettings(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, "INSERT INTO ipam_hostsync_settings (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING", tenantID)
	return err
}

// EnsureHostSyncSettings implements repo.HostSyncStore.
func (d *DB) EnsureHostSyncSettings(ctx context.Context, tenantID string) (out store.HostSyncSettings, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := ensureSettings(ctx, tx, tenantID); e != nil {
			return e
		}
		var e error
		out, e = scanSettings(tx.QueryRow(ctx, "SELECT "+settingsCols+" FROM ipam_hostsync_settings WHERE tenant_id=$1", tenantID))
		return mapErr(e)
	})
	return
}

// GetHostSyncSettings implements repo.HostSyncStore.
func (d *DB) GetHostSyncSettings(ctx context.Context, tenantID string) (out store.HostSyncSettings, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanSettings(tx.QueryRow(ctx, "SELECT "+settingsCols+" FROM ipam_hostsync_settings WHERE tenant_id=$1", tenantID))
		return mapErr(e)
	})
	return
}

// UpdateHostSyncSettings implements repo.HostSyncStore. The UPDATE waits for
// every apply transaction holding FOR SHARE on the row, so no host-sync change
// commits after a disable commits (SR-006).
func (d *DB) UpdateHostSyncSettings(ctx context.Context, s store.HostSyncSettings, audit store.AuditRow) error {
	return d.tenant(ctx, s.TenantID, func(tx pgx.Tx) error {
		if e := ensureSettings(ctx, tx, s.TenantID); e != nil {
			return e
		}
		excl := s.ExcludedInterfaces
		if excl == nil {
			excl = []string{}
		}
		if _, e := tx.Exec(ctx, `UPDATE ipam_hostsync_settings SET
			reconcile_requested = reconcile_requested OR ($2 AND NOT enabled),
			status = CASE WHEN NOT $2 THEN 'disabled' WHEN status = 'disabled' THEN 'ok' ELSE status END,
			enabled=$2, full_interval_minutes=$3, excluded_interfaces=$4, updated_by=$5, updated_at=now()
			WHERE tenant_id=$1`, s.TenantID, s.Enabled, s.FullIntervalMinutes, excl, s.UpdatedBy); e != nil {
			return mapErr(e)
		}
		return appendAuditTx(ctx, tx, audit)
	})
}

// RequestReconcile implements repo.HostSyncStore.
func (d *DB) RequestReconcile(ctx context.Context, tenantID string, audit store.AuditRow) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := ensureSettings(ctx, tx, tenantID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "UPDATE ipam_hostsync_settings SET reconcile_requested=true WHERE tenant_id=$1", tenantID); e != nil {
			return e
		}
		return appendAuditTx(ctx, tx, audit)
	})
}

// ListHostSyncSettings implements repo.HostSyncStore (system scope).
func (d *DB) ListHostSyncSettings(ctx context.Context) (out []store.HostSyncSettings, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+settingsCols+" FROM ipam_hostsync_settings ORDER BY tenant_id")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			s, e := scanSettings(rows)
			if e != nil {
				return e
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return
}

// SaveHostSyncState implements repo.HostSyncStore.
func (d *DB) SaveHostSyncState(ctx context.Context, tenantID string, st store.HostSyncStatus) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := ensureSettings(ctx, tx, tenantID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE ipam_hostsync_settings SET status=$2, last_error=$3,
			changed_since=coalesce($4, changed_since), last_poll_at=coalesce($5, last_poll_at),
			last_reconcile_at=coalesce($6, last_reconcile_at),
			reconcile_requested = CASE WHEN $7 THEN false ELSE reconcile_requested END,
			hosts_reported=$8, hosts_failed=$9 WHERE tenant_id=$1`,
			tenantID, st.Status, st.LastError, st.ChangedSince, st.LastPollAt, st.LastReconcileAt,
			st.ClearReconcile, st.HostsReported, st.HostsFailed)
		return mapErr(e)
	})
}

// ---- reads for the admin surface

// HostDevices implements repo.HostSyncStore.
func (d *DB) HostDevices(ctx context.Context, tenantID string) (out []store.Device, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = queryDevices(ctx, tx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND inventory_host_id IS NOT NULL ORDER BY id", tenantID)
		return e
	})
	return
}

const devStateCols = `device_id::text, tenant_id::text, inventory_host_id::text, snapshot_id, collected_at,
	applied_at, trigger, changes, issues`

// GetHostSyncDeviceState implements repo.HostSyncStore.
func (d *DB) GetHostSyncDeviceState(ctx context.Context, tenantID, deviceID string) (out store.HostSyncDeviceState, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var issues []store.HostSyncIssue
		e := tx.QueryRow(ctx, "SELECT "+devStateCols+" FROM ipam_hostsync_device_state WHERE tenant_id=$1 AND device_id=$2",
			tenantID, deviceID).Scan(&out.DeviceID, &out.TenantID, &out.InventoryHostID, &out.SnapshotID,
			&out.CollectedAt, &out.AppliedAt, &out.Trigger, &out.Changes, &issues)
		out.Issues = issues
		return mapErr(e)
	})
	return
}

// HostSyncCounts implements repo.HostSyncStore.
func (d *DB) HostSyncCounts(ctx context.Context, tenantID string) (nr, cf int64, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM ipam_devices WHERE tenant_id=$1 AND report_state='not_reported'),
			(SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND conflict)`, tenantID).Scan(&nr, &cf)
	})
	return
}

const guestCols = `g.id::text, g.tenant_id::text, g.host_device_id::text, g.guest_ref, g.name, g.kind, g.platform, g.macs,
	coalesce(g.guest_device_id::text,''), g.last_reported_at`

func scanGuest(sc scanner, withName bool) (store.HypervisorGuest, error) {
	var g store.HypervisorGuest
	dst := []any{&g.ID, &g.TenantID, &g.HostDeviceID, &g.GuestRef, &g.Name, &g.Kind, &g.Platform, &g.MACs, &g.GuestDeviceID, &g.LastReportedAt}
	if withName {
		dst = append(dst, &g.GuestDeviceName)
	}
	err := sc.Scan(dst...)
	return g, err
}

func queryGuests(ctx context.Context, tx pgx.Tx, withName bool, q string, args ...any) ([]store.HypervisorGuest, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.HypervisorGuest
	for rows.Next() {
		g, err := scanGuest(rows, withName)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListGuests implements repo.HostSyncStore.
func (d *DB) ListGuests(ctx context.Context, tenantID, hostDeviceID string) (out []store.HypervisorGuest, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = queryGuests(ctx, tx, true, "SELECT "+guestCols+`, coalesce(gd.name,'')
			FROM ipam_hypervisor_guests g LEFT JOIN ipam_devices gd ON gd.id = g.guest_device_id
			WHERE g.tenant_id=$1 AND g.host_device_id=$2 ORDER BY g.guest_ref`, tenantID, hostDeviceID)
		return e
	})
	return
}

// ClearAddressConflict implements repo.HostSyncStore.
func (d *DB) ClearAddressConflict(ctx context.Context, tenantID, addressID string, audit store.AuditRow) (out store.IPAddress, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_ip_addresses SET conflict=false, move_count=0, move_window_start=NULL, updated_at=now()
			WHERE tenant_id=$1 AND id=$2`, tenantID, addressID)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		if out, e = scanAddress(tx.QueryRow(ctx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND id=$2", tenantID, addressID)); e != nil {
			return mapErr(e)
		}
		return appendAuditTx(ctx, tx, audit)
	})
	return
}

// ---- apply

// ApplyHostReport implements repo.HostSyncStore: one tenant transaction
// holding the tenant's advisory lock (several IPAM replicas) and FOR SHARE on
// its settings row; a disabled tenant aborts with ErrSyncDisabled.
func (d *DB) ApplyHostReport(ctx context.Context, tenantID string, fn func(tx repo.HostTx) error) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('ipam.hostsync:' || $1))", tenantID); e != nil {
			return e
		}
		if e := ensureSettings(ctx, tx, tenantID); e != nil {
			return mapErr(e)
		}
		var enabled bool
		if e := tx.QueryRow(ctx, "SELECT enabled FROM ipam_hostsync_settings WHERE tenant_id=$1 FOR SHARE", tenantID).Scan(&enabled); e != nil {
			return mapErr(e)
		}
		if !enabled {
			return repo.ErrSyncDisabled
		}
		return fn(&hostTx{ctx: ctx, tx: tx, tid: tenantID})
	})
}

type hostTx struct {
	ctx context.Context
	tx  pgx.Tx
	tid string
}

func queryDevices(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]store.Device, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Device
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dev)
	}
	return out, rows.Err()
}

func queryAddresses(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]store.IPAddress, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.IPAddress
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t *hostTx) DeviceByInventoryHost(hostID string) (store.Device, bool, error) {
	dev, err := scanDevice(t.tx.QueryRow(t.ctx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND inventory_host_id=$2 FOR UPDATE", t.tid, hostID))
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Device{}, false, nil
	}
	return dev, err == nil, err
}

func (t *hostTx) DevicesBySerial(serial string) ([]store.Device, error) {
	return queryDevices(t.ctx, t.tx, "SELECT "+deviceCols+` FROM ipam_devices
		WHERE tenant_id=$1 AND $2 <> '' AND lower(btrim(serial_number)) = lower(btrim($2)) ORDER BY id`, t.tid, serial)
}

func (t *hostTx) DevicesByNames(names []string) ([]store.Device, error) {
	lower := make([]string, len(names))
	for i, n := range names {
		lower[i] = strings.ToLower(n)
	}
	return queryDevices(t.ctx, t.tx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND lower(name) = ANY($2) ORDER BY id", t.tid, lower)
}

func (t *hostTx) Interfaces(deviceID string) ([]store.DeviceInterface, error) {
	rows, err := t.tx.Query(t.ctx, "SELECT "+ifaceCols+" FROM ipam_device_interfaces WHERE tenant_id=$1 AND device_id=$2 ORDER BY name", t.tid, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.DeviceInterface
	for rows.Next() {
		i, err := scanIface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (t *hostTx) AddressesByValue(addrs []string) ([]store.IPAddress, error) {
	return queryAddresses(t.ctx, t.tx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND address = ANY($2) ORDER BY address FOR UPDATE", t.tid, addrs)
}

func (t *hostTx) AddressesOfDevice(deviceID string) ([]store.IPAddress, error) {
	return queryAddresses(t.ctx, t.tx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND device_id=$2 ORDER BY address FOR UPDATE", t.tid, deviceID)
}

func (t *hostTx) Subnets() ([]store.Subnet, error) {
	rows, err := t.tx.Query(t.ctx, "SELECT "+subnetCols+" FROM ipam_subnets WHERE tenant_id=$1 ORDER BY id", t.tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Subnet
	for rows.Next() {
		s, err := scanSubnet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *hostTx) Packages(deviceID string) ([]store.DevicePackage, error) {
	rows, err := t.tx.Query(t.ctx, "SELECT "+pkgCols+" FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2 ORDER BY name", t.tid, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.DevicePackage
	for rows.Next() {
		p, err := scanPkg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *hostTx) Guests(hostDeviceID string) ([]store.HypervisorGuest, error) {
	return queryGuests(t.ctx, t.tx, false, "SELECT "+guestCols+" FROM ipam_hypervisor_guests g WHERE g.tenant_id=$1 AND g.host_device_id=$2 ORDER BY g.guest_ref", t.tid, hostDeviceID)
}

func (t *hostTx) DevicesByMAC(macs []string) ([]repo.MACOwner, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT DISTINCT lower(i.mac_address), d.id::text, coalesce(d.hypervisor_device_id::text,'')
		FROM ipam_device_interfaces i JOIN ipam_devices d ON d.id = i.device_id
		WHERE i.tenant_id=$1 AND i.mac_address <> '' AND lower(i.mac_address) = ANY($2) ORDER BY 1, 2`, t.tid, macs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repo.MACOwner
	for rows.Next() {
		var o repo.MACOwner
		if err := rows.Scan(&o.MAC, &o.DeviceID, &o.HypervisorDeviceID); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (t *hostTx) GuestRowsByMAC(macs []string) ([]store.HypervisorGuest, error) {
	return queryGuests(t.ctx, t.tx, false, "SELECT "+guestCols+" FROM ipam_hypervisor_guests g WHERE g.tenant_id=$1 AND g.macs && $2 ORDER BY g.id", t.tid, macs)
}

func (t *hostTx) GuestDevicesOf(hostDeviceID string) ([]store.Device, error) {
	return queryDevices(t.ctx, t.tx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND hypervisor_device_id=$2 ORDER BY id", t.tid, hostDeviceID)
}

func (t *hostTx) InsertDevice(dv store.Device) error {
	dv.TenantID = t.tid
	now := time.Now().UTC()
	if _, err := t.tx.Exec(t.ctx, deviceInsertSQL, deviceInsertArgs(dv, now)...); err != nil {
		return mapErr(err)
	}
	_, err := t.tx.Exec(t.ctx, `UPDATE ipam_devices SET inventory_host_id=$3, virtualization_kind=$4, update_status=$5,
		report_state=$6, last_report_at=$7, report_digest=$8 WHERE tenant_id=$1 AND id=$2`,
		t.tid, dv.ID, np(dv.InventoryHostID), dv.VirtualizationKind, nzStatus(dv.UpdateStatus), dv.ReportState, dv.LastReportAt, dv.ReportDigest)
	return mapErr(err)
}

func nzStatus(s string) string {
	if s == "" {
		return store.UpdUnknown
	}
	return s
}

// UpdateDeviceReported writes exactly the columns the host sync owns (D8);
// description, tags, location, rack, asset tag, status, contact,
// ipmi_secret_ref, firmware and created_by are not in the statement.
func (t *hostTx) UpdateDeviceReported(dv store.Device) error {
	ct, err := t.tx.Exec(t.ctx, `UPDATE ipam_devices SET
		name=$3, device_type=$4, virtualization_kind=$5, os_type=$6, os_version=$7, manufacturer=$8, model=$9,
		serial_number=$10, primary_ip=$11, primary_ipv6=$12, management_ip=$13, reboot_required=$14,
		unattended_upgrades=$15, update_status=$16, last_seen=$17, source=$18, inventory_host_id=$19,
		report_state=$20, last_report_at=$21, report_digest=$22, updated_at=now()
		WHERE tenant_id=$1 AND id=$2`,
		t.tid, dv.ID, dv.Name, dv.DeviceType, dv.VirtualizationKind, dv.OSType, dv.OSVersion, dv.Manufacturer, dv.Model,
		dv.SerialNumber, dv.PrimaryIP, dv.PrimaryIPv6, dv.ManagementIP, dv.RebootRequired, dv.UnattendedUpgrades,
		nzStatus(dv.UpdateStatus), dv.LastSeen, dv.Source, np(dv.InventoryHostID), dv.ReportState, dv.LastReportAt, dv.ReportDigest)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (t *hostTx) UpsertInterfaceReported(i store.DeviceInterface, create bool) error {
	if create {
		i.TenantID = t.tid
		if _, err := t.tx.Exec(t.ctx, ifaceInsertSQL, ifaceInsertArgs(i, time.Now().UTC())...); err != nil {
			return mapErr(err)
		}
	}
	// name and description are never written on update; SNMP if_index and
	// link columns belong to the scan / port correlation.
	ct, err := t.tx.Exec(t.ctx, `UPDATE ipam_device_interfaces SET mac_address=$3, interface_type=$4, speed_mbps=$5,
		enabled=$6, report_state=$7, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		t.tid, i.ID, i.MACAddress, i.InterfaceType, i.SpeedMbps, i.Enabled, i.ReportState)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (t *hostTx) CreateSubnetAuto(s store.Subnet) error {
	now := time.Now().UTC()
	_, err := t.tx.Exec(t.ctx, `INSERT INTO ipam_subnets
		(id, tenant_id, name, cidr, parent_id, status, ip_version, network_address, broadcast_address, mask,
		 prefix_length, created_by, origin, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`,
		s.ID, t.tid, s.Name, s.CIDR, np(s.ParentID), s.Status, s.IPVersion, s.NetworkAddress, s.BroadcastAddr, s.Mask,
		s.PrefixLength, s.CreatedBy, s.Origin, now)
	return mapErr(err)
}

func (t *hostTx) InsertAddressReported(a store.IPAddress) error {
	now := time.Now().UTC()
	_, err := t.tx.Exec(t.ctx, `INSERT INTO ipam_ip_addresses
		(id, tenant_id, address, subnet_id, hostname, mac_address, device_id, interface_name, status, address_type,
		 is_primary, last_seen, created_by, report_state, previous_device_id, moved_at, move_count, move_window_start,
		 conflict, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$20)`,
		a.ID, t.tid, a.Address, a.SubnetID, a.Hostname, a.MACAddress, np(a.DeviceID), a.InterfaceName, a.Status,
		a.AddressType, a.IsPrimary, a.LastSeen, a.CreatedBy, a.ReportState, np(a.PreviousDeviceID), a.MovedAt,
		a.MoveCount, a.MoveWindowStart, a.Conflict, now)
	return mapErr(err)
}

// UpdateAddressReported writes only the host-sync-owned address columns
// (D8): description, note, tags, owner, dns_name, ptr_record, reverse DNS,
// lease, status, address type and subnet stay as they are.
func (t *hostTx) UpdateAddressReported(a store.IPAddress) error {
	ct, err := t.tx.Exec(t.ctx, `UPDATE ipam_ip_addresses SET device_id=$3, interface_name=$4, mac_address=$5,
		hostname=$6, is_primary=$7, last_seen=$8, report_state=$9, previous_device_id=$10, moved_at=$11,
		move_count=$12, move_window_start=$13, conflict=$14, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		t.tid, a.ID, np(a.DeviceID), a.InterfaceName, a.MACAddress, a.Hostname, a.IsPrimary, a.LastSeen,
		a.ReportState, np(a.PreviousDeviceID), a.MovedAt, a.MoveCount, a.MoveWindowStart, a.Conflict)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

// ReplacePendingPackages replaces a device's package rows in one batched
// statement (up to 5000 rows per host report).
func (t *hostTx) ReplacePendingPackages(deviceID string, pkgs []store.DevicePackage) error {
	if _, err := t.tx.Exec(t.ctx, "DELETE FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2", t.tid, deviceID); err != nil {
		return mapErr(err)
	}
	if len(pkgs) == 0 {
		return nil
	}
	n := len(pkgs)
	ids, names, cur, avail, mgr := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	needs, sec := make([]bool, n), make([]bool, n)
	for i, p := range pkgs {
		ids[i] = p.ID
		if ids[i] == "" {
			ids[i] = store.NewID()
		}
		names[i], cur[i], avail[i], mgr[i], needs[i], sec[i] = p.Name, p.CurrentVersion, p.AvailableVersion, p.PackageManager, p.NeedsUpdate, p.IsSecurityUpdate
	}
	_, err := t.tx.Exec(t.ctx, `INSERT INTO ipam_device_packages
		(id, tenant_id, device_id, name, current_version, available_version, needs_update, is_security_update, package_manager)
		SELECT u.id, $1, $2, u.name, u.cur, u.avail, u.needs, u.sec, u.mgr
		FROM unnest($3::uuid[], $4::text[], $5::text[], $6::text[], $7::bool[], $8::bool[], $9::text[])
		AS u(id, name, cur, avail, needs, sec, mgr)`, t.tid, deviceID, ids, names, cur, avail, needs, sec, mgr)
	return mapErr(err)
}

func (t *hostTx) ReplaceGuests(hostDeviceID string, guests []store.HypervisorGuest) error {
	if _, err := t.tx.Exec(t.ctx, "DELETE FROM ipam_hypervisor_guests WHERE tenant_id=$1 AND host_device_id=$2", t.tid, hostDeviceID); err != nil {
		return mapErr(err)
	}
	for _, g := range guests {
		macs := slices.Clone(g.MACs)
		if macs == nil {
			macs = []string{}
		}
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO ipam_hypervisor_guests
			(id, tenant_id, host_device_id, guest_ref, name, kind, platform, macs, guest_device_id, last_reported_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, g.ID, t.tid, hostDeviceID, g.GuestRef, g.Name, g.Kind, g.Platform,
			macs, np(g.GuestDeviceID), g.LastReportedAt); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func (t *hostTx) exec1(q string, args ...any) error {
	ct, err := t.tx.Exec(t.ctx, q, args...)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (t *hostTx) SetHypervisor(deviceID, hypervisorID string) error {
	return t.exec1("UPDATE ipam_devices SET hypervisor_device_id=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2", t.tid, deviceID, np(hypervisorID))
}

func (t *hostTx) SetGuestDevice(guestRowID, deviceID string) error {
	return t.exec1("UPDATE ipam_hypervisor_guests SET guest_device_id=$3 WHERE tenant_id=$1 AND id=$2", t.tid, guestRowID, np(deviceID))
}

func (t *hostTx) MarkDeviceNotReported(deviceID string) error {
	if err := t.exec1("UPDATE ipam_devices SET report_state='not_reported', updated_at=now() WHERE tenant_id=$1 AND id=$2", t.tid, deviceID); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, "UPDATE ipam_device_interfaces SET report_state='not_reported', updated_at=now() WHERE tenant_id=$1 AND device_id=$2 AND report_state='reported'", t.tid, deviceID); err != nil {
		return err
	}
	_, err := t.tx.Exec(t.ctx, "UPDATE ipam_ip_addresses SET report_state='not_reported', updated_at=now() WHERE tenant_id=$1 AND device_id=$2 AND report_state='reported'", t.tid, deviceID)
	return err
}

func (t *hostTx) SaveDeviceState(st store.HostSyncDeviceState) error {
	issues := st.Issues
	if issues == nil {
		issues = []store.HostSyncIssue{}
	}
	_, err := t.tx.Exec(t.ctx, `INSERT INTO ipam_hostsync_device_state
		(device_id, tenant_id, inventory_host_id, snapshot_id, collected_at, applied_at, trigger, changes, issues)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)
		ON CONFLICT (device_id) DO UPDATE SET inventory_host_id=EXCLUDED.inventory_host_id,
			snapshot_id=EXCLUDED.snapshot_id, collected_at=EXCLUDED.collected_at, applied_at=EXCLUDED.applied_at,
			trigger=EXCLUDED.trigger, changes=EXCLUDED.changes, issues=EXCLUDED.issues`,
		st.DeviceID, t.tid, st.InventoryHostID, st.SnapshotID, st.CollectedAt, st.AppliedAt, st.Trigger, st.Changes, mustJSON(issues))
	return mapErr(err)
}

func (t *hostTx) AppendAudit(row store.AuditRow) error {
	row.TenantID = t.tid
	return appendAuditTx(t.ctx, t.tx, row)
}

func appendAuditTx(ctx context.Context, tx pgx.Tx, row store.AuditRow) error {
	id := row.ID
	if id == "" {
		id = store.NewID()
	}
	at := row.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var detail any
	if row.Detail != nil {
		detail = mustJSON(row.Detail)
	}
	_, err := tx.Exec(ctx, `INSERT INTO ipam_audit_events
		(id, tenant_id, at, actor_kind, actor_id, action, subject_kind, subject_id, target, outcome, reason, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)`,
		id, row.TenantID, at, row.ActorKind, row.ActorID, row.Action, row.SubjectKind, row.SubjectID,
		row.Target, row.Outcome, row.Reason, detail)
	return err
}

var _ repo.HostTx = (*hostTx)(nil)
