//go:build integration

package repodb_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/snmpcred"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestSubnetSNMPStore (021 T054): migration 0005 -> 0006 upgrade with a legacy
// warden reference, RLS isolation of ipam_subnet_snmp, cascade on subnet
// delete, a seal/open round trip through the real store, the CHECK
// constraints, and the scan job SNMP phase columns (T045).
func TestSubnetSNMPStore(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, adminDSN, 5); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := repodb.New(st)
	legacy := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "legacy", CIDR: "10.9.0.0/24", IPVersion: 4, SNMPSecretRef: "warden-snmp", SNMPVersion: 2}
	if err := db.CreateSubnet(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatalf("upgrade to 0006: %v", err)
	}
	if n, err := db.LegacySNMPRefCount(ctx); err != nil || n != 1 {
		t.Fatalf("legacy count %d %v", n, err)
	}

	parent := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "parent", CIDR: "10.0.0.0/16", IPVersion: 4}
	child := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "child", CIDR: "10.0.1.0/24", IPVersion: 4, ParentID: parent.ID}
	other := store.Subnet{ID: store.NewID(), TenantID: tenantB, Name: "other", CIDR: "10.0.0.0/16", IPVersion: 4}
	for _, s := range []store.Subnet{parent, child, other} {
		if err := db.CreateSubnet(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	env, _ := sealed.NewEnvelope(bytes.Repeat([]byte{2}, 32))
	in := snmpcred.Input{Version: 3, User: "lab", SecurityLevel: snmpcred.LevelAuthPriv, AuthProtocol: "SHA256", AuthPassword: "authpass1",
		PrivProtocol: "AES256", PrivPassword: "privpass1"}
	blob, err := snmpcred.Seal(env, tenantA, parent.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	m := in.Meta()
	row := store.SubnetSNMP{TenantID: tenantA, SubnetID: parent.ID, Version: m.Version, SecurityLevel: m.SecurityLevel,
		AuthProtocol: m.AuthProtocol, PrivProtocol: m.PrivProtocol, Sealed: blob, UpdatedBy: "u1"}
	audit := store.AuditRow{Action: "snmp_credentials_set", ActorKind: "user", ActorID: "u1", SubjectKind: "subnet", SubjectID: parent.ID, Outcome: "ok",
		Detail: map[string]any{"protocol_version": 3}}
	if err := db.PutSubnetSNMP(ctx, row, audit); err != nil {
		t.Fatal(err)
	}
	// Replace keeps one row.
	if err := db.PutSubnetSNMP(ctx, row, audit); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetSubnetSNMP(ctx, tenantA, parent.ID)
	if err != nil || got.Version != 3 || got.PrivProtocol != "AES256" || got.UpdatedAt.IsZero() {
		t.Fatalf("get %+v %v", got, err)
	}
	sec, err := snmpcred.Open(env, tenantA, parent.ID, got.Sealed)
	if err != nil || sec.User != "lab" || sec.PrivPassword != "privpass1" {
		t.Fatalf("round trip through the store: %v", err)
	}
	if _, err := snmpcred.Open(env, tenantA, child.ID, got.Sealed); !errors.Is(err, snmpcred.ErrUnreadable) {
		t.Fatal("blob opens for another subnet")
	}
	list, err := db.ListSubnetSNMP(ctx, tenantA)
	if err != nil || len(list) != 1 || list[0].Sealed != nil {
		t.Fatalf("list %+v %v", list, err)
	}

	// RLS: tenant B sees nothing and cannot write to tenant A's subnet.
	if l, _ := db.ListSubnetSNMP(ctx, tenantB); len(l) != 0 {
		t.Fatal("tenant B lists tenant A credentials")
	}
	if _, err := db.GetSubnetSNMP(ctx, tenantB, parent.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant B reads tenant A credentials: %v", err)
	}
	if err := db.PutSubnetSNMP(ctx, store.SubnetSNMP{TenantID: tenantB, SubnetID: parent.ID, Version: 2, Sealed: []byte{1}}, store.AuditRow{Action: "snmp_credentials_set", ActorKind: "user", SubjectKind: "subnet", Outcome: "ok"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant B writes on tenant A subnet: %v", err)
	}
	if err := db.DeleteSubnetSNMP(ctx, tenantB, parent.ID, store.AuditRow{}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant B deletes tenant A credentials: %v", err)
	}
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	var n int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM ipam_audit_events WHERE action='snmp_credentials_set'").Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit rows in the same transaction: %d %v", n, err)
	}

	// CHECK constraints reject inconsistent rows.
	for name, bad := range map[string]store.SubnetSNMP{
		"version 1":        {TenantID: tenantA, SubnetID: child.ID, Version: 1, Sealed: []byte{1}},
		"v2c with level":   {TenantID: tenantA, SubnetID: child.ID, Version: 2, SecurityLevel: "authPriv", Sealed: []byte{1}},
		"v3 without auth":  {TenantID: tenantA, SubnetID: child.ID, Version: 3, SecurityLevel: "authNoPriv", Sealed: []byte{1}},
		"authPriv no priv": {TenantID: tenantA, SubnetID: child.ID, Version: 3, SecurityLevel: "authPriv", AuthProtocol: "SHA", Sealed: []byte{1}},
		"empty blob":       {TenantID: tenantA, SubnetID: child.ID, Version: 2, Sealed: []byte{}},
	} {
		if err := db.PutSubnetSNMP(ctx, bad, store.AuditRow{Action: "snmp_credentials_set", ActorKind: "user", SubjectKind: "subnet", Outcome: "ok"}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// Scan job SNMP phase columns persist (T045) and survive the claim.
	job := store.IPScanJob{ID: store.NewID(), TenantID: tenantA, SubnetID: child.ID, Status: store.ScanPending, TriggeredBy: store.TriggerManual}
	if err := db.CreateScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimDueScanJobs(ctx, time.Now().Add(time.Hour), 5)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claim %v %v", claimed, err)
	}
	j := claimed[0]
	j.SNMPStatus, j.SNMPSourceSubnetID, j.SNMPProbed, j.SNMPNoAnswer, j.SNMPRejected, j.SNMPDiscoveredCount = store.SNMPRan, parent.ID, 7, 3, 2, 1
	if err := db.UpdateScanJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListScanJobs(ctx, tenantA, store.ScanFilter{})
	if err != nil || len(jobs) != 1 || jobs[0].SNMPStatus != store.SNMPRan || jobs[0].SNMPSourceSubnetID != parent.ID ||
		jobs[0].SNMPProbed != 7 || jobs[0].SNMPNoAnswer != 3 || jobs[0].SNMPRejected != 2 {
		t.Fatalf("scan job phase %+v %v", jobs, err)
	}

	// Deleting the subnet deletes its credentials (FK cascade).
	if err := db.DeleteSubnet(ctx, tenantA, parent.ID, true); err != nil {
		t.Fatal(err)
	}
	if l, _ := db.ListSubnetSNMP(ctx, tenantA); len(l) != 0 {
		t.Fatal("credentials survived the subnet")
	}
	if err := db.DeleteSubnetSNMP(ctx, tenantA, parent.ID, store.AuditRow{}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete after cascade: %v", err)
	}
}
