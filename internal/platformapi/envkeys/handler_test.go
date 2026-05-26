// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// --------------------------------------------------------------------------
// Shared test fakes — used across Tasks 1, 2, and 3.
// --------------------------------------------------------------------------

// fakeLiteLLM is a per-test stub for litellm.Client that records calls and
// returns canned values. Only the four Phase 3 method surface the envkeys
// handlers use is meaningfully implemented; the rest stub to zero values.
type fakeLiteLLM struct {
	userInfoByEmailFn func(ctx context.Context, email string) (*litellm.UserInfo, error)
	userNewFn         func(ctx context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error)
	teamMemberAddFn   func(ctx context.Context, teamID, userID, role string) error
	keyGenerateFn     func(ctx context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error)
	revokeKeyFn       func(ctx context.Context, keyID string) error

	revokeKeyCalls []string
	keyGenerateReq *litellm.KeyGenerateRequest
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
func (f *fakeLiteLLM) UserInfoByEmail(ctx context.Context, email string) (*litellm.UserInfo, error) {
	if f.userInfoByEmailFn == nil {
		return nil, litellm.ErrNotFound
	}
	return f.userInfoByEmailFn(ctx, email)
}
func (f *fakeLiteLLM) UserNew(ctx context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	if f.userNewFn == nil {
		return &litellm.UserInfo{UserID: "u-" + req.UserEmail, UserEmail: req.UserEmail}, nil
	}
	return f.userNewFn(ctx, req)
}
func (f *fakeLiteLLM) TeamMemberAdd(ctx context.Context, teamID, userID, role string) error {
	if f.teamMemberAddFn == nil {
		return nil
	}
	return f.teamMemberAddFn(ctx, teamID, userID, role)
}
func (f *fakeLiteLLM) KeyGenerate(ctx context.Context, req *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	f.keyGenerateReq = req
	if f.keyGenerateFn == nil {
		return &litellm.KeyGenerateResponse{Key: req.Key, Token: "ll-token-" + req.UserID, UserID: req.UserID}, nil
	}
	return f.keyGenerateFn(ctx, req)
}
func (f *fakeLiteLLM) RevokeKey(ctx context.Context, keyID string) error {
	f.revokeKeyCalls = append(f.revokeKeyCalls, keyID)
	if f.revokeKeyFn == nil {
		return nil
	}
	return f.revokeKeyFn(ctx, keyID)
}

// Compile-time canary catches future Client interface widening — same
// discipline as internal/litellm/noop.go.
var _ litellm.Client = (*fakeLiteLLM)(nil)

// fakeStore implements the envStore interface envkeys handlers consume.
type fakeStore struct {
	envs              map[string]*achv1alpha1.Environment
	accessGroupSynced map[string]bool
	terminating       map[string]bool
	getErr            error
	termErr           error
	syncErr           error
}

func (f *fakeStore) GetEnvironment(_ context.Context, name string) (*achv1alpha1.Environment, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if env, ok := f.envs[name]; ok {
		return env, nil
	}
	return nil, nil
}
func (f *fakeStore) EnvironmentTerminating(_ context.Context, name string) (bool, error) {
	if f.termErr != nil {
		return false, f.termErr
	}
	if v, ok := f.terminating[name]; ok {
		return v, nil
	}
	if env, ok := f.envs[name]; ok && env.DeletionTimestamp != nil {
		return true, nil
	}
	return false, nil
}
func (f *fakeStore) EnvironmentAccessGroupSynced(_ context.Context, name string) (bool, error) {
	if f.syncErr != nil {
		return false, f.syncErr
	}
	if v, ok := f.accessGroupSynced[name]; ok {
		return v, nil
	}
	return false, nil
}

// fakeDB implements the dbOps interface envkeys handlers consume.
type fakeDB struct {
	insertFn func(ctx context.Context, row db.EkInsertRow) error
	getFn    func(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	revokeFn func(ctx context.Context, keyID string) (*db.EkKeyInfo, error)
	listFn   func(ctx context.Context, ownerEmail string, limit int, cursor string) ([]db.EkKeyInfo, string, error)
	listAdm  func(ctx context.Context, owner *string, limit int, cursor string) ([]db.EkKeyInfo, string, error)

	insertCalls int
}

func (f *fakeDB) InsertEnvironmentKey(ctx context.Context, row db.EkInsertRow) error {
	f.insertCalls++
	if f.insertFn == nil {
		return nil
	}
	return f.insertFn(ctx, row)
}
func (f *fakeDB) GetEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
	if f.getFn == nil {
		return nil, nil
	}
	return f.getFn(ctx, keyID)
}
func (f *fakeDB) RevokeEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
	if f.revokeFn == nil {
		return nil, nil
	}
	return f.revokeFn(ctx, keyID)
}
func (f *fakeDB) ListEnvironmentKeysByOwner(ctx context.Context, ownerEmail string, limit int, cursor string) ([]db.EkKeyInfo, string, error) {
	if f.listFn == nil {
		return nil, "", nil
	}
	return f.listFn(ctx, ownerEmail, limit, cursor)
}
func (f *fakeDB) ListEnvironmentKeysByOwnerWithFilter(ctx context.Context, ownerEmailFilter *string, limit int, cursor string) ([]db.EkKeyInfo, string, error) {
	if f.listAdm == nil {
		return nil, "", nil
	}
	return f.listAdm(ctx, ownerEmailFilter, limit, cursor)
}

// fakeRedis implements the redisOps interface envkeys handlers consume.
type fakeRedis struct {
	delCalls []string
	delErr   error
}

func (f *fakeRedis) Del(_ context.Context, key string) error {
	f.delCalls = append(f.delCalls, key)
	return f.delErr
}

// envBuilder builds a minimal Environment CR with the fields tests need.
func envBuilder(name string, authorizedTeams []string) *achv1alpha1.Environment {
	return &achv1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ach-system"},
		Spec: achv1alpha1.EnvironmentSpec{
			AuthorizedTeams: authorizedTeams,
		},
	}
}

// newDepsWith assembles a Deps for tests, wiring the fakes + a non-empty
// pepper and an audit logger writing to the returned buffer.
func newDepsWith(t *testing.T, ll *fakeLiteLLM, dbf *fakeDB, st *fakeStore, rds *fakeRedis) (Deps, *bytes.Buffer) {
	t.Helper()
	var auditBuf bytes.Buffer
	deps := Deps{
		LiteLLM:   ll,
		DB:        dbf,
		Store:     st,
		Redis:     rds,
		Pepper:    []byte("test-pepper-not-secret"),
		Audit:     audit.NewLogger(&auditBuf),
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Namespace: "ach-system",
	}
	return deps, &auditBuf
}

// callWithAuthnCtx runs an HTTP request through a handler with a
// pre-populated KeyContext + request-id seeded directly into ctx
// (bypassing the real Authn middleware so tests stay self-contained).
func callWithAuthnCtx(t *testing.T, h http.Handler, method, path string, body io.Reader, kc middleware.KeyContext) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	info := &keystore.KeyInfo{
		KeyID:      kc.KeyID,
		KeyType:    kc.KeyType,
		OwnerEmail: kc.OwnerEmail,
		Status:     "active",
	}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, kc.IsAdmin)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// makePkKeyCtx builds a pk_ KeyContext for happy-path tests.
func makePkKeyCtx(email string, admin bool) middleware.KeyContext {
	return middleware.KeyContext{
		KeyID:      "pkid_01h7azv3jryr2gh3kdpr3y0rkw",
		KeyType:    keys.PrefixPk,
		OwnerEmail: email,
		IsAdmin:    admin,
	}
}

// makeEkKeyCtx builds an ek_ KeyContext to verify caller-type guards.
func makeEkKeyCtx(email string) middleware.KeyContext {
	return middleware.KeyContext{
		KeyID:      "ekid_01h7azv3jryr2gh3kdpr3y0rkw",
		KeyType:    keys.PrefixEk,
		OwnerEmail: email,
	}
}

// errorEnvelopeCode extracts {"error":{"code":"…"}} from a JSON body.
func errorEnvelopeCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Err struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v body=%s", err, string(body))
	}
	return env.Err.Code
}

// auditOutcomes parses the audit buffer and returns the outcome attribute
// values one per line.
func auditOutcomes(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if v, ok := rec["outcome"].(string); ok {
			out = append(out, v)
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pgUniqueViolation returns a pgconn.PgError with code 23505 (unique
// violation) and the given constraint name, mimicking what pgx surfaces
// from the production INSERT.
func pgUniqueViolation(constraint string) error {
	return &pgconn.PgError{
		Code:           "23505",
		ConstraintName: constraint,
		Message:        "duplicate key value violates unique constraint",
	}
}

// --------------------------------------------------------------------------
// Task 1: CreateHandler tests (12 cases per the PLAN behavior block).
// --------------------------------------------------------------------------

func TestCreateHandlerHappyPath(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"my-key"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp CreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !strings.HasPrefix(resp.KeyID, keys.EkidKeyIDPrefix) {
		t.Errorf("expected ekid_ prefix on key_id, got %q", resp.KeyID)
	}
	if !strings.HasPrefix(resp.Plaintext, keys.EkBearerPrefix) {
		t.Errorf("expected ek_ prefix on plaintext, got %q", resp.Plaintext)
	}
	if resp.Environment != "prod" {
		t.Errorf("environment: got %q, want prod", resp.Environment)
	}
	if resp.Name != "my-key" {
		t.Errorf("name: got %q, want my-key", resp.Name)
	}
	if resp.OwnerEmail != "alice@example.com" {
		t.Errorf("owner_email: got %q, want alice@example.com", resp.OwnerEmail)
	}
	if resp.CreatedAt == "" {
		t.Errorf("created_at: empty")
	}
	if dbf.insertCalls != 1 {
		t.Errorf("expected exactly 1 InsertEnvironmentKey call, got %d", dbf.insertCalls)
	}
	if ll.keyGenerateReq == nil {
		t.Fatalf("expected KeyGenerate to be called")
	}
	if ll.keyGenerateReq.MaxBudget != nil {
		t.Errorf("KEY-10 violation: MaxBudget must be nil, got %v", *ll.keyGenerateReq.MaxBudget)
	}
	wantAg := []string{"prod"}
	if !equalStrings(ll.keyGenerateReq.AccessGroups, wantAg) {
		t.Errorf("AccessGroups: got %v, want %v", ll.keyGenerateReq.AccessGroups, wantAg)
	}
	outs := auditOutcomes(t, auditBuf)
	if !contains(outs, audit.OutcomeCreated) {
		t.Errorf("expected audit outcome=created, got %v", outs)
	}
}

func TestCreateHandlerEkCallerRejected(t *testing.T) {
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{}
	st := &fakeStore{}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makeEkKeyCtx("alice@example.com"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeInvalidKeyType {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeInvalidKeyType)
	}
	if dbf.insertCalls != 0 {
		t.Errorf("expected 0 DB inserts on ek_ rejection, got %d", dbf.insertCalls)
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("expected 0 LiteLLM calls on ek_ rejection, got %d", len(ll.revokeKeyCalls))
	}
}

func TestCreateHandlerEnvironmentNotFound(t *testing.T) {
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{}
	st := &fakeStore{} // no envs map entry for "missing"
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"missing","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeEnvironmentNotFound {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeEnvironmentNotFound)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeEnvironmentNotFound) {
		t.Errorf("expected audit outcome=environment_not_found")
	}
}

func TestCreateHandlerEnvironmentTerminating(t *testing.T) {
	now := metav1.Now()
	env := envBuilder("draining", []string{"team-a"})
	env.DeletionTimestamp = &now
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{}
	st := &fakeStore{envs: map[string]*achv1alpha1.Environment{"draining": env}}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"draining","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (terminating treated as not-found per D-12)", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeEnvironmentNotFound {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeEnvironmentNotFound)
	}
}

func TestCreateHandlerAccessGroupNotSynced(t *testing.T) {
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": false},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeNotReady {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeNotReady)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeNotReady) {
		t.Errorf("expected audit outcome=not_ready")
	}
}

func TestCreateHandlerUnauthorizedTeam(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-y"}}, nil
		},
	}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-x"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeUnauthorizedTeam {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeUnauthorizedTeam)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeUnauthorizedTeam) {
		t.Errorf("expected audit outcome=unauthorized_team")
	}
}

// TestCreateHandlerVerifyOrCreateIdempotentFallback exercises the §8.2
// step-5 verify-or-create LiteLLM user provision idempotent path. The
// realistic scenario for env-keys callers is that the user ALREADY
// exists in LiteLLM (SSO onboarding via Plan 03-07's callback handler
// created them). The defense-in-depth verify-or-create can still hit
// the UserNew branch if (a) the verify-or-create stage races with a
// LiteLLM-side user purge or (b) the test setup deliberately models
// the absent-then-present transition.
//
// This test models the realistic flow: LookupCallerTeams (step 4)
// surfaces the user's existing teams; the verify-or-create call in
// step 6 sees ErrNotFound on the SECOND UserInfoByEmail call (a
// LiteLLM-side flake) and the handler runs the UserNew + TeamMemberAdd
// fallback. The team intersection passes because LookupCallerTeams
// already returned ["team-a"].
func TestCreateHandlerVerifyOrCreateIdempotentFallback(t *testing.T) {
	userNewCalled := false
	teamAddCalled := false
	calls := 0
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			calls++
			if calls == 1 {
				// LookupCallerTeams's call — user exists with team-a.
				return &litellm.UserInfo{UserID: "u-existing", UserEmail: email, Teams: []string{"team-a"}}, nil
			}
			// Step-6 idempotent verify race — return ErrNotFound to drive
			// the UserNew branch.
			return nil, litellm.ErrNotFound
		},
		userNewFn: func(_ context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
			userNewCalled = true
			return &litellm.UserInfo{UserID: "u-new", UserEmail: req.UserEmail, Teams: []string{"team-a"}}, nil
		},
		teamMemberAddFn: func(_ context.Context, teamID, _, _ string) error {
			if teamID != "default" {
				t.Errorf("TeamMemberAdd teamID: got %q, want default", teamID)
			}
			teamAddCalled = true
			return nil
		},
	}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !userNewCalled {
		t.Errorf("expected UserNew to be called when verify-or-create sees ErrNotFound")
	}
	if !teamAddCalled {
		t.Errorf("expected TeamMemberAdd(default,...) to be called after UserNew")
	}
}

func TestCreateHandlerKeyGenerateFailure(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
		keyGenerateFn: func(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
			return nil, errors.New("transport: connection reset")
		},
	}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeLitellmUnreachable {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeLitellmUnreachable)
	}
	if dbf.insertCalls != 0 {
		t.Errorf("InsertEnvironmentKey must NOT be called when KeyGenerate fails")
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeLitellmUnreachable) {
		t.Errorf("expected audit outcome=litellm_unreachable")
	}
}

func TestCreateHandlerDBInsertFailureWithLiteLLMCompensation(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	dbf := &fakeDB{
		insertFn: func(_ context.Context, _ db.EkInsertRow) error {
			return fmt.Errorf("db: InsertEnvironmentKey: connection_lost")
		},
	}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeDbInsertFailed {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeDbInsertFailed)
	}
	if len(ll.revokeKeyCalls) != 1 {
		t.Errorf("expected exactly 1 LiteLLM compensation RevokeKey call, got %d", len(ll.revokeKeyCalls))
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeDbInsertFailed) {
		t.Errorf("expected audit outcome=db_insert_failed")
	}
}

// Test 10a (WARN-03): credential_hash UNIQUE collision → 500 + compensation, no retry.
func TestCreateHandlerCredentialHashCollisionNoRetry(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	dbf := &fakeDB{
		insertFn: func(_ context.Context, _ db.EkInsertRow) error {
			return pgUniqueViolation("environment_keys_credential_hash_key")
		},
	}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeDbInsertFailed {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeDbInsertFailed)
	}
	if dbf.insertCalls != 1 {
		t.Errorf("expected exactly 1 InsertEnvironmentKey call (no retry on credential_hash collision), got %d", dbf.insertCalls)
	}
	if len(ll.revokeKeyCalls) != 1 {
		t.Errorf("expected exactly 1 LiteLLM compensation RevokeKey call, got %d", len(ll.revokeKeyCalls))
	}
}

// Test 10b (WARN-03): ekid_ PK collision → retry once with new ulid → succeeds, NO compensation.
func TestCreateHandlerEkidCollisionRetrySucceeds(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	var seenKeyIDs []string
	calls := 0
	dbf := &fakeDB{
		insertFn: func(_ context.Context, row db.EkInsertRow) error {
			calls++
			seenKeyIDs = append(seenKeyIDs, row.KeyID)
			if calls == 1 {
				return pgUniqueViolation("environment_keys_pkey")
			}
			return nil
		},
	}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (retry should succeed); body=%s", rec.Code, rec.Body.String())
	}
	if dbf.insertCalls != 2 {
		t.Errorf("expected exactly 2 InsertEnvironmentKey calls (retry once on ekid_ collision), got %d", dbf.insertCalls)
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("expected ZERO LiteLLM RevokeKey calls (LiteLLM key reused on retry), got %d (%v)", len(ll.revokeKeyCalls), ll.revokeKeyCalls)
	}
	if len(seenKeyIDs) != 2 || seenKeyIDs[0] == seenKeyIDs[1] {
		t.Errorf("expected two DISTINCT key_ids across the two inserts, got %v", seenKeyIDs)
	}
}

// Test 10c (WARN-03): ekid_ PK collision on the RETRY → 500 + LiteLLM revoked once.
func TestCreateHandlerEkidCollisionRetryFails(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	dbf := &fakeDB{
		insertFn: func(_ context.Context, _ db.EkInsertRow) error {
			return pgUniqueViolation("environment_keys_pkey")
		},
	}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if dbf.insertCalls != 2 {
		t.Errorf("expected exactly 2 InsertEnvironmentKey calls, got %d", dbf.insertCalls)
	}
	if len(ll.revokeKeyCalls) != 1 {
		t.Errorf("expected exactly 1 LiteLLM compensation RevokeKey call after both retries failed, got %d", len(ll.revokeKeyCalls))
	}
}

func TestCreateHandlerPlaintextExactlyOnce(t *testing.T) {
	ll := &fakeLiteLLM{
		userInfoByEmailFn: func(_ context.Context, email string) (*litellm.UserInfo, error) {
			return &litellm.UserInfo{UserID: "u-1", UserEmail: email, Teams: []string{"team-a"}}, nil
		},
	}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, auditBuf := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var resp CreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	count := strings.Count(rec.Body.String(), resp.Plaintext)
	if count != 1 {
		t.Errorf("plaintext appears %d times in response body, want exactly 1", count)
	}
	// Plaintext MUST NOT appear in any response header.
	for name, vals := range rec.Header() {
		for _, v := range vals {
			if strings.Contains(v, resp.Plaintext) {
				t.Errorf("plaintext leaked into response header %q=%q", name, v)
			}
		}
	}
	// Plaintext MUST NOT appear in audit records.
	if strings.Contains(auditBuf.String(), resp.Plaintext) {
		t.Errorf("plaintext leaked into audit buffer")
	}
}

func TestCreateHandlerDisallowUnknownFields(t *testing.T) {
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{}
	st := &fakeStore{
		envs:              map[string]*achv1alpha1.Environment{"prod": envBuilder("prod", []string{"team-a"})},
		accessGroupSynced: map[string]bool{"prod": true},
	}
	deps, _ := newDepsWith(t, ll, dbf, st, &fakeRedis{})
	h := CreateHandler(deps)
	body := strings.NewReader(`{"environment":"prod","name":"k1","extra_field":"x"}`)
	rec := callWithAuthnCtx(t, h, "POST", "/", body, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != "invalid_argument" {
		t.Errorf("envelope code: got %q, want invalid_argument", code)
	}
	if dbf.insertCalls != 0 {
		t.Errorf("expected ZERO DB work on invalid_argument, got %d", dbf.insertCalls)
	}
	if len(ll.revokeKeyCalls) != 0 || ll.keyGenerateReq != nil {
		t.Errorf("expected ZERO LiteLLM work on invalid_argument")
	}
}

// --------------------------------------------------------------------------
// Task 2: ListHandler tests (7 cases) + GetHandler tests (6 cases).
// --------------------------------------------------------------------------

func TestListHandlerNonAdminScopedToCaller(t *testing.T) {
	var seenOwner string
	dbf := &fakeDB{
		listFn: func(_ context.Context, ownerEmail string, _ int, _ string) ([]db.EkKeyInfo, string, error) {
			seenOwner = ownerEmail
			return []db.EkKeyInfo{{KeyID: "ekid_1"}, {KeyID: "ekid_2"}, {KeyID: "ekid_3"}}, "", nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if seenOwner != "alice@example.com" {
		t.Errorf("expected ListEnvironmentKeysByOwner called with alice@example.com, got %q", seenOwner)
	}
}

func TestListHandlerNonAdminRejectsOwnerEmailFilter(t *testing.T) {
	dbf := &fakeDB{}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/?owner_email=bob@example.com", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != "invalid_argument" {
		t.Errorf("envelope code: got %q, want invalid_argument", code)
	}
}

func TestListHandlerAdminSeesAll(t *testing.T) {
	var seenFilter *string
	dbf := &fakeDB{
		listAdm: func(_ context.Context, f *string, _ int, _ string) ([]db.EkKeyInfo, string, error) {
			seenFilter = f
			return []db.EkKeyInfo{{KeyID: "ekid_a"}, {KeyID: "ekid_b"}}, "", nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/", nil, makePkKeyCtx("admin@example.com", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if seenFilter != nil {
		t.Errorf("expected nil owner filter for admin no-filter list, got %v", *seenFilter)
	}
}

func TestListHandlerAdminWithOwnerEmailFilter(t *testing.T) {
	var seenFilter *string
	dbf := &fakeDB{
		listAdm: func(_ context.Context, f *string, _ int, _ string) ([]db.EkKeyInfo, string, error) {
			seenFilter = f
			return []db.EkKeyInfo{{KeyID: "ekid_b"}}, "", nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/?owner_email=bob@example.com", nil, makePkKeyCtx("admin@example.com", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if seenFilter == nil || *seenFilter != "bob@example.com" {
		t.Errorf("expected owner_email filter %q, got %v", "bob@example.com", seenFilter)
	}
}

func TestListHandlerEkCallerRejected(t *testing.T) {
	dbf := &fakeDB{}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/", nil, makeEkKeyCtx("workload@example.com"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeInvalidKeyType {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeInvalidKeyType)
	}
}

func TestListHandlerPagination(t *testing.T) {
	var seenLimit int
	var seenCursor string
	dbf := &fakeDB{
		listFn: func(_ context.Context, _ string, limit int, cursor string) ([]db.EkKeyInfo, string, error) {
			seenLimit = limit
			seenCursor = cursor
			return []db.EkKeyInfo{{KeyID: "ekid_1"}}, "next-cursor-opaque", nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/?limit=3&cursor=abc", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if seenLimit != 3 {
		t.Errorf("limit: got %d, want 3", seenLimit)
	}
	if seenCursor != "abc" {
		t.Errorf("cursor: got %q, want abc", seenCursor)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["next_cursor"] != "next-cursor-opaque" {
		t.Errorf("next_cursor: got %v, want next-cursor-opaque", resp["next_cursor"])
	}
}

func TestListHandlerLimitTooLarge(t *testing.T) {
	dbf := &fakeDB{}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	h := ListHandler(deps)
	rec := callWithAuthnCtx(t, h, "GET", "/?limit=600", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != "invalid_argument" {
		t.Errorf("envelope code: got %q, want invalid_argument", code)
	}
}

// --- GetHandler ---

func TestGetHandlerHappyPathOwner(t *testing.T) {
	dbf := &fakeDB{
		getFn: func(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID:       keyID,
				Environment: "prod",
				OwnerEmail:  "alice@example.com",
				Name:        "k1",
				Status:      "active",
			}, nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetHandlerPrefixMismatch(t *testing.T) {
	dbCalls := 0
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			dbCalls++
			return nil, nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/pkid_aaaaaaaaaaaaaaaaaaaaaaaaaa", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if dbCalls != 0 {
		t.Errorf("DB called %d times despite invalid prefix; must be 0", dbCalls)
	}
}

func TestGetHandlerPlaintextPrefixRejected(t *testing.T) {
	dbCalls := 0
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			dbCalls++
			return nil, nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/ek_xyzaaaaaaaaaaaaaaaaaaaaaaaa", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if dbCalls != 0 {
		t.Errorf("DB called %d times for ek_ (plaintext) input; must be 0", dbCalls)
	}
}

func TestGetHandlerNotKeyOwner(t *testing.T) {
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID:       "ekid_xx",
				Environment: "prod",
				OwnerEmail:  "bob@example.com", // different owner
				Status:      "active",
			}, nil
		},
	}
	deps, auditBuf := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeNotKeyOwner {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeNotKeyOwner)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeNotKeyOwner) {
		t.Errorf("expected audit outcome=not_key_owner")
	}
}

func TestGetHandlerAdminCrossesOwner(t *testing.T) {
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "bob@example.com", Status: "active",
			}, nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil, makePkKeyCtx("admin@example.com", true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (admin reads any row); body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetHandlerUnknownKeyID(t *testing.T) {
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return nil, nil
		},
	}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Get("/{key_id}", GetHandler(deps))
	rec := callWithAuthnCtx(t, r, "GET", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil, makePkKeyCtx("alice@example.com", false))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
}

// --------------------------------------------------------------------------
// Task 3: RevokeHandler tests (8 cases) + Mount registration check.
// --------------------------------------------------------------------------

func TestRevokeHandlerHappyPath(t *testing.T) {
	token := "ll-token-xyz"
	credHash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	dbRevokeCalled := false
	ll := &fakeLiteLLM{}
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "alice@example.com",
				LiteLLMToken: &token, CredentialHash: credHash, Status: "active",
			}, nil
		},
		revokeFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			dbRevokeCalled = true
			return &db.EkKeyInfo{KeyID: "ekid_xx", Status: "revoked"}, nil
		},
	}
	rds := &fakeRedis{}
	deps, auditBuf := newDepsWith(t, ll, dbf, &fakeStore{}, rds)
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !dbRevokeCalled {
		t.Errorf("expected db.RevokeEnvironmentKey to be called")
	}
	if len(ll.revokeKeyCalls) != 1 || ll.revokeKeyCalls[0] != token {
		t.Errorf("expected LiteLLM RevokeKey(%q) once, got %v", token, ll.revokeKeyCalls)
	}
	wantKey := "ach:key:" + credHash
	if len(rds.delCalls) != 1 || rds.delCalls[0] != wantKey {
		t.Errorf("Redis DEL: got %v, want [%q]", rds.delCalls, wantKey)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeRevoked) {
		t.Errorf("expected audit outcome=revoked")
	}
}

func TestRevokeHandlerPrefixMismatch(t *testing.T) {
	dbCalls := 0
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			dbCalls++
			return nil, nil
		},
	}
	ll := &fakeLiteLLM{}
	deps, _ := newDepsWith(t, ll, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/pkid_aaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if dbCalls != 0 {
		t.Errorf("DB called %d times despite invalid prefix; must be 0", dbCalls)
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("LiteLLM called despite invalid prefix; must be 0")
	}
}

func TestRevokeHandlerUnknownKey(t *testing.T) {
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) { return nil, nil },
	}
	ll := &fakeLiteLLM{}
	deps, _ := newDepsWith(t, ll, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("LiteLLM called for unknown key; must be 0")
	}
}

func TestRevokeHandlerNotKeyOwner(t *testing.T) {
	token := "ll-token-zz"
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "bob@example.com",
				LiteLLMToken: &token, Status: "active",
			}, nil
		},
	}
	ll := &fakeLiteLLM{}
	deps, auditBuf := newDepsWith(t, ll, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeNotKeyOwner {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeNotKeyOwner)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeNotKeyOwner) {
		t.Errorf("expected audit outcome=not_key_owner")
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("LiteLLM called for non-owner; must be 0")
	}
}

func TestRevokeHandlerLiteLLMUnreachable(t *testing.T) {
	token := "ll-token-down"
	credHash := "abcd"
	revokeDBCalled := false
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "alice@example.com",
				LiteLLMToken: &token, CredentialHash: credHash, Status: "active",
			}, nil
		},
		revokeFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			revokeDBCalled = true
			return &db.EkKeyInfo{Status: "revoked"}, nil
		},
	}
	ll := &fakeLiteLLM{
		revokeKeyFn: func(_ context.Context, _ string) error {
			return errors.New("network: connection refused")
		},
	}
	rds := &fakeRedis{}
	deps, auditBuf := newDepsWith(t, ll, dbf, &fakeStore{}, rds)
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeLitellmUnreachable {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeLitellmUnreachable)
	}
	if revokeDBCalled {
		t.Errorf("KEY-08 violation: db.RevokeEnvironmentKey must NOT run before LiteLLM ack")
	}
	if len(rds.delCalls) != 0 {
		t.Errorf("KEY-08 violation: Redis.DEL must NOT run before LiteLLM ack, got %v", rds.delCalls)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeLitellmUnreachable) {
		t.Errorf("expected audit outcome=litellm_unreachable")
	}
}

func TestRevokeHandlerDBFlipFailureAfterLiteLLMAck(t *testing.T) {
	token := "ll-token-xx"
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "alice@example.com",
				LiteLLMToken: &token, CredentialHash: "abcd", Status: "active",
			}, nil
		},
		revokeFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return nil, errors.New("db: connection lost")
		},
	}
	ll := &fakeLiteLLM{}
	rds := &fakeRedis{}
	deps, auditBuf := newDepsWith(t, ll, dbf, &fakeStore{}, rds)
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if code := errorEnvelopeCode(t, rec.Body.Bytes()); code != audit.OutcomeInternalError {
		t.Errorf("envelope code: got %q, want %q", code, audit.OutcomeInternalError)
	}
	if len(ll.revokeKeyCalls) != 1 {
		t.Errorf("expected LiteLLM RevokeKey called once before DB flip; got %d", len(ll.revokeKeyCalls))
	}
	if len(rds.delCalls) != 0 {
		t.Errorf("Redis.DEL must NOT run on DB flip failure, got %v", rds.delCalls)
	}
	if !contains(auditOutcomes(t, auditBuf), audit.OutcomeInternalError) {
		t.Errorf("expected audit outcome=internal_error")
	}
}

func TestRevokeHandlerAdminCrossesOwner(t *testing.T) {
	token := "ll-token-aa"
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "bob@example.com",
				LiteLLMToken: &token, CredentialHash: "abcd", Status: "active",
			}, nil
		},
		revokeFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{KeyID: "ekid_xx", Status: "revoked"}, nil
		},
	}
	ll := &fakeLiteLLM{}
	deps, _ := newDepsWith(t, ll, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "admin@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, true) // admin
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRevokeHandlerAlreadyRevoked(t *testing.T) {
	token := "ll-token-r"
	dbf := &fakeDB{
		getFn: func(_ context.Context, _ string) (*db.EkKeyInfo, error) {
			return &db.EkKeyInfo{
				KeyID: "ekid_xx", Environment: "prod", OwnerEmail: "alice@example.com",
				LiteLLMToken: &token, Status: "revoked",
			}, nil
		},
	}
	ll := &fakeLiteLLM{}
	deps, _ := newDepsWith(t, ll, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest("DELETE", "/ekid_01h7azv3jryr2gh3kdpr3y0rkw", nil)
	info := &keystore.KeyInfo{KeyType: keys.PrefixPk, OwnerEmail: "alice@example.com"}
	ctx := middleware.WithRequestID(req.Context(), "req_test")
	ctx = middleware.WithKeyContext(ctx, info, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (already-revoked treated as not-found)", rec.Code)
	}
	if len(ll.revokeKeyCalls) != 0 {
		t.Errorf("LiteLLM called on already-revoked key; must be 0")
	}
}

func TestMountRegistersFourRoutes(t *testing.T) {
	dbf := &fakeDB{}
	deps, _ := newDepsWith(t, &fakeLiteLLM{}, dbf, &fakeStore{}, &fakeRedis{})
	r := chi.NewRouter()
	r.Route("/platform/env-keys", Mount(deps))

	wantRoutes := map[string]bool{
		"POST /platform/env-keys/":           false,
		"GET /platform/env-keys/":            false,
		"GET /platform/env-keys/{key_id}":    false,
		"DELETE /platform/env-keys/{key_id}": false,
	}

	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if _, ok := wantRoutes[key]; ok {
			wantRoutes[key] = true
		}
		return nil
	})
	for want, seen := range wantRoutes {
		if !seen {
			t.Errorf("Mount missing route: %q", want)
		}
	}
}

// ListTeamsByAlias is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, _ string) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// EnsureDefaultTeam is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) EnsureDefaultTeam(_ context.Context) error { return nil }

// §7 stubs — interface satisfaction only.
func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ string, _ []string) error { return nil }
func (f *fakeLiteLLM) BindTeamToAccessGroup(_ context.Context, _, _ string) error      { return nil }
func (f *fakeLiteLLM) ListAccessGroupBindings(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
