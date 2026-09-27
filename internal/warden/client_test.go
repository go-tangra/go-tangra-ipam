package warden

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	wardenv1 "github.com/go-tangra/go-tangra-warden/sdk/v4/api/proto/warden/v1"
)

const (
	testRef   = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"
	testToken = "user-platform-token"
	testPass  = "bmc-P4ss-LEAK"
)

// secretsServer is an in-process warden.v1.Secrets that records the
// authorization metadata of every call and answers with a configured error.
type secretsServer struct {
	wardenv1.UnimplementedSecretsServer
	mu      sync.Mutex
	auth    []string
	methods []string
	getErr  error
	pwErr   error
	delay   time.Duration
}

func (s *secretsServer) record(ctx context.Context, m string) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth = append(s.auth, strings.Join(md.Get("authorization"), ","))
	s.methods = append(s.methods, m)
}

func (s *secretsServer) Get(ctx context.Context, req *wardenv1.GetRequest) (*wardenv1.GetResponse, error) {
	s.record(ctx, "Get")
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &wardenv1.GetResponse{Secret: &wardenv1.Secret{Id: req.GetId(), Name: "zax-5 IPMI", Username: "ADMIN",
		FolderPath: "/infra/bmc", HostUrl: "lanplus://10.1.112.14:6230", Description: "rack 5"}}, nil
}

func (s *secretsServer) GetPassword(ctx context.Context, req *wardenv1.GetPasswordRequest) (*wardenv1.GetPasswordResponse, error) {
	s.record(ctx, "GetPassword")
	if s.pwErr != nil {
		return nil, s.pwErr
	}
	return &wardenv1.GetPasswordResponse{Password: testPass, Version: 3}, nil
}

func (s *secretsServer) calls() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...), append([]string(nil), s.auth...)
}

func newTestClient(t *testing.T, srv *secretsServer) Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	wardenv1.RegisterSecretsServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cc, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return New(cc)
}

func userCtx() context.Context { return WithUserToken(context.Background(), testToken) }

func TestClientMetaForwardsUserToken(t *testing.T) {
	srv := &secretsServer{}
	c := newTestClient(t, srv)
	m, err := c.Meta(userCtx(), testRef)
	if err != nil {
		t.Fatal(err)
	}
	want := SecretMeta{ID: testRef, Name: "zax-5 IPMI", Username: "ADMIN", FolderPath: "/infra/bmc", HostURL: "lanplus://10.1.112.14:6230"}
	if m != want {
		t.Fatalf("meta = %+v, want %+v", m, want)
	}
	methods, auth := srv.calls()
	if len(methods) != 1 || methods[0] != "Get" || auth[0] != "Bearer "+testToken {
		t.Fatalf("calls = %v auth = %v", methods, auth)
	}
}

func TestClientCredentialsCombinesGetAndPassword(t *testing.T) {
	srv := &secretsServer{}
	c := newTestClient(t, srv)
	cr, err := c.Credentials(userCtx(), testRef)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Username != "ADMIN" || cr.Password != testPass || cr.HostURL != "lanplus://10.1.112.14:6230" {
		t.Fatalf("credentials fields wrong (username=%q host=%q)", cr.Username, cr.HostURL)
	}
	methods, auth := srv.calls()
	if strings.Join(methods, ",") != "Get,GetPassword" {
		t.Fatalf("methods = %v", methods)
	}
	for _, a := range auth {
		if a != "Bearer "+testToken {
			t.Fatalf("authorization = %q", a)
		}
	}
}

func TestClientRefusesWithoutTokenOrValidRef(t *testing.T) {
	srv := &secretsServer{}
	c := newTestClient(t, srv)
	if _, err := c.Meta(context.Background(), testRef); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("no token meta: %v", err)
	}
	if _, err := c.Credentials(WithUserToken(context.Background(), "  "), testRef); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("blank token credentials: %v", err)
	}
	if _, err := c.Meta(userCtx(), ""); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("empty ref: %v", err)
	}
	if _, err := c.Credentials(userCtx(), "../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad ref: %v", err)
	}
	if methods, _ := srv.calls(); len(methods) != 0 {
		t.Fatalf("refused calls reached warden: %v", methods)
	}
}

func TestClientErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
		not  error
	}{
		{"not found", status.Error(codes.NotFound, "not_found"), ErrNotFound, nil},
		{"invalid", status.Error(codes.InvalidArgument, "id required"), ErrNotFound, nil},
		{"per-secret refusal", status.Error(codes.PermissionDenied, "forbidden"), ErrForbidden, ErrUnavailable},
		{"unauthenticated", status.Error(codes.Unauthenticated, "unauthenticated"), ErrForbidden, nil},
		{"mesh policy refusal", status.Error(codes.PermissionDenied, ""), ErrPolicyDenied, ErrForbidden},
		{"unavailable", status.Error(codes.Unavailable, "vault_unavailable "+testPass), ErrUnavailable, nil},
		{"internal", status.Error(codes.Internal, "boom"), ErrUnavailable, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, &secretsServer{getErr: tc.err})
			_, err := c.Meta(userCtx(), testRef)
			if !errors.Is(err, tc.want) {
				t.Fatalf("meta err = %v, want %v", err, tc.want)
			}
			if tc.not != nil && errors.Is(err, tc.not) {
				t.Fatalf("meta err %v must not be %v", err, tc.not)
			}
			if strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), "boom") {
				t.Fatalf("warden message leaked into %q", err)
			}
			if errors.Is(tc.want, ErrPolicyDenied) && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("policy denial must read as unavailable: %v", err)
			}
		})
	}
	// Password RPC failure after a good Get.
	c := newTestClient(t, &secretsServer{pwErr: status.Error(codes.PermissionDenied, "forbidden")})
	if _, err := c.Credentials(userCtx(), testRef); !errors.Is(err, ErrForbidden) {
		t.Fatalf("password refusal: %v", err)
	}
	c = newTestClient(t, &secretsServer{getErr: status.Error(codes.NotFound, "not_found")})
	if _, err := c.Credentials(userCtx(), testRef); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credentials get not found: %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	srv := &secretsServer{delay: 200 * time.Millisecond}
	c := newTestClient(t, srv)
	c.(*grpcClient).timeout = 20 * time.Millisecond
	if _, err := c.Meta(userCtx(), testRef); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout: %v", err)
	}
}

func TestClientNonStatusError(t *testing.T) {
	if err := mapErr(errors.New("dial tcp: refused")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("plain error: %v", err)
	}
	if err := mapErr(context.DeadlineExceeded); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deadline: %v", err)
	}
}

func TestCredentialsRedacted(t *testing.T) {
	cr := Credentials{Username: "ADMIN", Password: testPass, HostURL: "lan://h"}
	for _, s := range []string{fmt.Sprint(cr), fmt.Sprintf("%v %+v %#v %s", cr, cr, cr, cr), fmt.Sprint(&cr)} {
		if strings.Contains(s, testPass) || strings.Contains(s, "ADMIN") {
			t.Fatalf("credentials printed: %s", s)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "creds", cr)
	if strings.Contains(buf.String(), testPass) || !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("slog: %s", buf.String())
	}
}

func TestValidRef(t *testing.T) {
	for ref, want := range map[string]bool{
		testRef: true, strings.ToUpper(testRef): true, "": false, "abc": false,
		testRef + "x": false, "01928f7e3c1a7b449d2e5a6b7c8d9e0f": false,
	} {
		if got := ValidRef(ref); got != want {
			t.Errorf("ValidRef(%q) = %v, want %v", ref, got, want)
		}
	}
}
