// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockSigner struct {
	mu          sync.Mutex
	lastClaims  jwt.Claims
	signCalls   int
	returnToken string
	returnErr   error
}

func (m *mockSigner) Sign(_ context.Context, c jwt.Claims) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signCalls++
	m.lastClaims = c
	if m.returnErr != nil {
		return "", m.returnErr
	}
	if m.returnToken == "" {
		return "TOKEN", nil
	}
	return m.returnToken, nil
}

func (m *mockSigner) JWKS() []jwt.JWK { return nil }

func (m *mockSigner) Loaded() bool { return true }

type mockTeamsResolver struct {
	teams []string
	err   error
}

func (m *mockTeamsResolver) Resolve(_ context.Context, _ string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.teams, nil
}

func handlerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	return s
}

func makeEnv(name string, mcps, a2as, teams []string) *achv1alpha1.Environment {
	return &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ach-system"},
		Spec: achv1alpha1.EnvironmentSpec{
			Runtime: achv1alpha1.RuntimeBlock{
				MCPServers: mcps,
				A2AAgents:  a2as,
			},
			AuthorizedTeams: teams,
		},
	}
}

func makeBIP(name, kind, target string, forwardJWT bool) *achv1alpha1.BackendIdentityPolicy {
	return &achv1alpha1.BackendIdentityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ach-system"},
		Spec: achv1alpha1.BackendIdentityPolicySpec{
			Target: achv1alpha1.BackendTargetRef{
				Kind: kind,
				Name: target,
			},
			ForwardIdentityJWT: forwardJWT,
		},
	}
}

// requestWithKC constructs a request with KeyContext + request-id attached.
// Returns the request configured with chi URL params for {name} routes.
func requestWithKC(t *testing.T, method, path string, kc middleware.KeyContext, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = int64(len(body))
	}
	ctx := middleware.WithKeyContext(r.Context(), &keystore.KeyInfo{
		KeyID:         kc.KeyID,
		KeyType:       kc.KeyType,
		OwnerEmail:    kc.OwnerEmail,
		Environment:   kc.Environment,
		LiteLLMToken:  kc.LiteLLMToken,
		LiteLLMUserID: kc.LiteLLMUserID,
	}, kc.IsAdmin)
	ctx = middleware.WithRequestID(ctx, "req_test")
	return r.WithContext(ctx)
}

func upstreamSpy() (*httptest.Server, *upstreamRec) {
	rec := &upstreamRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.calls++
		rec.lastAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		rec.lastBody = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return srv, rec
}

type upstreamRec struct {
	mu       sync.Mutex
	calls    int
	lastAuth string
	lastBody []byte
}

func (r *upstreamRec) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}
func (r *upstreamRec) LastAuth() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastAuth
}
func (r *upstreamRec) LastBody() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastBody
}

func mkDeps(t *testing.T, upstream *httptest.Server, k8s client.Client, signer jwt.Signer, resolver precheck.Deps) HandlerDeps {
	t.Helper()
	u := mustParseURL(t, upstream.URL)
	return HandlerDeps{
		Deps: Deps{
			LiteLLMUpstream:  u,
			LiteLLMSharedKey: "shared",
			Logger:           slog.Default(),
		},
		Signer:       signer,
		PrecheckDeps: resolver,
		BaseURL:      "https://ach.example.com",
		Namespace:    "ach-system",
	}
}

// H1: HandlerV1 with pk_ — pure proxy, no JWT, body unchanged.
func TestHandlerV1_PkPassthrough(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	k8s := fake.NewClientBuilder().WithScheme(handlerScheme(t)).Build()
	signer := &mockSigner{}
	deps := mkDeps(t, upstream, k8s, signer, precheck.Deps{K8sClient: k8s, TeamsResolver: &mockTeamsResolver{}, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
	body := `{"model":"x","messages":[]}`
	r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, body)
	w := httptest.NewRecorder()
	HandlerV1(deps)(w, r)

	if rec.Calls() != 1 {
		t.Errorf("upstream calls = %d; want 1", rec.Calls())
	}
	if rec.LastAuth() != "" {
		t.Errorf("Authorization = %q; want empty (no JWT on /v1)", rec.LastAuth())
	}
	if signer.signCalls != 0 {
		t.Errorf("signer called %d times on /v1; want 0", signer.signCalls)
	}
	// body must be byte-identical to input for pk_ traffic
	if string(rec.LastBody()) != body {
		t.Errorf("body changed for pk_; got %s; want %s", rec.LastBody(), body)
	}
}

// H14: HandlerV1 with ek_ + Environment → metadata.tags injected.
func TestHandlerV1_EkTagInjection(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	k8s := fake.NewClientBuilder().WithScheme(handlerScheme(t)).Build()
	deps := mkDeps(t, upstream, k8s, &mockSigner{}, precheck.Deps{K8sClient: k8s, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "prod"}
	r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, `{"model":"x"}`)
	w := httptest.NewRecorder()
	HandlerV1(deps)(w, r)

	if rec.Calls() != 1 {
		t.Fatalf("upstream calls = %d", rec.Calls())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.LastBody(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	meta, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("missing metadata; body=%s", rec.LastBody())
	}
	tags, _ := meta["tags"].([]any)
	if len(tags) != 1 || tags[0] != "environment:prod" {
		t.Errorf("tags = %v; want [environment:prod]", tags)
	}
}

// H3: HandlerMCP with ek_ authorized + BIP winner opt-in → JWT attached on upstream.
func TestHandlerMCP_EkWithJWT(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	env := makeEnv("demo", []string{"server-x"}, nil, nil)
	bip := makeBIP("pol-a", "MCPServer", "server-x", true)
	k8s := fake.NewClientBuilder().
		WithScheme(handlerScheme(t)).
		WithObjects(env, bip).
		WithIndex(&achv1alpha1.BackendIdentityPolicy{}, "spec.target", func(o client.Object) []string {
			p := o.(*achv1alpha1.BackendIdentityPolicy)
			return []string{p.Spec.Target.Kind + "/" + p.Spec.Target.Name}
		}).
		Build()

	signer := &mockSigner{returnToken: "ACH-TOKEN"}
	deps := mkDeps(t, upstream, k8s, signer, precheck.Deps{K8sClient: k8s, TeamsResolver: &mockTeamsResolver{}, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	r := requestWithKC(t, http.MethodGet, "/mcp/server-x/tools", kc, "")

	// Need chi route-context for URLParam
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "server-x")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandlerMCP(deps)(w, r)

	if rec.Calls() != 1 {
		t.Fatalf("upstream calls = %d; w body=%s", rec.Calls(), w.Body.String())
	}
	if rec.LastAuth() != "Bearer ACH-TOKEN" {
		t.Errorf("Authorization = %q; want Bearer ACH-TOKEN", rec.LastAuth())
	}
	if signer.lastClaims.Aud != "mcp:server-x" {
		t.Errorf("aud = %s; want mcp:server-x", signer.lastClaims.Aud)
	}
	if signer.lastClaims.Sub != "ach-system/u@e" {
		t.Errorf("sub = %s; want ach-system/u@e", signer.lastClaims.Sub)
	}
	if signer.lastClaims.Iss != "https://ach.example.com" {
		t.Errorf("iss = %s; want https://ach.example.com", signer.lastClaims.Iss)
	}
}

// H4: HandlerMCP with no BIP → no JWT, request forwarded.
func TestHandlerMCP_NoPolicyNoJWT(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	env := makeEnv("demo", []string{"server-x"}, nil, nil)
	k8s := fake.NewClientBuilder().
		WithScheme(handlerScheme(t)).
		WithObjects(env).
		WithIndex(&achv1alpha1.BackendIdentityPolicy{}, "spec.target", func(o client.Object) []string {
			p := o.(*achv1alpha1.BackendIdentityPolicy)
			return []string{p.Spec.Target.Kind + "/" + p.Spec.Target.Name}
		}).
		Build()
	signer := &mockSigner{}
	deps := mkDeps(t, upstream, k8s, signer, precheck.Deps{K8sClient: k8s, TeamsResolver: &mockTeamsResolver{}, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	r := requestWithKC(t, http.MethodGet, "/mcp/server-x", kc, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "server-x")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandlerMCP(deps)(w, r)

	if rec.Calls() != 1 {
		t.Fatalf("upstream calls = %d", rec.Calls())
	}
	if rec.LastAuth() != "" {
		t.Errorf("Authorization = %q; want empty (no BIP)", rec.LastAuth())
	}
	if signer.signCalls != 0 {
		t.Errorf("signer.signCalls = %d; want 0", signer.signCalls)
	}
}

// H6: HandlerMCP — precheck fails (ek_ name not in env) → 403 envelope, upstream not reached.
func TestHandlerMCP_PrecheckFail(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	env := makeEnv("demo", []string{"server-x"}, nil, nil)
	k8s := fake.NewClientBuilder().WithScheme(handlerScheme(t)).WithObjects(env).Build()
	deps := mkDeps(t, upstream, k8s, &mockSigner{}, precheck.Deps{K8sClient: k8s, TeamsResolver: &mockTeamsResolver{}, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	r := requestWithKC(t, http.MethodGet, "/mcp/server-MISSING", kc, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "server-MISSING")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandlerMCP(deps)(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", w.Code)
	}
	if rec.Calls() != 0 {
		t.Errorf("upstream calls = %d; want 0 (failure must not reach upstream)", rec.Calls())
	}
	var env2 struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env2); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if env2.Error.Code != "unauthorized_resource" {
		t.Errorf("code = %s; want unauthorized_resource", env2.Error.Code)
	}
}

// H11: HandlerA2A claims aud is "a2a:<name>".
func TestHandlerA2A_ClaimsShape(t *testing.T) {
	upstream, _ := upstreamSpy()
	defer upstream.Close()

	env := makeEnv("demo", nil, []string{"agent-y"}, nil)
	bip := makeBIP("pol-a", "A2AAgent", "agent-y", true)
	k8s := fake.NewClientBuilder().
		WithScheme(handlerScheme(t)).
		WithObjects(env, bip).
		WithIndex(&achv1alpha1.BackendIdentityPolicy{}, "spec.target", func(o client.Object) []string {
			p := o.(*achv1alpha1.BackendIdentityPolicy)
			return []string{p.Spec.Target.Kind + "/" + p.Spec.Target.Name}
		}).
		Build()
	signer := &mockSigner{}
	deps := mkDeps(t, upstream, k8s, signer, precheck.Deps{K8sClient: k8s, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	r := requestWithKC(t, http.MethodGet, "/a2a/agent-y", kc, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "agent-y")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandlerA2A(deps)(w, r)

	if signer.lastClaims.Aud != "a2a:agent-y" {
		t.Errorf("aud = %s; want a2a:agent-y", signer.lastClaims.Aud)
	}
}

// H9: HandlerMCP — signing failure → 500 envelope, upstream not reached.
func TestHandlerMCP_SigningFailure(t *testing.T) {
	upstream, rec := upstreamSpy()
	defer upstream.Close()

	env := makeEnv("demo", []string{"server-x"}, nil, nil)
	bip := makeBIP("pol-a", "MCPServer", "server-x", true)
	k8s := fake.NewClientBuilder().
		WithScheme(handlerScheme(t)).
		WithObjects(env, bip).
		WithIndex(&achv1alpha1.BackendIdentityPolicy{}, "spec.target", func(o client.Object) []string {
			p := o.(*achv1alpha1.BackendIdentityPolicy)
			return []string{p.Spec.Target.Kind + "/" + p.Spec.Target.Name}
		}).
		Build()
	signer := &mockSigner{returnErr: errors.New("signer down")}
	deps := mkDeps(t, upstream, k8s, signer, precheck.Deps{K8sClient: k8s, Namespace: "ach-system"})

	kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
	r := requestWithKC(t, http.MethodGet, "/mcp/server-x", kc, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "server-x")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandlerMCP(deps)(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", w.Code)
	}
	if rec.Calls() != 0 {
		t.Errorf("upstream calls = %d; want 0", rec.Calls())
	}
	var env2 struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env2)
	if env2.Error.Code != "internal_error" {
		t.Errorf("code = %s; want internal_error", env2.Error.Code)
	}
}

// classifyPrecheckErr sanity table.
func TestClassifyPrecheckErr(t *testing.T) {
	tests := []struct {
		err           error
		wantStatus    int
		wantCode      string
	}{
		{precheck.ErrUnauthorizedResource, http.StatusForbidden, "unauthorized_resource"},
		{precheck.ErrUnauthorizedTeam, http.StatusForbidden, "unauthorized_team"},
		{precheck.ErrLiteLLMUnreachable, http.StatusServiceUnavailable, "litellm_unreachable"},
		{precheck.ErrInvalidKeyType, http.StatusUnauthorized, "invalid_key_type"},
		{precheck.ErrEnvironmentNotFound, http.StatusNotFound, "environment_not_found"},
		{errors.New("anything else"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tt := range tests {
		_, status, code := classifyPrecheckErr(tt.err)
		if status != tt.wantStatus || code != tt.wantCode {
			t.Errorf("err=%v → (%d, %s); want (%d, %s)", tt.err, status, code, tt.wantStatus, tt.wantCode)
		}
	}
}
