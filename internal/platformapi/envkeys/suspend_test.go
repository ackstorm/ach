// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// envKeyStateDB is a controllable dbOps fake for the suspend/resume/
// revoke-from-any-state tests. getRow is what the first GetEnvironmentKey
// call (loadOwnedEk's read, or conflictFor's re-read) returns; getRowNext,
// when non-nil, is what every SUBSEQUENT GetEnvironmentKey call returns —
// used to simulate the state a concurrent actor left behind by the time
// conflictFor re-reads after a lost race.
type envKeyStateDB struct {
	getCalls   int
	getRow     *db.EkKeyInfo
	getRowNext *db.EkKeyInfo

	suspendIDs []string
	suspendRow *db.EkKeyInfo // returned on success; nil (with nil err) simulates a lost race
	suspendErr error

	resumeIDs []string
	resumeRow *db.EkKeyInfo
	resumeErr error

	revokeIDs []string
	revokeRow *db.EkKeyInfo
	revokeErr error
}

func (d *envKeyStateDB) InsertEnvironmentKey(context.Context, db.EkInsertRow) error { return nil }

func (d *envKeyStateDB) GetEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	d.getCalls++
	if d.getCalls > 1 && d.getRowNext != nil {
		return d.getRowNext, nil
	}
	return d.getRow, nil
}

func (d *envKeyStateDB) RevokeEnvironmentKey(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
	d.revokeIDs = append(d.revokeIDs, keyID)
	return d.revokeRow, d.revokeErr
}

func (d *envKeyStateDB) SuspendEnvironmentKey(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
	d.suspendIDs = append(d.suspendIDs, keyID)
	return d.suspendRow, d.suspendErr
}

func (d *envKeyStateDB) ResumeEnvironmentKey(_ context.Context, keyID string) (*db.EkKeyInfo, error) {
	d.resumeIDs = append(d.resumeIDs, keyID)
	return d.resumeRow, d.resumeErr
}

func (d *envKeyStateDB) ListKeys(context.Context, db.KeyListFilter, int, string) ([]db.KeyListItem, string, error) {
	return nil, "", nil
}

func (d *envKeyStateDB) RevokePersonalKeyByOwner(context.Context, string, string) (*string, error) {
	return nil, nil
}

// resumeLiteLLM is a controllable fake litellm.Client for the Resume
// access-check path (LookupCallerTeams): userInfoErr forces the
// "LiteLLM down" 503 branch; otherwise UserInfoByEmail returns teams
// (raw ids, matched directly against the Environment's authorizedTeams —
// no ListAllTeams alias resolution needed for these tests).
type resumeLiteLLM struct {
	*litellm.NoopClient
	teams       []string
	userInfoErr error
}

func (c *resumeLiteLLM) UserInfoByEmail(_ context.Context, email string) (*litellm.UserInfo, error) {
	if c.userInfoErr != nil {
		return nil, c.userInfoErr
	}
	return &litellm.UserInfo{UserID: "llu-" + email, UserEmail: email, Teams: c.teams}, nil
}

func (c *resumeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// suspendableEkRow is the base active ek_ row every suspend/resume test
// starts from unless it overrides Status/ExpiresAt.
func suspendableEkRow() *db.EkKeyInfo {
	return &db.EkKeyInfo{
		KeyID:          "ekid_target00000000000000001",
		OwnerEmail:     "alice@example.com",
		Environment:    "platform",
		Status:         statusActive,
		CredentialHash: "chash",
	}
}

// captureAuditDeps builds a Deps whose Audit logger writes to buf (text
// handler) so tests can assert on the emitted action/outcome attributes.
func captureAuditDeps(fdb dbOps, fll litellm.Client, store envStore, redis redisOps, buf *bytes.Buffer) Deps {
	return Deps{
		DB:      fdb,
		LiteLLM: fll,
		Store:   store,
		Redis:   redis,
		Audit:   slog.New(slog.NewTextHandler(buf, nil)),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func serveKeyActionAs(h http.HandlerFunc, targetKeyID, verb, ownerEmail string, isAdmin bool) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Post("/{key_id}/"+verb, h)

	req := httptest.NewRequest(http.MethodPost, "/"+targetKeyID+"/"+verb, nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_caller00000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: ownerEmail,
	}, isAdmin)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func serveSuspend(deps Deps, targetKeyID string) *httptest.ResponseRecorder {
	return serveKeyActionAs(SuspendHandler(deps), targetKeyID, "suspend", "alice@example.com", false)
}

func serveResume(deps Deps, targetKeyID string) *httptest.ResponseRecorder {
	return serveKeyActionAs(ResumeHandler(deps), targetKeyID, "resume", "alice@example.com", false)
}

func TestSuspend_OwnerFlipsAndDelsCache(t *testing.T) {
	row := suspendableEkRow()
	suspended := *row
	suspended.Status = "suspended"
	fdb := &envKeyStateDB{getRow: row, suspendRow: &suspended}
	redis := &recordRedis{}
	var buf bytes.Buffer
	deps := captureAuditDeps(fdb, nil, nil, redis, &buf)

	rec := serveSuspend(deps, row.KeyID)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(fdb.suspendIDs) != 1 || fdb.suspendIDs[0] != row.KeyID {
		t.Errorf("SuspendEnvironmentKey calls = %v, want [%s]", fdb.suspendIDs, row.KeyID)
	}
	if !redis.delCalled {
		t.Error("Redis DEL must run after the DB flip")
	}
	if !strings.Contains(buf.String(), "platform.ek.suspend") || !strings.Contains(buf.String(), "suspended") {
		t.Errorf("audit log missing suspend action/outcome: %s", buf.String())
	}
}

func TestSuspend_IdempotentAndConflicts(t *testing.T) {
	t.Run("already suspended", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

		rec := serveSuspend(deps, row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (idempotent); body=%s", rec.Code, rec.Body.String())
		}
		if len(fdb.suspendIDs) != 0 {
			t.Error("SuspendEnvironmentKey must not be called when already suspended")
		}
	})

	t.Run("revoked", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "revoked"
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

		rec := serveSuspend(deps, row.KeyID)

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "key_revoked") {
			t.Fatalf("status/body = %d/%s, want 409 key_revoked", rec.Code, rec.Body.String())
		}
	})

	t.Run("expired", func(t *testing.T) {
		row := suspendableEkRow()
		past := time.Now().Add(-time.Hour)
		row.ExpiresAt = &past
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

		rec := serveSuspend(deps, row.KeyID)

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "key_expired") {
			t.Fatalf("status/body = %d/%s, want 409 key_expired", rec.Code, rec.Body.String())
		}
	})
}

func TestSuspend_NotOwner(t *testing.T) {
	t.Run("non-admin other owner", func(t *testing.T) {
		row := suspendableEkRow()
		row.OwnerEmail = "bob@example.com"
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

		rec := serveKeyActionAs(SuspendHandler(deps), row.KeyID, "suspend", "alice@example.com", false)

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not_key_owner") {
			t.Fatalf("status/body = %d/%s, want 403 not_key_owner", rec.Code, rec.Body.String())
		}
	})

	t.Run("admin may suspend any owner's key", func(t *testing.T) {
		row := suspendableEkRow()
		row.OwnerEmail = "bob@example.com"
		suspended := *row
		suspended.Status = "suspended"
		fdb := &envKeyStateDB{getRow: row, suspendRow: &suspended}
		deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

		rec := serveKeyActionAs(SuspendHandler(deps), row.KeyID, "suspend", "admin@example.com", true)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestSuspend_WorksWithoutEnvironmentAccess (§11): suspend has no access
// check at all — a caller who has since lost Environment access can still
// suspend their own key. deps.Store/LiteLLM are left nil: if SuspendHandler
// ever touched either, this test would panic on the nil interface call.
func TestSuspend_WorksWithoutEnvironmentAccess(t *testing.T) {
	row := suspendableEkRow()
	suspended := *row
	suspended.Status = "suspended"
	fdb := &envKeyStateDB{getRow: row, suspendRow: &suspended}
	deps := captureAuditDeps(fdb, nil, nil, &recordRedis{}, &bytes.Buffer{})

	rec := serveSuspend(deps, row.KeyID)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

func TestResume_RequiresAccessAndLiveness(t *testing.T) {
	env := &fakeEnvStore{env: &db.EnvironmentRow{Name: "platform", AuthorizedTeams: []string{"team-a"}}}

	t.Run("owner in teams, suspended -> 204 + DEL", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		active := *row
		active.Status = statusActive
		fdb := &envKeyStateDB{getRow: row, resumeRow: &active}
		redis := &recordRedis{}
		fll := &resumeLiteLLM{NoopClient: &litellm.NoopClient{}, teams: []string{"team-a"}}
		deps := captureAuditDeps(fdb, fll, env, redis, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		if len(fdb.resumeIDs) != 1 {
			t.Error("ResumeEnvironmentKey must be called")
		}
		if !redis.delCalled {
			t.Error("Redis DEL must run after the DB flip")
		}
	})

	t.Run("owner not in teams -> 403 unauthorized_team", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		fdb := &envKeyStateDB{getRow: row}
		fll := &resumeLiteLLM{NoopClient: &litellm.NoopClient{}, teams: []string{"team-z"}}
		deps := captureAuditDeps(fdb, fll, env, &recordRedis{}, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "unauthorized_team") {
			t.Fatalf("status/body = %d/%s, want 403 unauthorized_team", rec.Code, rec.Body.String())
		}
		if len(fdb.resumeIDs) != 0 {
			t.Error("ResumeEnvironmentKey must not be called without access")
		}
	})

	t.Run("LiteLLM down -> 503", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		fdb := &envKeyStateDB{getRow: row}
		fll := &resumeLiteLLM{NoopClient: &litellm.NoopClient{}, userInfoErr: errors.New("dial tcp: connection refused")}
		deps := captureAuditDeps(fdb, fll, env, &recordRedis{}, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("expired -> 409 key_expired", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		past := time.Now().Add(-time.Hour)
		row.ExpiresAt = &past
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, env, &recordRedis{}, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "key_expired") {
			t.Fatalf("status/body = %d/%s, want 409 key_expired", rec.Code, rec.Body.String())
		}
	})

	t.Run("revoked -> 409 key_revoked", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "revoked"
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, env, &recordRedis{}, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "key_revoked") {
			t.Fatalf("status/body = %d/%s, want 409 key_revoked", rec.Code, rec.Body.String())
		}
	})

	t.Run("already active -> 204 idempotent", func(t *testing.T) {
		row := suspendableEkRow() // Status already statusActive
		fdb := &envKeyStateDB{getRow: row}
		deps := captureAuditDeps(fdb, nil, env, &recordRedis{}, &bytes.Buffer{})

		rec := serveResume(deps, row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (idempotent); body=%s", rec.Code, rec.Body.String())
		}
		if len(fdb.resumeIDs) != 0 {
			t.Error("ResumeEnvironmentKey must not be called when already active")
		}
	})
}

// TestResume_RaceWithRevokeIsNotResurrection: DB.ResumeEnvironmentKey lost
// the race (a concurrent revoke won) and returns (nil, nil). The handler
// MUST re-read and answer 409 for the state it finds — never resurrect the
// key with a 204.
func TestResume_RaceWithRevokeIsNotResurrection(t *testing.T) {
	row := suspendableEkRow()
	row.Status = "suspended"
	revoked := *row
	revoked.Status = "revoked"
	fdb := &envKeyStateDB{getRow: row, getRowNext: &revoked, resumeRow: nil, resumeErr: nil}
	env := &fakeEnvStore{env: &db.EnvironmentRow{Name: "platform", AuthorizedTeams: []string{"team-a"}}}
	fll := &resumeLiteLLM{NoopClient: &litellm.NoopClient{}, teams: []string{"team-a"}}
	deps := captureAuditDeps(fdb, fll, env, &recordRedis{}, &bytes.Buffer{})

	rec := serveResume(deps, row.KeyID)

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "key_revoked") {
		t.Fatalf("status/body = %d/%s, want 409 key_revoked (never a resurrection)", rec.Code, rec.Body.String())
	}
}

// TestRevoke_FromAnyStateAndIdempotent exercises DELETE /platform/keys/{id}
// (via serveRevokeEnv, revoke_env_test.go) from every ek_ state.
func TestRevoke_FromAnyStateAndIdempotent(t *testing.T) {
	t.Run("suspended -> LiteLLM RevokeKey called -> 204", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "suspended"
		tok := "sk-llt-token"
		row.LiteLLMToken = &tok
		fdb := &revokeEnvDB{getRow: row}
		fll := &revokePersonalLiteLLM{NoopClient: &litellm.NoopClient{}}
		redis := &recordRedis{}

		rec := serveRevokeEnv(revokeEnvDeps(fdb, fll, redis), row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		if fll.revokedToken != tok {
			t.Errorf("LiteLLM.RevokeKey token = %q, want %q", fll.revokedToken, tok)
		}
		if !fdb.revokeCalled {
			t.Error("DB flip must run")
		}
	})

	t.Run("revoked -> 204 with NO LiteLLM call", func(t *testing.T) {
		row := suspendableEkRow()
		row.Status = "revoked"
		fdb := &revokeEnvDB{getRow: row}
		fll := &revokePersonalLiteLLM{NoopClient: &litellm.NoopClient{}}
		redis := &recordRedis{}

		rec := serveRevokeEnv(revokeEnvDeps(fdb, fll, redis), row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		if fll.revokedToken != "" {
			t.Errorf("LiteLLM.RevokeKey must not be called on an already-revoked key; got token %q", fll.revokedToken)
		}
		if fdb.revokeCalled {
			t.Error("DB flip must not run again on an already-revoked key")
		}
	})

	t.Run("expired (active row past expires_at) -> 204", func(t *testing.T) {
		row := suspendableEkRow() // Status active
		past := time.Now().Add(-time.Hour)
		row.ExpiresAt = &past
		tok := "sk-llt-token"
		row.LiteLLMToken = &tok
		fdb := &revokeEnvDB{getRow: row}
		fll := &revokePersonalLiteLLM{NoopClient: &litellm.NoopClient{}}
		redis := &recordRedis{}

		rec := serveRevokeEnv(revokeEnvDeps(fdb, fll, redis), row.KeyID)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
		if !fdb.revokeCalled {
			t.Error("DB flip must run for an expired-but-not-revoked key")
		}
	})
}
