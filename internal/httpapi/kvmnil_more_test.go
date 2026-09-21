package httpapi

import (
	"context"
	"testing"

	"github.com/go-freya/freya/internal/testrt"
	"github.com/go-freya/freya/internal/testutil"
	"github.com/go-freya/freya/services/auth/pkg/authclient"

	"github.com/go-freya/freya/services/ipam/internal/addresses"
	"github.com/go-freya/freya/services/ipam/internal/backup"
	"github.com/go-freya/freya/services/ipam/internal/devices"
	"github.com/go-freya/freya/services/ipam/internal/dnscfg"
	"github.com/go-freya/freya/services/ipam/internal/groups"
	"github.com/go-freya/freya/services/ipam/internal/ipmi"
	"github.com/go-freya/freya/services/ipam/internal/locations"
	"github.com/go-freya/freya/services/ipam/internal/memstore"
	"github.com/go-freya/freya/services/ipam/internal/scan"
	"github.com/go-freya/freya/services/ipam/internal/stats"
	"github.com/go-freya/freya/services/ipam/internal/subnets"
	"github.com/go-freya/freya/services/ipam/internal/vlans"
	"github.com/go-freya/freya/services/ipam/internal/warden"
)

// newAPINoKVM builds an API whose Deps.KVM is nil, so the KVM console route
// reports 501 not_implemented.
func newAPINoKVM(t *testing.T) *apiFixture {
	t.Helper()
	mem := memstore.New()
	rt := testrt.New(t, testutil.MustCA("example.org"), "ipam")

	pub := &recPub{}
	wf := warden.NewFake()
	wf.Put(bmcRef, map[string]string{
		"username": bmcUser, "password": bmcPass, "protocol": "2.0", "port": "623",
	}, warden.SecretMeta{Name: "bmc-1", Description: "test BMC creds"})

	dns := dnscfg.New(mem)
	dns.SetLookup(func(_ context.Context, _ string) ([]string, error) {
		return []string{"host.example.org."}, nil
	})

	bmc := ipmi.NewFake()
	deps := Deps{
		Subnets:   subnets.New(mem),
		Addresses: addresses.New(mem, pub, 0, 0),
		Devices:   devices.New(mem),
		Vlans:     vlans.New(mem),
		Locations: locations.New(mem),
		Groups:    groups.New(mem),
		Stats:     stats.New(mem),
		Backup:    backup.New(mem),
		DNS:       dns,
		Scan:      scan.New(mem, nil, nil, nil, wf, pub, scan.Config{MaxHosts: 65536}, nil),
		BMC:       bmc,
		KVM:       nil, // <- exercises the KVM==nil branch
		Warden:    wf,
	}

	v := fakeVerifier{ids: map[string]authclient.Identity{
		"admin": {UserID: apiAdmin, TenantID: apiTenant, Roles: []string{"admin"}},
		"user":  {UserID: apiUser, TenantID: apiTenant, Roles: []string{"user"}},
	}}
	s, err := NewHandler(rt, WithVerifier(v))
	if err != nil {
		t.Fatal(err)
	}
	s.Register(deps)
	return &apiFixture{s: s, mem: mem, bmc: bmc, warden: wf}
}

// TestKVMSessionNotImplemented covers the KVM==nil branch: the console route
// returns 501 when no KVM manager is wired.
func TestKVMSessionNotImplemented(t *testing.T) {
	f := newAPINoKVM(t)
	did := f.newDeviceWithBMC(t)
	if w := f.req(t, "POST", p+"/devices/"+did+"/kvm-session", "admin", ""); w.Code != 501 {
		t.Fatalf("kvm-session no manager: want 501, got %d %s", w.Code, w.Body)
	}
}
