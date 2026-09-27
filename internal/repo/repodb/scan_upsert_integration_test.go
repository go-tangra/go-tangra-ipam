//go:build integration

package repodb_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestScanUpsertKeepsAdminFields: a scan of an already-known address records
// only what it observed (liveness, a found reverse-DNS name). It must never
// blank or reset administrator fields — description, note, tags, device
// binding, status, type, subnet, DNS/PTR — which it used to do.
func TestScanUpsertKeepsAdminFields(t *testing.T) {
	adminDSN, appDSN := startDB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, adminDSN); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, appDSN, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := repodb.New(st)

	sub := store.Subnet{ID: store.NewID(), TenantID: tenantA, Name: "lab", CIDR: "10.40.0.0/24", Status: "active", IPVersion: 4}
	if err := db.CreateSubnet(ctx, sub); err != nil {
		t.Fatal(err)
	}
	orig := store.IPAddress{ID: store.NewID(), TenantID: tenantA, Address: "10.40.0.5", SubnetID: sub.ID,
		Hostname: "printer-2", Description: "2nd floor printer", Note: "ask facilities",
		Tags: map[string]string{"room": "204"}, Status: store.IPReserved, AddressType: store.AddrGateway,
		PTRRecord: "printer-2.lab.", DNSName: "printer-2.lab"}
	if err := db.CreateAddress(ctx, orig); err != nil {
		t.Fatal(err)
	}

	seen := time.Now().UTC().Truncate(time.Second)
	created, err := db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.40.0.5",
		SubnetID: sub.ID, Status: store.IPActive, AddressType: store.AddrHost, LastSeen: &seen})
	if err != nil || created {
		t.Fatalf("upsert: created=%v err=%v", created, err)
	}
	got, err := db.GetAddress(ctx, tenantA, orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != orig.Description || got.Note != orig.Note || got.Tags["room"] != "204" ||
		got.Hostname != "printer-2" || got.Status != store.IPReserved || got.AddressType != store.AddrGateway ||
		got.PTRRecord != orig.PTRRecord || got.DNSName != orig.DNSName || got.SubnetID != sub.ID {
		t.Fatalf("scan changed administrator fields: %+v", got)
	}
	if got.LastSeen == nil || !got.LastSeen.Equal(seen) {
		t.Fatalf("last_seen not recorded: %v", got.LastSeen)
	}

	// An offline address that answers becomes active; a found reverse-DNS
	// name is recorded; administrator fields still survive.
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `UPDATE ipam_ip_addresses SET status='offline' WHERE id=$1`, orig.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertAddressByAddress(ctx, store.IPAddress{TenantID: tenantA, Address: "10.40.0.5",
		SubnetID: sub.ID, Hostname: "printer-2.lab", HasReverseDNS: true, Status: store.IPActive,
		AddressType: store.AddrHost, LastSeen: &seen}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAddress(ctx, tenantA, orig.ID)
	if got.Status != store.IPActive || got.Hostname != "printer-2.lab" || got.Description != orig.Description {
		t.Fatalf("offline→active / rDNS: %+v", got)
	}
}
