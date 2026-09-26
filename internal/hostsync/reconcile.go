package hostsync

import (
	"context"
	"errors"
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/invclient"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// reconcile compares the digests of every inventory host of the tenant with
// the devices' stored digests (FR-008, research D5): changed or unknown hosts
// are fetched and applied, retired hosts and hosts deleted from inventory are
// marked as no longer reported.
func (r *Runner) reconcile(ctx context.Context, s store.HostSyncSettings, ru *run) error {
	digests, err := r.deviceDigests(ctx, s.TenantID)
	if err != nil {
		return err
	}
	var rows []invclient.Report
	if err := r.inv.ListHostReports(ctx, s.TenantID, invclient.Filter{Digest: true}, func(p []invclient.Report) error {
		rows = append(rows, p...)
		return nil
	}); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		hid := strings.ToLower(row.Host.ID)
		seen[hid] = true
		d, known := digests[hid]
		if row.Host.Status == "retired" {
			if known && d.state != store.RepNotReported {
				if _, err := r.markGone(ctx, s.TenantID, hid, "retired", TriggerReconcile, ru.id); err != nil {
					return err
				}
				ru.changes++
			}
			continue
		}
		if known && d.state == store.RepReported && row.Digest != "" && d.digest == row.Digest {
			continue
		}
		full, err := r.inv.GetHostReport(ctx, s.TenantID, row.Host.ID)
		if errors.Is(err, invclient.ErrNotFound) {
			continue // snapshot gone between the listing and the fetch
		}
		if err != nil {
			return err
		}
		ru.fetched++
		if _, err := r.handle(ctx, s, full, TriggerReconcile, ru, digests, true); err != nil {
			return err
		}
		r.sleep(ctx, r.cfg.Pace)
	}
	for hid, d := range digests {
		if !seen[hid] && d.state != store.RepNotReported {
			if _, err := r.markGone(ctx, s.TenantID, hid, "deleted", TriggerReconcile, ru.id); err != nil {
				return err
			}
			ru.changes++
		}
	}
	return nil
}
