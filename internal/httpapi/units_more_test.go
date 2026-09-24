package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// TestErrorType exercises the Error value's Error() method.
func TestErrorType(t *testing.T) {
	e := &Error{Status: http.StatusTeapot, Reason: "teapot"}
	if e.Error() != "teapot" {
		t.Fatalf("Error(): got %q", e.Error())
	}
}

// TestWriteDetail confirms the reason+detail envelope and status.
func TestWriteDetail(t *testing.T) {
	w := httptest.NewRecorder()
	WriteDetail(w, ErrValidation, map[string]any{"field": "cidr"})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["reason"] != "validation_failed" {
		t.Fatalf("reason: %s", w.Body)
	}
	det, _ := body["detail"].(map[string]any)
	if det["field"] != "cidr" {
		t.Fatalf("detail: %s", w.Body)
	}
}

// TestStatusMapping covers every branch of Status.
func TestStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"error", ErrForbidden, http.StatusForbidden, "forbidden"},
		{"wrapped-error", errors.New("x: " + "y"), http.StatusServiceUnavailable, "temporarily_unavailable"},
		{"maxbytes", &http.MaxBytesError{Limit: 1}, http.StatusRequestEntityTooLarge, "body_too_large"},
		{"store-notfound", store.ErrNotFound, http.StatusNotFound, "not_found"},
		{"store-conflict", store.ErrConflict, http.StatusConflict, "conflict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotReason := Status(c.err)
			if gotStatus != c.status || gotReason != c.reason {
				t.Fatalf("Status(%v): got %d/%s want %d/%s", c.err, gotStatus, gotReason, c.status, c.reason)
			}
		})
	}
}

// TestFail exercises the Fail path for both a mapped *Error and a 5xx that logs.
func TestFail(t *testing.T) {
	r := httptest.NewRequest("GET", "https://localhost/x", nil)

	w := httptest.NewRecorder()
	Fail(w, r, nil, ErrNotFound)
	if w.Code != http.StatusNotFound {
		t.Fatalf("mapped: got %d", w.Code)
	}

	// A generic error collapses to 503; log is nil so the >=500 branch is safe.
	w = httptest.NewRecorder()
	Fail(w, r, nil, errors.New("boom"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("generic: got %d", w.Code)
	}
}

// TestDecodeJSON covers the valid, unknown-field, trailing-data, oversize and
// default-limit branches.
func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	newReq := func(body string) *http.Request {
		return httptest.NewRequest("POST", "https://localhost/x", strings.NewReader(body))
	}

	// Valid body, explicit limit.
	var p1 payload
	if err := DecodeJSON(newReq(`{"name":"ok"}`), &p1, 1024); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if p1.Name != "ok" {
		t.Fatalf("valid decode: %+v", p1)
	}

	// Unknown field -> malformed.
	var p2 payload
	if err := DecodeJSON(newReq(`{"nope":1}`), &p2, 0); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown field: got %v", err)
	}

	// Trailing data after the first JSON value -> malformed.
	var p3 payload
	if err := DecodeJSON(newReq(`{"name":"a"}{}`), &p3, 0); !errors.Is(err, ErrMalformed) {
		t.Fatalf("trailing: got %v", err)
	}

	// Oversized body -> body_too_large.
	big := `{"name":"` + strings.Repeat("z", 200) + `"}`
	var p4 payload
	if err := DecodeJSON(newReq(big), &p4, 16); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversize: got %v", err)
	}
}

// TestExtensionInt covers each numeric type plus the unsupported fallthrough.
func TestExtensionInt(t *testing.T) {
	cases := []struct {
		v    any
		want int64
		ok   bool
	}{
		{float64(42), 42, true},
		{int(7), 7, true},
		{int64(9), 9, true},
		{json.Number("123"), 123, true},
		{json.Number("bad"), 0, false},
		{"nope", 0, false},
	}
	for _, c := range cases {
		got, ok := extensionInt(c.v)
		if got != c.want || ok != c.ok {
			t.Fatalf("extensionInt(%v): got %d/%v want %d/%v", c.v, got, ok, c.want, c.ok)
		}
	}
}

// TestExtensionBool covers true, false and non-bool.
func TestExtensionBool(t *testing.T) {
	if !extensionBool(true) {
		t.Fatal("true")
	}
	if extensionBool(false) {
		t.Fatal("false")
	}
	if extensionBool("x") {
		t.Fatal("non-bool")
	}
}

// TestOptBool covers present-true, present-false, absent and unparseable.
func TestOptBool(t *testing.T) {
	q := url.Values{}
	q.Set("t", "true")
	q.Set("f", "false")
	q.Set("bad", "notabool")
	if b := optBool(q, "t"); b == nil || *b != true {
		t.Fatalf("true: %v", b)
	}
	if b := optBool(q, "f"); b == nil || *b != false {
		t.Fatalf("false: %v", b)
	}
	if b := optBool(q, "absent"); b != nil {
		t.Fatalf("absent: %v", b)
	}
	if b := optBool(q, "bad"); b != nil {
		t.Fatalf("unparseable: %v", b)
	}
}

// TestBoolParam covers present-truthy, present-false, absent and unparseable.
func TestBoolParam(t *testing.T) {
	req := func(qs string) *http.Request {
		return httptest.NewRequest("GET", "https://localhost/x?"+qs, nil)
	}
	if !boolParam(req("force=true"), "force") {
		t.Fatal("truthy")
	}
	if boolParam(req("force=false"), "force") {
		t.Fatal("false")
	}
	if boolParam(req(""), "force") {
		t.Fatal("absent")
	}
	if boolParam(req("force=maybe"), "force") {
		t.Fatal("unparseable")
	}
}

// TestAtoiDefault covers empty, valid and invalid.
func TestAtoiDefault(t *testing.T) {
	if atoiDefault("", 5) != 5 {
		t.Fatal("empty")
	}
	if atoiDefault("12", 5) != 12 {
		t.Fatal("valid")
	}
	if atoiDefault("xx", 5) != 5 {
		t.Fatal("invalid")
	}
}

// TestDecodeScanOptions covers the no-body default and the parsed-body path.
func TestDecodeScanOptions(t *testing.T) {
	// No body: ContentLength 0 -> defaults.
	r := httptest.NewRequest("POST", "https://localhost/x", nil)
	opts, err := decodeScanOptions(r)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if opts.EnableSNMP || opts.EnableDNSUpdate || opts.SkipReverseDNS {
		t.Fatalf("empty opts: %+v", opts)
	}

	// Populated body.
	r = httptest.NewRequest("POST", "https://localhost/x",
		strings.NewReader(`{"enable_snmp":true,"enable_dns_update":true,"skip_reverse_dns":true}`))
	opts, err = decodeScanOptions(r)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !opts.EnableSNMP || !opts.EnableDNSUpdate || !opts.SkipReverseDNS {
		t.Fatalf("body opts: %+v", opts)
	}

	// Malformed body -> error.
	r = httptest.NewRequest("POST", "https://localhost/x", strings.NewReader(`{`))
	if _, err := decodeScanOptions(r); err == nil {
		t.Fatal("malformed: want error")
	}
}

// TestJSONRaw covers the valid-JSON and string-fallback branches.
func TestJSONRaw(t *testing.T) {
	v := jsonRaw([]byte(`{"a":1}`))
	if _, ok := v.(json.RawMessage); !ok {
		t.Fatalf("valid json: got %T", v)
	}
	v = jsonRaw([]byte(`not json`))
	if s, ok := v.(string); !ok || s != "not json" {
		t.Fatalf("fallback: got %T %v", v, v)
	}
}
