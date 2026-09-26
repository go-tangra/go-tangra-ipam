package invclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	tA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

func TestErrorCodes(t *testing.T) {
	e := &Error{Code: CodeOutdated, Err: errors.New("unimplemented")}
	if !errors.Is(e, ErrUnavailable) || Code(e) != CodeOutdated || e.Error() == "" || e.Unwrap() == nil {
		t.Fatal("coded error")
	}
	if (&Error{Code: CodePermissionDenied}).Error() != "invclient: permission_denied" {
		t.Fatal("message")
	}
	if Code(nil) != "" || Code(errors.New("x")) != CodeUnavailable {
		t.Fatal("Code")
	}
}

func TestFakeTenantScoping(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	t0 := time.Unix(1000, 0)
	f.Put(Report{TenantID: tA, Host: Host{ID: "h1"}, ChangedAt: t0, Digest: "d1", Interfaces: []Interface{{Name: "eth0"}}})
	f.Put(Report{TenantID: tA, Host: Host{ID: "h2"}, ChangedAt: t0.Add(time.Minute)})
	f.Put(Report{TenantID: tB, Host: Host{ID: "h3"}, ChangedAt: t0})
	ids, maxAt, err := f.ListReportTenants(ctx, time.Time{})
	if err != nil || len(ids) != 2 || !maxAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("tenants %v %v %v", ids, maxAt, err)
	}
	ids, _, _ = f.ListReportTenants(ctx, t0)
	if len(ids) != 1 || ids[0] != tA {
		t.Fatalf("changed since: %v", ids)
	}
	f.PageSize = 0
	var pages, n int
	err = f.ListHostReports(ctx, tA, Filter{}, func(p []Report) error { pages++; n += len(p); return nil })
	if err != nil || n != 2 || pages != 2 {
		t.Fatalf("paging %d/%d %v", pages, n, err)
	}
	_ = f.ListHostReports(ctx, tA, Filter{Digest: true, ChangedSince: t0.Add(-time.Second)}, func(p []Report) error {
		for _, r := range p {
			if len(r.Interfaces) != 0 || r.TenantID != tA {
				t.Fatal("digest view carries no interfaces and never another tenant")
			}
		}
		return nil
	})
	stop := errors.New("stop")
	if err := f.ListHostReports(ctx, tA, Filter{}, func([]Report) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("callback error")
	}
	if _, err := f.GetHostReport(ctx, tB, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant get must miss")
	}
	if r, err := f.GetHostReport(ctx, tA, "h1"); err != nil || r.Digest != "d1" {
		t.Fatal("get")
	}
	f.Delete(tA, "h1")
	if _, err := f.GetHostReport(ctx, tA, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted")
	}
	f.Down = true
	if _, _, err := f.ListReportTenants(ctx, time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("down")
	}
	if err := f.ListHostReports(ctx, tA, Filter{}, nil); Code(err) != CodeUnavailable {
		t.Fatal("down list")
	}
	if _, err := f.GetHostReport(ctx, tA, "h2"); err == nil {
		t.Fatal("down get")
	}
	f.Down, f.Err = false, &Error{Code: CodePermissionDenied}
	if _, _, err := f.ListReportTenants(ctx, time.Time{}); Code(err) != CodePermissionDenied {
		t.Fatal("injected")
	}
	if len(f.Calls) == 0 {
		t.Fatal("calls recorded")
	}
}
