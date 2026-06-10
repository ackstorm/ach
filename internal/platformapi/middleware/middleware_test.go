// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
)

// fakeResolver implements keystore.Resolver as a function adapter so
// tests can inject canned responses inline.
type fakeResolver func(plaintext string) (*keystore.KeyInfo, error)

func (f fakeResolver) Resolve(_ context.Context, p string) (*keystore.KeyInfo, error) {
	return f(p)
}

// newAuditCapture returns an audit logger wired to a bytes.Buffer the
// caller can inspect after a request runs.
func newAuditCapture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return audit.NewLogger(&buf), &buf
}

// newOpCapture returns an operational logger wired to a bytes.Buffer.
func newOpCapture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

// helloHandler is a no-op success handler used by middleware-chain tests
// that don't need to inspect ctx.
func helloHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

// TestRequestIDGeneratesULID — a request with no X-Request-Id gets a
// generated "req_<ulid>" both in the response header and in ctx.
func TestRequestIDGeneratesULID(t *testing.T) {
	var seen string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := RequestID(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	h.ServeHTTP(rec, req)
	got := rec.Header().Get("X-Request-Id")
	if got == "" || !strings.HasPrefix(got, "req_") {
		t.Fatalf("expected X-Request-Id with req_ prefix, got %q", got)
	}
	if seen != got {
		t.Fatalf("ctx request id %q differs from header %q", seen, got)
	}
}

// TestRequestIDOverridesCaller — even when the caller supplies
// X-Request-Id the middleware ALWAYS generates a fresh server-side
// "req_<ulid>" (T-03-05-06).
func TestRequestIDOverridesCaller(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequestID(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "client-spoofed")
	h.ServeHTTP(rec, req)
	got := rec.Header().Get("X-Request-Id")
	if got == "client-spoofed" {
		t.Fatalf("middleware preserved caller-supplied request id (forbidden)")
	}
	if !strings.HasPrefix(got, "req_") {
		t.Fatalf("expected req_ prefix, got %q", got)
	}
}

// TestRequestIDConcurrentUnique — concurrent requests receive different
// IDs (sanity check on ulid.Make monotonicity).
func TestRequestIDConcurrentUnique(t *testing.T) {
	h := RequestID(helloHandler(t))
	const N = 20
	ids := make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil)
			h.ServeHTTP(rec, req)
			ids[idx] = rec.Header().Get("X-Request-Id")
		}(i)
	}
	wg.Wait()
	seen := map[string]struct{}{}
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = struct{}{}
	}
}

// TestRecoverPanicWritesEnvelope — inner panic becomes a 500
// internal_error envelope; subsequent requests still work.
func TestRecoverPanicWritesEnvelope(t *testing.T) {
	opLog, _ := newOpCapture()
	auLog, auBuf := newAuditCapture()

	panicCount := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panicCount++
		if panicCount == 1 {
			panic("boom")
		}
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(RecoverPanic(opLog, auLog)(inner))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), audit.OutcomeInternalError) {
		t.Fatalf("expected internal_error envelope, body=%s", rec.Body.String())
	}
	if !strings.Contains(auBuf.String(), audit.OutcomeInternalError) {
		t.Fatalf("expected audit emission, audit=%s", auBuf.String())
	}

	// Second request still works (panic confined to one goroutine).
	rec2 := httptest.NewRecorder()
	chain.ServeHTTP(rec2, httptest.NewRequest("GET", "/x", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request expected 200, got %d", rec2.Code)
	}
}

// TestAccessLogShapeNoBodyNoHeaders — access log records
// {method, path, status, latency_ms, request_id} only; never request /
// response bodies or headers.
func TestAccessLogShapeNoBodyNoHeaders(t *testing.T) {
	opLog, opBuf := newOpCapture()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"secret":"super-private"}`)
	})
	chain := RequestID(AccessLog(opLog)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/secret", strings.NewReader(`{"password":"p@ss"}`))
	chain.ServeHTTP(rec, req)
	logged := opBuf.String()
	for _, banned := range []string{"super-private", "p@ss", "password"} {
		if strings.Contains(logged, banned) {
			t.Fatalf("access log leaked %q: %s", banned, logged)
		}
	}
	for _, want := range []string{`"method"`, `"path"`, `"status"`, `"latency_ms"`, `"request_id"`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("access log missing expected field %q: %s", want, logged)
		}
	}
}

// TestAccessLogRedactsAchKey — x-ach-key plaintext is NEVER logged
// (FWD-11 / T-03-05-01 invariant). The header value supplied by the
// client must not appear in the captured log buffer.
func TestAccessLogRedactsAchKey(t *testing.T) {
	opLog, opBuf := newOpCapture()
	chain := RequestID(AccessLog(opLog)(helloHandler(t)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_supersecretplaintext1234567")
	chain.ServeHTTP(rec, req)
	if strings.Contains(opBuf.String(), "pk_supersecretplaintext1234567") {
		t.Fatalf("access log leaked x-ach-key plaintext: %s", opBuf.String())
	}
	if strings.Contains(opBuf.String(), "pk_***") {
		t.Fatalf("access log leaked masked form (which would imply the header was processed): %s", opBuf.String())
	}
}

// TestContentTypeJSONSets — handler writes 200 with no Content-Type; the
// middleware sets application/json.
func TestContentTypeJSONSets(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	})
	chain := ContentTypeJSON(inner)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Fatalf("expected application/json content type, got %q", ct)
	}
}

// TestContentTypeJSONIdempotent — handler sets its own Content-Type
// (e.g. text/html for an SSO redirect page); the middleware does NOT
// overwrite it.
func TestContentTypeJSONIdempotent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusFound)
	})
	chain := ContentTypeJSON(inner)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Fatalf("middleware overwrote caller content type: got %q", ct)
	}
}

// TestAuthnHappyPathPk — valid pk_ plaintext resolves; ctx receives
// populated KeyContext with KeyType=PrefixPk.
func TestAuthnHappyPathPk(t *testing.T) {
	material := "sk-test-pk-material" // TESTING-PHASE (reverts FIX01 §A.6)
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com", LiteLLMKeyMaterial: &material}, nil
	})
	var observed KeyContext
	var ok bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed, ok = KeyContextFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, nil, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !ok || observed.KeyID != "pkid_a" || observed.KeyType != keys.PrefixPk {
		t.Fatalf("unexpected KeyContext: ok=%v info=%+v", ok, observed)
	}
	// TESTING-PHASE (reverts FIX01 §A.6): LiteLLMKeyMaterial must propagate
	// from KeyInfo through WithKeyContext into the observed KeyContext.
	if observed.LiteLLMKeyMaterial == nil || *observed.LiteLLMKeyMaterial != material {
		t.Fatalf("LiteLLMKeyMaterial = %v; want %q", observed.LiteLLMKeyMaterial, material)
	}
}

// TestAuthnHappyPathEk — valid ek_ plaintext resolves; KeyType=PrefixEk
// and Environment populated.
func TestAuthnHappyPathEk(t *testing.T) {
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "ekid_b", KeyType: keys.PrefixEk, OwnerEmail: "u@x.com", Environment: "prod"}, nil
	})
	var observed KeyContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed, _ = KeyContextFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, nil, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "ek_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	chain.ServeHTTP(rec, req)
	if observed.KeyType != keys.PrefixEk || observed.Environment != "prod" {
		t.Fatalf("unexpected KeyContext: %+v", observed)
	}
}

// TestAuthnMissingHeader — no x-ach-key → 401 missing_key envelope.
func TestAuthnMissingHeader(t *testing.T) {
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		t.Fatalf("resolver must NOT be called when header is missing")
		return nil, nil
	})
	chain := RequestID(Authn(resolver, nil, nil)(helloHandler(t)))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"missing_key"`) {
		t.Fatalf("expected missing_key envelope, body=%s", rec.Body.String())
	}
}

// TestAuthnInvalidBearer — resolver returns (nil, nil); Authn renders
// 401 expired_or_revoked.
func TestAuthnInvalidBearer(t *testing.T) {
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) { return nil, nil })
	chain := RequestID(Authn(resolver, nil, nil)(helloHandler(t)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_TOO_SHORT")
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), audit.OutcomeExpiredOrRevoked) {
		t.Fatalf("expected expired_or_revoked envelope, body=%s", rec.Body.String())
	}
}

// TestAuthnResolverErr — resolver returns a transient error; Authn
// renders 500 internal_error and emits a single audit line.
func TestAuthnResolverErr(t *testing.T) {
	auLog, auBuf := newAuditCapture()
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return nil, errors.New("db down")
	})
	chain := RequestID(Authn(resolver, nil, auLog)(helloHandler(t)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_zzzzzzzzzzzzzzzzzzzzzzzzzz")
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(auBuf.String(), audit.OutcomeInternalError) {
		t.Fatalf("expected audit internal_error emission, got %s", auBuf.String())
	}
	// The resolver error ('db down') MUST NOT appear in the response body
	// (T-03-02-02 / Hub §9.1).
	if strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("response leaked raw resolver error: %s", rec.Body.String())
	}
}

// TestAuthnDiscardsPlaintext — after a successful Authn pass the inner
// handler sees an empty x-ach-key header (D-19 / T-03-05-02).
func TestAuthnDiscardsPlaintext(t *testing.T) {
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com"}, nil
	})
	var seenHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Get("X-Ach-Key")
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, nil, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	chain.ServeHTTP(rec, req)
	if seenHeader != "" {
		t.Fatalf("inner handler observed x-ach-key=%q (must be empty)", seenHeader)
	}
}

// TestKeyContextAbsentOnRawCtx — KeyContextFromCtx on a context that
// hasn't passed through Authn returns the zero-value + false.
func TestKeyContextAbsentOnRawCtx(t *testing.T) {
	kc, ok := KeyContextFromCtx(context.Background())
	if ok {
		t.Fatalf("expected ok=false on bare context, got kc=%+v", kc)
	}
	if kc.KeyID != "" || kc.OwnerEmail != "" {
		t.Fatalf("expected zero-value KeyContext, got %+v", kc)
	}
}

// TestAuthnAdminAllowlistPositive — pk_ KeyContext gets IsAdmin=true
// when OwnerEmail is in the allowlist (BLK-02).
func TestAuthnAdminAllowlistPositive(t *testing.T) {
	allow := map[string]struct{}{"admin@x.com": {}}
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "admin@x.com"}, nil
	})
	var observed KeyContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed, _ = KeyContextFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, allow, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	chain.ServeHTTP(rec, req)
	if !observed.IsAdmin {
		t.Fatalf("expected IsAdmin=true for allowlisted pk_, got %+v", observed)
	}
}

// TestAuthnAdminAllowlistNegative — pk_ KeyContext gets IsAdmin=false
// when OwnerEmail is NOT in the allowlist.
func TestAuthnAdminAllowlistNegative(t *testing.T) {
	allow := map[string]struct{}{"admin@x.com": {}}
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "user@x.com"}, nil
	})
	var observed KeyContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed, _ = KeyContextFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, allow, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	chain.ServeHTTP(rec, req)
	if observed.IsAdmin {
		t.Fatalf("expected IsAdmin=false for non-allowlisted pk_, got %+v", observed)
	}
}

// TestAuthnEkNeverAdmin — even when the resolved ek_ KeyInfo has an
// allowlisted OwnerEmail, IsAdmin is FORCED to false (BLK-02 / admin
// endpoints reject ek_ upstream).
func TestAuthnEkNeverAdmin(t *testing.T) {
	allow := map[string]struct{}{"admin@x.com": {}}
	resolver := fakeResolver(func(string) (*keystore.KeyInfo, error) {
		return &keystore.KeyInfo{KeyID: "ekid_b", KeyType: keys.PrefixEk, OwnerEmail: "admin@x.com", Environment: "prod"}, nil
	})
	var observed KeyContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed, _ = KeyContextFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := RequestID(Authn(resolver, allow, nil)(inner))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Ach-Key", "ek_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	chain.ServeHTTP(rec, req)
	if observed.IsAdmin {
		t.Fatalf("expected IsAdmin=false for ek_ regardless of allowlist, got %+v", observed)
	}
}

// TestActorFromCtx — composes "<namespace>/<email>" with proper
// fallbacks. POD_NAMESPACE missing → "unknown"; OwnerEmail missing → "-".
func TestActorFromCtx(t *testing.T) {
	// No KeyContext, no POD_NAMESPACE.
	t.Setenv("POD_NAMESPACE", "")
	got := ActorFromCtx(context.Background())
	if got != "unknown/-" {
		t.Fatalf("expected unknown/-, got %q", got)
	}
	t.Setenv("POD_NAMESPACE", "ach-system")
	got = ActorFromCtx(context.Background())
	if got != "ach-system/-" {
		t.Fatalf("expected ach-system/-, got %q", got)
	}
	ctx := WithKeyContext(context.Background(), &keystore.KeyInfo{OwnerEmail: "u@x.com"}, false)
	got = ActorFromCtx(ctx)
	if got != "ach-system/u@x.com" {
		t.Fatalf("expected ach-system/u@x.com, got %q", got)
	}
}
