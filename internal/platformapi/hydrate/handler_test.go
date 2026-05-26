// SPDX-License-Identifier: Apache-2.0

package hydrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/hydrate"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

const (
	testNamespace = "ach-system"
	testBaseURL   = "https://ach.example.com"
)

// ----- fixtures ---------------------------------------------------------

func newScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	return s
}

// newEnv constructs an Environment CR. Pass empty slices to verify the
// []-not-null invariant; supply non-empty slices to drive runtime/context
// projections.
func newEnv(name string, authorizedTeams []string, runtime achv1alpha1.RuntimeBlock, ctxBlk achv1alpha1.ContextBlock) *achv1alpha1.Environment {
	return &achv1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: achv1alpha1.GroupVersion.String(),
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: authorizedTeams,
			Runtime:         runtime,
			Context:         ctxBlk,
		},
	}
}

func emptyRuntime() achv1alpha1.RuntimeBlock {
	return achv1alpha1.RuntimeBlock{
		Models:     []string{},
		MCPServers: []string{},
		A2AAgents:  []string{},
	}
}

func emptyContext() achv1alpha1.ContextBlock {
	return achv1alpha1.ContextBlock{
		Prompts:   []string{},
		Plugins:   []string{},
		Artifacts: []string{},
	}
}

func buildClient(t *testing.T, envs ...*achv1alpha1.Environment) client.Client {
	t.Helper()
	scheme := newScheme(t)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	objs := []client.Object{ns}
	for _, e := range envs {
		objs = append(objs, e)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&achv1alpha1.Environment{}).
		Build()
}

// fakeLiteLLM is a stub satisfying litellm.Client driving the team
// lookup path.
type fakeLiteLLM struct {
	userInfo func(email string) (*litellm.UserInfo, error)
}

func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	if f.userInfo == nil {
		return nil, nil
	}
	return f.userInfo(email)
}
func (f *fakeLiteLLM) DeleteAccessGroup(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) DeleteTag(_ context.Context, _ string) error         { return nil }
func (f *fakeLiteLLM) ListModels(_ context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) ListUserKeys(_ context.Context, _ string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) RevokeKey(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeLiteLLM) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

var _ litellm.Client = (*fakeLiteLLM)(nil)

func teamsFor(ts []string) *fakeLiteLLM {
	return &fakeLiteLLM{
		userInfo: func(email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-" + email, UserEmail: email, Teams: ts}, nil
		},
	}
}

// pkContext is a pk_ caller with the supplied email + admin flag.
func pkContext(email string, isAdmin bool) *middleware.KeyContext {
	return &middleware.KeyContext{
		KeyID:      "pkid_" + email,
		KeyType:    keys.PrefixPk,
		OwnerEmail: email,
		IsAdmin:    isAdmin,
	}
}

// ekContext is an ek_ caller bound to env.
func ekContext(env string) *middleware.KeyContext {
	return &middleware.KeyContext{
		KeyID:       "ekid_test",
		KeyType:     keys.PrefixEk,
		OwnerEmail:  "workload@example",
		Environment: env,
	}
}

func quietAudit() *slog.Logger {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func newDeps(c client.Client, ll litellm.Client) hydrate.Deps {
	return hydrate.Deps{
		Store:     store.New(c, testNamespace, logr.Discard()),
		LiteLLM:   ll,
		BaseURL:   testBaseURL,
		Allowlist: map[string]struct{}{},
		Audit:     quietAudit(),
		Namespace: testNamespace,
	}
}

// post executes a POST /platform/hydrate with the supplied body and KeyContext.
// Returns the recorder for assertion and the raw response body bytes.
func post(t *testing.T, deps hydrate.Deps, body string, kc *middleware.KeyContext) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var bodyReader *strings.Reader
	if body == "" {
		bodyReader = strings.NewReader("")
	} else {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/hydrate", bodyReader)
	if body != "" {
		req.ContentLength = int64(len(body))
	} else {
		req.ContentLength = 0
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	ctx = middleware.WithRequestID(ctx, "req_test")
	if kc != nil {
		info := &keystore.KeyInfo{
			KeyID:       kc.KeyID,
			KeyType:     kc.KeyType,
			OwnerEmail:  kc.OwnerEmail,
			Environment: kc.Environment,
		}
		ctx = middleware.WithKeyContext(ctx, info, kc.IsAdmin)
	}
	rec := httptest.NewRecorder()
	hydrate.HydrateHandler(deps)(rec, req.WithContext(ctx))
	return rec, rec.Body.Bytes()
}

func readJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode JSON: %v\nbody=%s", err, body)
	}
	return out
}

func readErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	out := readJSON(t, body)
	errBlock, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error block, got %v", out)
	}
	code, _ := errBlock["code"].(string)
	return code
}

// ----- H-1: pk_ happy path with populated env --------------------------

func TestH1PkHappyPath(t *testing.T) {
	env := newEnv("prod", []string{"a"},
		achv1alpha1.RuntimeBlock{
			Models:     []string{"m1"},
			MCPServers: []string{},
			A2AAgents:  []string{},
		},
		achv1alpha1.ContextBlock{
			Prompts:   []string{},
			Plugins:   []string{"plugin1"},
			Artifacts: []string{},
		},
	)
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	rec, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	out := readJSON(t, body)
	if out["schemaVersion"].(string) != "v1alpha1" {
		t.Fatalf("schemaVersion: got %v", out["schemaVersion"])
	}
	if out["environment"].(string) != "prod" {
		t.Fatalf("environment: got %v", out["environment"])
	}
	runtime := out["runtime"].(map[string]any)
	models := runtime["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"].(string) != "m1" {
		t.Fatalf("runtime.models: got %v", models)
	}
	contextBlk := out["context"].(map[string]any)
	plugins := contextBlk["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("context.plugins: got %v", plugins)
	}
	p := plugins[0].(map[string]any)
	if p["name"].(string) != "plugin1" {
		t.Fatalf("plugin name: %v", p["name"])
	}
	if p["downloadUrl"].(string) != "https://ach.example.com/content/plugin/plugin1" {
		t.Fatalf("plugin downloadUrl: %v", p["downloadUrl"])
	}
}

// ----- H-2: pk_ missing environment in body ---------------------------

func TestH2PkMissingEnvironment(t *testing.T) {
	deps := newDeps(buildClient(t), teamsFor([]string{"a"}))

	rec, body := post(t, deps, `{}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "missing_environment" {
		t.Fatalf("code: got %q want missing_environment", code)
	}
}

// ----- H-3: pk_ unauthorized team ------------------------------------

func TestH3PkUnauthorizedTeam(t *testing.T) {
	env := newEnv("prod", []string{"b"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	rec, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "unauthorized_team" {
		t.Fatalf("code: got %q want unauthorized_team", code)
	}
}

// ----- H-4: pk_ unknown env -----------------------------------------

func TestH4PkUnknownEnv(t *testing.T) {
	deps := newDeps(buildClient(t), teamsFor([]string{"a"}))

	rec, body := post(t, deps, `{"environment":"missing"}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "environment_not_found" {
		t.Fatalf("code: got %q want environment_not_found", code)
	}
}

// ----- H-5: ek_ happy path with no body env ------------------------

func TestH5EkBoundEnvNoBody(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{}))

	rec, body := post(t, deps, `{}`, ekContext("prod"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	out := readJSON(t, body)
	if out["environment"].(string) != "prod" {
		t.Fatalf("environment: got %v", out["environment"])
	}
}

// ----- H-6: ek_ body env matches binding -------------------------

func TestH6EkBodyMatchesBinding(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{}))

	rec, body := post(t, deps, `{"environment":"prod"}`, ekContext("prod"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
}

// ----- H-7: ek_ body env mismatch -> wrong_environment ---------

func TestH7EkBodyMismatch(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{}))

	rec, body := post(t, deps, `{"environment":"stage"}`, ekContext("prod"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "wrong_environment" {
		t.Fatalf("code: got %q want wrong_environment", code)
	}
}

// ----- H-8: unknown body field -> invalid_argument -------------

func TestH8UnknownBodyField(t *testing.T) {
	deps := newDeps(buildClient(t), teamsFor([]string{}))

	rec, body := post(t, deps, `{"environment":"prod","extra":"x"}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "invalid_argument" {
		t.Fatalf("code: got %q want invalid_argument", code)
	}
}

// ----- H-9: empty body — pk_ requires env, ek_ serves bound ----

func TestH9EmptyBodyPk(t *testing.T) {
	deps := newDeps(buildClient(t), teamsFor([]string{"a"}))

	rec, body := post(t, deps, ``, pkContext("caller@a.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pk_ empty body: status got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "missing_environment" {
		t.Fatalf("pk_ empty body: code got %q want missing_environment", code)
	}
}

func TestH9EmptyBodyEk(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{}))

	rec, body := post(t, deps, ``, ekContext("prod"))
	if rec.Code != http.StatusOK {
		t.Fatalf("ek_ empty body: status got %d body=%s", rec.Code, body)
	}
}

// ----- H-10: terminating env STILL served --------------------

func TestH10TerminatingEnvServed(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	now := metav1.NewTime(time.Now().UTC())
	env.DeletionTimestamp = &now
	// Fake client requires a finalizer when DeletionTimestamp is set.
	env.Finalizers = []string{"ach.ackstorm.ai/test-finalizer"}
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	rec, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("terminating env: status got %d body=%s", rec.Code, body)
	}
	out := readJSON(t, body)
	if out["environment"].(string) != "prod" {
		t.Fatalf("environment: got %v", out["environment"])
	}
}

// ----- H-11: schemaVersion strict literal -------------------

func TestH11SchemaVersionStrict(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	_, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	if !strings.Contains(string(body), `"schemaVersion":"v1alpha1"`) {
		t.Fatalf(`expected "schemaVersion":"v1alpha1" substring, got %s`, body)
	}
}

// ----- H-12: downloadUrl + endpoint construction (WARN-02) -

func TestH12RuntimeAndContextUrls(t *testing.T) {
	env := newEnv("prod", []string{"a"},
		achv1alpha1.RuntimeBlock{
			Models:     []string{"gpt-4"},
			MCPServers: []string{"github"},
			A2AAgents:  []string{"agentx"},
		},
		achv1alpha1.ContextBlock{
			Prompts:   []string{},
			Plugins:   []string{"plugin1"},
			Artifacts: []string{},
		},
	)
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	_, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	out := readJSON(t, body)

	runtime := out["runtime"].(map[string]any)
	model := runtime["models"].([]any)[0].(map[string]any)
	if model["id"].(string) != "gpt-4" {
		t.Errorf("model id: %v", model["id"])
	}
	if model["endpoint"].(string) != "https://ach.example.com/v1" {
		t.Errorf("model endpoint: %v", model["endpoint"])
	}
	mcp := runtime["mcpServers"].([]any)[0].(map[string]any)
	if mcp["id"].(string) != "github" {
		t.Errorf("mcp id: %v", mcp["id"])
	}
	if mcp["endpoint"].(string) != "https://ach.example.com/mcp/github" {
		t.Errorf("mcp endpoint: %v", mcp["endpoint"])
	}
	a2a := runtime["a2aAgents"].([]any)[0].(map[string]any)
	if a2a["id"].(string) != "agentx" {
		t.Errorf("a2a id: %v", a2a["id"])
	}
	if a2a["endpoint"].(string) != "https://ach.example.com/a2a/agentx" {
		t.Errorf("a2a endpoint: %v", a2a["endpoint"])
	}
	ctxBlk := out["context"].(map[string]any)
	p := ctxBlk["plugins"].([]any)[0].(map[string]any)
	if p["name"].(string) != "plugin1" || p["id"].(string) != "plugin1" {
		t.Errorf("plugin name/id: %v %v", p["name"], p["id"])
	}
	if p["downloadUrl"].(string) != "https://ach.example.com/content/plugin/plugin1" {
		t.Errorf("plugin downloadUrl: %v", p["downloadUrl"])
	}
}

// ----- H-13: empty arrays as [] (not null) ---------------

func TestH13EmptyArraysPresent(t *testing.T) {
	env := newEnv("prod", []string{"a"}, emptyRuntime(), emptyContext())
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	_, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	bodyStr := string(body)

	// Each of the 6 array fields must appear as `:[]` in the JSON wire.
	for _, want := range []string{
		`"models":[]`,
		`"mcpServers":[]`,
		`"a2aAgents":[]`,
		`"prompts":[]`,
		`"plugins":[]`,
		`"artifacts":[]`,
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("missing literal %q in body: %s", want, bodyStr)
		}
	}
	// And no field MUST be serialized as null.
	if strings.Contains(bodyStr, `"models":null`) ||
		strings.Contains(bodyStr, `"plugins":null`) {
		t.Errorf("an array field serialized as null in body: %s", bodyStr)
	}
}

// ----- H-14: NO plaintext (pk_*/ek_*) anywhere in the response -

func TestH14NoPlaintextInResponse(t *testing.T) {
	env := newEnv("prod", []string{"a"},
		achv1alpha1.RuntimeBlock{
			Models:     []string{"m1"},
			MCPServers: []string{"github"},
			A2AAgents:  []string{"agentx"},
		},
		achv1alpha1.ContextBlock{
			Prompts:   []string{"p1"},
			Plugins:   []string{"plugin1"},
			Artifacts: []string{"a1"},
		},
	)
	deps := newDeps(buildClient(t, env), teamsFor([]string{"a"}))

	_, body := post(t, deps, `{"environment":"prod"}`, pkContext("caller@a.com", false))
	// Match pk_ / ek_ followed by any 26 base32-lowercase chars (the
	// plaintext bearer grammar). The grep gate fails the test if any
	// substring matches.
	bearerRe := regexp.MustCompile(`\b(pk|ek)_[a-z2-7]{26}\b`)
	if loc := bearerRe.FindIndex(body); loc != nil {
		t.Fatalf("plaintext bearer detected at %v in response body: %s",
			loc, body[loc[0]:loc[1]])
	}
}

// ----- bonus: invalid JSON body returns invalid_argument ------

func TestInvalidJSONBody(t *testing.T) {
	deps := newDeps(buildClient(t), teamsFor([]string{}))

	rec, body := post(t, deps, `not json`, pkContext("caller@a.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
	if code := readErrorCode(t, body); code != "invalid_argument" {
		t.Fatalf("code: got %q want invalid_argument", code)
	}
}

// ----- bonus: admin pk_ skips team check -----------------

func TestAdminPkSkipsTeamCheck(t *testing.T) {
	env := newEnv("prod", []string{"b"}, emptyRuntime(), emptyContext()) // team b
	ll := &fakeLiteLLM{userInfo: func(string) (*litellm.UserInfo, error) {
		t.Fatal("LookupCallerTeams must NOT be invoked for admin caller")
		return nil, nil
	}}
	deps := newDeps(buildClient(t, env), ll)

	rec, body := post(t, deps, `{"environment":"prod"}`, pkContext("admin@ackstorm.ai", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, body)
	}
}
