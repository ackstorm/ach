// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// revokePersonalDB is a controllable fake for the RevokePersonalHandler tests.
// The revokeErr / revokeToken fields drive the DB outcome; revokeCallKeyID
// captures the key_id passed to RevokePersonalKeyByOwner for assertion.
type revokePersonalDB struct {
	// RevokePersonalKeyByOwner controls
	revokeToken     *string // returned litellmToken on success
	revokeErr       error   // returned error (nil = success)
	revokeCallKeyID string  // records the last key_id argument
	revokeCallOwner string  // records the last owner argument
}

func (d *revokePersonalDB) RevokePersonalKeyByOwner(_ context.Context, keyID, owner string) (*string, error) {
	d.revokeCallKeyID = keyID
	d.revokeCallOwner = owner
	return d.revokeToken, d.revokeErr
}

// Stub out the rest of the dbOps interface — these methods are not exercised
// by RevokePersonalHandler.
func (d *revokePersonalDB) InsertEnvironmentKey(context.Context, db.EkInsertRow) error {
	return nil
}
func (d *revokePersonalDB) GetEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	return nil, nil
}
func (d *revokePersonalDB) RevokeEnvironmentKey(context.Context, string) (*db.EkKeyInfo, error) {
	return nil, nil
}
func (d *revokePersonalDB) ListKeys(context.Context, db.KeyListFilter, int, string) ([]db.KeyListItem, string, error) {
	return nil, "", nil
}

// revokePersonalLiteLLM is a fake litellm.Client that records whether RevokeKey
// was called and can be configured to return an error.
type revokePersonalLiteLLM struct {
	*litellm.NoopClient
	revokeErr    error  // if non-nil, RevokeKey returns this
	revokedToken string // records the last token passed to RevokeKey
}

func (c *revokePersonalLiteLLM) RevokeKey(_ context.Context, token string) error {
	c.revokedToken = token
	return c.revokeErr
}

// newRevokePersonalDeps returns a minimal Deps wired with the given db fake
// and litellm fake. Audit and Logger write to io.Discard.
func newRevokePersonalDeps(fdb *revokePersonalDB, fll *revokePersonalLiteLLM) Deps {
	return Deps{
		DB:      fdb,
		LiteLLM: fll,
		Audit:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// serveRevokePersonal drives revokePersonalKey through a chi router
// (to enable chi.URLParam extraction) with the given key_id path segment
// and query string.
func serveRevokePersonal(deps Deps, callerKeyID, callerOwner, targetKeyID, query string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Delete("/{key_id}", revokePersonalKey(deps))

	path := "/" + targetKeyID
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      callerKeyID,
		KeyType:    keys.PrefixPk,
		OwnerEmail: callerOwner,
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// noopRedis is a minimal redisOps fake that always succeeds.
type noopRedis struct{}

func (r *noopRedis) Del(_ context.Context, _ string) error { return nil }

// dispatchDB is a combined dbOps fake for TestRevokeHandler_DispatchesByPrefix.
// It satisfies the full dbOps interface while providing controllable outcomes
// for both the ekid_ (GetEnvironmentKey / RevokeEnvironmentKey) and pkid_
// (RevokePersonalKeyByOwner) branches.
type dispatchDB struct {
	ekRow   *db.EkKeyInfo // returned by GetEnvironmentKey
	pkToken *string       // returned by RevokePersonalKeyByOwner
}

func (d *dispatchDB) InsertEnvironmentKey(_ context.Context, _ db.EkInsertRow) error { return nil }

func (d *dispatchDB) GetEnvironmentKey(_ context.Context, _ string) (*db.EkKeyInfo, error) {
	return d.ekRow, nil
}

func (d *dispatchDB) RevokeEnvironmentKey(_ context.Context, _ string) (*db.EkKeyInfo, error) {
	return d.ekRow, nil
}

func (d *dispatchDB) ListKeys(_ context.Context, _ db.KeyListFilter, _ int, _ string) ([]db.KeyListItem, string, error) {
	return nil, "", nil
}

func (d *dispatchDB) RevokePersonalKeyByOwner(_ context.Context, _ string, _ string) (*string, error) {
	return d.pkToken, nil
}

// TestRevokeHandler_DispatchesByPrefix asserts the unified DELETE
// /platform/keys/{key_id} routes ekid_ to the ek path (204) and pkid_ to the
// personal path (200), and rejects a bare/unknown prefix with 400.
func TestRevokeHandler_DispatchesByPrefix(t *testing.T) {
	ekToken := "sk-llt-ek-token"
	credHash := "abc123credHash"
	pkToken := "sk-llt-pk-token"

	// Combined deps that can service both the ekid_ and pkid_ revoke branches.
	fdb := &dispatchDB{
		ekRow: &db.EkKeyInfo{
			KeyID:          "ekid_target00000000000000001",
			OwnerEmail:     "alice@example.com",
			Environment:    "prod",
			Status:         statusActive,
			CredentialHash: credHash,
			LiteLLMToken:   &ekToken,
		},
		pkToken: &pkToken,
	}
	fll := &revokePersonalLiteLLM{NoopClient: &litellm.NoopClient{}}
	deps := Deps{
		DB:      fdb,
		LiteLLM: fll,
		Redis:   &noopRedis{},
		Audit:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// serve drives a DELETE /platform/keys/{key_id} through a chi router that
	// mounts the full /platform/keys family via MountKeys.
	serve := func(callerKeyID, callerOwner, targetKeyID string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		MountKeys(r, deps)

		path := "/platform/keys/" + targetKeyID
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
			KeyID:      callerKeyID,
			KeyType:    keys.PrefixPk,
			OwnerEmail: callerOwner,
		}, false)
		ctx = middleware.WithRequestID(ctx, "req_test")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// ekid_ → 204 No Content (LiteLLM-first ek revoke path).
	rec := serve("pkid_caller00000000000000000", "alice@example.com", "ekid_target00000000000000001")
	if rec.Code != http.StatusNoContent {
		t.Errorf("ekid_ branch: status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// pkid_ → 200 JSON (DB-first pk revoke path; caller != target so no 409 guard).
	rec = serve("pkid_caller00000000000000000", "alice@example.com", "pkid_target00000000000000001")
	if rec.Code != http.StatusOK {
		t.Errorf("pkid_ branch: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// garbage/unknown prefix → 400 invalid_argument (dispatcher default branch).
	rec = serve("pkid_caller00000000000000000", "alice@example.com", "garbage_key_id")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("garbage prefix: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRevokePersonalHandler drives the table of expected outcomes.
func TestRevokePersonalHandler(t *testing.T) {
	tok := "sk-litellm-abc"

	tests := []struct {
		name         string
		callerKeyID  string // authenticating key id
		callerOwner  string // authenticating owner email
		targetKeyID  string // {key_id} in the path
		query        string // raw query string (e.g. "force=true")
		dbErr        error  // error returned by RevokePersonalKeyByOwner
		dbToken      *string
		llmErr       error // error returned by litellm.RevokeKey
		wantStatus   int
		wantBodyPart string // substring that must appear in the response body
		wantDBCalled bool   // RevokePersonalKeyByOwner must have been called
	}{
		{
			name:         "own active key → 200 DB row flipped",
			callerKeyID:  "pkid_caller00000000000000000",
			callerOwner:  "alice@example.com",
			targetKeyID:  "pkid_target00000000000000001",
			dbToken:      &tok,
			wantStatus:   http.StatusOK,
			wantBodyPart: `"revoked"`,
			wantDBCalled: true,
		},
		{
			name:         "wrong owner → 404 no existence leak",
			callerKeyID:  "pkid_caller00000000000000000",
			callerOwner:  "mallory@example.com",
			targetKeyID:  "pkid_target00000000000000001",
			dbErr:        db.ErrKeyNotFoundOrNotOwner,
			wantStatus:   http.StatusNotFound,
			wantBodyPart: "key_not_found",
			wantDBCalled: true,
		},
		{
			name:         "active key without force → 409",
			callerKeyID:  "pkid_active00000000000000000",
			callerOwner:  "alice@example.com",
			targetKeyID:  "pkid_active00000000000000000", // same as callerKeyID
			wantStatus:   http.StatusConflict,
			wantBodyPart: codeCannotRevokeActiveKey,
			wantDBCalled: false, // DB must NOT be called before the guard fires
		},
		{
			name:         "active key with force=true → 200",
			callerKeyID:  "pkid_active00000000000000000",
			callerOwner:  "alice@example.com",
			targetKeyID:  "pkid_active00000000000000000",
			query:        "force=true",
			dbToken:      &tok,
			wantStatus:   http.StatusOK,
			wantBodyPart: `"revoked"`,
			wantDBCalled: true,
		},
		{
			name:         "ekid_ prefix → 400 (not a pkid_ key)",
			callerKeyID:  "pkid_caller00000000000000000",
			callerOwner:  "alice@example.com",
			targetKeyID:  "ekid_target00000000000000001",
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: keys.PkidKeyIDPrefix,
			wantDBCalled: false,
		},
		{
			name:         "litellm unreachable → still 200 (DB-first)",
			callerKeyID:  "pkid_caller00000000000000000",
			callerOwner:  "alice@example.com",
			targetKeyID:  "pkid_target00000000000000001",
			dbToken:      &tok,
			llmErr:       errors.New("dial tcp: connection refused"),
			wantStatus:   http.StatusOK,
			wantBodyPart: `"revoked"`,
			wantDBCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fdb := &revokePersonalDB{
				revokeToken: tc.dbToken,
				revokeErr:   tc.dbErr,
			}
			fll := &revokePersonalLiteLLM{
				NoopClient: &litellm.NoopClient{},
				revokeErr:  tc.llmErr,
			}
			deps := newRevokePersonalDeps(fdb, fll)

			rec := serveRevokePersonal(deps, tc.callerKeyID, tc.callerOwner, tc.targetKeyID, tc.query)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBodyPart != "" && !strings.Contains(rec.Body.String(), tc.wantBodyPart) {
				t.Errorf("body = %q, want to contain %q", rec.Body.String(), tc.wantBodyPart)
			}
			if tc.wantDBCalled && fdb.revokeCallKeyID == "" {
				t.Error("RevokePersonalKeyByOwner was not called, expected it to be called")
			}
			if !tc.wantDBCalled && fdb.revokeCallKeyID != "" {
				t.Errorf("RevokePersonalKeyByOwner was called with key_id=%q, expected no call", fdb.revokeCallKeyID)
			}
			// When DB is called with a non-sentinel error, assert owner was passed.
			if fdb.revokeCallKeyID != "" && fdb.revokeCallOwner != tc.callerOwner {
				t.Errorf("RevokePersonalKeyByOwner owner = %q, want %q", fdb.revokeCallOwner, tc.callerOwner)
			}
		})
	}
}
