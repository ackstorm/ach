// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// =========================== fakes ===========================

// recordingRedis records every Del call so tests can assert call
// ordering against db / litellm step recorders. Implements redisDeleter.
type recordingRedis struct {
	calls []string
	order *recorderOrder
}

func (r *recordingRedis) Del(_ context.Context, keys ...string) *redis.IntCmd {
	r.calls = append(r.calls, keys...)
	if r.order != nil {
		r.order.record("redis")
	}
	return redis.NewIntCmd(context.Background())
}

// fakeLitellm records RevokeKey + KeyGenerate calls and lets the test
// inject an error. Implements just enough of litellm.Client for the
// admin handler tests (the compile-time canary is in the larger lifelong
// internal/litellm tests).
type fakeLitellm struct {
	revokeCalled atomic.Int64
	revokeErr    error
	order        *recorderOrder
	revokedKeys  []string
}

// Asserts fakeLitellm satisfies litellm.Client at compile time.
var _ litellm.Client = (*fakeLitellm)(nil)

func (f *fakeLitellm) DeleteAccessGroup(context.Context, string) error { return nil }
func (f *fakeLitellm) DeleteTag(context.Context, string) error         { return nil }
func (f *fakeLitellm) ListModels(context.Context) ([]litellm.ModelInfoResponse, error) {
	return nil, nil
}
func (f *fakeLitellm) ListMCPServers(context.Context) ([]litellm.MCPServerEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) ListA2AAgents(context.Context) ([]litellm.AgentEntry, error) {
	return nil, nil
}
func (f *fakeLitellm) ListUserKeys(context.Context, string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) RevokeKey(_ context.Context, keyID string) error {
	f.revokeCalled.Add(1)
	f.revokedKeys = append(f.revokedKeys, keyID)
	if f.order != nil {
		f.order.record("litellm")
	}
	return f.revokeErr
}
func (f *fakeLitellm) UserNew(context.Context, *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) UserInfoByEmail(context.Context, string) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLitellm) TeamMemberAdd(context.Context, string, string, string) error { return nil }
func (f *fakeLitellm) KeyGenerate(context.Context, *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

// recorderOrder accumulates step names in the order they are observed,
// enabling ordering assertions across the three side effects.
type recorderOrder struct {
	mu    atomic.Int64
	steps []string
}

func (o *recorderOrder) record(step string) {
	o.steps = append(o.steps, step)
}

// =========================== fake K8s client ===========================

// achScheme is a runtime.Scheme registered with the four force-refresh
// kinds + the standard core kinds. Used by the controller-runtime fake
// client for F-* tests.
func achScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(achv1alpha1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

// newFakeClient returns a controller-runtime fake client seeded with
// the supplied objects. Patches are observed via the returned client.
func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fakeClient.NewClientBuilder().WithScheme(achScheme(t)).WithObjects(objs...).Build()
}

// =========================== fixtures ===========================

const (
	testNs    = "ach-system"
	adminMail = "admin@example.com"
)

func adminAllowlist() map[string]struct{} { return map[string]struct{}{adminMail: {}} }

// newAdminRouter wires a chi router with the AdminOnly middleware so the
// integration-style tests exercise the full middleware → handler chain
// for the admin pk_/email-revoke endpoints. The K8s client is opt-in
// via the caller (force-refresh tests pass a fake K8s client; revoke
// tests pass nil).
func newAdminRouter(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/platform/admin", func(r chi.Router) {
		r.Use(AdminOnly(deps.Allowlist, deps.Audit, deps.Namespace))
		r.Post("/keys/revoke", RevokeKeyHandler(deps))
		r.Post("/users/{email}/revoke-keys", RevokeUserKeysHandler(deps))
		r.Post("/refresh", ForceRefreshHandler(deps))
	})
	return r
}

// adminPostJSON wraps the boilerplate of building a POST request with
// the test's KeyContext + RequestID already in ctx.
func adminPostJSON(t *testing.T, h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := middleware.WithRequestID(r.Context(), "req_test")
	info := &keystore.KeyInfo{
		KeyID:      "pkid_admincaller",
		KeyType:    keys.PrefixPk,
		OwnerEmail: adminMail,
		Status:     "active",
	}
	ctx = middleware.WithKeyContext(ctx, info, true)
	h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// =========================== force-refresh tests (Task 3) ===========================

// F-1..F-4: per-kind happy path. Verify Patch lands the annotation.
func TestForceRefresh_Plugin(t *testing.T) {
	pl := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNs},
	}
	k := newFakeClient(t, pl)
	deps := Deps{
		K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"p1"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body)
	}
	// Read-back via the fake client and assert annotation present.
	got := &achv1alpha1.Plugin{}
	if err := k.Get(context.Background(),
		client.ObjectKey{Namespace: testNs, Name: "p1"}, got); err != nil {
		t.Fatalf("read-back: %v", err)
	}
	if got.GetAnnotations()[forceRefreshAnnotation] == "" {
		t.Fatalf("annotation not set: %v", got.GetAnnotations())
	}
}

func TestForceRefresh_Prompt(t *testing.T) {
	pr := &achv1alpha1.Prompt{ObjectMeta: metav1.ObjectMeta{Name: "pr1", Namespace: testNs}}
	k := newFakeClient(t, pr)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"prompt","name":"pr1"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body)
	}
	got := &achv1alpha1.Prompt{}
	_ = k.Get(context.Background(), client.ObjectKey{Namespace: testNs, Name: "pr1"}, got)
	if got.GetAnnotations()[forceRefreshAnnotation] == "" {
		t.Fatalf("annotation not set on Prompt")
	}
}

func TestForceRefresh_Artifact(t *testing.T) {
	a := &achv1alpha1.Artifact{ObjectMeta: metav1.ObjectMeta{Name: "a1", Namespace: testNs}}
	k := newFakeClient(t, a)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"artifact","name":"a1"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

func TestForceRefresh_PluginMarketplace(t *testing.T) {
	m := &achv1alpha1.PluginMarketplace{ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: testNs}}
	k := newFakeClient(t, m)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"pluginmarketplace","name":"m1"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

// F-5: unknown kind → 400.
func TestForceRefresh_UnknownKind(t *testing.T) {
	k := newFakeClient(t)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"environment","name":"e1"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body)
	}
}

// F-6: resource not found → 404.
func TestForceRefresh_NotFound(t *testing.T) {
	k := newFakeClient(t)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"missing"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// F-7: DisallowUnknownFields rejects extras.
func TestForceRefresh_UnknownField(t *testing.T) {
	pl := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNs}}
	k := newFakeClient(t, pl)
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"p1","extra":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// F-8/F-9: missing kind / name → 400.
func TestForceRefresh_MissingKind(t *testing.T) {
	deps := Deps{K8sClient: newFakeClient(t), Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"name":"p1"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
func TestForceRefresh_MissingName(t *testing.T) {
	deps := Deps{K8sClient: newFakeClient(t), Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// F-10: audit emission on success.
func TestForceRefresh_EmitsAudit(t *testing.T) {
	pl := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNs}}
	k := newFakeClient(t, pl)
	var buf bytes.Buffer
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&buf), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"p1"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	s := buf.String()
	if !strings.Contains(s, `"action":"platform.admin.refresh"`) {
		t.Fatalf("missing action; audit=%s", s)
	}
	if !strings.Contains(s, `"target.kind":"plugin"`) {
		t.Fatalf("missing target.kind; audit=%s", s)
	}
	if !strings.Contains(s, `"target.name":"p1"`) {
		t.Fatalf("missing target.name; audit=%s", s)
	}
}

// F-11: RBAC/Patch failure → 500.
func TestForceRefresh_PatchError(t *testing.T) {
	// Inject a wrapping client that returns Forbidden on Patch.
	pl := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNs}}
	base := newFakeClient(t, pl)
	k := &erroringPatchClient{Client: base, err: apierrors.NewForbidden(
		schema.GroupResource{Group: "ach.ackstorm.ai", Resource: "plugins"}, "p1", errors.New("forbidden"))}
	var buf bytes.Buffer
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&buf), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"p1"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), `"outcome":"internal_error"`) {
		t.Fatalf("expected internal_error audit; got %s", buf.String())
	}
}

// erroringPatchClient wraps a Client and forces a configured error on
// Patch calls; Get is delegated unchanged.
type erroringPatchClient struct {
	client.Client
	err error
}

func (c *erroringPatchClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return c.err
}

// F-12: conflict retry — Phase 3 does NOT retry. Document via assertion
// that a Patch error yields 500.
func TestForceRefresh_NoConflictRetry(t *testing.T) {
	pl := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNs}}
	base := newFakeClient(t, pl)
	k := &erroringPatchClient{Client: base, err: apierrors.NewConflict(
		schema.GroupResource{Group: "ach.ackstorm.ai", Resource: "plugins"}, "p1", errors.New("conflict"))}
	deps := Deps{K8sClient: k, Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/refresh",
		[]byte(`{"kind":"plugin","name":"p1"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (no retry policy in Phase 3), got %d", rec.Code)
	}
}

// =========================== mount tests (Task 3) ===========================

// M-1: Mount registers all three routes.
func TestMount_RoutesRegistered(t *testing.T) {
	deps := Deps{
		K8sClient: newFakeClient(t), Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs,
	}
	r := chi.NewRouter()
	r.Route("/platform/admin", Mount(deps))
	// Use chi.Walk to enumerate routes.
	got := map[string]bool{}
	_ = chi.Walk(r, func(method, route string, handler http.Handler, mws ...func(http.Handler) http.Handler) error {
		if method == "POST" {
			got[route] = true
		}
		return nil
	})
	want := []string{"/platform/admin/keys/revoke", "/platform/admin/users/{email}/revoke-keys", "/platform/admin/refresh"}
	for _, p := range want {
		if !got[p] {
			t.Fatalf("missing route POST %s; got %v", p, got)
		}
	}
}

// M-2: Mount applies AdminOnly — ek_ caller is rejected with 401
// invalid_key_type.
func TestMount_AppliesAdminOnly(t *testing.T) {
	deps := Deps{
		K8sClient: newFakeClient(t), Allowlist: adminAllowlist(),
		Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs,
	}
	r := chi.NewRouter()
	r.Route("/platform/admin", Mount(deps))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/platform/admin/refresh",
		bytes.NewReader([]byte(`{"kind":"plugin","name":"p1"}`)))
	ctx := middleware.WithRequestID(req.Context(), "req_m2")
	info := &keystore.KeyInfo{KeyID: "ekid_x", KeyType: keys.PrefixEk, OwnerEmail: "ek-caller@example.com"}
	ctx = middleware.WithKeyContext(ctx, info, false)
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from AdminOnly, got %d", rec.Code)
	}
}

// =========================== RevokeKeyHandler shape tests (no DB) ===========================
// These exercise the prefix dispatcher + decode-strict path without
// hitting Postgres. The DB-touching tests live in
// handler_integration_test.go (build-tagged) and are out of scope for
// this stdlib-only file.

// RV-6: invalid prefix → 400.
func TestRevokeKey_InvalidPrefix(t *testing.T) {
	deps := Deps{Allowlist: adminAllowlist(), Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"weird_xxx"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid prefix, got %d", rec.Code)
	}
}

// RV-7: DisallowUnknownFields.
func TestRevokeKey_UnknownField(t *testing.T) {
	deps := Deps{Allowlist: adminAllowlist(), Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"pkid_x","extra":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", rec.Code)
	}
}

// RV-8: missing key_id.
func TestRevokeKey_Missing(t *testing.T) {
	deps := Deps{Allowlist: adminAllowlist(), Audit: audit.NewLogger(&bytes.Buffer{}), Namespace: testNs}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing key_id, got %d", rec.Code)
	}
}

// (URL-decode happy/edge cases for RevokeUserKeys live in the
// integration-tagged tests where a real Postgres pool is available;
// the stdlib-only file cannot exercise the post-decode list path
// because the helper requires a non-nil pool.)

// =========================== readErrorBody helper ===========================

func readErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code
}

// =========================== unused imports stub ===========================
// readErrCode + recordingRedis + fakeLitellm + recorderOrder are
// exercised by the integration-tagged tests landed alongside; suppress
// unused-warning at file scope by referencing them in a noop.
var _ = readErrCode
var _ = (*recordingRedis)(nil)
var _ = (*fakeLitellm)(nil)
var _ = (*recorderOrder)(nil)
