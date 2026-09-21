//go:build integration

// Package repodb integration test: exercises the TimescaleDB-backed IPAM store
// and its repo wrappers against a real database (testcontainers). It covers
// migration, subnet + address CRUD, the duplicate-address / allocation guard
// (ErrConflict on SQLSTATE 23505), UpsertAddressByAddress created/updated,
// devices + interfaces + links + packages, vlan uniqueness, the location tree,
// ip-group members (the CheckIp data path), scan-job claiming via FOR UPDATE SKIP
// LOCKED, per-tenant row-level security, and tenant statistics. Run with:
//
//	go test -tags integration ./internal/repo/repodb/
//
// It skips cleanly when Docker/testcontainers is unavailable.
package repodb_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/go-freya/freya/services/ipam/internal/repo"
	"github.com/go-freya/freya/services/ipam/internal/repo/repodb"
	"github.com/go-freya/freya/services/ipam/internal/store"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

func startDB(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "timescale/timescaledb:latest-pg16", ExposedPorts: []string{"5432/tcp"},
			Env:        map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "ipam"},
			WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2 * time.Minute),
		}, Started: true,
	})
	if err != nil {
		t.Skipf("testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	adminDSN = "postgres://postgres:test@" + host + ":" + port.Port() + "/ipam?sslmode=disable"
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Exec(ctx, "CREATE ROLE ipam_app LOGIN PASSWORD 'app' NOBYPASSRLS")
	_ = conn.Close(ctx)
	appDSN = "postgres://ipam_app:app@" + host + ":" + port.Port() + "/ipam?sslmode=disable"
	return
}

func openRepo(t *testing.T) repo.Store {
	t.Helper()
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.Migrate(ctx, adminDSN); err != nil { // idempotent
		t.Fatalf("migrate idempotent: %v", err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return repodb.New(st)
}

func TestIPAMRepo(t *testing.T) {
	db := openRepo(t)
	ctx := context.Background()

	// --- Location tree ---
	loc := store.Location{ID: store.NewID(), TenantID: tenantA, Name: "dc1", Code: "DC1", LocationType: store.LocDatacenter}
	if err := db.CreateLocation(ctx, loc); err != nil {
		t.Fatalf("create location: %v", err)
	}
	child := store.Location{ID: store.NewID(), TenantID: tenantA, Name: "rack-1", Code: "R1", LocationType: store.LocRack, ParentID: loc.ID}
	if err := db.CreateLocation(ctx, child); err != nil {
		t.Fatalf("create child location: %v", err)
	}
	if got, err := db.GetLocation(ctx, tenantA, loc.ID); err != nil || got.ChildCount != 1 {
		t.Fatalf("location child count = %d (%v)", got.ChildCount, err)
	}
	// Duplicate name -> conflict.
	if err := db.CreateLocation(ctx, store.Location{ID: store.NewID(), TenantID: tenantA, Name: "dc1"}); err != repo.ErrConflict {
		t.Fatalf("duplicate location name should conflict, got %v", err)
	}

	// --- VLAN uniqueness ---
	vlan := store.Vlan{ID: store.NewID(), TenantID: tenantA, VlanID: 100, Name: "vlan100", LocationID: loc.ID}
	if err := db.CreateVlan(ctx, vlan); err != nil {
		t.Fatalf("create vlan: %v", err)
	}
	if err := db.CreateVlan(ctx, store.Vlan{ID: store.NewID(), TenantID: tenantA, VlanID: 100, Name: "other"}); err != repo.ErrConflict {
		t.Fatalf("duplicate vlan_id should conflict, got %v", err)
	}
	if err := db.CreateVlan(ctx, store.Vlan{ID: store.NewID(), TenantID: tenantA, VlanID: 101, Name: "vlan100"}); err != repo.ErrConflict {
		t.Fatalf("duplicate vlan name should conflict, got %v", err)
	}

	// --- Subnet CRUD ---
	subnet := store.Subnet{
		ID: store.NewID(), TenantID: tenantA, Name: "net-a", CIDR: "10.0.0.0/24",
		VlanID: vlan.ID, LocationID: loc.ID, PrefixLength: 24, IPVersion: 4,
	}
	if err := db.CreateSubnet(ctx, subnet); err != nil {
		t.Fatalf("create subnet: %v", err)
	}
	if err := db.CreateSubnet(ctx, store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "net-a", CIDR: "10.1.0.0/24"}); err != repo.ErrConflict {
		t.Fatalf("duplicate subnet name should conflict, got %v", err)
	}
	if got, err := db.GetSubnet(ctx, tenantA, subnet.ID); err != nil || got.TotalAddresses != 256 {
		t.Fatalf("subnet total addresses = %d (%v)", got.TotalAddresses, err)
	}
	if subs, err := db.SubnetsForVlan(ctx, tenantA, vlan.ID); err != nil || len(subs) != 1 {
		t.Fatalf("subnets for vlan = %d (%v)", len(subs), err)
	}
	if v, err := db.GetVlan(ctx, tenantA, vlan.ID); err != nil || v.SubnetCount != 1 {
		t.Fatalf("vlan subnet count = %d (%v)", v.SubnetCount, err)
	}

	// --- Address CRUD + allocation guard ---
	addr := store.IPAddress{ID: store.NewID(), TenantID: tenantA, Address: "10.0.0.5", SubnetID: subnet.ID, Hostname: "h5"}
	if err := db.CreateAddress(ctx, addr); err != nil {
		t.Fatalf("create address: %v", err)
	}
	// Duplicate (tenant, address) -> ErrConflict (the allocation guard).
	if err := db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantA, Address: "10.0.0.5", SubnetID: subnet.ID}); err != repo.ErrConflict {
		t.Fatalf("duplicate address should conflict, got %v", err)
	}
	if got, err := db.FindAddress(ctx, tenantA, "10.0.0.5"); err != nil || got.Hostname != "h5" {
		t.Fatalf("find address: %+v %v", got, err)
	}
	if n, err := db.CountAddressesInSubnet(ctx, tenantA, subnet.ID); err != nil || n != 1 {
		t.Fatalf("count addresses = %d (%v)", n, err)
	}
	if got, err := db.GetSubnet(ctx, tenantA, subnet.ID); err != nil || got.UsedAddresses != 1 || got.AvailableAddresses != 255 {
		t.Fatalf("subnet utilization used=%d avail=%d (%v)", got.UsedAddresses, got.AvailableAddresses, err)
	}
	if list, err := db.ListAllocatedAddresses(ctx, tenantA, subnet.ID); err != nil || len(list) != 1 || list[0] != "10.0.0.5" {
		t.Fatalf("allocated addresses: %+v %v", list, err)
	}

	// --- UpsertAddressByAddress created / updated ---
	created, err := db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.0.0.9", SubnetID: subnet.ID, Hostname: "new"})
	if err != nil || !created {
		t.Fatalf("upsert new should create: created=%v err=%v", created, err)
	}
	created, err = db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.0.0.9", SubnetID: subnet.ID, Hostname: "updated"})
	if err != nil || created {
		t.Fatalf("upsert existing should update: created=%v err=%v", created, err)
	}
	if got, err := db.FindAddress(ctx, tenantA, "10.0.0.9"); err != nil || got.Hostname != "updated" {
		t.Fatalf("upsert did not update hostname: %+v %v", got, err)
	}

	// --- Device + interface + links + packages ---
	dev, err := db.UpsertDeviceByName(ctx, store.Device{TenantID: tenantA, Name: "sw1", DeviceType: store.DevSwitch, LocationID: loc.ID})
	if err != nil || dev.ID == "" {
		t.Fatalf("upsert device: %+v %v", dev, err)
	}
	dev2, err := db.UpsertDeviceByName(ctx, store.Device{TenantID: tenantA, Name: "sw1", Manufacturer: "Cisco"})
	if err != nil || dev2.ID != dev.ID || dev2.Manufacturer != "Cisco" {
		t.Fatalf("upsert device should reuse row: %+v %v", dev2, err)
	}
	if err := db.CreateDevice(ctx, store.Device{ID: store.NewID(), TenantID: tenantA, Name: "sw1"}); err != repo.ErrConflict {
		t.Fatalf("duplicate device name should conflict, got %v", err)
	}
	iface, err := db.UpsertInterfaceByName(ctx, store.DeviceInterface{TenantID: tenantA, DeviceID: dev.ID, Name: "eth0", MACAddress: "aa:bb"})
	if err != nil || iface.ID == "" {
		t.Fatalf("upsert interface: %+v %v", iface, err)
	}
	iface2, err := db.UpsertInterfaceByName(ctx, store.DeviceInterface{TenantID: tenantA, DeviceID: dev.ID, Name: "eth0", MACAddress: "cc:dd"})
	if err != nil || iface2.ID != iface.ID || iface2.MACAddress != "cc:dd" {
		t.Fatalf("upsert interface should reuse row: %+v %v", iface2, err)
	}
	now := time.Now().UTC()
	if err := db.ReplaceInterfaceLinks(ctx, tenantA, iface.ID, []store.DeviceInterfaceLink{
		{TenantID: tenantA, InterfaceID: iface.ID, RemoteDeviceID: "rd1", LinkSource: store.LinkLLDP, LinkLastSeen: &now},
	}); err != nil {
		t.Fatalf("replace interface links: %v", err)
	}
	if links, err := db.ListInterfaceLinks(ctx, tenantA, iface.ID); err != nil || len(links) != 1 || links[0].RemoteDeviceID != "rd1" {
		t.Fatalf("list interface links: %+v %v", links, err)
	}
	if err := db.ReplaceDevicePackages(ctx, tenantA, dev.ID, []store.DevicePackage{
		{Name: "openssl", NeedsUpdate: true, IsSecurityUpdate: true, PackageManager: "apt"},
		{Name: "bash", NeedsUpdate: false},
	}); err != nil {
		t.Fatalf("replace packages: %v", err)
	}
	yes := true
	if pkgs, err := db.ListDevicePackages(ctx, tenantA, dev.ID, &yes, nil, ""); err != nil || len(pkgs) != 1 || pkgs[0].Name != "openssl" {
		t.Fatalf("list packages needs-update: %+v %v", pkgs, err)
	}
	if got, err := db.GetDevice(ctx, tenantA, dev.ID); err != nil || got.InterfaceCount != 1 || got.PackageUpdateCount != 1 || got.SecurityUpdateCount != 1 {
		t.Fatalf("device counts: if=%d pu=%d su=%d (%v)", got.InterfaceCount, got.PackageUpdateCount, got.SecurityUpdateCount, err)
	}

	// --- Host groups + device membership ---
	hg := store.HostGroup{ID: store.NewID(), TenantID: tenantA, Name: "core-switches"}
	if err := db.CreateHostGroup(ctx, hg); err != nil {
		t.Fatalf("create host group: %v", err)
	}
	if err := db.AddHostGroupMember(ctx, store.HostGroupMember{TenantID: tenantA, HostGroupID: hg.ID, DeviceID: dev.ID}); err != nil {
		t.Fatalf("add host group member: %v", err)
	}
	if err := db.AddHostGroupMember(ctx, store.HostGroupMember{TenantID: tenantA, HostGroupID: hg.ID, DeviceID: dev.ID}); err != repo.ErrConflict {
		t.Fatalf("duplicate host member should conflict, got %v", err)
	}
	if mems, err := db.ListHostGroupMembers(ctx, tenantA, hg.ID); err != nil || len(mems) != 1 || mems[0].DeviceName != "sw1" {
		t.Fatalf("list host group members (enriched): %+v %v", mems, err)
	}
	if hgs, err := db.ListDeviceHostGroups(ctx, tenantA, dev.ID); err != nil || len(hgs) != 1 {
		t.Fatalf("list device host groups: %+v %v", hgs, err)
	}

	// --- IP group members (CheckIp data path) ---
	ipg := store.IPGroup{ID: store.NewID(), TenantID: tenantA, Name: "allowlist"}
	if err := db.CreateIPGroup(ctx, ipg); err != nil {
		t.Fatalf("create ip group: %v", err)
	}
	if err := db.AddIPGroupMember(ctx, store.IPGroupMember{TenantID: tenantA, IPGroupID: ipg.ID, MemberType: store.MemberAddress, Value: "10.0.0.5"}); err != nil {
		t.Fatalf("add ip group member: %v", err)
	}
	if err := db.AddIPGroupMember(ctx, store.IPGroupMember{TenantID: tenantA, IPGroupID: ipg.ID, MemberType: store.MemberAddress, Value: "10.0.0.5"}); err != repo.ErrConflict {
		t.Fatalf("duplicate ip member value should conflict, got %v", err)
	}
	groups, members, err := db.AllIPGroupsWithMembers(ctx, tenantA, nil)
	if err != nil || len(groups) != 1 || len(members[ipg.ID]) != 1 || members[ipg.ID][0].Value != "10.0.0.5" {
		t.Fatalf("all ip groups with members: groups=%d members=%+v %v", len(groups), members, err)
	}

	// --- Scan job claim (FOR UPDATE SKIP LOCKED, system scope) ---
	job := store.IPScanJob{ID: store.NewID(), TenantID: tenantA, SubnetID: subnet.ID, Status: store.ScanPending, TriggeredBy: store.TriggerManual}
	if err := db.CreateScanJob(ctx, job); err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	if active, err := db.ActiveScanForSubnet(ctx, tenantA, subnet.ID); err != nil || !active {
		t.Fatalf("active scan for subnet = %v (%v)", active, err)
	}
	claimed, err := db.ClaimDueScanJobs(ctx, time.Now().UTC(), 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID || claimed[0].Status != store.ScanScanning {
		t.Fatalf("claim due scan jobs: %+v %v", claimed, err)
	}
	// Already claimed (scanning) -> not returned again.
	if again, err := db.ClaimDueScanJobs(ctx, time.Now().UTC(), 10); err != nil || len(again) != 0 {
		t.Fatalf("second claim should be empty: %+v %v", again, err)
	}

	// --- DNS config upsert ---
	if err := db.UpsertDNSConfig(ctx, store.DNSConfig{TenantID: tenantA, DNSServers: []string{"1.1.1.1"}, ReverseDNSEnabled: true}); err != nil {
		t.Fatalf("upsert dns config: %v", err)
	}
	if c, err := db.GetDNSConfig(ctx, tenantA); err != nil || len(c.DNSServers) != 1 || c.TimeoutMs != 5000 {
		t.Fatalf("get dns config: %+v %v", c, err)
	}

	// --- TenantStats ---
	st, err := db.TenantStats(ctx, tenantA)
	if err != nil {
		t.Fatalf("tenant stats: %v", err)
	}
	if st.TotalSubnets != 1 || st.TotalVlans != 1 || st.TotalDevices != 1 || st.TotalLocations != 2 {
		t.Fatalf("stats totals: %+v", st)
	}
	if st.TotalAddresses != 256 || st.UsedAddresses != 2 || st.DevicesByType[store.DevSwitch] != 1 {
		t.Fatalf("stats addresses/devices: %+v", st)
	}

	// --- Audit + TenantIDs (system scope) ---
	if err := db.AppendAudit(ctx, store.AuditRow{TenantID: tenantA, ActorKind: "user", Action: "subnet_created", SubjectKind: "subnet", SubjectID: subnet.ID, Outcome: "ok", Detail: map[string]any{"k": "v"}}); err != nil {
		t.Fatalf("append audit: %v", err)
	}
	if ids, err := db.TenantIDs(ctx); err != nil || len(ids) == 0 {
		t.Fatalf("tenant ids: %+v %v", ids, err)
	}

	// --- RLS isolation: tenant B sees nothing of tenant A ---
	if _, err := db.GetSubnet(ctx, tenantB, subnet.ID); err != repo.ErrNotFound {
		t.Fatalf("RLS breach: tenant B read tenant A subnet: %v", err)
	}
	if l, err := db.ListSubnets(ctx, tenantB, store.SubnetFilter{}); err != nil || len(l) != 0 {
		t.Fatalf("RLS breach: tenant B listed tenant A subnets: %d %v", len(l), err)
	}
	if _, err := db.FindAddress(ctx, tenantB, "10.0.0.5"); err != repo.ErrNotFound {
		t.Fatalf("RLS breach: tenant B found tenant A address: %v", err)
	}
	// Tenant B can create a colliding subnet name / address in its own scope.
	subB := store.Subnet{ID: store.NewID(), TenantID: tenantB, Name: "net-a", CIDR: "10.0.0.0/24"}
	if err := db.CreateSubnet(ctx, subB); err != nil {
		t.Fatalf("tenant B create own subnet: %v", err)
	}
	if err := db.CreateAddress(ctx, store.IPAddress{ID: store.NewID(), TenantID: tenantB, Address: "10.0.0.5", SubnetID: subB.ID}); err != nil {
		t.Fatalf("tenant B create own colliding address: %v", err)
	}

	// --- Delete guards ---
	if err := db.DeleteSubnet(ctx, tenantA, subnet.ID, false); err != repo.ErrNotEmpty {
		t.Fatalf("delete non-empty subnet without force should be ErrNotEmpty, got %v", err)
	}
	if err := db.DeleteSubnet(ctx, tenantA, subnet.ID, true); err != nil {
		t.Fatalf("force delete subnet: %v", err)
	}
	if _, err := db.GetSubnet(ctx, tenantA, subnet.ID); err != repo.ErrNotFound {
		t.Fatalf("subnet should be deleted, got %v", err)
	}
	// Cascade removed its addresses and scan jobs.
	if _, err := db.FindAddress(ctx, tenantA, "10.0.0.9"); err != repo.ErrNotFound {
		t.Fatalf("cascade should remove address, got %v", err)
	}
}
