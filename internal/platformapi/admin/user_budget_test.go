// SPDX-License-Identifier: Apache-2.0

package admin

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

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// budgetLiteLLM captures the tag budgets the admin user-budget route
// writes; every other Client method comes from NoopClient.
type budgetLiteLLM struct {
	*litellm.NoopClient
	tags      map[string]litellm.TagBudget
	upsertErr error
}

func newBudgetLiteLLM() *budgetLiteLLM {
	return &budgetLiteLLM{NoopClient: &litellm.NoopClient{}, tags: map[string]litellm.TagBudget{}}
}

func (c *budgetLiteLLM) UpsertTagBudget(_ context.Context, name string, b litellm.TagBudget) error {
	if c.upsertErr != nil {
		return c.upsertErr
	}
	c.tags[name] = b
	return nil
}

// newBudgetDeps wires the minimum admin.Deps the user-budget route needs:
// LiteLLM (the only side effect), the allowlist AdminOnly consults, and an
// audit sink the tests read back.
func newBudgetDeps(flm litellm.Client, auditBuf *bytes.Buffer) Deps {
	return Deps{
		LiteLLM:   flm,
		Allowlist: map[string]struct{}{"admin@x.example": {}},
		Audit:     slog.New(slog.NewTextHandler(auditBuf, nil)),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace: "ach-test",
	}
}

// patchUserBudget drives PATCH /platform/admin/users/{email}/budget through
// the real Mount, so AdminOnly gates the request exactly as in production.
func patchUserBudget(deps Deps, emailPath, body, callerEmail string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/platform/admin", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := middleware.WithRequestID(req.Context(), "req_test")
				ctx = middleware.WithKeyContext(ctx, &keystore.KeyInfo{
					KeyID:      "pkid_caller00000000000000000",
					KeyType:    keys.PrefixPk,
					OwnerEmail: callerEmail,
				}, false)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		Mount(deps)(r)
	})

	req := httptest.NewRequest(http.MethodPatch,
		"/platform/admin/users/"+emailPath+"/budget", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAdminUserBudgetWritesUserTag — the happy path: the ceiling lands on
// the caller-named user's "user:<email>" tag, 204, and it is audited. The
// email carries both '@' and '.', percent-encoded as a CLI would send it.
func TestAdminUserBudgetWritesUserTag(t *testing.T) {
	flm := newBudgetLiteLLM()
	var buf bytes.Buffer

	rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob%40x.example",
		`{"max_budget":42,"budget_duration":"30d"}`, "admin@x.example")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	got, ok := flm.tags["user:bob@x.example"]
	if !ok {
		t.Fatalf("no budget on user:bob@x.example; wrote %v", flm.tags)
	}
	if got.MaxBudget != 42 || got.BudgetDuration != "30d" {
		t.Fatalf("tag budget = %+v", got)
	}
	for _, want := range []string{"platform.admin.user.budget", "updated", "bob@x.example"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("audit log missing %q: %s", want, buf.String())
		}
	}
}

// TestAdminUserBudgetUnencodedEmail — chi matches a bare "bob@x.example"
// path segment too; '@' and '.' need no escaping to route.
func TestAdminUserBudgetUnencodedEmail(t *testing.T) {
	flm := newBudgetLiteLLM()
	var buf bytes.Buffer

	rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob@x.example", `{"max_budget":1}`, "admin@x.example")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := flm.tags["user:bob@x.example"]; !ok {
		t.Fatalf("wrote %v", flm.tags)
	}
}

// TestAdminUserBudgetZeroIsAValidCeiling — 0 means "refuse everything",
// which is why the request field is a *float64 (§15).
func TestAdminUserBudgetZeroIsAValidCeiling(t *testing.T) {
	flm := newBudgetLiteLLM()
	var buf bytes.Buffer

	rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob%40x.example", `{"max_budget":0}`, "admin@x.example")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := flm.tags["user:bob@x.example"]; !ok || got.MaxBudget != 0 {
		t.Fatalf("tag budget = %+v (ok=%v)", got, ok)
	}
}

// TestAdminUserBudgetNonAdminIsDenied — a non-allowlisted pk_ cannot write
// another person's ceiling, writes no tag, and the denial is audited.
func TestAdminUserBudgetNonAdminIsDenied(t *testing.T) {
	flm := newBudgetLiteLLM()
	var buf bytes.Buffer

	rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob%40x.example", `{"max_budget":42}`, "stranger@x.example")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if len(flm.tags) != 0 {
		t.Fatalf("a denied patch must write no tag, got %v", flm.tags)
	}
	if !strings.Contains(buf.String(), "not_admin") {
		t.Errorf("denial not audited: %s", buf.String())
	}
}

// TestAdminUserBudgetRejectsBadInput — same rule and envelope as the ek_
// route: max_budget required and >= 0, unknown fields refused.
func TestAdminUserBudgetRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"negative":      `{"max_budget":-1}`,
		"missing":       `{"budget_duration":"30d"}`,
		"unknown field": `{"max_budget":1,"nope":true}`,
		"malformed":     `{`,
	} {
		t.Run(name, func(t *testing.T) {
			flm := newBudgetLiteLLM()
			var buf bytes.Buffer

			rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob%40x.example", body, "admin@x.example")

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_argument") {
				t.Fatalf("status/body = %d/%s, want 400 invalid_argument", rec.Code, rec.Body.String())
			}
			if len(flm.tags) != 0 {
				t.Fatalf("a rejected patch must write no tag, got %v", flm.tags)
			}
		})
	}
}

// TestAdminUserBudgetUpstreamErrors — a LiteLLM 4xx (a malformed
// budget_duration, say) is 502 litellm_rejected; a transport failure is
// 503 litellm_unreachable. Same mapping as the ek_ route.
func TestAdminUserBudgetUpstreamErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"rejected":    {&litellm.APIError{StatusCode: 400}, http.StatusBadGateway},
		"unreachable": {errors.New("dial tcp: connection refused"), http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			flm := newBudgetLiteLLM()
			flm.upsertErr = tc.err
			var buf bytes.Buffer

			rec := patchUserBudget(newBudgetDeps(flm, &buf), "bob%40x.example", `{"max_budget":42}`, "admin@x.example")

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(buf.String(), "platform.admin.user.budget") {
				t.Errorf("failure not audited: %s", buf.String())
			}
		})
	}
}
