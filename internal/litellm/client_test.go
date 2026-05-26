// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

const testMasterKey = "sk-test-abc-DEF-123"

// 401 body literal lifted verbatim from 01-01-SUMMARY.md (spike Probe 8).
const litellmAuth401Body = `{"error":{"message":"Authentication Error, Invalid proxy server token passed. Received API Key = sk-test-abc-DEF-123, Key Hash (Token) =61def7928d739903cc1d300521e6ac878bf50e70720607e03ff077cd6c5cb57d. Unable to find token in cache or ` + "`LiteLLM_VerificationTokenTable`" + `","type":"token_not_found_in_db","param":"key","code":"401"}}`

func newTestClient(t *testing.T, url string) *RESTClient {
	t.Helper()
	return NewRESTClient(url, testMasterKey, logr.Discard())
}

// Test401IsTypedError — REL-06. Mock returns 401 with the literal
// LiteLLM 1.83.10 body shape recorded in 01-01-SUMMARY.md; assert the
// returned error satisfies errors.As(*Auth401Error).
func Test401IsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(litellmAuth401Body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	err := c.ProbeConnection(context.Background())
	if err == nil {
		t.Fatalf("expected error on 401, got nil")
	}

	var auth401 *Auth401Error
	if !errors.As(err, &auth401) {
		t.Fatalf("expected errors.As to resolve *Auth401Error; got %T: %v", err, err)
	}
	if auth401.Path != "/models" {
		t.Errorf("Auth401Error.Path: want /models, got %q", auth401.Path)
	}
	// IsAuth401 helper should agree.
	a, ok := IsAuth401(err)
	if !ok || a == nil {
		t.Errorf("IsAuth401 should detect the typed error")
	}
}

// TestProbeConnectionPathIsModels — the probe path is /models (NOT the
// legacy spec-§6.1 key-info path), per the spike pivot recorded in
// 01-01-SUMMARY.md. Mock captures r.URL.Path AND the auth header.
func TestProbeConnectionPathIsModels(t *testing.T) {
	var (
		mu        sync.Mutex
		gotPath   string
		gotAuth   string
		gotMethod string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.ProbeConnection(context.Background()); err != nil {
		t.Fatalf("ProbeConnection: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/models" {
		t.Fatalf("path: want /models (pivot from legacy key-info path), got %q", gotPath)
	}
	if gotMethod != "GET" {
		t.Errorf("method: want GET, got %q", gotMethod)
	}
	if gotAuth != "Bearer "+testMasterKey {
		t.Errorf("auth header: want %q, got %q", "Bearer "+testMasterKey, gotAuth)
	}
}

// TestAuthHeaderOverrideViaEnv — the ACH_LITELLM_AUTH_HEADER env
// var switches the auth header from Authorization: Bearer to
// x-litellm-api-key. Documents the escape hatch.
func TestAuthHeaderOverrideViaEnv(t *testing.T) {
	t.Setenv(EnvAuthHeader, "x-litellm-api-key")

	var (
		mu             sync.Mutex
		gotAuth        string
		gotXLitellmKey string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotXLitellmKey = r.Header.Get("x-litellm-api-key")
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.ProbeConnection(context.Background()); err != nil {
		t.Fatalf("ProbeConnection: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "" {
		t.Errorf("Authorization header should be empty when override set, got %q", gotAuth)
	}
	if gotXLitellmKey != testMasterKey {
		t.Errorf("x-litellm-api-key: want %q, got %q", testMasterKey, gotXLitellmKey)
	}
}

// TestMakeRequestDefersDrainAndClose — REL-04 reinforcement at the
// Client.makeRequest layer. The proper proxy for "drain+close success
// on every code path" is HTTP keepalive reuse: if the response body is
// drained+closed, net/http parks the underlying TCP connection in the
// idle pool and reuses it for the next request. If the body is NOT
// drained, the connection is abandoned and a fresh TCP handshake is
// required next time.
//
// We count UNIQUE connections opened by the server (StateNew). With
// drain+close working correctly, 1000 sequential requests should reuse
// a tiny pool (typically 1–2 connections). Without drain+close, the
// count grows ~linearly with the request count.
func TestMakeRequestDefersDrainAndClose(t *testing.T) {
	var newConns int64
	var activeNow int64
	var maxConcurrent int64

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Non-trivial body (1 KB) — exercises the read+close path.
		_, _ = w.Write([]byte(`{"ok":true,"size":1024,"data":"` + string(make([]byte, 1024)) + `"}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			atomic.AddInt64(&newConns, 1)
			cur := atomic.AddInt64(&activeNow, 1)
			for {
				maxC := atomic.LoadInt64(&maxConcurrent)
				if cur <= maxC || atomic.CompareAndSwapInt64(&maxConcurrent, maxC, cur) {
					break
				}
			}
		case http.StateClosed, http.StateHijacked:
			atomic.AddInt64(&activeNow, -1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	for i := 0; i < 1000; i++ {
		if err := c.ProbeConnection(context.Background()); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}

	// Keepalive reuse: a healthy drain+close path should open vastly
	// fewer than 1000 unique TCP connections — typically 1, sometimes
	// a handful due to idle timeouts. We assert <50 as a generous
	// upper bound that still cleanly distinguishes "drained" from
	// "leaked" (without drain, every request opens a fresh connection).
	got := atomic.LoadInt64(&newConns)
	if got > 50 {
		t.Fatalf("REL-04 leak suspected: 1000 sequential probes opened %d unique TCP connections (expected <50 due to keepalive reuse)", got)
	}

	// Sanity: at the end of the test there should be no more than a
	// few connections still active (keepalive idle pool); give the
	// runtime a brief moment to settle and then check max-concurrent
	// stayed bounded.
	time.Sleep(50 * time.Millisecond)
	if maxC := atomic.LoadInt64(&maxConcurrent); maxC > 20 {
		t.Errorf("max concurrent connections grew unbounded: %d (REL-04 keepalive starvation)", maxC)
	}
}

// TestNon2xxNon401IsGenericError — anything that is not 2xx and not 401
// is mapped to a generic error (NOT *Auth401Error). The error message
// includes the status code AND the parsed LiteLLM error code, but
// NEVER the raw body (§9.1).
func TestNon2xxNon401IsGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":{"message":"validation failed: model_name required","type":"validation_error","param":"model_name","code":"422"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.makeRequest(context.Background(), "POST", "/model/new", map[string]any{})
	if err == nil {
		t.Fatalf("expected error on 422")
	}
	var auth401 *Auth401Error
	if errors.As(err, &auth401) {
		t.Fatalf("422 must NOT be classified as *Auth401Error")
	}
	// Body content "validation failed: model_name required" must NOT
	// appear in the error string (§9.1 — bodies never in error
	// surfacing because they bubble into Events / Status conditions).
	if got := err.Error(); contains(got, "model_name required") {
		t.Errorf("error string leaked body content: %q", got)
	}
}

// TestDelete404IsSuccess — §7.7 idempotent delete: DELETE returning
// 404 is treated as success (the resource was already gone).
func TestDelete404IsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"message":"not found","type":"x","param":null,"code":"404"}}`))
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.makeRequest(context.Background(), "DELETE", "/v1/agents/zzz", nil)
	if err != nil {
		t.Errorf("DELETE 404 should be success, got: %v", err)
	}
}

// contains is a small substring helper. Avoids pulling strings into the
// test package import list when not otherwise needed.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// float64Ptr is a small constructor helper used by the Phase 3 type tests
// to populate *float64 fields without inline addressing. The
// KeyGenerateRequest.MaxBudget field uses *float64 (KEY-10 invariant —
// see Phase 3 D-25) so the "JSON null vs not present" distinction is
// expressible from caller code: nil pointer with omitempty drops the
// field entirely; a pointer-to-zero is intentionally serialized.
func float64Ptr(v float64) *float64 { return &v }

// TestPhase3TypesJSON covers the JSON marshal / unmarshal contract of the
// four Phase 3 types introduced by Plan 03-01 Task 1.
//
// The tests are intentionally narrow — they exercise the JSON tag shapes
// only, not the LiteLLM REST behavior (that lives in the per-method
// tests in users_test.go / keygen_test.go). The KEY-10 invariant
// (max_budget always nil for ACH) is enforced at the type level here:
// MaxBudget is *float64 with omitempty, so a nil pointer round-trips to
// "key absent in JSON" — never "max_budget: 0" or "max_budget: null".
func TestPhase3TypesJSON(t *testing.T) {
	t.Run("KeyGenerateRequest with nil MaxBudget omits max_budget", func(t *testing.T) {
		req := KeyGenerateRequest{Key: "pk_xyz", MaxBudget: nil}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(raw)
		if !contains(got, `"key":"pk_xyz"`) {
			t.Errorf("want key field present, got: %s", got)
		}
		if contains(got, `"max_budget"`) {
			t.Errorf("KEY-10 violation: max_budget should be absent when nil, got: %s", got)
		}
	})

	t.Run("KeyGenerateRequest with MaxBudget=0 serializes max_budget:0", func(t *testing.T) {
		req := KeyGenerateRequest{Key: "pk_xyz", MaxBudget: float64Ptr(0)}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(raw)
		if !contains(got, `"max_budget":0`) {
			t.Errorf("pointer-to-zero must serialize max_budget:0, got: %s", got)
		}
	})

	t.Run("TeamMemberAddRequest marshals nested member object", func(t *testing.T) {
		req := TeamMemberAddRequest{
			TeamID: "default",
			Member: TeamMember{UserID: "u-1", Role: "user"},
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(raw)
		want := `{"team_id":"default","member":{"user_id":"u-1","role":"user"}}`
		if got != want {
			t.Errorf("TeamMemberAddRequest JSON mismatch:\nwant: %s\ngot:  %s", want, got)
		}
	})

	t.Run("UserInfo round-trips Teams field", func(t *testing.T) {
		src := `{"user_id":"u-1","user_email":"a@b.c","teams":["default","ops"]}`
		var got UserInfo
		if err := json.Unmarshal([]byte(src), &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got.UserID != "u-1" {
			t.Errorf("UserID: want u-1, got %q", got.UserID)
		}
		if got.UserEmail != "a@b.c" {
			t.Errorf("UserEmail: want a@b.c, got %q", got.UserEmail)
		}
		if len(got.Teams) != 2 || got.Teams[0] != "default" || got.Teams[1] != "ops" {
			t.Errorf("Teams: want [default, ops], got %+v", got.Teams)
		}

		// Marshal back: nil Teams must drop the key (omitempty); populated
		// Teams must round-trip the array.
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("Re-marshal: %v", err)
		}
		if !contains(string(raw), `"teams":["default","ops"]`) {
			t.Errorf("teams round-trip failed: %s", raw)
		}

		// Empty Teams must omit the key (BLK-01: nil/empty slice is the
		// "no team info" signal Plan 03-08 will consume).
		empty := UserInfo{UserID: "u-1", UserEmail: "a@b.c"}
		raw2, err := json.Marshal(empty)
		if err != nil {
			t.Fatalf("Marshal empty: %v", err)
		}
		if contains(string(raw2), `"teams"`) {
			t.Errorf("empty Teams should be omitted, got: %s", raw2)
		}
	})
}

// TestNoopClient_Phase3 asserts the canned-value contract of NoopClient's
// Phase 3 stub methods (D-25). The SSO handler's unit tests (Plan 03-07)
// drive both the new-user branch (UserInfoByEmail → ErrNotFound, then
// UserNew + TeamMemberAdd + KeyGenerate) and the existing-user branch
// (UserInfoByEmail returns a constructed UserInfo) against this stub —
// the canned values below are what they observe deterministically.
func TestNoopClient_Phase3(t *testing.T) {
	noop := &NoopClient{}

	t.Run("UserNew echoes email + synthesizes user_id", func(t *testing.T) {
		got, err := noop.UserNew(context.Background(), &UserNewRequest{UserEmail: "a@b.c"})
		if err != nil {
			t.Fatalf("UserNew: %v", err)
		}
		if got == nil || got.UserEmail != "a@b.c" {
			t.Errorf("want UserEmail=a@b.c, got %+v", got)
		}
		// Synthesized user_id contract: prefixed with "noop-" so callers
		// can distinguish a stubbed value from a real LiteLLM-assigned id.
		if !strings.HasPrefix(got.UserID, "noop-") {
			t.Errorf("UserID: want 'noop-' prefix, got %q", got.UserID)
		}
	})

	t.Run("UserInfoByEmail returns ErrNotFound by default", func(t *testing.T) {
		got, err := noop.UserInfoByEmail(context.Background(), "any@x")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
		if got != nil {
			t.Errorf("want nil UserInfo, got %+v", got)
		}
	})

	t.Run("TeamMemberAdd returns nil unconditionally", func(t *testing.T) {
		if err := noop.TeamMemberAdd(context.Background(), "default", "u-1", "user"); err != nil {
			t.Errorf("want nil, got %v", err)
		}
	})

	t.Run("KeyGenerate echoes Key and synthesizes Token", func(t *testing.T) {
		got, err := noop.KeyGenerate(context.Background(), &KeyGenerateRequest{
			Key:    "pk_xxx",
			UserID: "u-1",
		})
		if err != nil {
			t.Fatalf("KeyGenerate: %v", err)
		}
		if got == nil {
			t.Fatal("nil response")
		}
		if got.Key != "pk_xxx" {
			t.Errorf("Key: want pk_xxx (echoed), got %q", got.Key)
		}
		if got.Token != "noop-token-u-1" {
			t.Errorf("Token: want noop-token-u-1 (synthesized), got %q", got.Token)
		}
		if got.UserID != "u-1" {
			t.Errorf("UserID: want u-1, got %q", got.UserID)
		}
	})
}
