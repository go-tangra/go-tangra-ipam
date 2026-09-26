package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

func snmpRow(tenant, subnet string, version int) store.SubnetSNMP {
	r := store.SubnetSNMP{TenantID: tenant, SubnetID: subnet, Version: version, Sealed: []byte{1, 2, 3}, UpdatedBy: "u1"}
	if version == 3 {
		r.SecurityLevel, r.AuthProtocol = "authNoPriv", "SHA256"
	}
	return r
}

func TestSubnetSNMPCRUD(t *testing.T) {
	ctx := context.Background()
	m := New()
	for _, s := range []store.Subnet{
		{ID: "s1", TenantID: "t1", Name: "a", CIDR: "10.0.0.0/24"},
		{ID: "s2", TenantID: "t1", Name: "b", CIDR: "10.0.1.0/24", SNMPSecretRef: "legacy"},
		{ID: "s9", TenantID: "t2", Name: "c", CIDR: "10.9.0.0/24", SNMPSecretRef: "legacy"},
	} {
		if err := m.CreateSubnet(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.GetSubnetSNMP(ctx, "t1", "s1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get none: %v", err)
	}
	audit := store.AuditRow{TenantID: "t1", Action: "snmp_credentials_set", SubjectID: "s1"}
	if err := m.PutSubnetSNMP(ctx, snmpRow("t1", "s1", 2), audit); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetSubnetSNMP(ctx, "t1", "s1")
	if err != nil || got.Version != 2 || string(got.Sealed) != "\x01\x02\x03" || got.UpdatedAt.IsZero() {
		t.Fatalf("get: %+v %v", got, err)
	}
	// Replace keeps one row per subnet.
	if err := m.PutSubnetSNMP(ctx, snmpRow("t1", "s1", 3), audit); err != nil {
		t.Fatal(err)
	}
	if err := m.PutSubnetSNMP(ctx, snmpRow("t1", "s2", 2), audit); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListSubnetSNMP(ctx, "t1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	for _, r := range list {
		if r.Sealed != nil {
			t.Fatal("list carries metadata only, never the blob")
		}
		if r.SubnetID == "s1" && (r.Version != 3 || r.AuthProtocol != "SHA256") {
			t.Fatalf("replaced row %+v", r)
		}
	}
	// Tenant isolation: another tenant sees nothing and cannot write to t1's subnet.
	if l, _ := m.ListSubnetSNMP(ctx, "t2"); len(l) != 0 {
		t.Fatal("t2 sees t1 rows")
	}
	if _, err := m.GetSubnetSNMP(ctx, "t2", "s1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("t2 reads t1 row")
	}
	if err := m.PutSubnetSNMP(ctx, snmpRow("t2", "s1", 2), audit); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant put: %v", err)
	}
	if err := m.PutSubnetSNMP(ctx, snmpRow("t1", "missing", 2), audit); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("put on unknown subnet: %v", err)
	}
	// Each write carried its audit row.
	n := 0
	for _, a := range m.Audit() {
		if a.Action == "snmp_credentials_set" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("audit rows %d, want 3", n)
	}
	// Delete: row gone + audit; again => not found without audit.
	if err := m.DeleteSubnetSNMP(ctx, "t1", "s2", store.AuditRow{TenantID: "t1", Action: "snmp_credentials_cleared"}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteSubnetSNMP(ctx, "t1", "s2", store.AuditRow{TenantID: "t1", Action: "snmp_credentials_cleared"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	cleared := 0
	for _, a := range m.Audit() {
		if a.Action == "snmp_credentials_cleared" {
			cleared++
		}
	}
	if cleared != 1 {
		t.Fatalf("cleared audit rows %d", cleared)
	}
	// Deleting the subnet removes its credentials (cascade).
	if err := m.DeleteSubnet(ctx, "t1", "s1", true); err != nil {
		t.Fatal(err)
	}
	if l, _ := m.ListSubnetSNMP(ctx, "t1"); len(l) != 0 {
		t.Fatalf("cascade: %v", l)
	}
	// Legacy warden references are counted across tenants.
	if n, err := m.LegacySNMPRefCount(ctx); err != nil || n != 2 {
		t.Fatalf("legacy count %d %v", n, err)
	}
}

func TestSubnetSNMPInjectedFailures(t *testing.T) {
	ctx := context.Background()
	m := New()
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "a", CIDR: "10.0.0.0/24"})
	for _, name := range []string{"GetSubnetSNMP", "PutSubnetSNMP", "DeleteSubnetSNMP", "ListSubnetSNMP", "LegacySNMPRefCount"} {
		m.FailNext(name)
		var err error
		switch name {
		case "GetSubnetSNMP":
			_, err = m.GetSubnetSNMP(ctx, "t1", "s1")
		case "PutSubnetSNMP":
			err = m.PutSubnetSNMP(ctx, snmpRow("t1", "s1", 2), store.AuditRow{})
		case "DeleteSubnetSNMP":
			err = m.DeleteSubnetSNMP(ctx, "t1", "s1", store.AuditRow{})
		case "ListSubnetSNMP":
			_, err = m.ListSubnetSNMP(ctx, "t1")
		case "LegacySNMPRefCount":
			_, err = m.LegacySNMPRefCount(ctx)
		}
		if err == nil || errors.Is(err, repo.ErrNotFound) {
			t.Errorf("%s: want injected error, got %v", name, err)
		}
	}
}

// TestScanJobSNMPFields (T045): the SNMP phase fields persist and list.
func TestScanJobSNMPFields(t *testing.T) {
	ctx := context.Background()
	m := New()
	_ = m.CreateSubnet(ctx, store.Subnet{ID: "s1", TenantID: "t1", Name: "a", CIDR: "10.0.0.0/24"})
	j := store.IPScanJob{ID: "j1", TenantID: "t1", SubnetID: "s1", Status: store.ScanPending}
	if err := m.CreateScanJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j.SNMPStatus, j.SNMPSourceSubnetID, j.SNMPProbed, j.SNMPNoAnswer, j.SNMPRejected = store.SNMPRan, "s1", 5, 2, 1
	if err := m.UpdateScanJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	l, _ := m.ListScanJobs(ctx, "t1", store.ScanFilter{})
	if len(l) != 1 || l[0].SNMPStatus != store.SNMPRan || l[0].SNMPSourceSubnetID != "s1" || l[0].SNMPProbed != 5 || l[0].SNMPNoAnswer != 2 || l[0].SNMPRejected != 1 {
		t.Fatalf("scan job %+v", l)
	}
}
