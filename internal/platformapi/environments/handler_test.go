// SPDX-License-Identifier: Apache-2.0

package environments_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	"github.com/ackstorm/ach/internal/platformapi/environments"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/store"
)

const testNamespace = "ach-system"

// ----- fixtures ---------------------------------------------------------

// newScheme returns the runtime scheme with corev1 + the ACH v1alpha1 types
// registered — required for the fake client to know how to (de)serialize
// Environment CRs.
func newScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	return s
}

// newEnv constructs an Environment CR with the supplied authorized teams.
// Runtime + Context default to empty slices so the projection's `[]`
// invariant is exercised on the wire.
func newEnv(name string, teams []string) *achv1alpha1.Environment {
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
			AuthorizedTeams: teams,
			Runtime: achv1alpha1.RuntimeBlock{
				Models:     []string{},
				MCPServers: []string{},
				A2AAgents:  []string{},
			},
			Context: achv1alpha1.ContextBlock{
				Prompts:   []string{},
				Plugins:   []string{},
				Artifacts: []string{},
			},
		},
	}
}

// newEnvWithConditions returns an env carrying the supplied .status.conditions
// slice — used to verify EL-6 verbatim round-trip.
func newEnvWithConditions(name string, teams []string, conds []metav1.Condition) *achv1alpha1.Environment {
	env := newEnv(name, teams)
	env.Status.Conditions = conds
	return env
}

// buildClient returns a fake.Client seeded with the supplied Environment list.
// The status sub-resource is enabled via WithStatusSubresource so .status
// conditions round-trip through the fake.
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

// fakeLiteLLM is a stub satisfying litellm.Client used to drive the team
// lookup path (and to expose the call count + canned teams the tests
// configure).
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

// teamsFor returns a fakeLiteLLM whose UserInfoByEmail responds with the
// supplied team list. Empty slice means caller has no teams.
func teamsFor(ts []string) *fakeLiteLLM {
	return &fakeLiteLLM{
		userInfo: func(email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-" + email, UserEmail: email, Teams: ts}, nil
		},
	}
}

// authedRequest builds a request with a populated KeyContext +
// request_id in context, mimicking the middleware chain. Returns the
// recorder for the caller to assert against.
func authedRequest(t *testing.T, method, target string, kc *middleware.KeyContext) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	ctx := req.Context()
	ctx = middleware.WithRequestID(ctx, "req_test")
	if kc != nil {
		info := &keystore.KeyInfo{
			KeyID:      kc.KeyID,
			KeyType:    kc.KeyType,
			OwnerEmail: kc.OwnerEmail,
		}
		ctx = middleware.WithKeyContext(ctx, info, kc.IsAdmin)
	}
	return httptest.NewRecorder(), req.WithContext(ctx)
}

// servedRouter mounts environments.Mount onto a chi.Mux and serves a single
// request — used by the tests that exercise the /{name} route param.
func servedRouter(deps environments.Deps) *chi.Mux {
	r := chi.NewMux()
	r.Route("/platform/environments", environments.Mount(deps))
	return r
}

// readJSON unmarshals the body bytes into a map and fails the test on error.
func readJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode JSON: %v\nbody=%s", err, body)
	}
	return out
}

// readErrorCode extracts the §15.5 error envelope code or fails.
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

// pkContext is a helper for a non-admin pk_ caller with the supplied email.
func pkContext(email string, isAdmin bool) *middleware.KeyContext {
	return &middleware.KeyContext{
		KeyID:      "pkid_test-" + email,
		KeyType:    keys.PrefixPk,
		OwnerEmail: email,
		IsAdmin:    isAdmin,
	}
}

// ekContext is a helper for an ek_ caller — always rejected by these handlers.
func ekContext() *middleware.KeyContext {
	return &middleware.KeyContext{
		KeyID:      "ekid_test",
		KeyType:    keys.PrefixEk,
		OwnerEmail: "ek@workload",
	}
}

func quietAudit() *slog.Logger {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func newDeps(c client.Client, ll litellm.Client) environments.Deps {
	return environments.Deps{
		Store:     store.New(c, testNamespace, logr.Discard()),
		LiteLLM:   ll,
		Allowlist: map[string]struct{}{},
		Audit:     quietAudit(),
		Namespace: testNamespace,
	}
}

// ----- ListHandler tests (EL-1..EL-7) -----------------------------------

// EL-1: non-admin caller in team "a" sees env1 + env3 but not env2.
func TestEL1NonAdminListIntersection(t *testing.T) {
	c := buildClient(t,
		newEnv("env1", []string{"a"}),
		newEnv("env2", []string{"b"}),
		newEnv("env3", []string{"a", "c"}),
	)
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", pkContext("caller@a.com", false))
	environments.ListHandler(deps)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	out := readJSON(t, rec.Body.Bytes())
	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d (body=%s)", len(items), rec.Body.String())
	}
	names := map[string]bool{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		names[m["name"].(string)] = true
	}
	if !names["env1"] || !names["env3"] || names["env2"] {
		t.Fatalf("unexpected name set %v", names)
	}
}

// EL-2: admin sees every env regardless of team intersection.
func TestEL2AdminSeesAll(t *testing.T) {
	c := buildClient(t,
		newEnv("env1", []string{"a"}),
		newEnv("env2", []string{"b"}),
		newEnv("env3", []string{"c"}),
	)
	// LiteLLM should NOT be called for admin path; supply a panicker.
	ll := &fakeLiteLLM{userInfo: func(string) (*litellm.UserInfo, error) {
		t.Fatal("LookupCallerTeams must NOT be invoked for admin caller")
		return nil, nil
	}}
	deps := newDeps(c, ll)

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", pkContext("admin@ackstorm.ai", true))
	environments.ListHandler(deps)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	out := readJSON(t, rec.Body.Bytes())
	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items for admin, got %d", len(items))
	}
}

// EL-3: ek_ caller rejected with 401 invalid_key_type.
func TestEL3EkRejected(t *testing.T) {
	c := buildClient(t, newEnv("env1", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", ekContext())
	environments.ListHandler(deps)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if code := readErrorCode(t, rec.Body.Bytes()); code != "invalid_key_type" {
		t.Fatalf("code: got %q, want invalid_key_type", code)
	}
}

// EL-4: limit + cursor pagination across 7 envs all authorized; ?limit=3
// returns 3 + cursor; second page 3 + cursor; third page 1 + nil cursor.
func TestEL4Pagination(t *testing.T) {
	envs := []*achv1alpha1.Environment{}
	for i := 0; i < 7; i++ {
		envs = append(envs, newEnv("env"+strconv.Itoa(i), []string{"a"}))
	}
	c := buildClient(t, envs...)
	deps := newDeps(c, teamsFor([]string{"a"}))

	collected := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		target := "/platform/environments?limit=3"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		rec, req := authedRequest(t, http.MethodGet, target, pkContext("caller@a.com", false))
		environments.ListHandler(deps)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status %d body=%s", page, rec.Code, rec.Body.String())
		}
		out := readJSON(t, rec.Body.Bytes())
		items, _ := out["items"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			collected[m["name"].(string)] = true
		}
		nc, hasCursor := out["next_cursor"].(string)
		if !hasCursor {
			// nil next_cursor => last page
			if page == 0 {
				t.Fatalf("first page should have next_cursor; body=%s", rec.Body.String())
			}
			break
		}
		cursor = nc
	}
	if len(collected) != 7 {
		t.Fatalf("expected to collect 7 envs across pages, got %d (%v)", len(collected), collected)
	}
}

// EL-5: ?limit=600 → 400 invalid_argument; ?limit=0 → 400; ?limit=-1 → 400.
func TestEL5LimitCap(t *testing.T) {
	c := buildClient(t, newEnv("env1", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	for _, raw := range []string{"600", "0", "-1", "abc"} {
		rec, req := authedRequest(t, http.MethodGet, "/platform/environments?limit="+raw, pkContext("caller@a.com", false))
		environments.ListHandler(deps)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q: status got %d, want 400", raw, rec.Code)
		}
		if code := readErrorCode(t, rec.Body.Bytes()); code != "invalid_argument" {
			t.Fatalf("limit=%q: code got %q, want invalid_argument", raw, code)
		}
	}
}

// EL-6: conditions[] round-trip verbatim (Type, Status, Reason all preserved).
func TestEL6ConditionsVerbatim(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	conds := []metav1.Condition{
		{Type: store.ConditionTypeAccessGroupSynced, Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: now},
		{Type: "ContentReady", Status: metav1.ConditionFalse, Reason: "StaleCacheExpired", Message: "see CS-09", LastTransitionTime: now},
	}
	env := newEnvWithConditions("prod", []string{"a"}, conds)
	c := buildClient(t, env)
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", pkContext("caller@a.com", false))
	environments.ListHandler(deps)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	out := readJSON(t, rec.Body.Bytes())
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0].(map[string]any)
	gotConds, ok := item["conditions"].([]any)
	if !ok || len(gotConds) != 2 {
		t.Fatalf("expected 2 conditions, got %#v", item["conditions"])
	}
	for i, want := range conds {
		c := gotConds[i].(map[string]any)
		if c["type"].(string) != want.Type {
			t.Errorf("cond %d: type got %v, want %s", i, c["type"], want.Type)
		}
		if c["status"].(string) != string(want.Status) {
			t.Errorf("cond %d: status got %v, want %s", i, c["status"], want.Status)
		}
		if c["reason"].(string) != want.Reason {
			t.Errorf("cond %d: reason got %v, want %s", i, c["reason"], want.Reason)
		}
		if _, hasLTT := c["lastTransitionTime"]; !hasLTT {
			t.Errorf("cond %d: lastTransitionTime missing", i)
		}
	}
}

// EL-7: top-level keys are exactly {items, next_cursor}.
func TestEL7ResponseEnvelope(t *testing.T) {
	c := buildClient(t, newEnv("env1", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", pkContext("caller@a.com", false))
	environments.ListHandler(deps)(rec, req)

	out := readJSON(t, rec.Body.Bytes())
	if len(out) != 2 {
		t.Fatalf("expected 2 top-level keys, got %d (%v)", len(out), out)
	}
	if _, ok := out["items"]; !ok {
		t.Fatalf("missing items key")
	}
	if _, ok := out["next_cursor"]; !ok {
		t.Fatalf("missing next_cursor key")
	}
}

// ----- GetHandler tests (EG-1..EG-5) ------------------------------------

func servedGet(t *testing.T, deps environments.Deps, name string, kc *middleware.KeyContext) *httptest.ResponseRecorder {
	t.Helper()
	r := servedRouter(deps)
	req := httptest.NewRequest(http.MethodGet, "/platform/environments/"+name, nil)
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	if kc != nil {
		info := &keystore.KeyInfo{
			KeyID:      kc.KeyID,
			KeyType:    kc.KeyType,
			OwnerEmail: kc.OwnerEmail,
		}
		ctx = middleware.WithKeyContext(ctx, info, kc.IsAdmin)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// EG-1: pk_ caller authorized by team intersection — 200 with EnvironmentView.
func TestEG1GetHappy(t *testing.T) {
	c := buildClient(t, newEnv("prod", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec := servedGet(t, deps, "prod", pkContext("caller@a.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	out := readJSON(t, rec.Body.Bytes())
	if out["name"].(string) != "prod" {
		t.Fatalf("name: got %v want prod", out["name"])
	}
}

// EG-2: non-admin without team intersection → 403 unauthorized_team.
func TestEG2GetUnauthorized(t *testing.T) {
	c := buildClient(t, newEnv("prod", []string{"b"}))
	deps := newDeps(c, teamsFor([]string{"a"})) // caller is in "a", env in "b"

	rec := servedGet(t, deps, "prod", pkContext("caller@a.com", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	if code := readErrorCode(t, rec.Body.Bytes()); code != "unauthorized_team" {
		t.Fatalf("code: got %q want unauthorized_team", code)
	}
}

// EG-3: admin sees env regardless of team intersection.
func TestEG3GetAdminBypass(t *testing.T) {
	c := buildClient(t, newEnv("prod", []string{"b"}))
	ll := &fakeLiteLLM{userInfo: func(string) (*litellm.UserInfo, error) {
		t.Fatal("LookupCallerTeams must NOT be invoked for admin caller")
		return nil, nil
	}}
	deps := newDeps(c, ll)

	rec := servedGet(t, deps, "prod", pkContext("admin@ackstorm.ai", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

// EG-4: unknown env → 404 environment_not_found.
func TestEG4GetNotFound(t *testing.T) {
	c := buildClient(t) // empty
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec := servedGet(t, deps, "missing", pkContext("caller@a.com", false))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
	if code := readErrorCode(t, rec.Body.Bytes()); code != "environment_not_found" {
		t.Fatalf("code: got %q want environment_not_found", code)
	}
}

// EG-5: ek_ caller rejected with 401 invalid_key_type.
func TestEG5GetEkRejected(t *testing.T) {
	c := buildClient(t, newEnv("prod", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec := servedGet(t, deps, "prod", ekContext())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d, want 401", rec.Code)
	}
	if code := readErrorCode(t, rec.Body.Bytes()); code != "invalid_key_type" {
		t.Fatalf("code: got %q want invalid_key_type", code)
	}
}

// ----- bonus: response Content-Type is application/json --------------

// TestListContentTypeJSON asserts the success envelope sets the §15.5
// Content-Type header (downstream CLI parses on Content-Type).
func TestListContentTypeJSON(t *testing.T) {
	c := buildClient(t, newEnv("env1", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments", pkContext("caller@a.com", false))
	environments.ListHandler(deps)(rec, req)

	got := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type: got %q, want application/json prefix", got)
	}
}

// TestListInvalidCursor → 400 invalid_argument (defense against opaque-cursor
// corruption).
func TestListInvalidCursor(t *testing.T) {
	c := buildClient(t, newEnv("env1", []string{"a"}))
	deps := newDeps(c, teamsFor([]string{"a"}))

	rec, req := authedRequest(t, http.MethodGet, "/platform/environments?cursor=!!notbase64!!", pkContext("caller@a.com", false))
	environments.ListHandler(deps)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if code := readErrorCode(t, rec.Body.Bytes()); code != "invalid_argument" {
		t.Fatalf("code: got %q want invalid_argument", code)
	}
	// Sanity check on the base64 cursor encoding contract — encoded "42"
	// decodes back to 42.
	wantCursor := base64.StdEncoding.EncodeToString([]byte("42"))
	if got, _ := base64.StdEncoding.DecodeString(wantCursor); string(got) != "42" {
		t.Fatalf("base64 contract round-trip failed: %q vs 42", got)
	}
}
