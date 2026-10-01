package httpapi

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// 032 security review F-1: a legacy cursor/limit request is always bounded
// to 1..listquery.MaxPageSize (default legacyDefaultLimit), and walking the
// legacy cursor still reaches every row.

func TestLegacyLimitClamp(t *testing.T) {
	for v, want := range map[string]int{
		"": legacyDefaultLimit, "x": legacyDefaultLimit, "0": legacyDefaultLimit, "-5": legacyDefaultLimit,
		"7": 7, "200": listquery.MaxPageSize, "500": listquery.MaxPageSize, "100000": listquery.MaxPageSize,
	} {
		q := url.Values{"cursor": {"c"}}
		if v != "" {
			q.Set("limit", v)
		}
		if got := legacyLimit(q); got != want {
			t.Errorf("legacyLimit(%q) = %d, want %d", v, got, want)
		}
		if got := unpagedLimit(q); got != want {
			t.Errorf("unpagedLimit(%q) = %d, want %d", v, got, want)
		}
	}
	if got := unpagedLimit(url.Values{"status": {"active"}}); got != 0 {
		t.Errorf("unpagedLimit without cursor/limit = %d, want 0 (whole list)", got)
	}
}

// walkLegacy follows the legacy id cursor (the last item's id) with the base
// query (may be empty: cursor only), asserting every page holds at most max
// items, and returns the ids seen.
func walkLegacy(t *testing.T, get func(string) map[string]any, base string, max int) []string {
	t.Helper()
	with := func(cursor string) string {
		c := "cursor=" + url.QueryEscape(cursor)
		if base == "" {
			return c
		}
		return base + "&" + c
	}
	var ids []string
	seen := map[string]bool{}
	path := with("")
	for range 100 {
		raw, _ := get(path)["items"].([]any)
		if len(raw) > max {
			t.Fatalf("%s: %d items > %d", path, len(raw), max)
		}
		if len(raw) == 0 {
			return ids
		}
		var last string
		for _, x := range raw {
			last = x.(map[string]any)["id"].(string)
			if seen[last] {
				t.Fatalf("%s: %s twice", path, last)
			}
			seen[last] = true
			ids = append(ids, last)
		}
		path = with(last)
	}
	t.Fatal("legacy walk did not end")
	return nil
}

func TestLegacyListsBounded(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	const n = 230
	for i := range n {
		if err := f.mem.CreateDevice(ctx, store.Device{ID: store.NewID(), TenantID: apiTenant, Name: fmt.Sprintf("dev-%03d", i)}); err != nil {
			t.Fatal(err)
		}
		if err := f.mem.CreateLocation(ctx, store.Location{ID: store.NewID(), TenantID: apiTenant, Name: fmt.Sprintf("loc-%03d", i)}); err != nil {
			t.Fatal(err)
		}
		if err := f.mem.CreateIPGroup(ctx, store.IPGroup{ID: store.NewID(), TenantID: apiTenant, Name: fmt.Sprintf("ipg-%03d", i)}); err != nil {
			t.Fatal(err)
		}
		if err := f.mem.CreateHostGroup(ctx, store.HostGroup{ID: store.NewID(), TenantID: apiTenant, Name: fmt.Sprintf("hg-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, list := range []string{"/devices", "/locations", "/ip-groups", "/host-groups"} {
		get := func(q string) map[string]any {
			w := f.req(t, "GET", p+list+"?"+q, "admin", "")
			if w.Code != 200 {
				t.Fatalf("%s?%s = %d %s", list, q, w.Code, w.Body)
			}
			return decodeBody(t, w)
		}
		// Out-of-range limits are rejected at the edge; the handler clamps
		// whatever it is handed.
		for _, l := range []string{"100000", "0", "-5"} {
			w := f.req(t, "GET", p+list+"?limit="+l, "admin", "")
			if raw, _ := decodeBody(t, w)["items"].([]any); w.Code != 422 && len(raw) > listquery.MaxPageSize {
				t.Fatalf("%s?limit=%s = %d, %d items", list, l, w.Code, len(raw))
			}
		}
		if raw, _ := get("limit=500")["items"].([]any); len(raw) != listquery.MaxPageSize {
			t.Fatalf("%s?limit=500: %d items", list, len(raw))
		}
		if raw, _ := get("cursor=")["items"].([]any); len(raw) != legacyDefaultLimit {
			t.Fatalf("%s?cursor=: %d items", list, len(raw))
		}
		if ids := walkLegacy(t, get, "limit=500", listquery.MaxPageSize); len(ids) != n {
			t.Fatalf("%s limit=500 walk reached %d", list, len(ids))
		}
		if ids := walkLegacy(t, get, "", legacyDefaultLimit); len(ids) != n {
			t.Fatalf("%s cursor walk reached %d", list, len(ids))
		}
	}
	// The unpaged pickers still read the whole list without cursor/limit.
	for _, list := range []string{"/locations", "/ip-groups", "/host-groups"} {
		if raw, _ := decodeBody(t, f.req(t, "GET", p+list, "admin", ""))["items"].([]any); len(raw) != n {
			t.Fatalf("%s whole list: %d items", list, len(raw))
		}
	}
}
