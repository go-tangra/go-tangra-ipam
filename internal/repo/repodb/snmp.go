package repodb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Subnet SNMP credentials (feature 021): tenant transactions under RLS; the
// sealed blob is read only by GetSubnetSNMP.

const subnetSNMPMeta = `tenant_id::text, subnet_id::text, version, security_level, auth_protocol, priv_protocol, updated_by, updated_at`

func scanSubnetSNMP(sc scanner, withBlob bool) (store.SubnetSNMP, error) {
	var r store.SubnetSNMP
	dest := []any{&r.TenantID, &r.SubnetID, &r.Version, &r.SecurityLevel, &r.AuthProtocol, &r.PrivProtocol, &r.UpdatedBy, &r.UpdatedAt}
	if withBlob {
		dest = append(dest, &r.Sealed)
	}
	if err := sc.Scan(dest...); err != nil {
		return store.SubnetSNMP{}, err
	}
	return r, nil
}

// GetSubnetSNMP implements repo.Store.
func (d *DB) GetSubnetSNMP(ctx context.Context, tenantID, subnetID string) (out store.SubnetSNMP, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanSubnetSNMP(tx.QueryRow(ctx, "SELECT "+subnetSNMPMeta+", sealed FROM ipam_subnet_snmp WHERE tenant_id=$1 AND subnet_id=$2",
			tenantID, subnetID), true)
		return mapErr(e)
	})
	return
}

// PutSubnetSNMP implements repo.Store: upsert the row (the subnet must be in
// the tenant) and write the audit row in the same transaction.
func (d *DB) PutSubnetSNMP(ctx context.Context, row store.SubnetSNMP, audit store.AuditRow) error {
	return d.tenant(ctx, row.TenantID, func(tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM ipam_subnets WHERE tenant_id=$1 AND id=$2)", row.TenantID, row.SubnetID).Scan(&exists); e != nil {
			return mapErr(e)
		}
		if !exists {
			return repo.ErrNotFound
		}
		if _, e := tx.Exec(ctx, `INSERT INTO ipam_subnet_snmp
			(tenant_id, subnet_id, version, security_level, auth_protocol, priv_protocol, sealed, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
			ON CONFLICT (tenant_id, subnet_id) DO UPDATE SET version=EXCLUDED.version,
			  security_level=EXCLUDED.security_level, auth_protocol=EXCLUDED.auth_protocol,
			  priv_protocol=EXCLUDED.priv_protocol, sealed=EXCLUDED.sealed,
			  updated_by=EXCLUDED.updated_by, updated_at=now()`,
			row.TenantID, row.SubnetID, row.Version, row.SecurityLevel, row.AuthProtocol, row.PrivProtocol,
			row.Sealed, row.UpdatedBy); e != nil {
			return mapErr(e)
		}
		audit.TenantID = row.TenantID
		return appendAuditTx(ctx, tx, audit)
	})
}

// DeleteSubnetSNMP implements repo.Store.
func (d *DB) DeleteSubnetSNMP(ctx context.Context, tenantID, subnetID string, audit store.AuditRow) error {
	return d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		ct, e := tx.Exec(ctx, "DELETE FROM ipam_subnet_snmp WHERE tenant_id=$1 AND subnet_id=$2", tenantID, subnetID)
		if e != nil {
			return mapErr(e)
		}
		if ct.RowsAffected() == 0 {
			return repo.ErrNotFound
		}
		audit.TenantID = tenantID
		return appendAuditTx(ctx, tx, audit)
	})
}

// ListSubnetSNMP implements repo.Store (metadata only, never the blob).
func (d *DB) ListSubnetSNMP(ctx context.Context, tenantID string) (out []store.SubnetSNMP, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, "SELECT "+subnetSNMPMeta+" FROM ipam_subnet_snmp WHERE tenant_id=$1 ORDER BY subnet_id", tenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanSubnetSNMP(rows, false)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return
}

// LegacySNMPRefCount implements repo.Store (system scope).
func (d *DB) LegacySNMPRefCount(ctx context.Context) (n int64, err error) {
	err = d.system(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM ipam_subnets WHERE snmp_secret_ref <> ''").Scan(&n)
	})
	return
}
