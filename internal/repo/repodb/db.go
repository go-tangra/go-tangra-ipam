// Package repodb binds repo.Store to TimescaleDB via *store.Store. Tenant-scoped
// calls run in a tenant transaction (RLS); system-scoped calls (scan-job
// claiming, tenant enumeration, audit) run under the system-scope pin. Unique
// violations (SQLSTATE 23505) map to repo.ErrConflict — this is the duplicate-IP
// / allocation guard on ipam_ip_addresses (tenant_id,address). ClaimDueScanJobs
// drains the work queue with FOR UPDATE SKIP LOCKED.
package repodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// DB implements repo.Store over *store.Store.
type DB struct {
	St *store.Store
}

// New wraps the store.
func New(st *store.Store) *DB { return &DB{St: st} }

// Close releases the underlying pool.
func (d *DB) Close() { d.St.Close() }

func (d *DB) tenant(ctx context.Context, tid string, fn func(tx pgx.Tx) error) error {
	return d.St.Tx(ctx, store.Scope{TenantID: tid}, fn)
}
func (d *DB) system(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return d.St.Tx(ctx, store.Scope{System: true}, fn)
}

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface{ Scan(dest ...any) error }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return repo.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return repo.ErrConflict
	}
	return err
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func nzTags(t map[string]string) map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return t
}

func unmarshalTags(b []byte) map[string]string {
	out := map[string]string{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// np returns a *string for a nullable uuid column: "" -> NULL.
func np(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// subnetTotal returns the address capacity of a CIDR (0 when unparseable or too
// wide to represent).
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

// ---- subnets

const subnetCols = `id, tenant_id, name, cidr, description, gateway, dns_servers,
	coalesce(vlan_id::text,''), coalesce(parent_id::text,''), coalesce(location_id::text,''),
	status, ip_version, network_address, broadcast_address, mask, prefix_length,
	snmp_secret_ref, snmp_version, tags, created_by, created_at, updated_at`

func scanSubnet(sc scanner) (store.Subnet, error) {
	var s store.Subnet
	var tags []byte
	if err := sc.Scan(&s.ID, &s.TenantID, &s.Name, &s.CIDR, &s.Description, &s.Gateway, &s.DNSServers,
		&s.VlanID, &s.ParentID, &s.LocationID, &s.Status, &s.IPVersion, &s.NetworkAddress,
		&s.BroadcastAddr, &s.Mask, &s.PrefixLength, &s.SNMPSecretRef, &s.SNMPVersion, &tags,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return store.Subnet{}, err
	}
	s.Tags = unmarshalTags(tags)
	return s, nil
}

func computeSubnet(ctx context.Context, tx pgx.Tx, s *store.Subnet) error {
	var used int64
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND subnet_id=$2", s.TenantID, s.ID).Scan(&used); err != nil {
		return err
	}
	total := subnetTotal(s.CIDR)
	s.TotalAddresses = total
	s.UsedAddresses = used
	if total > 0 {
		s.AvailableAddresses = total - used
		s.Utilization = float64(used) / float64(total)
	}
	return nil
}

func (d *DB) CreateSubnet(ctx context.Context, s store.Subnet) error {
	if s.ID == "" {
		s.ID = store.NewID()
	}
	if s.Status == "" {
		s.Status = store.SubnetActive
	}
	if s.IPVersion == 0 {
		s.IPVersion = 4
	}
	now := time.Now().UTC()
	return d.tenant(ctx, s.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_subnets
			(id, tenant_id, name, cidr, description, gateway, dns_servers, vlan_id, parent_id, location_id,
			 status, ip_version, network_address, broadcast_address, mask, prefix_length, snmp_secret_ref,
			 snmp_version, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19::jsonb,$20,$21,$21)`,
			s.ID, s.TenantID, s.Name, s.CIDR, s.Description, s.Gateway, s.DNSServers, np(s.VlanID), np(s.ParentID),
			np(s.LocationID), s.Status, s.IPVersion, s.NetworkAddress, s.BroadcastAddr, s.Mask, s.PrefixLength,
			s.SNMPSecretRef, s.SNMPVersion, mustJSON(nzTags(s.Tags)), s.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetSubnet(ctx context.Context, tenantID, id string) (out store.Subnet, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		s, e := scanSubnet(tx.QueryRow(ctx, "SELECT "+subnetCols+" FROM ipam_subnets WHERE tenant_id=$1 AND id=$2", tenantID, id))
		if e != nil {
			return mapErr(e)
		}
		if e := computeSubnet(ctx, tx, &s); e != nil {
			return e
		}
		out = s
		return nil
	})
	return
}

func (d *DB) ListSubnets(ctx context.Context, tenantID string, f store.SubnetFilter) (out []store.Subnet, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + subnetCols + " FROM ipam_subnets WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.VlanID != "" {
			add(" AND vlan_id = $%d", f.VlanID)
		}
		if f.ParentID != "" {
			add(" AND parent_id = $%d", f.ParentID)
		}
		if f.LocationID != "" {
			add(" AND location_id = $%d", f.LocationID)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.IPVersion != 0 {
			add(" AND ip_version = $%d", f.IPVersion)
		}
		if f.Query != "" {
			args = append(args, "%"+f.Query+"%")
			b.WriteString(fmt.Sprintf(" AND (name ILIKE $%d OR cidr ILIKE $%d)", len(args), len(args)))
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var list []store.Subnet
		for rows.Next() {
			s, e := scanSubnet(rows)
			if e != nil {
				return e
			}
			list = append(list, s)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		for i := range list {
			if e := computeSubnet(ctx, tx, &list[i]); e != nil {
				return e
			}
		}
		out = list
		return nil
	})
	return
}

func (d *DB) UpdateSubnet(ctx context.Context, s store.Subnet) error {
	now := time.Now().UTC()
	return d.tenant(ctx, s.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_subnets SET
			name=$3, cidr=$4, description=$5, gateway=$6, dns_servers=$7, vlan_id=$8, parent_id=$9,
			location_id=$10, status=$11, ip_version=$12, network_address=$13, broadcast_address=$14,
			mask=$15, prefix_length=$16, snmp_secret_ref=$17, snmp_version=$18, tags=$19::jsonb,
			created_by=$20, updated_at=$21
			WHERE tenant_id=$1 AND id=$2`,
			s.TenantID, s.ID, s.Name, s.CIDR, s.Description, s.Gateway, s.DNSServers, np(s.VlanID), np(s.ParentID),
			np(s.LocationID), s.Status, s.IPVersion, s.NetworkAddress, s.BroadcastAddr, s.Mask, s.PrefixLength,
			s.SNMPSecretRef, s.SNMPVersion, mustJSON(nzTags(s.Tags)), s.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteSubnet(ctx context.Context, tenantID, id string, force bool) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var n int64
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND subnet_id=$2", tenantID, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 && !force {
			return repo.ErrNotEmpty
		}
		// FK ON DELETE CASCADE removes addresses and scan jobs.
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_subnets WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) CountAddressesInSubnet(ctx context.Context, tenantID, subnetID string) (n int64, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND subnet_id=$2", tenantID, subnetID).Scan(&n)
	})
	return
}

func (d *DB) ListAllocatedAddresses(ctx context.Context, tenantID, subnetID string) (out []string, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT address FROM ipam_ip_addresses WHERE tenant_id=$1 AND subnet_id=$2 ORDER BY address", tenantID, subnetID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			if e := rows.Scan(&a); e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return
}

func (d *DB) SubnetsForVlan(ctx context.Context, tenantID, vlanID string) (out []store.Subnet, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+subnetCols+" FROM ipam_subnets WHERE tenant_id=$1 AND vlan_id=$2 ORDER BY id DESC", tenantID, vlanID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			s, e := scanSubnet(rows)
			if e != nil {
				return e
			}
			out = append(out, s)
		}
		if e := rows.Err(); e != nil {
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

func (d *DB) AllSubnetCIDRs(ctx context.Context, tenantID string) (out []store.Subnet, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+subnetCols+" FROM ipam_subnets WHERE tenant_id=$1 ORDER BY id DESC", tenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			s, e := scanSubnet(rows)
			if e != nil {
				return e
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return
}

// ---- ip addresses

const addrCols = `id, tenant_id, address, subnet_id, hostname, mac_address, description,
	coalesce(device_id::text,''), interface_name, status, address_type, is_primary, ptr_record,
	dns_name, owner, last_seen, lease_expiry, has_reverse_dns, note, tags, created_by, created_at, updated_at`

func scanAddress(sc scanner) (store.IPAddress, error) {
	var a store.IPAddress
	var tags []byte
	if err := sc.Scan(&a.ID, &a.TenantID, &a.Address, &a.SubnetID, &a.Hostname, &a.MACAddress, &a.Description,
		&a.DeviceID, &a.InterfaceName, &a.Status, &a.AddressType, &a.IsPrimary, &a.PTRRecord, &a.DNSName,
		&a.Owner, &a.LastSeen, &a.LeaseExpiry, &a.HasReverseDNS, &a.Note, &tags, &a.CreatedBy,
		&a.CreatedAt, &a.UpdatedAt); err != nil {
		return store.IPAddress{}, err
	}
	a.Tags = unmarshalTags(tags)
	return a, nil
}

func (d *DB) CreateAddress(ctx context.Context, a store.IPAddress) error {
	if a.ID == "" {
		a.ID = store.NewID()
	}
	if a.Status == "" {
		a.Status = store.IPActive
	}
	if a.AddressType == "" {
		a.AddressType = store.AddrHost
	}
	now := time.Now().UTC()
	return d.tenant(ctx, a.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_ip_addresses
			(id, tenant_id, address, subnet_id, hostname, mac_address, description, device_id, interface_name,
			 status, address_type, is_primary, ptr_record, dns_name, owner, last_seen, lease_expiry,
			 has_reverse_dns, note, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb,$21,$22,$22)`,
			a.ID, a.TenantID, a.Address, a.SubnetID, a.Hostname, a.MACAddress, a.Description, np(a.DeviceID),
			a.InterfaceName, a.Status, a.AddressType, a.IsPrimary, a.PTRRecord, a.DNSName, a.Owner, a.LastSeen,
			a.LeaseExpiry, a.HasReverseDNS, a.Note, mustJSON(nzTags(a.Tags)), a.CreatedBy, now)
		return mapErr(e) // 23505 -> ErrConflict (duplicate-IP / allocation guard)
	})
}

func (d *DB) GetAddress(ctx context.Context, tenantID, id string) (out store.IPAddress, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanAddress(tx.QueryRow(ctx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND id=$2", tenantID, id))
		return mapErr(e)
	})
	return
}

func (d *DB) FindAddress(ctx context.Context, tenantID, address string) (out store.IPAddress, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanAddress(tx.QueryRow(ctx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND address=$2", tenantID, address))
		return mapErr(e)
	})
	return
}

func (d *DB) ListAddresses(ctx context.Context, tenantID string, f store.AddressFilter) (out []store.IPAddress, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + addrCols + " FROM ipam_ip_addresses WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.SubnetID != "" {
			add(" AND subnet_id = $%d", f.SubnetID)
		}
		if f.DeviceID != "" {
			add(" AND device_id = $%d", f.DeviceID)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.AddressType != "" {
			add(" AND address_type = $%d", f.AddressType)
		}
		if f.AddressPrefix != "" {
			add(" AND address LIKE $%d", f.AddressPrefix+"%")
		}
		if f.HostnamePattern != "" {
			add(" AND hostname ILIKE $%d", "%"+f.HostnamePattern+"%")
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			a, e := scanAddress(rows)
			if e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return
}

func (d *DB) UpdateAddress(ctx context.Context, a store.IPAddress) error {
	now := time.Now().UTC()
	return d.tenant(ctx, a.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_ip_addresses SET
			address=$3, subnet_id=$4, hostname=$5, mac_address=$6, description=$7, device_id=$8,
			interface_name=$9, status=$10, address_type=$11, is_primary=$12, ptr_record=$13, dns_name=$14,
			owner=$15, last_seen=$16, lease_expiry=$17, has_reverse_dns=$18, note=$19, tags=$20::jsonb,
			created_by=$21, updated_at=$22
			WHERE tenant_id=$1 AND id=$2`,
			a.TenantID, a.ID, a.Address, a.SubnetID, a.Hostname, a.MACAddress, a.Description, np(a.DeviceID),
			a.InterfaceName, a.Status, a.AddressType, a.IsPrimary, a.PTRRecord, a.DNSName, a.Owner, a.LastSeen,
			a.LeaseExpiry, a.HasReverseDNS, a.Note, mustJSON(nzTags(a.Tags)), a.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteAddress(ctx context.Context, tenantID, id string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_ip_addresses WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) UpsertAddressByAddress(ctx context.Context, a store.IPAddress) (created bool, err error) {
	if a.ID == "" {
		a.ID = store.NewID()
	}
	if a.Status == "" {
		a.Status = store.IPActive
	}
	if a.AddressType == "" {
		a.AddressType = store.AddrHost
	}
	now := time.Now().UTC()
	err = d.tenant(ctx, a.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO ipam_ip_addresses
			(id, tenant_id, address, subnet_id, hostname, mac_address, description, device_id, interface_name,
			 status, address_type, is_primary, ptr_record, dns_name, owner, last_seen, lease_expiry,
			 has_reverse_dns, note, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb,$21,$22,$22)
			ON CONFLICT (tenant_id, address) DO UPDATE SET
				subnet_id=EXCLUDED.subnet_id, hostname=EXCLUDED.hostname, mac_address=EXCLUDED.mac_address,
				description=EXCLUDED.description, device_id=EXCLUDED.device_id, interface_name=EXCLUDED.interface_name,
				status=EXCLUDED.status, address_type=EXCLUDED.address_type, is_primary=EXCLUDED.is_primary,
				ptr_record=EXCLUDED.ptr_record, dns_name=EXCLUDED.dns_name, owner=EXCLUDED.owner,
				last_seen=EXCLUDED.last_seen, lease_expiry=EXCLUDED.lease_expiry, has_reverse_dns=EXCLUDED.has_reverse_dns,
				note=EXCLUDED.note, tags=EXCLUDED.tags, updated_at=EXCLUDED.updated_at
			RETURNING (xmax = 0)`,
			a.ID, a.TenantID, a.Address, a.SubnetID, a.Hostname, a.MACAddress, a.Description, np(a.DeviceID),
			a.InterfaceName, a.Status, a.AddressType, a.IsPrimary, a.PTRRecord, a.DNSName, a.Owner, a.LastSeen,
			a.LeaseExpiry, a.HasReverseDNS, a.Note, mustJSON(nzTags(a.Tags)), a.CreatedBy, now).Scan(&created)
	})
	return
}

func (d *DB) AddressesForDevice(ctx context.Context, tenantID, deviceID string) (out []store.IPAddress, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+addrCols+" FROM ipam_ip_addresses WHERE tenant_id=$1 AND device_id=$2 ORDER BY id DESC", tenantID, deviceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			a, e := scanAddress(rows)
			if e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return
}

// ---- devices

const deviceCols = `id, tenant_id, name, device_type, description, manufacturer, model, serial_number,
	asset_tag, coalesce(location_id::text,''), rack_id, rack_position, device_height_u, status, primary_ip,
	primary_ipv6, management_ip, os_type, os_version, firmware_version, contact, last_seen, ipmi_secret_ref,
	reboot_required, unattended_upgrades, tags, created_by, created_at, updated_at`

func scanDevice(sc scanner) (store.Device, error) {
	var d store.Device
	var tags []byte
	if err := sc.Scan(&d.ID, &d.TenantID, &d.Name, &d.DeviceType, &d.Description, &d.Manufacturer, &d.Model,
		&d.SerialNumber, &d.AssetTag, &d.LocationID, &d.RackID, &d.RackPosition, &d.DeviceHeightU, &d.Status,
		&d.PrimaryIP, &d.PrimaryIPv6, &d.ManagementIP, &d.OSType, &d.OSVersion, &d.FirmwareVersion, &d.Contact,
		&d.LastSeen, &d.IPMISecretRef, &d.RebootRequired, &d.UnattendedUpgrades, &tags, &d.CreatedBy,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		return store.Device{}, err
	}
	d.Tags = unmarshalTags(tags)
	return d, nil
}

func computeDevice(ctx context.Context, tx pgx.Tx, d *store.Device) error {
	return tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM ipam_device_interfaces WHERE tenant_id=$1 AND device_id=$2),
		(SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND device_id=$2),
		(SELECT count(*) FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2 AND needs_update),
		(SELECT count(*) FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2 AND is_security_update)`,
		d.TenantID, d.ID).Scan(&d.InterfaceCount, &d.AddressCount, &d.PackageUpdateCount, &d.SecurityUpdateCount)
}

func deviceInsertArgs(d store.Device, now time.Time) []any {
	return []any{d.ID, d.TenantID, d.Name, d.DeviceType, d.Description, d.Manufacturer, d.Model, d.SerialNumber,
		d.AssetTag, np(d.LocationID), d.RackID, d.RackPosition, d.DeviceHeightU, d.Status, d.PrimaryIP,
		d.PrimaryIPv6, d.ManagementIP, d.OSType, d.OSVersion, d.FirmwareVersion, d.Contact, d.LastSeen,
		d.IPMISecretRef, d.RebootRequired, d.UnattendedUpgrades, mustJSON(nzTags(d.Tags)), d.CreatedBy, now}
}

const deviceInsertSQL = `INSERT INTO ipam_devices
	(id, tenant_id, name, device_type, description, manufacturer, model, serial_number, asset_tag, location_id,
	 rack_id, rack_position, device_height_u, status, primary_ip, primary_ipv6, management_ip, os_type,
	 os_version, firmware_version, contact, last_seen, ipmi_secret_ref, reboot_required, unattended_upgrades,
	 tags, created_by, created_at, updated_at)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26::jsonb,$27,$28,$28)`

func (d *DB) CreateDevice(ctx context.Context, dev store.Device) error {
	if dev.ID == "" {
		dev.ID = store.NewID()
	}
	if dev.Status == "" {
		dev.Status = store.DevStActive
	}
	if dev.DeviceType == "" {
		dev.DeviceType = store.DevOther
	}
	now := time.Now().UTC()
	return d.tenant(ctx, dev.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, deviceInsertSQL, deviceInsertArgs(dev, now)...)
		return mapErr(e)
	})
}

func (d *DB) GetDevice(ctx context.Context, tenantID, id string) (out store.Device, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		dev, e := scanDevice(tx.QueryRow(ctx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND id=$2", tenantID, id))
		if e != nil {
			return mapErr(e)
		}
		if e := computeDevice(ctx, tx, &dev); e != nil {
			return e
		}
		out = dev
		return nil
	})
	return
}

func (d *DB) ListDevices(ctx context.Context, tenantID string, f store.DeviceFilter) (out []store.Device, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + deviceCols + " FROM ipam_devices WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.DeviceType != "" {
			add(" AND device_type = $%d", f.DeviceType)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.LocationID != "" {
			add(" AND location_id = $%d", f.LocationID)
		}
		if f.Manufacturer != "" {
			add(" AND manufacturer = $%d", f.Manufacturer)
		}
		if f.RackID != "" {
			add(" AND rack_id = $%d", f.RackID)
		}
		if f.Query != "" {
			args = append(args, "%"+f.Query+"%")
			b.WriteString(fmt.Sprintf(" AND (name ILIKE $%d OR primary_ip ILIKE $%d)", len(args), len(args)))
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var list []store.Device
		for rows.Next() {
			dev, e := scanDevice(rows)
			if e != nil {
				return e
			}
			list = append(list, dev)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		for i := range list {
			if e := computeDevice(ctx, tx, &list[i]); e != nil {
				return e
			}
		}
		out = list
		return nil
	})
	return
}

func (d *DB) UpdateDevice(ctx context.Context, dev store.Device) error {
	now := time.Now().UTC()
	return d.tenant(ctx, dev.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_devices SET
			name=$3, device_type=$4, description=$5, manufacturer=$6, model=$7, serial_number=$8, asset_tag=$9,
			location_id=$10, rack_id=$11, rack_position=$12, device_height_u=$13, status=$14, primary_ip=$15,
			primary_ipv6=$16, management_ip=$17, os_type=$18, os_version=$19, firmware_version=$20, contact=$21,
			last_seen=$22, ipmi_secret_ref=$23, reboot_required=$24, unattended_upgrades=$25, tags=$26::jsonb,
			created_by=$27, updated_at=$28
			WHERE tenant_id=$1 AND id=$2`,
			dev.TenantID, dev.ID, dev.Name, dev.DeviceType, dev.Description, dev.Manufacturer, dev.Model,
			dev.SerialNumber, dev.AssetTag, np(dev.LocationID), dev.RackID, dev.RackPosition, dev.DeviceHeightU,
			dev.Status, dev.PrimaryIP, dev.PrimaryIPv6, dev.ManagementIP, dev.OSType, dev.OSVersion,
			dev.FirmwareVersion, dev.Contact, dev.LastSeen, dev.IPMISecretRef, dev.RebootRequired,
			dev.UnattendedUpgrades, mustJSON(nzTags(dev.Tags)), dev.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteDevice(ctx context.Context, tenantID, id string, force bool) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var ifaces, addrs int64
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_device_interfaces WHERE tenant_id=$1 AND device_id=$2", tenantID, id).Scan(&ifaces); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_ip_addresses WHERE tenant_id=$1 AND device_id=$2", tenantID, id).Scan(&addrs); e != nil {
			return e
		}
		if (ifaces > 0 || addrs > 0) && !force {
			return repo.ErrNotEmpty
		}
		// FK cascades remove interfaces/links/packages/host-group members;
		// addresses.device_id is SET NULL by its FK.
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_devices WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

// mergeDevice overlays the non-empty/non-zero summary fields of src onto dst. It
// is the scan-path merge: a discovery update never clobbers a known field with a
// blank/zero value.
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

func (d *DB) UpsertDeviceByName(ctx context.Context, dev store.Device) (out store.Device, err error) {
	now := time.Now().UTC()
	err = d.tenant(ctx, dev.TenantID, func(tx pgx.Tx) error {
		existing, e := scanDevice(tx.QueryRow(ctx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND name=$2 FOR UPDATE", dev.TenantID, dev.Name))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if errors.Is(e, pgx.ErrNoRows) {
			nd := dev
			if nd.ID == "" {
				nd.ID = store.NewID()
			}
			if nd.Status == "" {
				nd.Status = store.DevStActive
			}
			if nd.DeviceType == "" {
				nd.DeviceType = store.DevOther
			}
			if _, e := tx.Exec(ctx, deviceInsertSQL, deviceInsertArgs(nd, now)...); e != nil {
				return mapErr(e)
			}
			out = nd
			return computeDevice(ctx, tx, &out)
		}
		m := mergeDevice(existing, dev)
		if _, e := tx.Exec(ctx, `UPDATE ipam_devices SET
			device_type=$3, description=$4, manufacturer=$5, model=$6, serial_number=$7, asset_tag=$8,
			location_id=$9, rack_id=$10, rack_position=$11, device_height_u=$12, status=$13, primary_ip=$14,
			primary_ipv6=$15, management_ip=$16, os_type=$17, os_version=$18, firmware_version=$19, contact=$20,
			last_seen=$21, ipmi_secret_ref=$22, reboot_required=$23, unattended_upgrades=$24, tags=$25::jsonb, updated_at=$26
			WHERE tenant_id=$1 AND id=$2`,
			m.TenantID, existing.ID, m.DeviceType, m.Description, m.Manufacturer, m.Model, m.SerialNumber, m.AssetTag,
			np(m.LocationID), m.RackID, m.RackPosition, m.DeviceHeightU, m.Status, m.PrimaryIP, m.PrimaryIPv6,
			m.ManagementIP, m.OSType, m.OSVersion, m.FirmwareVersion, m.Contact, m.LastSeen, m.IPMISecretRef,
			m.RebootRequired, m.UnattendedUpgrades, mustJSON(nzTags(m.Tags)), now); e != nil {
			return mapErr(e)
		}
		m.ID = existing.ID
		m.CreatedAt = existing.CreatedAt
		out = m
		return computeDevice(ctx, tx, &out)
	})
	return
}

// ---- device interfaces + links

const ifaceCols = `id, tenant_id, device_id, name, mac_address, interface_type, enabled, speed_mbps,
	description, if_index, remote_device_id, remote_interface_id, remote_port_name, link_source, link_vlan,
	link_last_seen, created_at, updated_at`

func scanIface(sc scanner) (store.DeviceInterface, error) {
	var i store.DeviceInterface
	if err := sc.Scan(&i.ID, &i.TenantID, &i.DeviceID, &i.Name, &i.MACAddress, &i.InterfaceType, &i.Enabled,
		&i.SpeedMbps, &i.Description, &i.IfIndex, &i.RemoteDeviceID, &i.RemoteInterfaceID, &i.RemotePortName,
		&i.LinkSource, &i.LinkVlan, &i.LinkLastSeen, &i.CreatedAt, &i.UpdatedAt); err != nil {
		return store.DeviceInterface{}, err
	}
	return i, nil
}

func ifaceInsertArgs(i store.DeviceInterface, now time.Time) []any {
	return []any{i.ID, i.TenantID, i.DeviceID, i.Name, i.MACAddress, i.InterfaceType, i.Enabled, i.SpeedMbps,
		i.Description, i.IfIndex, i.RemoteDeviceID, i.RemoteInterfaceID, i.RemotePortName, i.LinkSource,
		i.LinkVlan, i.LinkLastSeen, now}
}

const ifaceInsertSQL = `INSERT INTO ipam_device_interfaces
	(id, tenant_id, device_id, name, mac_address, interface_type, enabled, speed_mbps, description, if_index,
	 remote_device_id, remote_interface_id, remote_port_name, link_source, link_vlan, link_last_seen,
	 created_at, updated_at)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`

func (d *DB) CreateInterface(ctx context.Context, i store.DeviceInterface) error {
	if i.ID == "" {
		i.ID = store.NewID()
	}
	now := time.Now().UTC()
	return d.tenant(ctx, i.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, ifaceInsertSQL, ifaceInsertArgs(i, now)...)
		return mapErr(e)
	})
}

func (d *DB) GetInterface(ctx context.Context, tenantID, id string) (out store.DeviceInterface, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanIface(tx.QueryRow(ctx, "SELECT "+ifaceCols+" FROM ipam_device_interfaces WHERE tenant_id=$1 AND id=$2", tenantID, id))
		return mapErr(e)
	})
	return
}

func (d *DB) ListInterfaces(ctx context.Context, tenantID, deviceID string) (out []store.DeviceInterface, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+ifaceCols+" FROM ipam_device_interfaces WHERE tenant_id=$1 AND device_id=$2 ORDER BY if_index, name", tenantID, deviceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			i, e := scanIface(rows)
			if e != nil {
				return e
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return
}

func (d *DB) UpsertInterfaceByName(ctx context.Context, i store.DeviceInterface) (out store.DeviceInterface, err error) {
	if i.ID == "" {
		i.ID = store.NewID()
	}
	now := time.Now().UTC()
	err = d.tenant(ctx, i.TenantID, func(tx pgx.Tx) error {
		q := ifaceInsertSQL + `
			ON CONFLICT (device_id, name) DO UPDATE SET
				mac_address=EXCLUDED.mac_address, interface_type=EXCLUDED.interface_type, enabled=EXCLUDED.enabled,
				speed_mbps=EXCLUDED.speed_mbps, description=EXCLUDED.description, if_index=EXCLUDED.if_index,
				remote_device_id=EXCLUDED.remote_device_id, remote_interface_id=EXCLUDED.remote_interface_id,
				remote_port_name=EXCLUDED.remote_port_name, link_source=EXCLUDED.link_source,
				link_vlan=EXCLUDED.link_vlan, link_last_seen=EXCLUDED.link_last_seen, updated_at=EXCLUDED.updated_at
			RETURNING ` + ifaceCols
		var e error
		out, e = scanIface(tx.QueryRow(ctx, q, ifaceInsertArgs(i, now)...))
		return mapErr(e)
	})
	return
}

func (d *DB) DeleteInterface(ctx context.Context, tenantID, id string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_device_interfaces WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

const linkCols = `id, tenant_id, interface_id, remote_device_id, remote_interface_id, remote_port_name,
	link_source, link_vlan, link_last_seen`

func scanLink(sc scanner) (store.DeviceInterfaceLink, error) {
	var l store.DeviceInterfaceLink
	if err := sc.Scan(&l.ID, &l.TenantID, &l.InterfaceID, &l.RemoteDeviceID, &l.RemoteInterfaceID,
		&l.RemotePortName, &l.LinkSource, &l.LinkVlan, &l.LinkLastSeen); err != nil {
		return store.DeviceInterfaceLink{}, err
	}
	return l, nil
}

func (d *DB) ReplaceInterfaceLinks(ctx context.Context, tenantID, interfaceID string, links []store.DeviceInterfaceLink) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Verify the interface exists in this tenant.
		var exists bool
		if e := tx.QueryRow(ctx, "SELECT true FROM ipam_device_interfaces WHERE tenant_id=$1 AND id=$2", tenantID, interfaceID).Scan(&exists); e != nil {
			return mapErr(e)
		}
		if _, e := tx.Exec(ctx, "DELETE FROM ipam_device_interface_links WHERE tenant_id=$1 AND interface_id=$2", tenantID, interfaceID); e != nil {
			return mapErr(e)
		}
		for _, l := range links {
			id := l.ID
			if id == "" {
				id = store.NewID()
			}
			if _, e := tx.Exec(ctx, `INSERT INTO ipam_device_interface_links
				(id, tenant_id, interface_id, remote_device_id, remote_interface_id, remote_port_name, link_source, link_vlan, link_last_seen)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				id, tenantID, interfaceID, l.RemoteDeviceID, l.RemoteInterfaceID, l.RemotePortName, l.LinkSource, l.LinkVlan, l.LinkLastSeen); e != nil {
				return mapErr(e)
			}
		}
		return nil
	})
}

func (d *DB) ListInterfaceLinks(ctx context.Context, tenantID, interfaceID string) (out []store.DeviceInterfaceLink, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+linkCols+" FROM ipam_device_interface_links WHERE tenant_id=$1 AND interface_id=$2 ORDER BY id", tenantID, interfaceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			l, e := scanLink(rows)
			if e != nil {
				return e
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return
}

// ---- device packages

const pkgCols = `id, tenant_id, device_id, name, current_version, available_version, needs_update,
	is_security_update, package_manager, description, created_at, updated_at`

func scanPkg(sc scanner) (store.DevicePackage, error) {
	var p store.DevicePackage
	if err := sc.Scan(&p.ID, &p.TenantID, &p.DeviceID, &p.Name, &p.CurrentVersion, &p.AvailableVersion,
		&p.NeedsUpdate, &p.IsSecurityUpdate, &p.PackageManager, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return store.DevicePackage{}, err
	}
	return p, nil
}

func (d *DB) ReplaceDevicePackages(ctx context.Context, tenantID, deviceID string, pkgs []store.DevicePackage) error {
	now := time.Now().UTC()
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "DELETE FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2", tenantID, deviceID); e != nil {
			return mapErr(e)
		}
		for _, p := range pkgs {
			id := p.ID
			if id == "" {
				id = store.NewID()
			}
			if _, e := tx.Exec(ctx, `INSERT INTO ipam_device_packages
				(id, tenant_id, device_id, name, current_version, available_version, needs_update, is_security_update, package_manager, description, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
				id, tenantID, deviceID, p.Name, p.CurrentVersion, p.AvailableVersion, p.NeedsUpdate, p.IsSecurityUpdate, p.PackageManager, p.Description, now); e != nil {
				return mapErr(e)
			}
		}
		return nil
	})
}

func (d *DB) ListDevicePackages(ctx context.Context, tenantID, deviceID string, needsUpdate, securityOnly *bool, manager string) (out []store.DevicePackage, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + pkgCols + " FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2")
		args := []any{tenantID, deviceID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if needsUpdate != nil {
			add(" AND needs_update = $%d", *needsUpdate)
		}
		if securityOnly != nil {
			add(" AND is_security_update = $%d", *securityOnly)
		}
		if manager != "" {
			add(" AND package_manager = $%d", manager)
		}
		b.WriteString(" ORDER BY name")
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			p, e := scanPkg(rows)
			if e != nil {
				return e
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return
}

func (d *DB) DeleteDevicePackages(ctx context.Context, tenantID, deviceID string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "DELETE FROM ipam_device_packages WHERE tenant_id=$1 AND device_id=$2", tenantID, deviceID)
		return mapErr(e)
	})
}

// ---- vlans

const vlanCols = `id, tenant_id, vlan_id, name, description, domain, coalesce(location_id::text,''),
	status, tags, created_by, created_at, updated_at`

func scanVlan(sc scanner) (store.Vlan, error) {
	var v store.Vlan
	var tags []byte
	if err := sc.Scan(&v.ID, &v.TenantID, &v.VlanID, &v.Name, &v.Description, &v.Domain, &v.LocationID,
		&v.Status, &tags, &v.CreatedBy, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return store.Vlan{}, err
	}
	v.Tags = unmarshalTags(tags)
	return v, nil
}

func computeVlan(ctx context.Context, tx pgx.Tx, v *store.Vlan) error {
	return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_subnets WHERE tenant_id=$1 AND vlan_id=$2", v.TenantID, v.ID).Scan(&v.SubnetCount)
}

func (d *DB) CreateVlan(ctx context.Context, v store.Vlan) error {
	if v.ID == "" {
		v.ID = store.NewID()
	}
	if v.Status == "" {
		v.Status = store.VlanActive
	}
	now := time.Now().UTC()
	return d.tenant(ctx, v.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_vlans
			(id, tenant_id, vlan_id, name, description, domain, location_id, status, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$11)`,
			v.ID, v.TenantID, v.VlanID, v.Name, v.Description, v.Domain, np(v.LocationID), v.Status, mustJSON(nzTags(v.Tags)), v.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetVlan(ctx context.Context, tenantID, id string) (out store.Vlan, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, e := scanVlan(tx.QueryRow(ctx, "SELECT "+vlanCols+" FROM ipam_vlans WHERE tenant_id=$1 AND id=$2", tenantID, id))
		if e != nil {
			return mapErr(e)
		}
		if e := computeVlan(ctx, tx, &v); e != nil {
			return e
		}
		out = v
		return nil
	})
	return
}

func (d *DB) ListVlans(ctx context.Context, tenantID string, f store.VlanFilter) (out []store.Vlan, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + vlanCols + " FROM ipam_vlans WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.LocationID != "" {
			add(" AND location_id = $%d", f.LocationID)
		}
		if f.Domain != "" {
			add(" AND domain = $%d", f.Domain)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.VlanIDMin != 0 {
			add(" AND vlan_id >= $%d", f.VlanIDMin)
		}
		if f.VlanIDMax != 0 {
			add(" AND vlan_id <= $%d", f.VlanIDMax)
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var list []store.Vlan
		for rows.Next() {
			v, e := scanVlan(rows)
			if e != nil {
				return e
			}
			list = append(list, v)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		for i := range list {
			if e := computeVlan(ctx, tx, &list[i]); e != nil {
				return e
			}
		}
		out = list
		return nil
	})
	return
}

func (d *DB) UpdateVlan(ctx context.Context, v store.Vlan) error {
	now := time.Now().UTC()
	return d.tenant(ctx, v.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_vlans SET
			vlan_id=$3, name=$4, description=$5, domain=$6, location_id=$7, status=$8, tags=$9::jsonb, created_by=$10, updated_at=$11
			WHERE tenant_id=$1 AND id=$2`,
			v.TenantID, v.ID, v.VlanID, v.Name, v.Description, v.Domain, np(v.LocationID), v.Status, mustJSON(nzTags(v.Tags)), v.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteVlan(ctx context.Context, tenantID, id string, force bool) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var n int64
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_subnets WHERE tenant_id=$1 AND vlan_id=$2", tenantID, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 && !force {
			return repo.ErrNotEmpty
		}
		// Subnets' vlan_id is SET NULL by its FK.
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_vlans WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

// ---- locations

const locCols = `id, tenant_id, name, code, location_type, description, coalesce(parent_id::text,''),
	path, address, city, state, country, postal_code, latitude, longitude, contact, phone, email, status,
	rack_size_u, tags, created_by, created_at, updated_at`

func scanLocation(sc scanner) (store.Location, error) {
	var l store.Location
	var tags []byte
	if err := sc.Scan(&l.ID, &l.TenantID, &l.Name, &l.Code, &l.LocationType, &l.Description, &l.ParentID,
		&l.Path, &l.Address, &l.City, &l.State, &l.Country, &l.PostalCode, &l.Latitude, &l.Longitude,
		&l.Contact, &l.Phone, &l.Email, &l.Status, &l.RackSizeU, &tags, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return store.Location{}, err
	}
	l.Tags = unmarshalTags(tags)
	return l, nil
}

func computeLocation(ctx context.Context, tx pgx.Tx, l *store.Location) error {
	return tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM ipam_locations WHERE tenant_id=$1 AND parent_id=$2),
		(SELECT count(*) FROM ipam_devices WHERE tenant_id=$1 AND location_id=$2),
		(SELECT count(*) FROM ipam_subnets WHERE tenant_id=$1 AND location_id=$2),
		(SELECT count(*) FROM ipam_vlans WHERE tenant_id=$1 AND location_id=$2)`,
		l.TenantID, l.ID).Scan(&l.ChildCount, &l.DeviceCount, &l.SubnetCount, &l.VlanCount)
}

func (d *DB) CreateLocation(ctx context.Context, l store.Location) error {
	if l.ID == "" {
		l.ID = store.NewID()
	}
	if l.Status == "" {
		l.Status = store.LocStActive
	}
	now := time.Now().UTC()
	return d.tenant(ctx, l.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_locations
			(id, tenant_id, name, code, location_type, description, parent_id, path, address, city, state,
			 country, postal_code, latitude, longitude, contact, phone, email, status, rack_size_u, tags,
			 created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::jsonb,$22,$23,$23)`,
			l.ID, l.TenantID, l.Name, l.Code, l.LocationType, l.Description, np(l.ParentID), l.Path, l.Address,
			l.City, l.State, l.Country, l.PostalCode, l.Latitude, l.Longitude, l.Contact, l.Phone, l.Email,
			l.Status, l.RackSizeU, mustJSON(nzTags(l.Tags)), l.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetLocation(ctx context.Context, tenantID, id string) (out store.Location, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		l, e := scanLocation(tx.QueryRow(ctx, "SELECT "+locCols+" FROM ipam_locations WHERE tenant_id=$1 AND id=$2", tenantID, id))
		if e != nil {
			return mapErr(e)
		}
		if e := computeLocation(ctx, tx, &l); e != nil {
			return e
		}
		out = l
		return nil
	})
	return
}

func (d *DB) ListLocations(ctx context.Context, tenantID string, f store.LocationFilter) (out []store.Location, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + locCols + " FROM ipam_locations WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.ParentID != "" {
			add(" AND parent_id = $%d", f.ParentID)
		}
		if f.LocationType != "" {
			add(" AND location_type = $%d", f.LocationType)
		}
		if f.Country != "" {
			add(" AND country = $%d", f.Country)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		var list []store.Location
		for rows.Next() {
			l, e := scanLocation(rows)
			if e != nil {
				return e
			}
			list = append(list, l)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		for i := range list {
			if e := computeLocation(ctx, tx, &list[i]); e != nil {
				return e
			}
		}
		out = list
		return nil
	})
	return
}

func (d *DB) UpdateLocation(ctx context.Context, l store.Location) error {
	now := time.Now().UTC()
	return d.tenant(ctx, l.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_locations SET
			name=$3, code=$4, location_type=$5, description=$6, parent_id=$7, path=$8, address=$9, city=$10,
			state=$11, country=$12, postal_code=$13, latitude=$14, longitude=$15, contact=$16, phone=$17,
			email=$18, status=$19, rack_size_u=$20, tags=$21::jsonb, created_by=$22, updated_at=$23
			WHERE tenant_id=$1 AND id=$2`,
			l.TenantID, l.ID, l.Name, l.Code, l.LocationType, l.Description, np(l.ParentID), l.Path, l.Address,
			l.City, l.State, l.Country, l.PostalCode, l.Latitude, l.Longitude, l.Contact, l.Phone, l.Email,
			l.Status, l.RackSizeU, mustJSON(nzTags(l.Tags)), l.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteLocation(ctx context.Context, tenantID, id string, force bool) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var n int64
		if e := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM ipam_locations WHERE tenant_id=$1 AND parent_id=$2) +
			(SELECT count(*) FROM ipam_devices WHERE tenant_id=$1 AND location_id=$2) +
			(SELECT count(*) FROM ipam_subnets WHERE tenant_id=$1 AND location_id=$2) +
			(SELECT count(*) FROM ipam_vlans WHERE tenant_id=$1 AND location_id=$2)`, tenantID, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 && !force {
			return repo.ErrNotEmpty
		}
		// References are SET NULL by their FKs on delete.
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_locations WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

// ---- ip groups

const ipGroupCols = `g.id, g.tenant_id, g.name, g.description, g.status, g.tags, g.created_by, g.created_at, g.updated_at,
	(SELECT count(*) FROM ipam_ip_group_members mm WHERE mm.ip_group_id = g.id)`

func scanIPGroup(sc scanner) (store.IPGroup, error) {
	var g store.IPGroup
	var tags []byte
	if err := sc.Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &g.Status, &tags, &g.CreatedBy,
		&g.CreatedAt, &g.UpdatedAt, &g.MemberCount); err != nil {
		return store.IPGroup{}, err
	}
	g.Tags = unmarshalTags(tags)
	return g, nil
}

func (d *DB) CreateIPGroup(ctx context.Context, g store.IPGroup) error {
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	now := time.Now().UTC()
	return d.tenant(ctx, g.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_ip_groups
			(id, tenant_id, name, description, status, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$8)`,
			g.ID, g.TenantID, g.Name, g.Description, g.Status, mustJSON(nzTags(g.Tags)), g.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetIPGroup(ctx context.Context, tenantID, id string) (out store.IPGroup, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanIPGroup(tx.QueryRow(ctx, "SELECT "+ipGroupCols+" FROM ipam_ip_groups g WHERE g.tenant_id=$1 AND g.id=$2", tenantID, id))
		return mapErr(e)
	})
	return
}

func (d *DB) ListIPGroups(ctx context.Context, tenantID string, limit int, cursorID string) (out []store.IPGroup, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		q := "SELECT " + ipGroupCols + " FROM ipam_ip_groups g WHERE g.tenant_id=$1"
		args := []any{tenantID}
		if cursorID != "" {
			args = append(args, cursorID)
			q += fmt.Sprintf(" AND g.id < $%d", len(args))
		}
		q += " ORDER BY g.id DESC"
		if limit > 0 {
			args = append(args, limit)
			q += fmt.Sprintf(" LIMIT $%d", len(args))
		}
		rows, e := tx.Query(ctx, q, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			g, e := scanIPGroup(rows)
			if e != nil {
				return e
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return
}

func (d *DB) UpdateIPGroup(ctx context.Context, g store.IPGroup) error {
	now := time.Now().UTC()
	return d.tenant(ctx, g.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_ip_groups SET name=$3, description=$4, status=$5, tags=$6::jsonb, created_by=$7, updated_at=$8 WHERE tenant_id=$1 AND id=$2`,
			g.TenantID, g.ID, g.Name, g.Description, g.Status, mustJSON(nzTags(g.Tags)), g.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteIPGroup(ctx context.Context, tenantID, id string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_ip_groups WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) AddIPGroupMember(ctx context.Context, m store.IPGroupMember) error {
	if m.ID == "" {
		m.ID = store.NewID()
	}
	if m.MemberType == "" {
		m.MemberType = store.MemberAddress
	}
	return d.tenant(ctx, m.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_ip_group_members
			(id, tenant_id, ip_group_id, member_type, value, description, sequence)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			m.ID, m.TenantID, m.IPGroupID, m.MemberType, m.Value, m.Description, m.Sequence)
		return mapErr(e)
	})
}

func (d *DB) RemoveIPGroupMember(ctx context.Context, tenantID, memberID string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_ip_group_members WHERE tenant_id=$1 AND id=$2", tenantID, memberID)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) UpdateIPGroupMember(ctx context.Context, m store.IPGroupMember) error {
	return d.tenant(ctx, m.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_ip_group_members SET member_type=$3, value=$4, description=$5, sequence=$6 WHERE tenant_id=$1 AND id=$2`,
			m.TenantID, m.ID, m.MemberType, m.Value, m.Description, m.Sequence)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func scanIPMember(sc scanner) (store.IPGroupMember, error) {
	var m store.IPGroupMember
	if err := sc.Scan(&m.ID, &m.TenantID, &m.IPGroupID, &m.MemberType, &m.Value, &m.Description, &m.Sequence); err != nil {
		return store.IPGroupMember{}, err
	}
	return m, nil
}

const ipMemberCols = `id, tenant_id, ip_group_id, member_type, value, description, sequence`

func (d *DB) ListIPGroupMembers(ctx context.Context, tenantID, groupID string) (out []store.IPGroupMember, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+ipMemberCols+" FROM ipam_ip_group_members WHERE tenant_id=$1 AND ip_group_id=$2 ORDER BY sequence, id", tenantID, groupID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			m, e := scanIPMember(rows)
			if e != nil {
				return e
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return
}

func (d *DB) AllIPGroupsWithMembers(ctx context.Context, tenantID string, groupIDs []string) (groups []store.IPGroup, members map[string][]store.IPGroupMember, err error) {
	members = map[string][]store.IPGroupMember{}
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		gq := "SELECT " + ipGroupCols + " FROM ipam_ip_groups g WHERE g.tenant_id=$1"
		gargs := []any{tenantID}
		if len(groupIDs) > 0 {
			gargs = append(gargs, groupIDs)
			gq += fmt.Sprintf(" AND g.id = ANY($%d)", len(gargs))
		}
		gq += " ORDER BY g.id DESC"
		grows, e := tx.Query(ctx, gq, gargs...)
		if e != nil {
			return e
		}
		defer grows.Close()
		for grows.Next() {
			g, e := scanIPGroup(grows)
			if e != nil {
				return e
			}
			groups = append(groups, g)
		}
		if e := grows.Err(); e != nil {
			return e
		}
		mq := "SELECT " + ipMemberCols + " FROM ipam_ip_group_members WHERE tenant_id=$1"
		margs := []any{tenantID}
		if len(groupIDs) > 0 {
			margs = append(margs, groupIDs)
			mq += fmt.Sprintf(" AND ip_group_id = ANY($%d)", len(margs))
		}
		mq += " ORDER BY sequence, id"
		mrows, e := tx.Query(ctx, mq, margs...)
		if e != nil {
			return e
		}
		defer mrows.Close()
		for mrows.Next() {
			m, e := scanIPMember(mrows)
			if e != nil {
				return e
			}
			members[m.IPGroupID] = append(members[m.IPGroupID], m)
		}
		return mrows.Err()
	})
	return
}

// ---- host groups

const hostGroupCols = `g.id, g.tenant_id, g.name, g.description, g.status, g.tags, g.created_by, g.created_at, g.updated_at,
	(SELECT count(*) FROM ipam_host_group_members mm WHERE mm.host_group_id = g.id)`

func scanHostGroup(sc scanner) (store.HostGroup, error) {
	var g store.HostGroup
	var tags []byte
	if err := sc.Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &g.Status, &tags, &g.CreatedBy,
		&g.CreatedAt, &g.UpdatedAt, &g.MemberCount); err != nil {
		return store.HostGroup{}, err
	}
	g.Tags = unmarshalTags(tags)
	return g, nil
}

func (d *DB) CreateHostGroup(ctx context.Context, g store.HostGroup) error {
	if g.ID == "" {
		g.ID = store.NewID()
	}
	if g.Status == "" {
		g.Status = store.GroupActive
	}
	now := time.Now().UTC()
	return d.tenant(ctx, g.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_host_groups
			(id, tenant_id, name, description, status, tags, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$8)`,
			g.ID, g.TenantID, g.Name, g.Description, g.Status, mustJSON(nzTags(g.Tags)), g.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetHostGroup(ctx context.Context, tenantID, id string) (out store.HostGroup, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanHostGroup(tx.QueryRow(ctx, "SELECT "+hostGroupCols+" FROM ipam_host_groups g WHERE g.tenant_id=$1 AND g.id=$2", tenantID, id))
		return mapErr(e)
	})
	return
}

func (d *DB) ListHostGroups(ctx context.Context, tenantID string, limit int, cursorID string) (out []store.HostGroup, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		q := "SELECT " + hostGroupCols + " FROM ipam_host_groups g WHERE g.tenant_id=$1"
		args := []any{tenantID}
		if cursorID != "" {
			args = append(args, cursorID)
			q += fmt.Sprintf(" AND g.id < $%d", len(args))
		}
		q += " ORDER BY g.id DESC"
		if limit > 0 {
			args = append(args, limit)
			q += fmt.Sprintf(" LIMIT $%d", len(args))
		}
		rows, e := tx.Query(ctx, q, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			g, e := scanHostGroup(rows)
			if e != nil {
				return e
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return
}

func (d *DB) UpdateHostGroup(ctx context.Context, g store.HostGroup) error {
	now := time.Now().UTC()
	return d.tenant(ctx, g.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_host_groups SET name=$3, description=$4, status=$5, tags=$6::jsonb, created_by=$7, updated_at=$8 WHERE tenant_id=$1 AND id=$2`,
			g.TenantID, g.ID, g.Name, g.Description, g.Status, mustJSON(nzTags(g.Tags)), g.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) DeleteHostGroup(ctx context.Context, tenantID, id string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_host_groups WHERE tenant_id=$1 AND id=$2", tenantID, id)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) AddHostGroupMember(ctx context.Context, m store.HostGroupMember) error {
	if m.ID == "" {
		m.ID = store.NewID()
	}
	return d.tenant(ctx, m.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_host_group_members
			(id, tenant_id, host_group_id, device_id, sequence) VALUES ($1,$2,$3,$4,$5)`,
			m.ID, m.TenantID, m.HostGroupID, m.DeviceID, m.Sequence)
		return mapErr(e)
	})
}

func (d *DB) RemoveHostGroupMember(ctx context.Context, tenantID, memberID string) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_host_group_members WHERE tenant_id=$1 AND id=$2", tenantID, memberID)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) UpdateHostGroupMember(ctx context.Context, m store.HostGroupMember) error {
	return d.tenant(ctx, m.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_host_group_members SET device_id=$3, sequence=$4 WHERE tenant_id=$1 AND id=$2`,
			m.TenantID, m.ID, m.DeviceID, m.Sequence)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func scanHostMember(sc scanner) (store.HostGroupMember, error) {
	var m store.HostGroupMember
	if err := sc.Scan(&m.ID, &m.TenantID, &m.HostGroupID, &m.DeviceID, &m.Sequence,
		&m.DeviceName, &m.DeviceType, &m.DeviceStatus, &m.DevicePrimaryIP); err != nil {
		return store.HostGroupMember{}, err
	}
	return m, nil
}

const hostMemberCols = `m.id, m.tenant_id, m.host_group_id, m.device_id, m.sequence,
	coalesce(d.name,''), coalesce(d.device_type,''), coalesce(d.status,''), coalesce(d.primary_ip,'')`

func (d *DB) ListHostGroupMembers(ctx context.Context, tenantID, groupID string) (out []store.HostGroupMember, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+hostMemberCols+` FROM ipam_host_group_members m
			LEFT JOIN ipam_devices d ON d.id = m.device_id
			WHERE m.tenant_id=$1 AND m.host_group_id=$2 ORDER BY m.sequence, m.id`, tenantID, groupID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			m, e := scanHostMember(rows)
			if e != nil {
				return e
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return
}

func (d *DB) ListDeviceHostGroups(ctx context.Context, tenantID, deviceID string) (out []store.HostGroup, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+hostGroupCols+` FROM ipam_host_groups g
			WHERE g.tenant_id=$1 AND g.id IN (SELECT host_group_id FROM ipam_host_group_members WHERE tenant_id=$1 AND device_id=$2)
			ORDER BY g.id DESC`, tenantID, deviceID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			g, e := scanHostGroup(rows)
			if e != nil {
				return e
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return
}

// ---- scan jobs

const scanJobCols = `id, tenant_id, subnet_id, status, progress, status_message, total_addresses,
	scanned_count, alive_count, new_count, updated_count, snmp_discovered_count, triggered_by, retry_count,
	max_retries, next_retry_at, timeout_ms, concurrency, skip_reverse_dns, tcp_probe_ports, enable_snmp,
	enable_dns_update, started_at, completed_at, created_by, created_at, updated_at`

func scanScanJob(sc scanner) (store.IPScanJob, error) {
	var j store.IPScanJob
	if err := sc.Scan(&j.ID, &j.TenantID, &j.SubnetID, &j.Status, &j.Progress, &j.StatusMessage,
		&j.TotalAddresses, &j.ScannedCount, &j.AliveCount, &j.NewCount, &j.UpdatedCount, &j.SNMPDiscoveredCount,
		&j.TriggeredBy, &j.RetryCount, &j.MaxRetries, &j.NextRetryAt, &j.TimeoutMs, &j.Concurrency,
		&j.SkipReverseDNS, &j.TCPProbePorts, &j.EnableSNMP, &j.EnableDNSUpdate, &j.StartedAt, &j.CompletedAt,
		&j.CreatedBy, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return store.IPScanJob{}, err
	}
	return j, nil
}

func (d *DB) CreateScanJob(ctx context.Context, j store.IPScanJob) error {
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
	now := time.Now().UTC()
	return d.tenant(ctx, j.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_ip_scan_jobs
			(id, tenant_id, subnet_id, status, progress, status_message, total_addresses, scanned_count,
			 alive_count, new_count, updated_count, snmp_discovered_count, triggered_by, retry_count, max_retries,
			 next_retry_at, timeout_ms, concurrency, skip_reverse_dns, tcp_probe_ports, enable_snmp,
			 enable_dns_update, started_at, completed_at, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$26)`,
			j.ID, j.TenantID, j.SubnetID, j.Status, j.Progress, j.StatusMessage, j.TotalAddresses, j.ScannedCount,
			j.AliveCount, j.NewCount, j.UpdatedCount, j.SNMPDiscoveredCount, j.TriggeredBy, j.RetryCount, j.MaxRetries,
			j.NextRetryAt, j.TimeoutMs, j.Concurrency, j.SkipReverseDNS, j.TCPProbePorts, j.EnableSNMP,
			j.EnableDNSUpdate, j.StartedAt, j.CompletedAt, j.CreatedBy, now)
		return mapErr(e)
	})
}

func (d *DB) GetScanJob(ctx context.Context, tenantID, id string) (out store.IPScanJob, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanScanJob(tx.QueryRow(ctx, "SELECT "+scanJobCols+" FROM ipam_ip_scan_jobs WHERE tenant_id=$1 AND id=$2", tenantID, id))
		return mapErr(e)
	})
	return
}

func (d *DB) ListScanJobs(ctx context.Context, tenantID string, f store.ScanFilter) (out []store.IPScanJob, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var b strings.Builder
		b.WriteString("SELECT " + scanJobCols + " FROM ipam_ip_scan_jobs WHERE tenant_id=$1")
		args := []any{tenantID}
		add := func(cond string, val any) {
			args = append(args, val)
			b.WriteString(fmt.Sprintf(cond, len(args)))
		}
		if f.SubnetID != "" {
			add(" AND subnet_id = $%d", f.SubnetID)
		}
		if f.Status != "" {
			add(" AND status = $%d", f.Status)
		}
		if f.CursorID != "" {
			add(" AND id < $%d", f.CursorID)
		}
		b.WriteString(" ORDER BY id DESC")
		if f.Limit > 0 {
			add(" LIMIT $%d", f.Limit)
		}
		rows, e := tx.Query(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			j, e := scanScanJob(rows)
			if e != nil {
				return e
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return
}

func (d *DB) UpdateScanJob(ctx context.Context, j store.IPScanJob) error {
	now := time.Now().UTC()
	return d.tenant(ctx, j.TenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, `UPDATE ipam_ip_scan_jobs SET
			subnet_id=$3, status=$4, progress=$5, status_message=$6, total_addresses=$7, scanned_count=$8,
			alive_count=$9, new_count=$10, updated_count=$11, snmp_discovered_count=$12, triggered_by=$13,
			retry_count=$14, max_retries=$15, next_retry_at=$16, timeout_ms=$17, concurrency=$18,
			skip_reverse_dns=$19, tcp_probe_ports=$20, enable_snmp=$21, enable_dns_update=$22, started_at=$23,
			completed_at=$24, created_by=$25, updated_at=$26
			WHERE tenant_id=$1 AND id=$2`,
			j.TenantID, j.ID, j.SubnetID, j.Status, j.Progress, j.StatusMessage, j.TotalAddresses, j.ScannedCount,
			j.AliveCount, j.NewCount, j.UpdatedCount, j.SNMPDiscoveredCount, j.TriggeredBy, j.RetryCount, j.MaxRetries,
			j.NextRetryAt, j.TimeoutMs, j.Concurrency, j.SkipReverseDNS, j.TCPProbePorts, j.EnableSNMP,
			j.EnableDNSUpdate, j.StartedAt, j.CompletedAt, j.CreatedBy, now)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		return nil
	})
}

func (d *DB) ClaimDueScanJobs(ctx context.Context, at time.Time, limit int) (out []store.IPScanJob, err error) {
	if limit <= 0 {
		limit = 1
	}
	now := time.Now().UTC()
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			WITH due AS (
				SELECT id FROM ipam_ip_scan_jobs
				WHERE status='pending' AND (next_retry_at IS NULL OR next_retry_at <= $1)
				ORDER BY COALESCE(next_retry_at, created_at) ASC
				LIMIT $2
				FOR UPDATE SKIP LOCKED
			)
			UPDATE ipam_ip_scan_jobs j SET status='scanning', started_at=$3, updated_at=$3
			FROM due WHERE j.id = due.id
			RETURNING j.*`, at, limit, now)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			j, e := scanScanJob(rows)
			if e != nil {
				return e
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return
}

func (d *DB) ActiveScanForSubnet(ctx context.Context, tenantID, subnetID string) (active bool, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM ipam_ip_scan_jobs WHERE tenant_id=$1 AND subnet_id=$2 AND status IN ('pending','scanning'))`,
			tenantID, subnetID).Scan(&active)
	})
	return
}

// ---- dns config

func (d *DB) GetDNSConfig(ctx context.Context, tenantID string) (out store.DNSConfig, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var servers []byte
		e := tx.QueryRow(ctx, `SELECT id, tenant_id, dns_servers, timeout_ms, use_system_dns_fallback, reverse_dns_enabled, created_at, updated_at
			FROM ipam_dns_configs WHERE tenant_id=$1`, tenantID).Scan(
			&out.ID, &out.TenantID, &servers, &out.TimeoutMs, &out.UseSystemDNSFallback, &out.ReverseDNSEnabled, &out.CreatedAt, &out.UpdatedAt)
		if e != nil {
			return mapErr(e)
		}
		if len(servers) > 0 {
			_ = json.Unmarshal(servers, &out.DNSServers)
		}
		return nil
	})
	return
}

func (d *DB) UpsertDNSConfig(ctx context.Context, c store.DNSConfig) error {
	if c.ID == "" {
		c.ID = store.NewID()
	}
	if c.TimeoutMs == 0 {
		c.TimeoutMs = 5000
	}
	now := time.Now().UTC()
	servers := c.DNSServers
	if servers == nil {
		servers = []string{}
	}
	return d.tenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `INSERT INTO ipam_dns_configs
			(id, tenant_id, dns_servers, timeout_ms, use_system_dns_fallback, reverse_dns_enabled, created_at, updated_at)
			VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$7)
			ON CONFLICT (tenant_id) DO UPDATE SET
				dns_servers=EXCLUDED.dns_servers, timeout_ms=EXCLUDED.timeout_ms,
				use_system_dns_fallback=EXCLUDED.use_system_dns_fallback,
				reverse_dns_enabled=EXCLUDED.reverse_dns_enabled, updated_at=EXCLUDED.updated_at`,
			c.ID, c.TenantID, mustJSON(servers), c.TimeoutMs, c.UseSystemDNSFallback, c.ReverseDNSEnabled, now)
		return mapErr(e)
	})
}

// ---- statistics

func (d *DB) TenantStats(ctx context.Context, tenantID string) (out repo.Stats, err error) {
	out = repo.Stats{DevicesByType: map[string]int64{}}
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Subnet capacities + allocated counts.
		rows, e := tx.Query(ctx, `SELECT s.cidr,
			(SELECT count(*) FROM ipam_ip_addresses a WHERE a.tenant_id=s.tenant_id AND a.subnet_id=s.id)
			FROM ipam_subnets s WHERE s.tenant_id=$1`, tenantID)
		if e != nil {
			return e
		}
		var totalCap, usedTotal int64
		for rows.Next() {
			var cidr string
			var used int64
			if e := rows.Scan(&cidr, &used); e != nil {
				rows.Close()
				return e
			}
			out.TotalSubnets++
			totalCap += subnetTotal(cidr)
			usedTotal += used
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		out.TotalAddresses = totalCap
		out.UsedAddresses = usedTotal
		if totalCap > 0 {
			out.AvailableAddresses = totalCap - usedTotal
			out.OverallUtilization = float64(usedTotal) / float64(totalCap)
		}
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_vlans WHERE tenant_id=$1", tenantID).Scan(&out.TotalVlans); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, "SELECT count(*) FROM ipam_locations WHERE tenant_id=$1", tenantID).Scan(&out.TotalLocations); e != nil {
			return e
		}
		drows, e := tx.Query(ctx, "SELECT device_type, count(*) FROM ipam_devices WHERE tenant_id=$1 GROUP BY device_type", tenantID)
		if e != nil {
			return e
		}
		defer drows.Close()
		for drows.Next() {
			var dt string
			var n int64
			if e := drows.Scan(&dt, &n); e != nil {
				return e
			}
			out.DevicesByType[dt] = n
			out.TotalDevices += n
		}
		return drows.Err()
	})
	return
}

func (d *DB) TenantIDs(ctx context.Context) (out []string, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT DISTINCT tenant_id::text FROM (
			SELECT tenant_id FROM ipam_subnets
			UNION SELECT tenant_id FROM ipam_devices
			UNION SELECT tenant_id FROM ipam_vlans
			UNION SELECT tenant_id FROM ipam_locations
			UNION SELECT tenant_id FROM ipam_ip_scan_jobs) u ORDER BY 1`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e := rows.Scan(&id); e != nil {
				return e
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return
}

// ---- audit

func (d *DB) AppendAudit(ctx context.Context, row store.AuditRow) error {
	return d.system(ctx, func(tx pgx.Tx) error {
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
		_, e := tx.Exec(ctx, `INSERT INTO ipam_audit_events
			(id, tenant_id, at, actor_kind, actor_id, action, subject_kind, subject_id, target, outcome, reason, detail)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)`,
			id, row.TenantID, at, row.ActorKind, row.ActorID, row.Action, row.SubjectKind, row.SubjectID,
			row.Target, row.Outcome, row.Reason, detail)
		return e
	})
}

var _ repo.Store = (*DB)(nil)
