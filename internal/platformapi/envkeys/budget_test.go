// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// patchBudget drives PATCH /platform/keys/{key_id}/budget as ownerEmail.
func patchBudget(deps Deps, keyID, body, callerEmail string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Patch("/{key_id}/budget", BudgetHandler(deps))

	req := httptest.NewRequest(http.MethodPatch, "/"+keyID+"/budget", strings.NewReader(body))
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID:      "pkid_caller00000000000000000",
		KeyType:    keys.PrefixPk,
		OwnerEmail: callerEmail,
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// TestCreateKeyWithBudgetWritesKeyTag — POST /platform/keys {budget} caps
// that one ek_ through its own tag.
func TestCreateKeyWithBudgetWritesKeyTag(t *testing.T) {
	fdb := &fakeEkDB{}
	deps, flm := newCreateDeps(fdb)

	rec := doCreate(deps, `{"environment":"prod","name":"k","budget":{"max_budget":25,"budget_duration":"30d"}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fdb.inserted == nil {
		t.Fatal("no row inserted")
	}
	got := flm.upsertedTags["key:"+fdb.inserted.KeyID]
	if got.MaxBudget != 25 || got.BudgetDuration != "30d" {
		t.Fatalf("key tag budget = %+v (all tags %v)", got, flm.upsertedTags)
	}
}

// TestCreateKeyWithoutBudgetWritesNoTag — the common case stays a single
// LiteLLM mint: no budget block, no tag.
func TestCreateKeyWithoutBudgetWritesNoTag(t *testing.T) {
	fdb := &fakeEkDB{}
	deps, flm := newCreateDeps(fdb)

	if rec := doCreate(deps, `{"environment":"prod","name":"k"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(flm.upsertedTags) != 0 {
		t.Fatalf("want no tag writes, got %v", flm.upsertedTags)
	}
}

// TestPatchBudgetUpdatesTag — PATCH replaces the ceiling, 204, owner-scoped.
func TestPatchBudgetUpdatesTag(t *testing.T) {
	row := suspendableEkRow()
	fdb := &envKeyStateDB{getRow: row}
	flm := newCaptureLiteLLM()
	deps := captureAuditDeps(fdb, flm, nil, &recordRedis{}, &bytes.Buffer{})

	rec := patchBudget(deps, row.KeyID, `{"max_budget":50}`, row.OwnerEmail)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if got := flm.upsertedTags["key:"+row.KeyID]; got.MaxBudget != 50 {
		t.Fatalf("tag = %+v", got)
	}
}

// TestPatchBudgetNotOwnerIs403 — same authorization shape as suspend.
func TestPatchBudgetNotOwnerIs403(t *testing.T) {
	row := suspendableEkRow()
	fdb := &envKeyStateDB{getRow: row}
	flm := newCaptureLiteLLM()
	deps := captureAuditDeps(fdb, flm, nil, &recordRedis{}, &bytes.Buffer{})

	rec := patchBudget(deps, row.KeyID, `{"max_budget":50}`, "stranger@example.com")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if len(flm.upsertedTags) != 0 {
		t.Fatalf("a rejected patch must write no tag, got %v", flm.upsertedTags)
	}
}

// TestPatchBudgetRejectsBadInput — 400 invalid_argument, ACH's envelope.
func TestPatchBudgetRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"negative":      `{"max_budget":-1}`,
		"missing":       `{"budget_duration":"30d"}`,
		"unknown field": `{"max_budget":1,"nope":true}`,
		"malformed":     `{`,
	} {
		t.Run(name, func(t *testing.T) {
			row := suspendableEkRow()
			flm := newCaptureLiteLLM()
			deps := captureAuditDeps(&envKeyStateDB{getRow: row}, flm, nil, &recordRedis{}, &bytes.Buffer{})

			rec := patchBudget(deps, row.KeyID, body, row.OwnerEmail)

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), codeInvalidArgument) {
				t.Fatalf("status/body = %d/%s, want 400 invalid_argument", rec.Code, rec.Body.String())
			}
			if len(flm.upsertedTags) != 0 {
				t.Fatalf("a rejected patch must write no tag, got %v", flm.upsertedTags)
			}
		})
	}
}

// TestPatchBudgetRevokedKeyIs409 — a revoked key has no tag to cap.
func TestPatchBudgetRevokedKeyIs409(t *testing.T) {
	row := suspendableEkRow()
	row.Status = statusRevoked
	flm := newCaptureLiteLLM()
	deps := captureAuditDeps(&envKeyStateDB{getRow: row}, flm, nil, &recordRedis{}, &bytes.Buffer{})

	rec := patchBudget(deps, row.KeyID, `{"max_budget":50}`, row.OwnerEmail)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRevokeDeletesKeyTagAndBudget — a revoked key leaves neither a stale
// budget tag nor its budget object behind (the tag delete orphans it).
func TestRevokeDeletesKeyTagAndBudget(t *testing.T) {
	row := suspendableEkRow()
	revoked := *row
	revoked.Status = statusRevoked
	fdb := &envKeyStateDB{getRow: row, revokeRow: &revoked}
	flm := newCaptureLiteLLM()
	deps := captureAuditDeps(fdb, flm, nil, &recordRedis{}, &bytes.Buffer{})

	r := chi.NewRouter()
	r.Delete("/{key_id}", RevokeHandler(deps))
	req := httptest.NewRequest(http.MethodDelete, "/"+row.KeyID, nil)
	ctx := middleware.WithKeyContext(req.Context(), &keystore.KeyInfo{
		KeyID: "pkid_caller00000000000000000", KeyType: keys.PrefixPk, OwnerEmail: row.OwnerEmail,
	}, false)
	ctx = middleware.WithRequestID(ctx, "req_test")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	tag := "key:" + row.KeyID
	if !flm.deletedTags[tag] {
		t.Errorf("revoke must delete the key tag; deleted = %v", flm.deletedTags)
	}
	if !flm.deletedBudgets[tag] {
		t.Errorf("revoke must delete the key budget; deleted = %v", flm.deletedBudgets)
	}
}
