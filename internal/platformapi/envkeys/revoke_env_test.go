// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// revokeEnvDB is a controllable dbOps fake for the ekid_ revoke path. It records
// whether the DB flip (RevokeEnvironmentKey) ran so tests can assert the
// LiteLLM-first ordering: the flip must run iff LiteLLM confirmed the key gone.
type revokeEnvDB struct {
	getRow       *db.EkKeyInfo
	revokeCalled bool
}

func (d *revokeEnvDB) InsertEnvironmentKey(context.Context, db.EkInsertRow) error { return nil }
func (d *revokeEnvDB) GetEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	return d.getRow, nil
}
func (d *revokeEnvDB) RevokeEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	d.revokeCalled = true
	return d.getRow, nil
}
func (d *revokeEnvDB) ListKeys(context.Context, db.KeyListFilter, int, string) ([]db.KeyListItem, string, error) {
	return nil, "", nil
}
func (d *revokeEnvDB) RevokePersonalKeyByOwner(context.Context, string, string) (*string, error) {
	return nil, nil
}

// recordRedis records whether the best-effort cache DEL ran.
type recordRedis struct{ delCalled bool }

func (r *recordRedis) Del(context.Context, string) error { r.delCalled = true; return nil }

func serveRevokeEnv(deps Deps, targetKeyID string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Delete("/{key_id}", revokeEnvironmentKey(deps))

	req := httptest.NewRequest(http.MethodDelete, "/"+targetKeyID, nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_caller00000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "alice@example.com",
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func activeEkRow() *db.EkKeyInfo {
	tok := "sk-llt-token"
	return &db.EkKeyInfo{
		KeyID:          "ekid_target00000000000000001",
		OwnerEmail:     "alice@example.com",
		Environment:    "platform",
		Status:         statusActive,
		CredentialHash: "chash",
		LiteLLMToken:   &tok,
	}
}

func revokeEnvDeps(fdb dbOps, fll litellm.Client, redis redisOps) Deps {
	return Deps{
		DB:      fdb,
		LiteLLM: fll,
		Redis:   redis,
		Audit:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestRevokeEnvironmentKey_LiteLLMNotFound_ProceedsToDBFlip: a 404 from
// LiteLLM /key/delete means the virtual key is already gone — the LiteLLM-first
// barrier's goal (kill upstream first) is already satisfied. The handler must
// treat that as idempotent success and proceed to the DB flip + Redis DEL,
// returning 204. Without this, an operator who deletes the LiteLLM key directly
// can never revoke the ACH row via the CLI (the reported bug).
func TestRevokeEnvironmentKey_LiteLLMNotFound_ProceedsToDBFlip(t *testing.T) {
	fdb := &revokeEnvDB{getRow: activeEkRow()}
	fll := &revokePersonalLiteLLM{
		NoopClient: &litellm.NoopClient{},
		revokeErr: &litellm.APIError{
			Method: http.MethodPost, Path: "/key/delete",
			StatusCode: http.StatusNotFound, Code: "not_found_error",
		},
	}
	redis := &recordRedis{}

	rec := serveRevokeEnv(revokeEnvDeps(fdb, fll, redis), "ekid_target00000000000000001")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !fdb.revokeCalled {
		t.Error("DB flip must run when LiteLLM confirms the key is already gone (404)")
	}
	if !redis.delCalled {
		t.Error("Redis DEL must run after the DB flip")
	}
}

// TestRevokeEnvironmentKey_LiteLLMOtherError_FailsClosed: any LiteLLM error
// that is NOT a confirmed not-found (e.g. 5xx unreachable, 401/403 rejected)
// leaves the key's upstream state UNKNOWN. The KEY-08 invariant must hold: the
// DB row stays active, no Redis DEL, and the error is surfaced so the caller
// retries cleanly. Only a positive not-found may bypass the barrier.
func TestRevokeEnvironmentKey_LiteLLMOtherError_FailsClosed(t *testing.T) {
	fdb := &revokeEnvDB{getRow: activeEkRow()}
	fll := &revokePersonalLiteLLM{
		NoopClient: &litellm.NoopClient{},
		revokeErr: &litellm.APIError{
			Method: http.MethodPost, Path: "/key/delete",
			StatusCode: http.StatusInternalServerError, Code: "500", Transient: true,
		},
	}
	redis := &recordRedis{}

	rec := serveRevokeEnv(revokeEnvDeps(fdb, fll, redis), "ekid_target00000000000000001")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (litellm_unreachable); body=%s", rec.Code, rec.Body.String())
	}
	if fdb.revokeCalled {
		t.Error("DB flip must NOT run when LiteLLM did not confirm the key is gone (KEY-08)")
	}
	if redis.delCalled {
		t.Error("Redis DEL must NOT run without a DB flip")
	}
}
