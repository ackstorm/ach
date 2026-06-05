// SPDX-License-Identifier: Apache-2.0

// Unit tests for internal/orphan/runnable.go.
//
// DB calls are stubbed by overriding the Runnable.ListUsers and
// Runnable.ListKeyIDs function-typed fields — no real Postgres
// dependency. LiteLLM is stubbed by the local fakeLiteLLM (same
// shape as snapshot/snapshot_test.go's fake; one *RevokeKey* counter
// added). The audit logger is wired to a bytes.Buffer so tests can
// assert the exact emitted JSON shape per D-18.

package orphan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
)

// fakeLiteLLM is a minimal litellm.Client implementation. Fields are
// read on every call; tests mutate them between calls (no other
// goroutine reads them) to drive each branch.
type fakeLiteLLM struct {
	mu sync.Mutex
	// userKeysByUser is the per-user keylist returned from ListUserKeys.
	userKeysByUser map[string][]litellm.UserKeyInfo
	// listErr (if non-nil) is returned from EVERY ListUserKeys call,
	// overriding userKeysByUser.
	listErr error
	// revokeErr (if non-nil) is returned from EVERY RevokeKey call.
	revokeErr error
	// listCalls counts ListUserKeys invocations across users.
	listCalls atomic.Int64
	// revokedKeys is appended-to on every RevokeKey call (the keyID arg).
	revokedKeys []string
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

func (f *fakeLiteLLM) ListUserKeys(_ context.Context, userID string) ([]litellm.UserKeyInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls.Add(1)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.userKeysByUser[userID], nil
}

func (f *fakeLiteLLM) RevokeKey(_ context.Context, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokedKeys = append(f.revokedKeys, keyID)
	if f.revokeErr != nil {
		return f.revokeErr
	}
	return nil
}

// Phase 3 Plan 03-01 — interface widened. The orphan runnable does not
// invoke these methods; stub them to satisfy the litellm.Client interface.
func (f *fakeLiteLLM) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}

func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	return nil, nil
}

func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeLiteLLM) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

// newTestRunnable returns a Runnable with the LiteLLM client wired
// (default test seam returns no users / no keys) plus the audit
// buffer the caller asserts against. The DB pool is nil — TickOnce
// only forwards it to the function-typed test seams, which ignore it.
func newTestRunnable(t *testing.T, fake *fakeLiteLLM) (*Runnable, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	r := &Runnable{
		Client:   fake,
		DB:       (*pgxpool.Pool)(nil),
		Audit:    audit.NewLogger(buf),
		Interval: 10 * time.Minute,
		Log:      logr.Discard(),
		// Fresh registry per test: counters start at 0 and there is no
		// global double-register panic across the suite.
		Metrics:    NewMetrics(prometheus.NewRegistry()),
		ListUsers:  func(_ context.Context, _ *pgxpool.Pool) ([]string, error) { return nil, nil },
		ListKeyIDs: func(_ context.Context, _ *pgxpool.Pool) ([]string, error) { return nil, nil },
	}
	return r, buf
}

// auditLines splits the audit buffer into one JSON object per non-empty line.
func auditLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("audit line not JSON: %q (err=%v)", line, err)
		}
		out = append(out, m)
	}
	return out
}

// assertNoForbiddenAttrs is the CR-03 / IN-06 enforcement: every audit
// event emitted by the orphan-cleanup Runnable — success OR failure path
// — MUST NOT carry any plaintext / body / alias / error-text attribute.
// audit/doc.go forbids these per the no-scrubbing contract.
//
// Apply to every line you assert against, regardless of which outcome
// branch produced it.
func assertNoForbiddenAttrs(t *testing.T, line map[string]any) {
	t.Helper()
	for _, forbidden := range []string{"key_alias", "credential_hash", "bearer", "body", "header", "err"} {
		if v, present := line[forbidden]; present {
			t.Errorf("forbidden key %q present in audit event; value=%v", forbidden, v)
		}
	}
}

// TestRunnable_TickOnce_EmptyUsers asserts that an empty ACH-managed user
// set is a no-op: no LiteLLM calls, no audit events. This is the Phase 2
// steady-state per the plan's `<objective>`.
func TestRunnable_TickOnce_EmptyUsers(t *testing.T) {
	fake := &fakeLiteLLM{}
	r, buf := newTestRunnable(t, fake)

	r.TickOnce(context.Background())

	if got := fake.listCalls.Load(); got != 0 {
		t.Errorf("ListUserKeys called %d times on empty user set; want 0", got)
	}
	if got := buf.Len(); got != 0 {
		t.Errorf("audit buffer non-empty on empty user set: %d bytes (%q)", got, buf.String())
	}
}

// TestRunnable_TickOnce_OneOrphan asserts the ACH-orphan path: one user,
// one ACH-minted key (ach_key_id present) that is >10min old and whose
// ach_key_id is NOT in the active ACH set → exactly one RevokeKey call
// (by the opaque Token) + one audit event with outcome=revoked.
func TestRunnable_TickOnce_OneOrphan(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {{
				Token:     "sk-orphan",
				UserID:    "u1",
				CreatedAt: time.Now().Add(-20 * time.Minute),
				KeyAlias:  "should-not-leak-into-audit",
				Metadata:  map[string]any{"ach_key_id": "pkid-orphan"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	// Non-empty active set that does NOT contain pkid-orphan: ACH has
	// other live keys, so the B1 empty-set fail-safe does not trip and
	// the untracked orphan is revoked normally.
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-someone-else"}, nil
	}

	r.TickOnce(context.Background())

	if got := fake.revokedKeys; len(got) != 1 || got[0] != "sk-orphan" {
		t.Fatalf("revokedKeys = %v; want [sk-orphan]", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["msg"]; got != "operator.orphan-cleanup" {
		t.Errorf("msg = %v; want operator.orphan-cleanup", got)
	}
	if got := lines[0]["target.name"]; got != "sk-orphan" {
		t.Errorf("target.name = %v; want sk-orphan", got)
	}
	if got := lines[0]["outcome"]; got != OutcomeRevoked {
		t.Errorf("outcome = %v; want %q", got, OutcomeRevoked)
	}
	if _, present := lines[0]["key_alias"]; present {
		t.Errorf("key_alias leaked into audit event (operational log only): %v", lines[0]["key_alias"])
	}
	if got := testutil.ToFloat64(r.Metrics.Candidates); got != 1 {
		t.Errorf("candidates_total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(r.Metrics.Revoked); got != 1 {
		t.Errorf("revoked_total = %v; want 1", got)
	}
}

// TestRunnable_TickOnce_SkipTooNew: an ACH-owned (ach_key_id present),
// untracked key that is <10min old must be skipped (no revoke, no audit
// event) — the OrphanAgeFloor race defender wins even over an ACH-owned
// orphan. This is the load-bearing race-defender test (Hub §18.4).
func TestRunnable_TickOnce_SkipTooNew(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {{
				Token:     "sk-too-new",
				UserID:    "u1",
				CreatedAt: time.Now().Add(-5 * time.Minute),
				Metadata:  map[string]any{"ach_key_id": "pkid-too-new"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times for too-new key; want 0", got)
	}
	if got := buf.Len(); got != 0 {
		t.Errorf("audit buffer non-empty for too-new key: %q", buf.String())
	}
}

// TestRunnable_TickOnce_SkipNonOrphan: an ACH-owned key whose ach_key_id
// IS in the active ACH set must be skipped — it is not orphan, ACH owns
// it AND still tracks it. The membership join is metadata.ach_key_id ↔
// the active key_id set (NOT the opaque Token).
func TestRunnable_TickOnce_SkipNonOrphan(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {{
				Token:     "sk-active",
				UserID:    "u1",
				CreatedAt: time.Now().Add(-20 * time.Minute),
				Metadata:  map[string]any{"ach_key_id": "pkid-active"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-active"}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times on non-orphan key; want 0", got)
	}
	if got := buf.Len(); got != 0 {
		t.Errorf("audit buffer non-empty for non-orphan key: %q", buf.String())
	}
}

// TestRunnable_TickOnce_ForeignKeyLeftAlone is THE ownership-gate test:
// a key with NO ach_key_id (foreign — manual dashboard / tf-* / token-
// factory), older than the floor, with an empty active set, must be left
// untouched. Before the gate, an empty active set made every such key an
// "orphan" and revoked it (the production incident). Covers both
// Metadata==nil and Metadata present-but-missing-ach_key_id.
func TestRunnable_TickOnce_ForeignKeyLeftAlone(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {
				// Metadata entirely absent.
				{Token: "sk-foreign-nil", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute)},
				// Metadata present (rich, token-factory style) but no ach_key_id.
				{Token: "sk-foreign-richmeta", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"source": "token-factory", "email": "x@ackstorm.com"}},
			},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	// Active set empty — the exact condition that previously triggered a
	// fleet-wide revoke. Under the gate, foreign keys are still spared.
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times on foreign keys; want 0 (ach_key_id gate): %v", got, fake.revokedKeys)
	}
	if got := buf.Len(); got != 0 {
		t.Errorf("audit buffer non-empty for foreign keys: %q", buf.String())
	}
}

// TestRunnable_TickOnce_MixedUser: a single user owns one foreign key, one
// ACH-orphan (ach_key_id not in active set), and one ACH-tracked key
// (ach_key_id in active set). Only the ACH-orphan's opaque Token is
// revoked; the foreign and tracked keys are left alone.
func TestRunnable_TickOnce_MixedUser(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {
				{Token: "sk-foreign", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"source": "token-factory"}},
				{Token: "sk-ach-orphan", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"ach_key_id": "pkid-gone"}},
				{Token: "sk-ach-tracked", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"ach_key_id": "ekid-live"}},
			},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"ekid-live"}, nil
	}

	r.TickOnce(context.Background())

	if got := fake.revokedKeys; len(got) != 1 || got[0] != "sk-ach-orphan" {
		t.Fatalf("revokedKeys = %v; want [sk-ach-orphan] (only the ACH-orphan, by its Token)", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["target.name"]; got != "sk-ach-orphan" {
		t.Errorf("target.name = %v; want sk-ach-orphan", got)
	}
	if got := lines[0]["outcome"]; got != OutcomeRevoked {
		t.Errorf("outcome = %v; want %q", got, OutcomeRevoked)
	}
	assertNoForbiddenAttrs(t, lines[0])
}

// TestRunnable_TickOnce_DryRun (B3): with DryRun=true, an ACH-owned
// untracked orphan is NOT revoked and emits NO audit event — only the
// candidates + skipped{dry_run} counters move. The reversible, image-level
// neutralize.
func TestRunnable_TickOnce_DryRun(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {{
				Token:     "sk-would-revoke",
				UserID:    "u1",
				CreatedAt: time.Now().Add(-20 * time.Minute),
				Metadata:  map[string]any{"ach_key_id": "pkid-would"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.DryRun = true
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-other"}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times in dry-run; want 0", got)
	}
	if got := buf.Len(); got != 0 {
		t.Errorf("audit buffer non-empty in dry-run (operational log only): %q", buf.String())
	}
	if got := testutil.ToFloat64(r.Metrics.Candidates); got != 1 {
		t.Errorf("candidates_total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(r.Metrics.Skipped.WithLabelValues(SkipReasonDryRun)); got != 1 {
		t.Errorf("skipped{dry_run} = %v; want 1", got)
	}
	if got := testutil.ToFloat64(r.Metrics.Revoked); got != 0 {
		t.Errorf("revoked_total = %v; want 0 in dry-run", got)
	}
}

// TestRunnable_TickOnce_EmptyActiveSetGuard (B1): an empty active set with
// an ACH-owned candidate present is the mis-wire signature — revocation is
// skipped for the whole tick via a single skipped_empty_active_set audit
// event, and skipped{empty_active_set} records the spared candidate count.
func TestRunnable_TickOnce_EmptyActiveSetGuard(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {{
				Token:     "sk-ach-owned",
				UserID:    "u1",
				CreatedAt: time.Now().Add(-20 * time.Minute),
				Metadata:  map[string]any{"ach_key_id": "pkid-owned"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	// Active set genuinely empty — the exact mis-wire shape.
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times under empty-active-set guard; want 0", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["outcome"]; got != OutcomeSkippedEmptyActiveSet {
		t.Errorf("outcome = %v; want %q", got, OutcomeSkippedEmptyActiveSet)
	}
	if got := lines[0]["target.kind"]; got != "tick" {
		t.Errorf("target.kind = %v; want tick", got)
	}
	assertNoForbiddenAttrs(t, lines[0])
	if got := testutil.ToFloat64(r.Metrics.Skipped.WithLabelValues(SkipReasonEmptyActiveSet)); got != 1 {
		t.Errorf("skipped{empty_active_set} = %v; want 1", got)
	}
}

// TestRunnable_TickOnce_CircuitBreaker (B2): more candidates than MaxRevoke
// aborts revocation for the tick — one skipped_circuit_breaker audit event,
// nothing revoked, skipped{circuit_breaker} == candidate count.
func TestRunnable_TickOnce_CircuitBreaker(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {
				{Token: "sk-1", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute), Metadata: map[string]any{"ach_key_id": "pkid-1"}},
				{Token: "sk-2", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute), Metadata: map[string]any{"ach_key_id": "pkid-2"}},
				{Token: "sk-3", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute), Metadata: map[string]any{"ach_key_id": "pkid-3"}},
			},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.MaxRevoke = 2
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-live"}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times over circuit-breaker cap; want 0", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["outcome"]; got != OutcomeSkippedCircuitBreaker {
		t.Errorf("outcome = %v; want %q", got, OutcomeSkippedCircuitBreaker)
	}
	if got := lines[0]["candidate_count"]; got != float64(3) {
		t.Errorf("candidate_count = %v; want 3", got)
	}
	assertNoForbiddenAttrs(t, lines[0])
	if got := testutil.ToFloat64(r.Metrics.Skipped.WithLabelValues(SkipReasonCircuitBreaker)); got != 3 {
		t.Errorf("skipped{circuit_breaker} = %v; want 3", got)
	}
}

// TestRunnable_TickOnce_LiteLLMUnreachable: ListUserKeys errors on the
// FIRST user → the tick aborts cleanly with exactly one audit event
// (outcome=litellm_unreachable, target.kind=tick).
func TestRunnable_TickOnce_LiteLLMUnreachable(t *testing.T) {
	fake := &fakeLiteLLM{
		listErr: errors.New("network: dial tcp: connection refused"),
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}

	r.TickOnce(context.Background())

	if got := len(fake.revokedKeys); got != 0 {
		t.Errorf("RevokeKey called %d times on unreachable upstream; want 0", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["outcome"]; got != OutcomeLiteLLMUnreachable {
		t.Errorf("outcome = %v; want %q", got, OutcomeLiteLLMUnreachable)
	}
	if got := lines[0]["target.kind"]; got != "tick" {
		t.Errorf("target.kind = %v; want tick (the abort event characterizes the whole tick)", got)
	}
	if got := lines[0]["user_id"]; got != "u1" {
		t.Errorf("user_id = %v; want u1 (the user whose ListUserKeys failed)", got)
	}
	// CR-03: failure-path audit events MUST NOT carry err / bearer /
	// body etc. — the audit handler does not scrub, so raw error text
	// is forbidden by audit/doc.go.
	assertNoForbiddenAttrs(t, lines[0])
}

// TestRunnable_TickOnce_RevokeFailureContinues: two orphan keys for one
// user; RevokeKey returns an error on EVERY call → TWO audit events
// (both outcome=revoke_failed); the tick does NOT abort.
func TestRunnable_TickOnce_RevokeFailureContinues(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"u1": {
				{Token: "sk-rf-1", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"ach_key_id": "pkid-rf-1"}},
				{Token: "sk-rf-2", UserID: "u1", CreatedAt: time.Now().Add(-20 * time.Minute),
					Metadata: map[string]any{"ach_key_id": "pkid-rf-2"}},
			},
		},
		revokeErr: errors.New("litellm: revoke 503"),
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1"}, nil
	}
	// Non-empty active set (B1 fail-safe must not trip); neither pkid-rf-*
	// is tracked, so both are true orphans whose revoke is attempted.
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-other"}, nil
	}

	r.TickOnce(context.Background())

	// Both revokes were attempted.
	if got := len(fake.revokedKeys); got != 2 {
		t.Errorf("RevokeKey attempt count = %d; want 2 (revoke failures must NOT abort the tick)", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d; want 2: %s", len(lines), buf.String())
	}
	for i, l := range lines {
		if got := l["outcome"]; got != OutcomeRevokeFailed {
			t.Errorf("line %d outcome = %v; want %q", i, got, OutcomeRevokeFailed)
		}
		if got := l["target.kind"]; got != "litellm_key" {
			t.Errorf("line %d target.kind = %v; want litellm_key", i, got)
		}
		// CR-03: failure-path audit events MUST NOT carry err / bearer /
		// body etc. — see assertNoForbiddenAttrs rationale.
		assertNoForbiddenAttrs(t, l)
	}
}

// TestRunnable_TickOnce_MultipleUsers_OneFailsListUserKeys: ListUserKeys
// errors on the first user enumerated → the tick aborts at that user;
// subsequent users in the userIDs slice are NOT processed (Hub §18.4
// abort-on-unreachable). Asserts exactly one ListUserKeys call.
func TestRunnable_TickOnce_MultipleUsers_OneFailsListUserKeys(t *testing.T) {
	fake := &fakeLiteLLM{
		listErr: errors.New("network: connection refused"),
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"u1", "u2"}, nil
	}

	r.TickOnce(context.Background())

	if got := fake.listCalls.Load(); got != 1 {
		t.Errorf("ListUserKeys called %d times; want 1 (abort after first failure)", got)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	if got := lines[0]["user_id"]; got != "u1" {
		t.Errorf("user_id on abort = %v; want u1", got)
	}
	if got := lines[0]["outcome"]; got != OutcomeLiteLLMUnreachable {
		t.Errorf("outcome = %v; want %q", got, OutcomeLiteLLMUnreachable)
	}
	// CR-03: failure-path audit events MUST NOT carry err / bearer /
	// body etc.
	assertNoForbiddenAttrs(t, lines[0])
}

// TestRunnable_AuditEventShape verifies the success-path audit event
// against the exact D-18 shape: required keys present, no extra
// top-level keys carry plaintext or body content.
func TestRunnable_AuditEventShape(t *testing.T) {
	fake := &fakeLiteLLM{
		userKeysByUser: map[string][]litellm.UserKeyInfo{
			"user-abc": {{
				Token:     "sk-shape",
				UserID:    "user-abc",
				CreatedAt: time.Now().Add(-20 * time.Minute),
				KeyAlias:  "alias-must-not-be-in-audit",
				Metadata:  map[string]any{"ach_key_id": "pkid-shape"},
			}},
		},
	}
	r, buf := newTestRunnable(t, fake)
	r.ListUsers = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"user-abc"}, nil
	}
	// Non-empty active set (B1 fail-safe must not trip) that does not
	// contain pkid-shape, so the orphan is revoked and audited.
	r.ListKeyIDs = func(_ context.Context, _ *pgxpool.Pool) ([]string, error) {
		return []string{"pkid-other"}, nil
	}

	r.TickOnce(context.Background())

	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d; want 1: %s", len(lines), buf.String())
	}
	l := lines[0]

	// Required D-18 + Plan 02-04 fields.
	wantAudit := true
	if got := l["audit"]; got != wantAudit {
		t.Errorf("audit attr = %v (%T); want true (audit.NewLogger contract)", got, got)
	}
	if got := l["msg"]; got != "operator.orphan-cleanup" {
		t.Errorf("msg = %v; want operator.orphan-cleanup", got)
	}
	if got := l["target.kind"]; got != "litellm_key" {
		t.Errorf("target.kind = %v; want litellm_key", got)
	}
	if got := l["target.name"]; got != "sk-shape" {
		t.Errorf("target.name = %v; want sk-shape", got)
	}
	if got := l["outcome"]; got != OutcomeRevoked {
		t.Errorf("outcome = %v; want %q", got, OutcomeRevoked)
	}
	if got := l["user_id"]; got != "user-abc" {
		t.Errorf("user_id = %v; want user-abc", got)
	}

	// Forbidden keys (no plaintext / body / alias leakage).
	for _, forbidden := range []string{"key_alias", "credential_hash", "bearer", "body", "header", "err"} {
		if _, present := l[forbidden]; present {
			t.Errorf("forbidden key %q present in audit event; value=%v", forbidden, l[forbidden])
		}
	}

	// Top-level key set: only the documented ones should be present.
	// slog always includes "time" and "level" at the top — accept those.
	allowed := map[string]struct{}{
		"time": {}, "level": {}, "msg": {},
		"audit":       {},
		"target.kind": {}, "target.name": {},
		"outcome": {}, "user_id": {},
	}
	for k := range l {
		if _, ok := allowed[k]; !ok {
			t.Errorf("unexpected top-level key %q in audit event (value=%v)", k, l[k])
		}
	}
}

// TestRunnable_StartRespectsCtxCancel verifies the manager.Runnable
// lifecycle: a Start goroutine running on a short interval returns
// nil within a short window after ctx cancellation. Asserts the loop
// never deadlocks on shutdown.
func TestRunnable_StartRespectsCtxCancel(t *testing.T) {
	fake := &fakeLiteLLM{}
	r, _ := newTestRunnable(t, fake)
	r.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()

	// Let at least one tick fire.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v; want nil on ctx cancellation", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Start did not return within 1s of ctx cancellation")
	}
}

// ListTeamsByAlias is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, _ string) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// ListAllTeams is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// EnsureDefaultTeam is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) EnsureDefaultTeam(_ context.Context) error { return nil }

// §7 stubs — interface satisfaction only (issue #17: /v1 surface).
func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UpdateAccessGroup(_ context.Context, _ string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }
