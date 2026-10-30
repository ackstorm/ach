// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

func prodEnv() *db.EnvironmentRow {
	return &db.EnvironmentRow{Namespace: "ach", Name: "prod", AuthorizedTeams: []string{"default"}}
}

func newMintReq(deps Deps, rec *httptest.ResponseRecorder, action string) *createReq {
	return &createReq{
		deps: deps, w: rec, ctx: context.Background(), action: action, extra: map[string]string{"k": "v"},
		req:    CreateRequest{Environment: "prod", Name: "k"},
		keyCtx: middleware.KeyContext{OwnerEmail: "user@example.com"},
		target: &audit.Target{Kind: "environment", Name: "prod"},
	}
}

// TestMint_LeavesResponseAndAuditToCaller pins the seam POST /platform/keys
// and POST /platform/keys/exchange both build on.
func TestMint_LeavesResponseAndAuditToCaller(t *testing.T) {
	fdb := &fakeEkDB{}
	deps, _ := newCreateDeps(fdb)
	var auditBuf bytes.Buffer
	deps.Audit = slog.New(slog.NewJSONHandler(&auditBuf, nil))
	rec := httptest.NewRecorder()

	keyID, plaintext, ok := newMintReq(deps, rec, audit.ActionEkCreate).mint(prodEnv(), "llu-user@example.com")
	if !ok || !strings.HasPrefix(keyID, "ekid_") || !strings.HasPrefix(plaintext, "ek-") {
		t.Fatalf("mint = %q, %q, %v", keyID, plaintext, ok)
	}
	if fdb.inserted == nil || fdb.inserted.KeyID != keyID {
		t.Fatalf("inserted = %+v", fdb.inserted)
	}
	if rec.Body.Len() != 0 || auditBuf.Len() != 0 {
		t.Fatalf("mint wrote on success: body=%q audit=%q", rec.Body.String(), auditBuf.String())
	}
}

func TestMint_FailureAuditsWithCallerAction(t *testing.T) {
	deps, flm := newCreateDeps(&fakeEkDB{})
	flm.teams = []litellm.TeamListEntry{{TeamID: "team-uuid-default", TeamAlias: "default"}} // no ach-env-prod shell
	var auditBuf bytes.Buffer
	deps.Audit = slog.New(slog.NewJSONHandler(&auditBuf, nil))
	rec := httptest.NewRecorder()

	if _, _, ok := newMintReq(deps, rec, "platform.ek.test").mint(prodEnv(), "u"); ok {
		t.Fatal("mint succeeded without a shell team")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(auditBuf.String(), `"action":"platform.ek.test"`) || !strings.Contains(auditBuf.String(), `"k":"v"`) {
		t.Fatalf("audit does not carry the caller's action and extra: %s", auditBuf.String())
	}
}
