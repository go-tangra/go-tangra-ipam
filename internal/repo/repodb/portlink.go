package repodb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func queryIfaces(ctx context.Context, tx pgx.Tx, q string, args ...any) ([]store.DeviceInterface, error) {
	rows, err := tx.Query(ctx, q, args...)
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

// PortLinkData implements repo.PortLinkStore (tenant scope, RLS).
func (d *DB) PortLinkData(ctx context.Context, tenantID string) (out repo.PortLinkData, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		if out.Switches, e = queryDevices(ctx, tx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND device_type='switch' ORDER BY id", tenantID); e != nil {
			return e
		}
		if len(out.Switches) == 0 {
			return nil
		}
		if out.SwitchIfaces, e = queryIfaces(ctx, tx, "SELECT "+ifaceCols+` FROM ipam_device_interfaces
			WHERE tenant_id=$1 AND device_id IN (SELECT id FROM ipam_devices WHERE tenant_id=$1 AND device_type='switch') ORDER BY id`, tenantID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, "SELECT "+linkCols+` FROM ipam_device_interface_links
			WHERE tenant_id=$1 AND link_source IN ('snmp_fdb','lldp') ORDER BY id`, tenantID)
		if e != nil {
			return e
		}
		for rows.Next() {
			l, e := scanLink(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out.Links = append(out.Links, l)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		if out.Hosts, e = queryDevices(ctx, tx, "SELECT "+deviceCols+" FROM ipam_devices WHERE tenant_id=$1 AND source='host_report' ORDER BY id", tenantID); e != nil {
			return e
		}
		if out.HostIfaces, e = queryIfaces(ctx, tx, "SELECT "+ifaceCols+` FROM ipam_device_interfaces
			WHERE tenant_id=$1 AND report_state='reported' AND mac_address <> ''
			AND device_id IN (SELECT id FROM ipam_devices WHERE tenant_id=$1 AND source='host_report') ORDER BY id`, tenantID); e != nil {
			return e
		}
		if e = attachIfaceLinks(ctx, tx, tenantID, out.HostIfaces); e != nil {
			return e
		}
		if out.Addresses, e = queryAddresses(ctx, tx, "SELECT "+addrCols+` FROM ipam_ip_addresses
			WHERE tenant_id=$1 AND (mac_address <> '' OR link_port_id IS NOT NULL) ORDER BY id`, tenantID); e != nil {
			return e
		}
		out.NetworkMACs, e = networkMACs(ctx, tx, tenantID)
		return e
	})
	return
}

// SetInterfaceLinks implements repo.PortLinkStore: only the flat link columns
// and the per-switch link sets (Links, complete) of the given interfaces are
// written, with the audit rows, in one tenant transaction.
func (d *DB) SetInterfaceLinks(ctx context.Context, tenantID string, ifaces []store.DeviceInterface, audit []store.AuditRow) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		b := &pgx.Batch{}
		sizes := make([]int, 0, len(ifaces))
		for _, i := range ifaces {
			b.Queue(`UPDATE ipam_device_interfaces SET remote_device_id=$3, remote_interface_id=$4,
				remote_port_name=$5, link_source=$6, link_vlan=$7, link_last_seen=$8, updated_at=now()
				WHERE tenant_id=$1 AND id=$2`, tenantID, i.ID, i.RemoteDeviceID, i.RemoteInterfaceID, i.RemotePortName,
				i.LinkSource, i.LinkVlan, i.LinkLastSeen)
			queueHostLinks(b, tenantID, store.HostKindInterface, i.ID, i.Links)
			sizes = append(sizes, 1+len(i.Links))
		}
		if err := execLinkBatch(ctx, tx, b, sizes); err != nil {
			return err
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
