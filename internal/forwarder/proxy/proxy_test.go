// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	return u
}

// testProxyDEK is a deterministic 32-byte data-encryption key for the
// decrypt-on-use path (G3). The forwarder stores key material as a sealed
// keycrypt blob, so tests seal with this DEK and set it on Deps.
func testProxyDEK() []byte {
	k := make([]byte, keycrypt.KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// sealMaterial returns the keycrypt blob the forwarder would read from the
// KeyContext (the platform-api sealed it on mint). Director decrypts it back
// to the plaintext the tests assert on.
func sealMaterial(t *testing.T, plaintext string) string {
	t.Helper()
	blob, err := keycrypt.Seal(testProxyDEK(), []byte(plaintext))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return blob
}

func newDepsWithUpstream(t *testing.T, upstream *httptest.Server) Deps {
	t.Helper()
	return Deps{
		LiteLLMUpstream: mustParseURL(t, upstream.URL),
		Logger:          slog.Default(),
	}
}

func ctxWithKeyAndJWT(kc middleware.KeyContext, jwt string) context.Context {
	ctx := middleware.WithKeyContext(context.Background(), &keystore.KeyInfo{
		KeyID:              kc.KeyID,
		KeyType:            kc.KeyType,
		OwnerEmail:         kc.OwnerEmail,
		Environment:        kc.Environment,
		LiteLLMToken:       kc.LiteLLMToken,
		LiteLLMKeyMaterial: kc.LiteLLMKeyMaterial,
		LiteLLMUserID:      kc.LiteLLMUserID,
	}, kc.IsAdmin)
	if jwt != "" {
		ctx = WithJWT(ctx, jwt)
	}
	return ctx
}

// PR1: New returns a non-nil ReverseProxy.
func TestNew_NonNil(t *testing.T) {
	deps := Deps{LiteLLMUpstream: mustParseURL(t, "http://localhost:1"), Logger: slog.Default()}
	if got := New(deps); got == nil {
		t.Fatal("New returned nil")
	}
}

// PR2+PR3: Director rewrites scheme/host, strips Authorization + x-ach-*, and
// — TESTING-PHASE (reverts FIX01 §A.6 / D-13) — writes the CALLER's own LiteLLM
// virtual key as x-litellm-api-key (bare on /v1), with NO x-litellm-key-id.
func TestDirector_ForwardsUserMaterial(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream:  mustParseURL(t, "http://litellm.svc.cluster.local:4000"),
		Logger:           slog.Default(),
		KeyEncryptionKey: testProxyDEK(),
	}
	rp := New(deps)

	material := "sk-user-1"
	sealed := sealMaterial(t, material)
	kc := middleware.KeyContext{
		KeyType:            keys.PrefixPk,
		OwnerEmail:         "u@example.com",
		LiteLLMKeyMaterial: &sealed,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer evil")
	req.Header.Set("x-ach-key", "pk_xyz")
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxWithKeyAndJWT(kc, ""))

	rp.Director(req)

	if req.URL.Scheme != "http" || req.URL.Host != "litellm.svc.cluster.local:4000" {
		t.Errorf("scheme/host = %s://%s; want upstream", req.URL.Scheme, req.URL.Host)
	}
	if req.URL.Path != "/v1/chat/completions" {
		t.Errorf("path = %s; want verbatim preserved", req.URL.Path)
	}
	if req.Host != "" {
		t.Errorf("req.Host = %q; want empty (Go fills from URL.Host)", req.Host)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q; want empty (stripped before JWT-LAST write)", got)
	}
	if got := req.Header.Get("x-ach-key"); got != "" {
		t.Errorf("x-ach-key = %q; want stripped", got)
	}
	if got := req.Header.Get("x-litellm-api-key"); got != material {
		t.Errorf("x-litellm-api-key = %q; want bare user material %q on /v1", got, material)
	}
	if _, ok := req.Header["X-Litellm-Key-Id"]; ok {
		t.Errorf("x-litellm-key-id must be absent (delegation removed)")
	}
}

// Issue #41: the "Bearer " prefix on x-litellm-api-key is MCP-only. /mcp
// gets "Bearer <material>"; /v1 gets the bare value (asserted in
// TestDirector_ForwardsUserMaterial).
func TestDirector_McpBearerPrefix(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream:  mustParseURL(t, "http://litellm.svc.cluster.local:4000"),
		Logger:           slog.Default(),
		KeyEncryptionKey: testProxyDEK(),
	}
	rp := New(deps)

	material := "sk-user-1"
	sealed := sealMaterial(t, material)
	kc := middleware.KeyContext{
		KeyType:            keys.PrefixPk,
		OwnerEmail:         "u@example.com",
		LiteLLMKeyMaterial: &sealed,
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/some-server", strings.NewReader("{}"))
	req = req.WithContext(ctxWithKeyAndJWT(kc, ""))

	rp.Director(req)

	if got := req.Header.Get("x-litellm-api-key"); got != "Bearer "+material {
		t.Errorf("x-litellm-api-key on /mcp = %q; want Bearer %s", got, material)
	}
	if _, ok := req.Header["X-Litellm-Key-Id"]; ok {
		t.Errorf("x-litellm-key-id must be absent (delegation removed)")
	}
}

// Gemini route: LiteLLM's native Google AI Studio passthrough reads the
// virtual key from x-goog-api-key (NOT x-litellm-api-key — that's the /v1
// proxy). The Director must move the caller's key to x-goog-api-key and drop
// the ignored x-litellm-api-key so LiteLLM does not 401 with
// "Virtual Key expected ... 'sk-'". Verified empirically against LiteLLM
// v1.83.10: x-litellm-api-key → 401, x-goog-api-key → auth OK.
func TestDirector_GeminiGoogAPIKey(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream:  mustParseURL(t, "http://litellm.svc.cluster.local:4000"),
		Logger:           slog.Default(),
		KeyEncryptionKey: testProxyDEK(),
	}
	rp := New(deps)

	material := "sk-user-1"
	sealed := sealMaterial(t, material)
	kc := middleware.KeyContext{
		KeyType:            keys.PrefixPk,
		OwnerEmail:         "u@example.com",
		LiteLLMKeyMaterial: &sealed,
	}
	req := httptest.NewRequest(http.MethodPost,
		"/gemini/v1beta/models/gemini-flash-latest:generateContent", strings.NewReader("{}"))
	// Adversary tries to smuggle their own goog key — must be stripped, then
	// re-set with the caller's material.
	req.Header.Set("x-goog-api-key", "evil-client-key")
	req = req.WithContext(ctxWithKeyAndJWT(kc, ""))

	rp.Director(req)

	if got := req.Header.Get("x-goog-api-key"); got != material {
		t.Errorf("x-goog-api-key on /gemini = %q; want caller material %q", got, material)
	}
	if _, ok := req.Header["X-Litellm-Api-Key"]; ok {
		t.Errorf("x-litellm-api-key must be absent on /gemini (LiteLLM ignores it there → 401)")
	}
}

// Issue #41 (B): LiteLLM v1.87.1's MCP gateway grants non-admin virtual
// keys ONLY on the exact single-segment /mcp/{server} route
// (mcp_inference_routes lists "/mcp/{subpath}", one segment); a trailing
// slash or deeper subpath falls through to the proxy-admin-only route check
// (500). Since Feature 2 forwards the caller's OWN (non-admin) key, the
// Director must collapse any /mcp/<server>/... to the bare /mcp/<server> the
// gateway accepts. Hydrate already writes the bare URL; this normalizes
// clients (and the e2e) that append a slash/subpath.
func TestDirector_McpPathNormalizedToBareServer(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream:  mustParseURL(t, "http://litellm.svc:4000"),
		Logger:           slog.Default(),
		KeyEncryptionKey: testProxyDEK(),
	}
	rp := New(deps)
	sealed := sealMaterial(t, "sk-user-1")

	cases := []struct {
		in, want string
	}{
		{"/mcp/demo-mcp-jwt", "/mcp/demo-mcp-jwt"},     // already bare — unchanged
		{"/mcp/demo-mcp-jwt/", "/mcp/demo-mcp-jwt"},    // trailing slash stripped
		{"/mcp/demo-mcp-jwt/mcp", "/mcp/demo-mcp-jwt"}, // streamable subpath collapsed
		{"/mcp/demo-mcp-jwt/tools/call", "/mcp/demo-mcp-jwt"},
		{"/v1/chat/completions", "/v1/chat/completions"}, // non-mcp untouched
	}
	for _, tc := range cases {
		kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e", LiteLLMKeyMaterial: &sealed}
		req := httptest.NewRequest(http.MethodPost, tc.in, strings.NewReader("{}"))
		req = req.WithContext(ctxWithKeyAndJWT(kc, ""))
		rp.Director(req)
		if req.URL.Path != tc.want {
			t.Errorf("Director(%q): path = %q; want %q", tc.in, req.URL.Path, tc.want)
		}
	}
}

// PR5: JWT written LAST — strip clears any client Authorization, then
// the per-request JWT (via WithJWT) is the sole bearer on the upstream req.
func TestDirector_JWTWrittenLast(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream:  mustParseURL(t, "http://upstream:4000"),
		Logger:           slog.Default(),
		KeyEncryptionKey: testProxyDEK(),
	}
	rp := New(deps)
	material := "sk-user-1"
	sealed := sealMaterial(t, material)
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e", LiteLLMKeyMaterial: &sealed}

	req := httptest.NewRequest(http.MethodGet, "/mcp/foo", nil)
	req.Header.Set("Authorization", "Bearer client-injected") // adversary tries to spoof
	req = req.WithContext(ctxWithKeyAndJWT(kc, "ACH-JWT-TOKEN"))

	rp.Director(req)

	if got := req.Header.Get("Authorization"); got != "Bearer ACH-JWT-TOKEN" {
		t.Errorf("Authorization = %q; want Bearer ACH-JWT-TOKEN (strip first, JWT last)", got)
	}
	if got := req.Header.Get("x-litellm-api-key"); got != "Bearer "+material {
		t.Errorf("x-litellm-api-key on /mcp = %q; want Bearer %s (MCP prefix survives JWT-last write)", got, material)
	}
}

// PR6: ErrorHandler renders 502 envelope, never echoes raw err.
func TestErrorHandler_502Envelope(t *testing.T) {
	// Build a listener that immediately closes — guarantees a transport error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // closed before any client connects

	deps := Deps{
		LiteLLMUpstream: mustParseURL(t, "http://"+addr),
		Logger:          slog.Default(),
	}
	rp := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req = req.WithContext(middleware.WithRequestID(req.Context(), "req_test_001"))
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d; want 502", rec.Code)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v; body=%s", err, rec.Body.String())
	}
	if env.Error.Code != "upstream_unreachable" {
		t.Errorf("code = %s; want upstream_unreachable", env.Error.Code)
	}
	if env.RequestID != "req_test_001" {
		t.Errorf("request_id = %s; want req_test_001", env.RequestID)
	}
	// No transport-level leak (no "connection refused" / hostname in body).
	body := rec.Body.String()
	if strings.Contains(body, "connection refused") || strings.Contains(body, addr) {
		t.Errorf("error body leaked transport details: %s", body)
	}
}

// A caller that disconnects mid-proxy (an /mcp SSE stream closed by the
// client) cancels the inbound request context. That is a client disconnect,
// not an upstream fault: the ErrorHandler must NOT render a 502 envelope
// (the client is already gone) and must NOT count litellm_unreachable.
func TestErrorHandler_ClientCancelNo502(t *testing.T) {
	deps := Deps{
		LiteLLMUpstream: mustParseURL(t, "http://127.0.0.1:1"),
		Logger:          slog.Default(),
	}
	rp := New(deps)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client already gone
	req := httptest.NewRequest(http.MethodGet, "/mcp/mcp-gitlab", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadGateway {
		t.Fatalf("client cancel must not yield 502; got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("client cancel must write no body; got %q", rec.Body.String())
	}
}

// PR7: ModifyResponse pass-through — upstream's status/headers/body verbatim.
func TestModifyResponse_PassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amazing", "1")
		w.WriteHeader(418)
		_, _ = w.Write([]byte("teapot"))
	}))
	defer upstream.Close()

	rp := New(newDepsWithUpstream(t, upstream))
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anything", nil))

	if rec.Code != 418 {
		t.Errorf("status = %d; want 418", rec.Code)
	}
	if got := rec.Header().Get("X-Amazing"); got != "1" {
		t.Errorf("X-Amazing = %q; want 1", got)
	}
	if got := rec.Body.String(); got != "teapot" {
		t.Errorf("body = %q; want teapot", got)
	}
}

// PR8: SSE pass-through — chunked body streams through without being buffered.
// Verified by sending 3 chunks each 50ms apart and asserting overall
// transit time is at least ~150ms (no buffering would defer all chunks
// to end-of-handler).
func TestStreaming_PassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("data: chunk\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	rp := New(newDepsWithUpstream(t, upstream))
	server := httptest.NewServer(rp)
	defer server.Close()

	start := time.Now()
	resp, err := http.Get(server.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("response too fast (%v) — body may have been buffered, not streamed", elapsed)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q; want text/event-stream", got)
	}
}

// PR1-extra: keyTypeFor maps BearerPrefix to Hub §18.5 short labels.
func TestKeyTypeFor(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"none", context.Background(), "none"},
		{"pk", ctxWithKeyAndJWT(middleware.KeyContext{KeyType: keys.PrefixPk}, ""), "pk"},
		{"ek", ctxWithKeyAndJWT(middleware.KeyContext{KeyType: keys.PrefixEk}, ""), "ek"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyTypeFor(tt.ctx); got != tt.want {
				t.Errorf("got %s; want %s", got, tt.want)
			}
		})
	}
}

// PR-extra: routeFor classifies paths into Hub §18.5 route labels.
func TestRouteFor(t *testing.T) {
	tests := map[string]string{
		"/v1/chat/completions": "/v1",
		"/v2/model/info":       "/v2",
		"/gemini/foo":          "/gemini",
		"/mcp/server-x/tools":  "/mcp",
		"/a2a/agent-y":         "/a2a",
		"/healthz":             "unknown",
	}
	for path, want := range tests {
		if got := routeFor(path); got != want {
			t.Errorf("routeFor(%s) = %s; want %s", path, got, want)
		}
	}
}
