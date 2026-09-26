package hostplan

import (
	"sort"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/audit"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// MaxAuditNames bounds the package names listed in one packages_updated row.
const MaxAuditNames = 200

// packages replaces the device's package rows with the pending updates the
// host reports (FR-018, research D12). An unknown, unsupported or failed
// update state leaves the rows untouched: unknown is never "up to date".
func (pl *planner) packages() {
	if pl.r.Updates.Status != store.UpdUpToDate && pl.r.Updates.Status != store.UpdAvailable {
		return
	}
	cur := map[string]store.DevicePackage{}
	for _, p := range pl.st.Packages {
		cur[p.Name] = p
	}
	want := make([]store.DevicePackage, 0, len(pl.r.Pending))
	var added, removed []string
	changed, security := 0, 0
	seen := map[string]bool{}
	for _, p := range pl.r.Pending {
		np := store.DevicePackage{TenantID: pl.p.TenantID, DeviceID: pl.dev.ID, Name: p.Name, CurrentVersion: p.Installed,
			AvailableVersion: p.Available, NeedsUpdate: true, IsSecurityUpdate: p.Security, PackageManager: pl.r.Updates.Manager}
		if p.Security {
			security++
		}
		seen[p.Name] = true
		if ex, ok := cur[p.Name]; ok {
			np.ID = ex.ID
			if ex.CurrentVersion != np.CurrentVersion || ex.AvailableVersion != np.AvailableVersion || !ex.NeedsUpdate ||
				ex.IsSecurityUpdate != np.IsSecurityUpdate || ex.PackageManager != np.PackageManager {
				changed++
			}
		} else {
			np.ID = pl.p.NewID()
			added = append(added, p.Name)
		}
		want = append(want, np)
	}
	for name := range cur {
		if !seen[name] {
			removed = append(removed, name)
		}
	}
	if len(added)+len(removed)+changed == 0 {
		return
	}
	sort.Strings(removed)
	pl.add(Op{Kind: OpReplacePackages, DeviceID: pl.dev.ID, Packages: want, Audit: []store.AuditRow{
		pl.row(audit.PackagesUpdated, audit.SubjectPackage, pl.dev.ID, map[string]any{
			"pending": len(want), "security": security, "changed": changed,
			"added": boundNames(added), "added_count": len(added),
			"removed": boundNames(removed), "removed_count": len(removed),
		}),
	}})
}

func boundNames(names []string) []any {
	if len(names) > MaxAuditNames {
		names = names[:MaxAuditNames]
	}
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}
