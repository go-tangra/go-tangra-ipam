package hostplan

import (
	"fmt"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func TestPendingUpdates(t *testing.T) {
	_, st := linked()
	st.Packages = []store.DevicePackage{
		{ID: "p1", Name: "openssl", CurrentVersion: "3.0.1", AvailableVersion: "3.0.2", NeedsUpdate: true, PackageManager: "apt"},
		{ID: "p2", Name: "vim", CurrentVersion: "1", AvailableVersion: "2", NeedsUpdate: true, PackageManager: "apt"},
	}
	r := report()
	r.Updates = hostreport.Updates{Manager: "apt", Status: store.UpdAvailable, RebootRequired: hostreport.TriTrue, AutomaticUpdates: hostreport.TriTrue}
	r.Pending = []hostreport.Package{{Name: "openssl", Installed: "3.0.1", Available: "3.0.3", Security: true}, {Name: "curl", Installed: "8", Available: "9"}}
	p := Build(st, r, params())
	rp := ops(p, OpReplacePackages)
	if len(rp) != 1 || len(rp[0].Packages) != 2 || rp[0].Packages[0].ID != "p1" || !rp[0].Packages[0].IsSecurityUpdate || !rp[0].Packages[1].NeedsUpdate {
		t.Fatalf("%+v", rp)
	}
	det := rp[0].Audit[0].Detail
	if rp[0].Audit[0].Action != "packages_updated" || det["pending"] != 2 || det["security"] != 1 || det["added_count"] != 1 || det["removed_count"] != 1 || det["changed"] != 1 {
		t.Fatalf("audit %+v", det)
	}
	dev := ops(p, OpUpdateDevice)[0].Device
	if !dev.RebootRequired || !dev.UnattendedUpgrades || dev.UpdateStatus != store.UpdAvailable {
		t.Fatalf("device %+v", dev)
	}
	// Up to date -> empty list replaces.
	r.Updates.Status, r.Pending = store.UpdUpToDate, nil
	if rp := ops(Build(st, r, params()), OpReplacePackages); len(rp) != 1 || len(rp[0].Packages) != 0 {
		t.Fatal("up to date clears")
	}
	// Same list -> no op.
	st.Packages = st.Packages[:1]
	st.Packages[0].IsSecurityUpdate, st.Packages[0].AvailableVersion = true, "3.0.3"
	r.Updates.Status = store.UpdAvailable
	r.Pending = []hostreport.Package{{Name: "openssl", Installed: "3.0.1", Available: "3.0.3", Security: true}}
	if rp := ops(Build(st, r, params()), OpReplacePackages); len(rp) != 0 {
		t.Fatal("unchanged list must not be rewritten")
	}
}

func TestUnknownUpdateStateKeepsPackages(t *testing.T) {
	_, st := linked()
	st.Packages = []store.DevicePackage{{ID: "p1", Name: "manual"}}
	for _, status := range []string{store.UpdUnknown, store.UpdUnsupported, store.UpdError} {
		r := report()
		r.Updates.Status = status
		p := Build(st, r, params())
		if len(ops(p, OpReplacePackages)) != 0 {
			t.Fatalf("%s replaced packages", status)
		}
		if ops(p, OpUpdateDevice)[0].Device.UpdateStatus != status {
			t.Fatalf("%s not shown as such", status)
		}
	}
}

func TestPackageAuditBounded(t *testing.T) {
	_, st := linked()
	r := report()
	r.Updates.Status = store.UpdAvailable
	for i := 0; i < hostreport.MaxPackages; i++ {
		r.Pending = append(r.Pending, hostreport.Package{Name: fmt.Sprintf("p%05d", i), Available: "2"})
	}
	rp := ops(Build(st, r, params()), OpReplacePackages)[0]
	if len(rp.Packages) != hostreport.MaxPackages {
		t.Fatal("5000 packages")
	}
	if names := rp.Audit[0].Detail["added"].([]any); len(names) != MaxAuditNames || rp.Audit[0].Detail["added_count"] != hostreport.MaxPackages {
		t.Fatalf("names %d", len(names))
	}
}
