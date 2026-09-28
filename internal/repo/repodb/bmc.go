package repodb

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// SetDeviceBMCRef implements repo.Store: the reference column and its audit
// row are written in one tenant transaction (feature 024).
func (d *DB) SetDeviceBMCRef(ctx context.Context, tenantID, deviceID, ref string, audit store.AuditRow) (previous string, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT ipmi_secret_ref FROM ipam_devices WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
			tenantID, deviceID).Scan(&previous); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return repo.ErrNotFound
			}
			return mapErr(e)
		}
		if _, e := tx.Exec(ctx, `UPDATE ipam_devices SET ipmi_secret_ref=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenantID, deviceID, ref); e != nil {
			return mapErr(e)
		}
		audit.TenantID = tenantID
		return appendAuditTx(ctx, tx, audit)
	})
	return previous, err
}
