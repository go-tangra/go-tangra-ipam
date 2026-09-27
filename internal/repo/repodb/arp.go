package repodb

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// GetARPSettings implements repo.ARPStore (defaults when no row exists).
func (d *DB) GetARPSettings(ctx context.Context, tenantID string) (out store.ARPSettings, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		out = store.DefaultARPSettings(tenantID)
		e := tx.QueryRow(ctx, `SELECT enabled, coalesce(excluded_devices::text[], '{}'), proxy_threshold, updated_by, updated_at
			FROM ipam_arp_settings WHERE tenant_id=$1`, tenantID).
			Scan(&out.Enabled, &out.ExcludedDevices, &out.ProxyThreshold, &out.UpdatedBy, &out.UpdatedAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	})
	return
}

// PutARPSettings implements repo.ARPStore.
func (d *DB) PutARPSettings(ctx context.Context, s store.ARPSettings, audit store.AuditRow) error {
	if s.ExcludedDevices == nil {
		s.ExcludedDevices = []string{}
	}
	return d.tenant(ctx, s.TenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO ipam_arp_settings (tenant_id, enabled, excluded_devices, proxy_threshold, updated_by, updated_at)
			VALUES ($1, $2, $3::uuid[], $4, $5, now())
			ON CONFLICT (tenant_id) DO UPDATE SET enabled=EXCLUDED.enabled, excluded_devices=EXCLUDED.excluded_devices,
				proxy_threshold=EXCLUDED.proxy_threshold, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			s.TenantID, s.Enabled, s.ExcludedDevices, s.ProxyThreshold, s.UpdatedBy); e != nil {
			return mapErr(e)
		}
		audit.TenantID = s.TenantID
		return appendAuditTx(ctx, tx, audit)
	})
}

// networkMACs reads the interface MACs of the tenant's network devices.
func networkMACs(ctx context.Context, tx pgx.Tx, tenantID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT i.mac_address FROM ipam_device_interfaces i JOIN ipam_devices dv ON dv.id = i.device_id
		WHERE i.tenant_id=$1 AND i.mac_address <> '' AND dv.device_type IN ('router','switch','firewall','load_balancer')`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var mac string
		if err := rows.Scan(&mac); err != nil {
			return nil, err
		}
		if m, ok := hostreport.NormalizeMAC(mac); ok {
			out[m] = true
		}
	}
	return out, rows.Err()
}

// NetworkMACs implements repo.ARPStore.
func (d *DB) NetworkMACs(ctx context.Context, tenantID string) (out map[string]bool, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = networkMACs(ctx, tx, tenantID)
		return e
	})
	return
}

// arpOpSQL maps an op to its guarded statement; ok is false for an unknown
// kind. The guards repeat the planner's provenance rules so a plan that is
// stale (a user set a MAC meanwhile) still never overwrites an agent or
// manual MAC.
func arpOpSQL(tenantID string, op store.ARPOp) (string, []any, bool) {
	switch op.Kind {
	case store.ARPFill, store.ARPUpdate:
		return `UPDATE ipam_ip_addresses SET mac_address=$3, mac_source='arp', mac_source_device_id=$4, mac_seen_at=$5,
			mac_conflict='', updated_at=now() WHERE tenant_id=$1 AND id=$2 AND (mac_address='' OR mac_source='arp')`,
			[]any{tenantID, op.AddressID, op.MAC, np(op.SourceDeviceID), op.At}, true
	case store.ARPConflict:
		return `UPDATE ipam_ip_addresses SET mac_conflict=$3, updated_at=now()
			WHERE tenant_id=$1 AND id=$2 AND mac_address <> '' AND mac_source <> 'arp'`,
			[]any{tenantID, op.AddressID, op.MAC}, true
	case store.ARPClearConflict:
		return `UPDATE ipam_ip_addresses SET mac_conflict='', mac_seen_at=$3 WHERE tenant_id=$1 AND id=$2`,
			[]any{tenantID, op.AddressID, op.At}, true
	case store.ARPTouch:
		return `UPDATE ipam_ip_addresses SET mac_seen_at=$3,
			mac_source_device_id=CASE WHEN mac_source='arp' THEN $4::uuid ELSE mac_source_device_id END
			WHERE tenant_id=$1 AND id=$2`,
			[]any{tenantID, op.AddressID, op.At, np(op.SourceDeviceID)}, true
	case store.ARPCreate:
		return `INSERT INTO ipam_ip_addresses (id, tenant_id, address, subnet_id, mac_address, status, address_type,
				last_seen, created_by, mac_source, mac_source_device_id, mac_seen_at, origin, created_at, updated_at)
			SELECT $2, $1, $3, $4, $5, 'active', 'host', $7, 'arp', 'arp', $6, $7, 'arp', now(), now()
			WHERE EXISTS (SELECT 1 FROM ipam_subnets WHERE tenant_id=$1 AND id=$4)
			ON CONFLICT DO NOTHING`,
			[]any{tenantID, op.AddressID, op.Address, op.SubnetID, op.MAC, np(op.SourceDeviceID), op.At}, true
	}
	return "", nil, false
}

// ApplyARP implements repo.ARPStore: every op in one batched tenant
// transaction, then the audit rows of the ops that applied and the summary.
func (d *DB) ApplyARP(ctx context.Context, tenantID string, ops []store.ARPOp, summary []store.AuditRow) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var queued []store.ARPOp
		b := &pgx.Batch{}
		for _, op := range ops {
			q, args, ok := arpOpSQL(tenantID, op)
			if !ok {
				continue
			}
			b.Queue(q, args...)
			queued = append(queued, op)
		}
		var audit []store.AuditRow
		if len(queued) > 0 {
			res := tx.SendBatch(ctx, b)
			for _, op := range queued {
				ct, e := res.Exec()
				if e != nil {
					_ = res.Close()
					return mapErr(e)
				}
				if ct.RowsAffected() > 0 {
					audit = append(audit, op.Audit...)
				}
			}
			if e := res.Close(); e != nil {
				return e
			}
		}
		audit = append(audit, summary...)
		return appendAuditBatch(ctx, tx, tenantID, audit)
	})
}

// appendAuditBatch writes audit rows in one batch inside tx.
func appendAuditBatch(ctx context.Context, tx pgx.Tx, tenantID string, rows []store.AuditRow) error {
	if len(rows) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, row := range rows {
		id, at := row.ID, row.At
		if id == "" {
			id = store.NewID()
		}
		if at.IsZero() {
			at = time.Now().UTC()
		}
		var detail any
		if row.Detail != nil {
			detail = mustJSON(row.Detail)
		}
		b.Queue(`INSERT INTO ipam_audit_events
			(id, tenant_id, at, actor_kind, actor_id, action, subject_kind, subject_id, target, outcome, reason, detail)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)`,
			id, tenantID, at, row.ActorKind, row.ActorID, row.Action, row.SubjectKind, row.SubjectID,
			row.Target, row.Outcome, row.Reason, detail)
	}
	return tx.SendBatch(ctx, b).Close()
}

// SetAddressLinks implements repo.PortLinkStore: only the link columns of
// the given addresses are written.
func (d *DB) SetAddressLinks(ctx context.Context, tenantID string, addrs []store.IPAddress, audit []store.AuditRow) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		for _, a := range addrs {
			var l store.AddressLink
			if a.Link != nil {
				l = *a.Link
			}
			ct, e := tx.Exec(ctx, `UPDATE ipam_ip_addresses SET link_switch_id=$3, link_port_id=$4, link_port_name=$5,
				link_vlan=$6, link_source=$7, link_last_seen=$8, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenantID, a.ID, np(l.SwitchID), np(l.PortID), l.PortName, l.VLAN, l.Source, l.LastSeen)
			if e != nil {
				return mapErr(e)
			}
			if ct.RowsAffected() == 0 {
				return repo.ErrNotFound
			}
		}
		for _, row := range audit {
			row.TenantID = tenantID
			if e := appendAuditTx(ctx, tx, row); e != nil {
				return e
			}
		}
		return nil
	})
}

// behindAddresses lists the addresses linked to a switch port.
func behindAddresses(ctx context.Context, tx pgx.Tx, tenantID, portID string) ([]store.BehindAddress, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, address, hostname FROM ipam_ip_addresses
		WHERE tenant_id=$1 AND link_port_id=$2::uuid ORDER BY address LIMIT 256`, tenantID, portID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.BehindAddress
	for rows.Next() {
		var b store.BehindAddress
		if err := rows.Scan(&b.AddressID, &b.Address, &b.Hostname); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ignoredJSON serialises the ARP ignored counters (never null).
func ignoredJSON(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	return mustJSON(m)
}
