package grpcapi

import (
	"context"
	"io"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	ipamv1 "github.com/go-tangra/go-tangra-ipam/sdk/v4/api/proto/ipam/v1"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/ipmi"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/kvm"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/warden"
)

// TestMeshPowerForwardsUserToken (024 T031): the mesh power/KVM RPCs fetch the
// BMC credentials with the platform token the gateway forwarded; without one
// warden is never asked and the BMC never contacted; reasons map to codes.
func TestMeshPowerForwardsUserToken(t *testing.T) {
	k := newKit(t)
	const ref = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"
	k.warden.Put(ref, warden.SecretMeta{Name: "bmc", Username: "admin"}, "pw")
	withCaller(t, "spiffe://example.org/svc/console", []string{"platform-admin"}, true)
	bg := context.Background()
	dev, err := k.device.Create(bg, &ipamv1.CreateDeviceRequest{TenantId: tenant, Device: &ipamv1.Device{
		Name: "srv", DeviceType: ipamv1.DeviceType_DEVICE_TYPE_SERVER, ManagementIp: "10.0.0.9"}})
	if err != nil {
		t.Fatal(err)
	}
	// Not configured yet.
	user := metadata.NewIncomingContext(bg, metadata.Pairs("authorization", "Bearer tok-1"))
	if _, err := k.device.PowerStatus(user, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "bmc_not_configured" {
		t.Fatalf("not configured: %v", err)
	}
	if _, err := k.mem.SetDeviceBMCRef(bg, tenant, dev.GetId(), ref, store.AuditRow{}); err != nil {
		t.Fatal(err)
	}

	// No forwarded token: refused before warden and the BMC.
	for _, call := range []func(context.Context) error{
		func(c context.Context) error {
			_, e := k.device.PowerStatus(c, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()})
			return e
		},
		func(c context.Context) error {
			_, e := k.device.Power(c, &ipamv1.PowerRequest{TenantId: tenant, Id: dev.GetId(), Action: ipamv1.PowerAction_POWER_ACTION_ON})
			return e
		},
		func(c context.Context) error {
			_, e := k.device.StartKvmSession(c, &ipamv1.StartKvmSessionRequest{TenantId: tenant, Id: dev.GetId()})
			return e
		},
	} {
		if err := call(bg); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("no token: %v", err)
		}
	}
	if len(k.warden.Calls()) != 0 || k.bmc.Calls != 0 {
		t.Fatalf("refused calls leaked: warden=%v bmc=%d", k.warden.Calls(), k.bmc.Calls)
	}

	// With the token: the user's identity reaches warden.
	if _, err := k.device.PowerStatus(user, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()}); err != nil {
		t.Fatal(err)
	}
	if c := k.warden.Calls(); len(c) != 1 || c[0].Token != "tok-1" {
		t.Fatalf("warden calls: %+v", c)
	}
	if _, err := k.device.Power(user, &ipamv1.PowerRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing action: %v", err)
	}

	// Reasons -> codes.
	k.warden.Deny("tok-1", ref)
	if _, err := k.device.StartKvmSession(user, &ipamv1.StartKvmSessionRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("denied kvm: %v", err)
	}
	other := metadata.NewIncomingContext(bg, metadata.Pairs("authorization", "Bearer tok-2"))
	k.warden.SetUnavailable(true)
	if _, err := k.device.PowerStatus(other, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "warden_unavailable" {
		t.Fatalf("warden down: %v", err)
	}
	k.warden.SetUnavailable(false)
	k.bmc.Err = ipmi.ErrUnreachable
	if _, err := k.device.PowerStatus(other, &ipamv1.PowerStatusRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("unreachable: %v", err)
	}
	k.bmc.Err = ipmi.ErrAuthFailed
	if _, err := k.device.Power(other, &ipamv1.PowerRequest{TenantId: tenant, Id: dev.GetId(), Action: ipamv1.PowerAction_POWER_ACTION_OFF}); status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "bmc_auth_failed" {
		t.Fatalf("auth failed: %v", err)
	}

	// KVM console web logins refused by the BMC.
	for _, tc := range []struct {
		rt     http.RoundTripper
		code   codes.Code
		reason string
	}{
		{bmcWeb(http.StatusCreated, true), codes.FailedPrecondition, "bmc_2fa_required"},
		{bmcWeb(http.StatusBadRequest), codes.ResourceExhausted, "bmc_session_limit"},
		{bmcWeb(http.StatusUnauthorized), codes.Unavailable, "bmc_auth_failed"},
	} {
		k.device.kvm = kvm.NewManager(nil, 0, kvm.WithTransport(tc.rt))
		_, err := k.device.StartKvmSession(other, &ipamv1.StartKvmSessionRequest{TenantId: tenant, Id: dev.GetId()})
		if status.Code(err) != tc.code || status.Convert(err).Message() != tc.reason {
			t.Fatalf("kvm %s: %v", tc.reason, err)
		}
	}
	k.device.kvm = kvm.NewManager(nil, 0, kvm.WithTransport(rtFunc(func(*http.Request) (*http.Response, error) { return nil, io.EOF })))
	if _, err := k.device.StartKvmSession(other, &ipamv1.StartKvmSessionRequest{TenantId: tenant, Id: dev.GetId()}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("kvm unreachable: %v", err)
	}
}
