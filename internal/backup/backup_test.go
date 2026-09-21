package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-freya/freya/services/ipam/internal/authz"
	"github.com/go-freya/freya/services/ipam/internal/backup"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

func subj(tenant string) authz.Subjects {
	return authz.Subjects{TenantID: tenant, UserID: "u", Roles: []string{"admin"}, ActorKind: authz.ActorUser}
}

// seed populates a tenant with one of each entity, with secret/PII fields set.
func seed(t *testing.T, m *memstore.Mem, tenant string) (vlanID, locID, subID, devID, ipgID, hgID string) {
	t.Helper()
	ctx := context.Background()

	v := store.Vlan{TenantID: tenant, VlanID: 42, Name: "vl"}
	if err := m.CreateVlan(ctx, v); err != nil {
		t.Fatalf("vlan: %v", err)
	}
	l := store.Location{TenantID: tenant, Name: "loc", LocationType: store.LocSite, Contact: "SECRET-contact", Phone: "555", Email: "a@b.c"}
	if err := m.CreateLocation(ctx, l); err != nil {
		t.Fatalf("loc: %v", err)
	}
	sn := store.Subnet{TenantID: tenant, Name: "net", CIDR: "10.0.0.0/24", SNMPSecretRef: "warden-snmp-123"}
	if err := m.CreateSubnet(ctx, sn); err != nil {
		t.Fatalf("subnet: %v", err)
	}
	d := store.Device{TenantID: tenant, Name: "dev", DeviceType: store.DevServer, IPMISecretRef: "warden-ipmi-999", Contact: "SECRET-owner"}
	if err := m.CreateDevice(ctx, d); err != nil {
		t.Fatalf("device: %v", err)
	}
	a := store.IPAddress{TenantID: tenant, Address: "10.0.0.5", Owner: "SECRET-user"}
	if err := m.CreateAddress(ctx, a); err != nil {
		t.Fatalf("addr: %v", err)
	}
	ipg := store.IPGroup{TenantID: tenant, Name: "ipg"}
	if err := m.CreateIPGroup(ctx, ipg); err != nil {
		t.Fatalf("ipg: %v", err)
	}
	hg := store.HostGroup{TenantID: tenant, Name: "hg"}
	if err := m.CreateHostGroup(ctx, hg); err != nil {
		t.Fatalf("hg: %v", err)
	}

	// fetch generated ids
	vlans, _ := m.ListVlans(ctx, tenant, store.VlanFilter{})
	locs, _ := m.ListLocations(ctx, tenant, store.LocationFilter{})
	subs, _ := m.AllSubnetCIDRs(ctx, tenant)
	devs, _ := m.ListDevices(ctx, tenant, store.DeviceFilter{})
	ipgs, _ := m.ListIPGroups(ctx, tenant, 0, "")
	hgs, _ := m.ListHostGroups(ctx, tenant, 0, "")
	vlanID, locID, subID, devID, ipgID, hgID = vlans[0].ID, locs[0].ID, subs[0].ID, devs[0].ID, ipgs[0].ID, hgs[0].ID

	// members
	if err := m.AddIPGroupMember(ctx, store.IPGroupMember{TenantID: tenant, IPGroupID: ipgID, MemberType: store.MemberSubnet, Value: "10.0.0.0/24"}); err != nil {
		t.Fatalf("ip member: %v", err)
	}
	if err := m.AddHostGroupMember(ctx, store.HostGroupMember{TenantID: tenant, HostGroupID: hgID, DeviceID: devID}); err != nil {
		t.Fatalf("host member: %v", err)
	}
	return
}

func TestExportStripsSecrets(t *testing.T) {
	m := memstore.New()
	tenant := store.NewID()
	seed(t, m, tenant)
	svc := backup.New(m)

	// includeSecrets=true must STILL strip.
	b, err := svc.Export(context.Background(), subj(tenant), true)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if b.SchemaVersion != backup.SchemaVersion {
		t.Fatalf("schema version = %d", b.SchemaVersion)
	}
	if len(b.Subnets) != 1 || len(b.Addresses) != 1 || len(b.Devices) != 1 ||
		len(b.Vlans) != 1 || len(b.Locations) != 1 || len(b.IPGroups) != 1 || len(b.HostGroups) != 1 {
		t.Fatalf("missing collections: %+v", b)
	}
	if b.Subnets[0].SNMPSecretRef != "" {
		t.Errorf("snmp ref leaked: %q", b.Subnets[0].SNMPSecretRef)
	}
	if b.Devices[0].IPMISecretRef != "" || b.Devices[0].Contact != "" {
		t.Errorf("device secret/PII leaked: %+v", b.Devices[0])
	}
	if b.Addresses[0].Owner != "" {
		t.Errorf("address owner leaked: %q", b.Addresses[0].Owner)
	}
	if l := b.Locations[0]; l.Contact != "" || l.Phone != "" || l.Email != "" {
		t.Errorf("location PII leaked: %+v", l)
	}
	if len(b.IPGroups[0].Members) != 1 || len(b.HostGroups[0].Members) != 1 {
		t.Errorf("members not exported")
	}
}

func TestRoundTrip(t *testing.T) {
	src := memstore.New()
	tenant := store.NewID()
	vlanID, locID, subID, devID, ipgID, hgID := seed(t, src, tenant)
	b, err := backup.New(src).Export(context.Background(), subj(tenant), false)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	dst := memstore.New()
	res, err := backup.New(dst).Import(context.Background(), subj(tenant), b, backup.ModeSkip)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SubnetsImported != 1 || res.AddressesImported != 1 || res.DevicesImported != 1 ||
		res.VlansImported != 1 || res.LocationsImported != 1 || res.IPGroupsImported != 1 || res.HostGroupsImported != 1 {
		t.Fatalf("import counts: %+v", res)
	}
	ctx := context.Background()
	// ids preserved
	if _, err := dst.GetVlan(ctx, tenant, vlanID); err != nil {
		t.Errorf("vlan id not preserved: %v", err)
	}
	if _, err := dst.GetLocation(ctx, tenant, locID); err != nil {
		t.Errorf("loc id not preserved: %v", err)
	}
	if _, err := dst.GetSubnet(ctx, tenant, subID); err != nil {
		t.Errorf("subnet id not preserved: %v", err)
	}
	if _, err := dst.GetDevice(ctx, tenant, devID); err != nil {
		t.Errorf("device id not preserved: %v", err)
	}
	members, _ := dst.ListIPGroupMembers(ctx, tenant, ipgID)
	if len(members) != 1 {
		t.Errorf("ip members not restored: %d", len(members))
	}
	hmembers, _ := dst.ListHostGroupMembers(ctx, tenant, hgID)
	if len(hmembers) != 1 {
		t.Errorf("host members not restored: %d", len(hmembers))
	}
}

func TestImportSkipVsOverwrite(t *testing.T) {
	src := memstore.New()
	tenant := store.NewID()
	_, _, subID, _, _, _ := seed(t, src, tenant)
	b, _ := backup.New(src).Export(context.Background(), subj(tenant), false)

	// Import into a store that already has the same tenant seeded (colliding ids
	// for entities is not guaranteed, so instead import twice into the same dst).
	dst := memstore.New()
	svc := backup.New(dst)
	ctx := context.Background()
	if _, err := svc.Import(ctx, subj(tenant), b, backup.ModeSkip); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// mutate the subnet name in dst, then re-import: skip must NOT change it.
	sn, _ := dst.GetSubnet(ctx, tenant, subID)
	sn.Name = "mutated"
	if err := dst.UpdateSubnet(ctx, sn); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	resSkip, err := svc.Import(ctx, subj(tenant), b, backup.ModeSkip)
	if err != nil {
		t.Fatalf("skip import: %v", err)
	}
	if resSkip.SubnetsSkipped != 1 || resSkip.SubnetsImported != 0 {
		t.Fatalf("skip counts: %+v", resSkip)
	}
	after, _ := dst.GetSubnet(ctx, tenant, subID)
	if after.Name != "mutated" {
		t.Fatalf("skip overwrote data: %q", after.Name)
	}

	// overwrite must restore the original name.
	resOw, err := svc.Import(ctx, subj(tenant), b, backup.ModeOverwrite)
	if err != nil {
		t.Fatalf("overwrite import: %v", err)
	}
	if resOw.SubnetsImported != 1 {
		t.Fatalf("overwrite counts: %+v", resOw)
	}
	restored, _ := dst.GetSubnet(ctx, tenant, subID)
	if restored.Name != "net" {
		t.Fatalf("overwrite did not restore: %q", restored.Name)
	}
}

func TestImportBadSchema(t *testing.T) {
	m := memstore.New()
	svc := backup.New(m)
	_, err := svc.Import(context.Background(), subj(store.NewID()), backup.Backup{SchemaVersion: 999}, backup.ModeSkip)
	if !errors.Is(err, backup.ErrBadSchema) {
		t.Fatalf("want ErrBadSchema, got %v", err)
	}
}

func TestExportForbidden(t *testing.T) {
	m := memstore.New()
	svc := backup.New(m)
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.Export(context.Background(), bad, false); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestSetClock(t *testing.T) {
	m := memstore.New()
	svc := backup.New(m)
	fixed := time.Unix(1700000000, 0).UTC()
	svc.SetClock(func() time.Time { return fixed })
	tenant := store.NewID()
	seed(t, m, tenant)
	b, err := svc.Export(context.Background(), subj(tenant), false)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !b.ExportedAt.Equal(fixed) {
		t.Fatalf("ExportedAt = %v, want %v", b.ExportedAt, fixed)
	}
}

func TestImportForbidden(t *testing.T) {
	m := memstore.New()
	svc := backup.New(m)
	bad := authz.Subjects{ActorKind: authz.ActorUser}
	if _, err := svc.Import(context.Background(), bad, backup.Backup{SchemaVersion: backup.SchemaVersion}, backup.ModeSkip); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestOverwriteAllCollections(t *testing.T) {
	src := memstore.New()
	tenant := store.NewID()
	seed(t, src, tenant)
	b, _ := backup.New(src).Export(context.Background(), subj(tenant), false)

	dst := memstore.New()
	svc := backup.New(dst)
	ctx := context.Background()
	if _, err := svc.Import(ctx, subj(tenant), b, backup.ModeOverwrite); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// second overwrite import must delete+recreate every collection.
	res, err := svc.Import(ctx, subj(tenant), b, backup.ModeOverwrite)
	if err != nil {
		t.Fatalf("second overwrite: %v", err)
	}
	if res.VlansImported != 1 || res.LocationsImported != 1 || res.DevicesImported != 1 ||
		res.AddressesImported != 1 || res.IPGroupsImported != 1 || res.HostGroupsImported != 1 {
		t.Fatalf("overwrite counts: %+v", res)
	}
}

func TestImportCreateFailures(t *testing.T) {
	ctx := context.Background()
	// Each create path propagates the store error.
	for _, mth := range []string{
		"CreateLocation", "CreateVlan", "CreateSubnet", "CreateDevice",
		"CreateAddress", "CreateIPGroup", "CreateHostGroup",
		"AddIPGroupMember", "AddHostGroupMember",
	} {
		src := memstore.New()
		tenant := store.NewID()
		seed(t, src, tenant)
		b, _ := backup.New(src).Export(ctx, subj(tenant), false)

		dst := memstore.New()
		dst.FailNext(mth)
		if _, err := backup.New(dst).Import(ctx, subj(tenant), b, backup.ModeSkip); err == nil {
			t.Fatalf("%s: expected injected error", mth)
		}
	}
}

func TestImportDeleteFailures(t *testing.T) {
	ctx := context.Background()
	for _, mth := range []string{
		"DeleteLocation", "DeleteVlan", "DeleteSubnet", "DeleteDevice",
		"DeleteAddress", "DeleteIPGroup", "DeleteHostGroup",
	} {
		src := memstore.New()
		tenant := store.NewID()
		seed(t, src, tenant)
		b, _ := backup.New(src).Export(ctx, subj(tenant), false)

		dst := memstore.New()
		svc := backup.New(dst)
		if _, err := svc.Import(ctx, subj(tenant), b, backup.ModeOverwrite); err != nil {
			t.Fatalf("seed import: %v", err)
		}
		dst.FailNext(mth)
		if _, err := svc.Import(ctx, subj(tenant), b, backup.ModeOverwrite); err == nil {
			t.Fatalf("%s: expected injected delete error", mth)
		}
	}
}
