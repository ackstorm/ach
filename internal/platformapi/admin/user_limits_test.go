// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

type limitsRecorder struct {
	called  bool
	email   string
	maxKeys int
	err     error
}

func newLimitsDeps(rec *limitsRecorder, auditBuf *bytes.Buffer) Deps {
	return Deps{
		Allowlist: map[string]struct{}{"admin@x.example": {}},
		Audit:     slog.New(slog.NewTextHandler(auditBuf, nil)),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace: "ach-test",
		SetMaxKeys: func(_ context.Context, email string, n int) error {
			rec.called, rec.email, rec.maxKeys = true, email, n
			return rec.err
		},
	}
}

func patchUserLimits(deps Deps, emailPath, body, callerEmail string) *httptest.ResponseRecorder {
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
		"/platform/admin/users/"+emailPath+"/limits", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAdminUserLimitsSetsCeiling(t *testing.T) {
	rec := &limitsRecorder{}
	rr := patchUserLimits(newLimitsDeps(rec, &bytes.Buffer{}), "u%40x.com", `{"max_keys":5}`, "admin@x.example")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if rec.email != "u@x.com" || rec.maxKeys != 5 {
		t.Fatalf("want u@x.com=5, got %q=%d", rec.email, rec.maxKeys)
	}
}

func TestAdminUserLimitsZeroIsAValidCeiling(t *testing.T) {
	rec := &limitsRecorder{}
	rr := patchUserLimits(newLimitsDeps(rec, &bytes.Buffer{}), "u@x.example", `{"max_keys":0}`, "admin@x.example")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("0 is a real ceiling: want 204, got %d", rr.Code)
	}
	if !rec.called || rec.maxKeys != 0 {
		t.Fatalf("want an explicit 0 recorded, called=%v got %d", rec.called, rec.maxKeys)
	}
}

func TestAdminUserLimitsRejectsBadInput(t *testing.T) {
	for _, body := range []string{`{"max_keys":-1}`, `{}`, `{"max_keys":1,"extra":true}`, `not json`} {
		rec := &limitsRecorder{}
		rr := patchUserLimits(newLimitsDeps(rec, &bytes.Buffer{}), "u@x.example", body, "admin@x.example")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: want 400, got %d", body, rr.Code)
		}
		if rec.called {
			t.Fatalf("body %s: nothing may be written on a refused request", body)
		}
	}
}

func TestAdminUserLimitsNonAdminIsDenied(t *testing.T) {
	rec := &limitsRecorder{}
	rr := patchUserLimits(newLimitsDeps(rec, &bytes.Buffer{}), "u@x.example", `{"max_keys":5}`, "nobody@x.example")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 not_admin, got %d: %s", rr.Code, rr.Body.String())
	}
	if rec.called {
		t.Fatalf("a denied caller must not reach the write")
	}
}

func TestAdminUserLimitsWriteFailureIs500(t *testing.T) {
	rec := &limitsRecorder{err: context.DeadlineExceeded}
	rr := patchUserLimits(newLimitsDeps(rec, &bytes.Buffer{}), "u@x.example", `{"max_keys":5}`, "admin@x.example")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rr.Code)
	}
}
