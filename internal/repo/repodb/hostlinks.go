package repodb

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Per-switch links of host interfaces and addresses (feature 022, research
// D6): ipam_host_switch_links keeps one link per (host, switch); the flat
// link columns stay the primary link.

// addrLinksCol is the addresses' per-switch links as a JSON array (a column
// of addrCols; the statement's FROM must be ipam_ip_addresses unaliased).
const addrLinksCol = `coalesce((SELECT json_agg(json_build_object('switch_id', l.switch_id, 'switch_name', coalesce(sw.name, ''),
	'port_id', l.port_id, 'port_name', l.port_name, 'vlan', l.vlan, 'source', l.source, 'last_seen', l.last_seen))
	FROM ipam_host_switch_links l LEFT JOIN ipam_devices sw ON sw.tenant_id = l.tenant_id AND sw.id = l.switch_id
	WHERE l.tenant_id = ipam_ip_addresses.tenant_id AND l.host_kind = 'address' AND l.host_id = ipam_ip_addresses.id), '[]')`

// decodeLinks parses addrLinksCol and marks the primary (the port of the
// flat link columns).
func decodeLinks(raw []byte, primaryPort string) []store.HostSwitchLink {
	var out []store.HostSwitchLink
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil || len(out) == 0 {
		return nil
	}
	return store.MarkPrimary(out, primaryPort)
}

// hostLinks returns the per-switch links of the given hosts of one kind, by
// host id, with the switch names.
func hostLinks(ctx context.Context, tx pgx.Tx, tenantID, kind string, ids []string) (map[string][]store.HostSwitchLink, error) {
	out := map[string][]store.HostSwitchLink{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT l.host_id::text, l.switch_id::text, coalesce(sw.name, ''), l.port_id::text, l.port_name,
		l.vlan, l.source, l.last_seen
		FROM ipam_host_switch_links l LEFT JOIN ipam_devices sw ON sw.tenant_id = l.tenant_id AND sw.id = l.switch_id
		WHERE l.tenant_id=$1 AND l.host_kind=$2 AND l.host_id = ANY($3) ORDER BY l.host_id, l.switch_id`, tenantID, kind, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var host string
		var l store.HostSwitchLink
		if err := rows.Scan(&host, &l.SwitchID, &l.SwitchName, &l.PortID, &l.PortName, &l.VLAN, &l.Source, &l.LastSeen); err != nil {
			return nil, err
		}
		out[host] = append(out[host], l)
	}
	return out, rows.Err()
}

// attachIfaceLinks fills the per-switch links of interfaces (primary first).
func attachIfaceLinks(ctx context.Context, tx pgx.Tx, tenantID string, ifaces []store.DeviceInterface) error {
	ids := make([]string, len(ifaces))
	for k, i := range ifaces {
		ids[k] = i.ID
	}
	m, err := hostLinks(ctx, tx, tenantID, store.HostKindInterface, ids)
	if err != nil {
		return err
	}
	for k := range ifaces {
		if ls := m[ifaces[k].ID]; len(ls) > 0 {
			ifaces[k].Links = store.MarkPrimary(ls, ifaces[k].RemoteInterfaceID)
		}
	}
	return nil
}

// queueHostLinks queues the replacement of one host's per-switch link set:
// its rows are deleted and links inserted (a link whose switch port no longer
// exists is skipped).
func queueHostLinks(b *pgx.Batch, tenantID, kind, id string, links []store.HostSwitchLink) {
	b.Queue(`DELETE FROM ipam_host_switch_links WHERE tenant_id=$1 AND host_kind=$2 AND host_id=$3`, tenantID, kind, id)
	for _, l := range links {
		b.Queue(`INSERT INTO ipam_host_switch_links (tenant_id, host_kind, host_id, switch_id, port_id, port_name, vlan, source, last_seen)
			SELECT $1,$2,$3,$4,$5,$6,$7,$8,coalesce($9::timestamptz, now())
			WHERE EXISTS (SELECT 1 FROM ipam_device_interfaces p WHERE p.tenant_id=$1 AND p.id=$5 AND p.device_id=$4)
			ON CONFLICT (tenant_id, host_kind, host_id, switch_id) DO UPDATE SET port_id=EXCLUDED.port_id,
				port_name=EXCLUDED.port_name, vlan=EXCLUDED.vlan, source=EXCLUDED.source, last_seen=EXCLUDED.last_seen`,
			tenantID, kind, id, l.SwitchID, l.PortID, l.PortName, l.VLAN, l.Source, l.LastSeen)
	}
}

// execLinkBatch runs a batch of per-host groups (one guarded UPDATE of the
// flat columns followed by that host's queueHostLinks statements); sizes[k]
// is the number of link statements of group k. A missing host (RLS, other
// tenant, deleted) is ErrNotFound.
func execLinkBatch(ctx context.Context, tx pgx.Tx, b *pgx.Batch, sizes []int) error {
	br := tx.SendBatch(ctx, b)
	for _, n := range sizes {
		ct, err := br.Exec()
		if err != nil {
			_ = br.Close()
			return mapErr(err)
		}
		if ct.RowsAffected() == 0 {
			_ = br.Close()
			return repo.ErrNotFound
		}
		for k := 0; k < n; k++ {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return mapErr(err)
			}
		}
	}
	return br.Close()
}
