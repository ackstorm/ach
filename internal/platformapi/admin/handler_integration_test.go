//go:build integration

// SPDX-License-Identifier: Apache-2.0

// Integration coverage for the DB-touching admin paths. Spins a fresh
// postgres:16-alpine per test (mirrors internal/db's
// setupPostgresForPhase2 pattern but kept local so the admin package
// owns its own infrastructure).
//
// Run with `go test ./internal/platformapi/admin/... -tags integration`.

package admin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
)

// =========================== setup ===========================

func setupPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}
	ctx := context.Background()
	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("ach_admin_test"),
		tcpostgres.WithUsername("ach_admin_test"),
		tcpostgres.WithPassword("ach_admin_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("docker required: %v", err)
	}
	conn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("conn string: %v", err)
	}
	migPath, err := filepath.Abs("../../../db/migrations")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("abs migrations path: %v", err)
	}
	if err := db.Migrate(conn, migPath); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, conn)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("open: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return pool, cleanup
}

// seedPersonalKey writes a single personal_keys row with the supplied
// key_id + credential_hash + status. Helper used by the integration
// tests so each one stays self-contained.
func seedPersonalKey(t *testing.T, pool *pgxpool.Pool, keyID, credHash, owner, token string) {
	t.Helper()
	ctx := context.Background()
	tokenPtr := &token
	if token == "" {
		tokenPtr = nil
	}
	if err := db.InsertPersonalKey(ctx, pool, db.PkInsertRow{
		KeyID:          keyID,
		CredentialHash: credHash,
		OwnerEmail:     owner,
		ExpiresAt:      time.Now().Add(7 * 24 * time.Hour),
		LiteLLMUserID:  &owner,
		LiteLLMToken:   tokenPtr,
	}); err != nil {
		t.Fatalf("seed pk: %v", err)
	}
}

func seedEnvironmentKey(t *testing.T, pool *pgxpool.Pool, keyID, credHash, owner, env, token string) {
	t.Helper()
	ctx := context.Background()
	tokenPtr := &token
	if token == "" {
		tokenPtr = nil
	}
	if err := db.InsertEnvironmentKey(ctx, pool, db.EkInsertRow{
		KeyID:          keyID,
		CredentialHash: credHash,
		Environment:    env,
		OwnerEmail:     owner,
		Name:           "test-ek",
		LiteLLMUserID:  &owner,
		LiteLLMToken:   tokenPtr,
	}); err != nil {
		t.Fatalf("seed ek: %v", err)
	}
}

// =========================== RV-1: pk happy path (DB-first ordering) ===========================

func TestRevokeKey_PkHappyPath_DBFirst(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	seedPersonalKey(t, pool, "pkid_rv1", "credhash-rv1", "victim@example.com", "litellm-tok-rv1")

	order := &recorderOrder{}
	ll := &fakeLitellm{order: order}
	rd := &recordingRedis{order: order}
	var auditBuf bytes.Buffer
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: rd,
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&auditBuf),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"pkid_rv1"}`))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body)
	}
	// Verify DB row was flipped.
	row, _ := db.GetPersonalKey(context.Background(), pool, "pkid_rv1")
	if row == nil || row.Status != "revoked" {
		t.Fatalf("expected status=revoked, got %+v", row)
	}
	// Verify LiteLLM was called with the right token.
	if ll.revokeCalled.Load() != 1 {
		t.Fatalf("expected 1 LiteLLM revoke, got %d", ll.revokeCalled.Load())
	}
	if len(ll.revokedKeys) != 1 || ll.revokedKeys[0] != "litellm-tok-rv1" {
		t.Fatalf("LiteLLM revoke called with wrong token: %v", ll.revokedKeys)
	}
	// Verify LiteLLM was invoked (the recorderOrder captures only the
	// LiteLLM step; the DB flip happens before it via db.RevokePersonalKey,
	// not recorded).
	if len(order.steps) < 1 || order.steps[0] != "litellm" {
		t.Fatalf("expected LiteLLM revoke step, got steps=%v", order.steps)
	}
	// Verify audit emitted with outcome=revoked.
	if !strings.Contains(auditBuf.String(), `"outcome":"revoked"`) {
		t.Fatalf("expected audit outcome=revoked, got %s", auditBuf.String())
	}
}

// =========================== RV-2: pk LiteLLM unreachable (WARN-04) ===========================

func TestRevokeKey_PkLiteLLMUnreachable_WARN04(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	seedPersonalKey(t, pool, "pkid_rv2", "credhash-rv2", "victim2@example.com", "litellm-tok-rv2")

	ll := &fakeLitellm{revokeErr: errors.New("connection refused")}
	rd := &recordingRedis{}
	var auditBuf bytes.Buffer
	var opBuf bytes.Buffer
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: rd,
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&auditBuf),
		Logger:    sluggish(&opBuf),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"pkid_rv2"}`))

	// WARN-04: response is 200 (NOT 503) — DB flip is the visible barrier.
	if rec.Code != 200 {
		t.Fatalf("WARN-04 violated: expected 200 on LiteLLM-unreachable, got %d", rec.Code)
	}
	// DB row IS flipped.
	row, _ := db.GetPersonalKey(context.Background(), pool, "pkid_rv2")
	if row == nil || row.Status != "revoked" {
		t.Fatalf("DB flip not visible: %+v", row)
	}
	// Audit outcome captures partial completion.
	if !strings.Contains(auditBuf.String(), `"outcome":"litellm_unreachable"`) {
		t.Fatalf("expected audit outcome=litellm_unreachable; got %s", auditBuf.String())
	}
	// WARN-04 invariant: stderr WARN log emitted.
	if !strings.Contains(opBuf.String(), "admin.pk-revoke: LiteLLM unreachable") {
		t.Fatalf("WARN-04 stderr WARN log missing; got %s", opBuf.String())
	}
}

// =========================== RV-3: pk already-revoked / unknown ===========================

func TestRevokeKey_PkUnknown(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	deps := Deps{
		Pool: pool, LiteLLM: &fakeLitellm{}, Redis: &recordingRedis{},
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&bytes.Buffer{}),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"pkid_unknown"}`))
	if rec.Code != 404 {
		t.Fatalf("expected 404 for unknown pk_, got %d", rec.Code)
	}
}

// =========================== RV-4: ek happy path (LiteLLM-first) ===========================

func TestRevokeKey_EkHappyPath_LiteLLMFirst(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	seedEnvironmentKey(t, pool, "ekid_rv4", "credhash-rv4", "wkload@example.com", "envA", "litellm-tok-rv4")

	order := &recorderOrder{}
	ll := &fakeLitellm{order: order}
	rd := &recordingRedis{order: order}
	var auditBuf bytes.Buffer
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: rd,
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&auditBuf),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"ekid_rv4"}`))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body)
	}
	if ll.revokeCalled.Load() != 1 {
		t.Fatalf("expected 1 LiteLLM revoke, got %d", ll.revokeCalled.Load())
	}
	// ordering: LiteLLM revoke is the recorded step (DB flip not captured by
	// recorder but happens after LiteLLM).
	if len(order.steps) < 1 || order.steps[0] != "litellm" {
		t.Fatalf("ordering violated: %v", order.steps)
	}
	row, _ := db.GetEnvironmentKey(context.Background(), pool, "ekid_rv4")
	if row == nil || row.Status != "revoked" {
		t.Fatalf("expected status=revoked; got %+v", row)
	}
	if !strings.Contains(auditBuf.String(), `"action":"platform.ek.revoke"`) {
		t.Fatalf("missing action=platform.ek.revoke; got %s", auditBuf.String())
	}
}

// =========================== RV-5: ek LiteLLM unreachable (503) ===========================

func TestRevokeKey_EkLiteLLMUnreachable(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	seedEnvironmentKey(t, pool, "ekid_rv5", "credhash-rv5", "wkload5@example.com", "envB", "litellm-tok-rv5")

	ll := &fakeLitellm{revokeErr: errors.New("connection refused")}
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: &recordingRedis{},
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&bytes.Buffer{}),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps), "/platform/admin/keys/revoke",
		[]byte(`{"key_id":"ekid_rv5"}`))
	// KEY-08: LiteLLM-first means LiteLLM-unreachable → 503. DB stays active.
	if rec.Code != 503 {
		t.Fatalf("expected 503 (KEY-08 LiteLLM-first), got %d body=%s", rec.Code, rec.Body)
	}
	row, _ := db.GetEnvironmentKey(context.Background(), pool, "ekid_rv5")
	if row == nil || row.Status != "active" {
		t.Fatalf("ek DB row should STAY active per KEY-08; got %+v", row)
	}
}

// =========================== RU-1: revoke-user-keys happy path ===========================

func TestRevokeUserKeys_HappyPath(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	seedPersonalKey(t, pool, "pkid_u1a", "ch-u1a", "u@x.com", "lt1")
	seedPersonalKey(t, pool, "pkid_u1b", "ch-u1b", "u@x.com", "lt2")
	seedEnvironmentKey(t, pool, "ekid_u1a", "ch-u1c", "u@x.com", "envX", "lt3")

	ll := &fakeLitellm{}
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: &recordingRedis{},
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&bytes.Buffer{}),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps),
		"/platform/admin/users/u%40x.com/revoke-keys", nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"revoked_count":3`) {
		t.Fatalf("expected revoked_count=3; got %s", body)
	}
}

// =========================== RU-3: revoke-user-keys with no keys ===========================

func TestRevokeUserKeys_NoKeys(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	deps := Deps{
		Pool: pool, LiteLLM: &fakeLitellm{}, Redis: &recordingRedis{},
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&bytes.Buffer{}),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps),
		"/platform/admin/users/nobody%40x.com/revoke-keys", nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"revoked_count":0`) {
		t.Fatalf("expected revoked_count=0; got %s", rec.Body.String())
	}
}

// =========================== RU-2: URL-decode preserves verbatim email ===========================

func TestRevokeUserKeys_URLDecodePlusSign(t *testing.T) {
	pool, cleanup := setupPostgres(t)
	defer cleanup()

	// Seed a row whose owner_email contains '+'; URL-encoded as %2B.
	seedPersonalKey(t, pool, "pkid_u2", "ch-u2", "u+tag@x.com", "lt-u2")

	ll := &fakeLitellm{}
	deps := Deps{
		Pool: pool, LiteLLM: ll, Redis: &recordingRedis{},
		Allowlist: adminAllowlist(),
		Audit:     audit.NewLogger(&bytes.Buffer{}),
		Namespace: testNs,
	}
	rec := adminPostJSON(t, newAdminRouter(t, deps),
		"/platform/admin/users/u%2Btag%40x.com/revoke-keys", nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"revoked_count":1`) {
		t.Fatalf("expected revoked_count=1; got %s", rec.Body.String())
	}
}

// =========================== slog buffer helper ===========================

// sluggish returns a *slog.Logger that writes JSON records to the
// supplied buffer. Used by RV-2 to inspect the WARN-04 stderr WARN
// log inline.
func sluggish(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
