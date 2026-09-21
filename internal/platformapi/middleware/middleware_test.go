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

// TestRecoverPanicRepropagatesAbortHandler — http.ErrAbortHandler is the
// stdlib sentinel httputil.ReverseProxy panics with when a streamed (SSE/MCP)
// response is aborted (client disconnect / upstream close). It MUST be
// re-panicked so net/http closes the conn silently — not logged as an ERROR
// nor turned into a (superfluous) 500 over an already-started stream.
func TestRecoverPanicRepropagatesAbortHandler(t *testing.T) {
	opLog, opBuf := newOpCapture()
	auLog, auBuf := newAuditCapture()

	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	})
	chain := RequestID(RecoverPanic(opLog, auLog)(inner))

	rec := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		chain.ServeHTTP(rec, httptest.NewRequest("GET", "/mcp/x", nil))
	}()

	if recovered != http.ErrAbortHandler {
		t.Fatalf("expected ErrAbortHandler to propagate, got %v", recovered)
	}
	if strings.Contains(opBuf.String(), "recovered from panic") {
		t.Fatalf("ErrAbortHandler must NOT be logged as a recovered panic: %s", opBuf.String())
	}
	if auBuf.Len() != 0 {
		t.Fatalf("ErrAbortHandler must NOT emit an audit event: %s", auBuf.String())
	}
	if rec.Code != http.StatusOK { // httptest default; RecoverPanic wrote no 500
		t.Fatalf("RecoverPanic must not write a 500 envelope over the stream, got %d", rec.Code)
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
	chain := RequestID(Authn(resolver, nil, nil, achOnly())(inner))
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
	chain := RequestID(Authn(resolver, nil, nil, achOnly())(inner))
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
	chain := RequestID(Authn(resolver, nil, nil, achOnly())(helloHandler(t)))
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
	chain := RequestID(Authn(resolver, nil, nil, achOnly())(helloHandler(t)))
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
	chain := RequestID(Authn(resolver, nil, auLog, achOnly())(helloHandler(t)))
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
	chain := RequestID(Authn(resolver, nil, nil, achOnly())(inner))
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
	chain := RequestID(Authn(resolver, allow, nil, achOnly())(inner))
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
	chain := RequestID(Authn(resolver, allow, nil, achOnly())(inner))
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
	chain := RequestID(Authn(resolver, allow, nil, achOnly())(inner))
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

// --- OAuth front door: credential slots, raw sk-, the challenge ------------

type slotResolver struct {
	info *keystore.KeyInfo
	last string
}

func (s *slotResolver) Resolve(_ context.Context, p string) (*keystore.KeyInfo, error) {
	s.last = p
	return s.info, nil
}

// achOnly is the classic platform-api slot: x-ach-key, resolved.
func achOnly() AuthnOptions { return AuthnOptions{Headers: []CredentialHeader{achKey}} }

func opts(hdrs ...CredentialHeader) AuthnOptions {
	return AuthnOptions{Headers: hdrs, Challenge: func(*http.Request) string {
		return `Bearer resource_metadata="https://ach.test/.well-known/oauth-protected-resource"`
	}}
}

var (
	achKey   = CredentialHeader{Name: "x-ach-key", Mode: ModeResolve}
	apiKey   = CredentialHeader{Name: "x-api-key", Mode: ModeResolve}
	genaiKey = CredentialHeader{Name: "x-genai-api-key", Mode: ModePassthrough}
)

func pkInfo() *keystore.KeyInfo {
	return &keystore.KeyInfo{KeyID: "pkid_1", KeyType: keys.PrefixPk, OwnerEmail: "u@x.com"}
}

func serveAuthn(res keystore.Resolver, opts AuthnOptions, req *http.Request, inner http.HandlerFunc) *httptest.ResponseRecorder {
	if inner == nil {
		inner = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	}
	rec := httptest.NewRecorder()
	RequestID(Authn(res, nil, nil, opts)(inner)).ServeHTTP(rec, req)
	return rec
}

func TestAuthn_MissingCredentialCarriesTheChallenge(t *testing.T) {
	rec := serveAuthn(&slotResolver{}, opts(achKey), httptest.NewRequest("GET", "/v1/models", nil), nil)
	if rec.Code != 401 || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="https://ach.test/.well-known/oauth-protected-resource"`) {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestAuthn_DeclaredResolveHeaders(t *testing.T) {
	res := &slotResolver{info: pkInfo()}
	for _, h := range []string{"x-ach-key", "x-api-key"} {
		var seen http.Header
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set(h, "pk-x")
		req.Header.Set("Authorization", "Bearer sk-ant-oat01-xyz") // the provider's own credential
		rec := serveAuthn(res, opts(achKey, apiKey), req, func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
		if rec.Code != 200 || res.last != "pk-x" || seen.Get(h) != "" || seen.Get("Authorization") != "Bearer sk-ant-oat01-xyz" {
			t.Fatalf("%s: %d resolved=%q hdr-after=%q auth=%q", h, rec.Code, res.last, seen.Get(h), seen.Get("Authorization"))
		}
	}
	// A header not in the list is not a slot.
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("x-api-key", "pk-x")
	if rec := serveAuthn(res, opts(achKey), req, nil); rec.Code != 401 {
		t.Fatalf("undeclared header: %d", rec.Code)
	}
}

func TestAuthn_PassthroughHeaderKeptNoIdentity(t *testing.T) {
	res := &slotResolver{info: pkInfo()}
	var raw string
	var hasKC bool
	var seen http.Header
	capture := func(_ http.ResponseWriter, r *http.Request) {
		raw, _ = RawLiteLLMKeyFromCtx(r.Context())
		_, hasKC = KeyContextFromCtx(r.Context())
		seen = r.Header.Clone()
	}
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("x-genai-api-key", "anything-goes")
	rec := serveAuthn(res, opts(achKey, genaiKey), req, capture)
	if rec.Code != 200 || raw != "anything-goes" || hasKC || res.last != "" || seen.Get("x-genai-api-key") != "anything-goes" {
		t.Fatalf("%d raw=%q kc=%v resolved=%q hdr=%q", rec.Code, raw, hasKC, res.last, seen.Get("x-genai-api-key"))
	}
	// List order decides: x-ach-key first wins over the passthrough header, which is left alone.
	req = httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("x-ach-key", "pk-x")
	req.Header.Set("x-genai-api-key", "anything-goes")
	raw, hasKC = "", false
	rec = serveAuthn(res, opts(achKey, genaiKey), req, capture)
	if rec.Code != 200 || raw != "" || !hasKC || res.last != "pk-x" || seen.Get("x-genai-api-key") != "anything-goes" {
		t.Fatalf("precedence: %d raw=%q kc=%v resolved=%q", rec.Code, raw, hasKC, res.last)
	}
}

// Authorization: our JWS is resolved (and removed); anything else is not
// ours and passes untouched; a JWS that does not verify is 401.
func TestAuthn_AuthorizationResolvesOnlyOurOAuthToken(t *testing.T) {
	res := &slotResolver{info: pkInfo()} // stands in for the OAuth resolver: any JWS "verifies"
	var seen http.Header
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer aaa.bbb.ccc")
	rec := serveAuthn(res, opts(achKey), req, func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	if rec.Code != 200 || res.last != "aaa.bbb.ccc" || seen.Get("Authorization") != "" {
		t.Fatalf("our token: %d resolved=%q auth-after=%q", rec.Code, res.last, seen.Get("Authorization"))
	}
	// Not ours (LiteLLM UI's own bearer, a raw key, Basic): no lookup, no
	// identity, forwarded untouched — the upstream authenticates it.
	for _, v := range []string{"Bearer pk-x", "Bearer sk-raw-123", "Basic dXNlcjpwYXNz"} {
		res.last = ""
		var hasKC bool
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", v)
		rec := serveAuthn(res, opts(achKey), req, func(_ http.ResponseWriter, r *http.Request) {
			_, hasKC = KeyContextFromCtx(r.Context())
			seen = r.Header.Clone()
		})
		if rec.Code != 200 || res.last != "" || hasKC || seen.Get("Authorization") != v {
			t.Fatalf("%q: %d resolved=%q kc=%v after=%q, want forwarded untouched", v, rec.Code, res.last, hasKC, seen.Get("Authorization"))
		}
	}
	unknown := &slotResolver{}
	req = httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer aaa.bbb.ccc")
	if rec := serveAuthn(unknown, opts(achKey), req, nil); rec.Code != 401 {
		t.Fatalf("foreign JWS: %d", rec.Code)
	}
}

// Optional (the catch-all): nothing required — anonymous goes through; a
// foreign Authorization goes through like everywhere; an invalid declared
// credential is still a 401.
func TestAuthn_OptionalLetsAnonymousThrough(t *testing.T) {
	res := &slotResolver{} // every credential resolves to "unknown"
	o := opts(achKey)
	o.Optional = true
	var hasKC, hasRaw bool
	capture := func(_ http.ResponseWriter, r *http.Request) {
		_, hasKC = KeyContextFromCtx(r.Context())
		_, hasRaw = RawLiteLLMKeyFromCtx(r.Context())
	}
	if rec := serveAuthn(res, o, httptest.NewRequest("GET", "/health", nil), capture); rec.Code != 200 || hasKC || hasRaw {
		t.Fatalf("anonymous: %d kc=%v raw=%v", rec.Code, hasKC, hasRaw)
	}
	for _, v := range []string{"Bearer sk-foreign", "Basic dXNlcjpwYXNz"} {
		var seen http.Header
		req := httptest.NewRequest("GET", "/health/license", nil)
		req.Header.Set("Authorization", v)
		rec := serveAuthn(res, o, req, func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
		if rec.Code != 200 || res.last != "" || seen.Get("Authorization") != v {
			t.Fatalf("Authorization %q on the catch-all: %d resolved=%q after=%q, want untouched", v, rec.Code, res.last, seen.Get("Authorization"))
		}
	}
	// Shaped like ours but not verifying (an expired ACH token): ours to
	// judge, 401 — never LiteLLM's bare 401.
	for h, v := range map[string]string{"x-ach-key": "pk-revoked", "Authorization": "Bearer aaa.bbb.ccc"} {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set(h, v)
		if rec := serveAuthn(res, o, req, nil); rec.Code != 401 {
			t.Fatalf("%s=%s must not pass: %d", h, v, rec.Code)
		}
	}
}

func TestParseCredentialHeaders(t *testing.T) {
	got, err := ParseCredentialHeaders(`[{"name":"X-Ach-Key","mode":"resolve"},{"name":"x-genai-api-key","mode":"passthrough"}]`)
	if err != nil || len(got) != 2 || got[0].Name != "x-ach-key" || got[1].Mode != ModePassthrough {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{
		`[{"name":"authorization","mode":"resolve"}]`,
		`[{"name":"x","mode":"forward"}]`,
		`[{"name":"","mode":"resolve"}]`,
		`[{"name":"x","mode":"resolve"},{"name":"X","mode":"resolve"}]`,
		`nope`,
	} {
		if _, err := ParseCredentialHeaders(bad); err == nil {
			t.Fatalf("%s must fail", bad)
		}
	}
	if got, err := ParseCredentialHeaders(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
}
