// SPDX-License-Identifier: Apache-2.0

package obsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

type scopeCall struct {
	email string
	w     evidence.Window
}

// fakeReader stands in for an edition's evidence reader: the core package
// mounts no route, so these tests serve two reports through Report and check
// the shared guard, window and error mapping.
type fakeReader struct {
	calls   []scopeCall
	listErr error
}

type stubReport struct{}

func (f *fakeReader) Context(_ context.Context, s evidence.Scope, w evidence.Window) (stubReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return stubReport{}, f.listErr
}

func (f *fakeReader) Workloads(_ context.Context, s evidence.Scope, w evidence.Window) (stubReport, error) {
	return f.Context(context.Background(), s, w)
}

// Deps is what a test hands the routes: nil Reader is ClickHouse not configured.
type Deps struct{ Reader *fakeReader }

func Mount(r chi.Router, d Deps) {
	var get func(context.Context, evidence.Scope, evidence.Window) (stubReport, error)
	var work func(context.Context, evidence.Scope, evidence.Window) (stubReport, error)
	if d.Reader != nil {
		get, work = d.Reader.Context, d.Reader.Workloads
	}
	r.Get(contextPath, Report(get, d.Reader != nil))
	r.Get(workloadsPath, Report(work, d.Reader != nil))
}

func ctxFor(t keys.BearerPrefix, email string) context.Context {
	return middleware.WithKeyContext(context.Background(),
		&keystore.KeyInfo{KeyID: "id", KeyType: t, OwnerEmail: email}, false)
}

func get(t *testing.T, d Deps, path string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	Mount(r, d)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Code string } `json:"error"`
		Code  string                `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != "" {
		return b.Error.Code
	}
	return b.Code
}

const (
	contextPath   = "/platform/observability/context"
	workloadsPath = "/platform/observability/workloads"
)

func TestRejectedCallersMakeZeroEvidenceCalls(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no key context (foreign credential passed through)": context.Background(),
		"ek credential": ctxFor(keys.PrefixEk, "a@x"),
	} {
		for _, path := range []string{contextPath, workloadsPath} {
			t.Run(name+path, func(t *testing.T) {
				f := &fakeReader{}
				rec := get(t, Deps{Reader: f}, path, ctx)
				if rec.Code != 401 || len(f.calls) != 0 {
					t.Fatalf("code=%d calls=%d", rec.Code, len(f.calls))
				}
			})
		}
	}
	if rec := get(t, Deps{Reader: &fakeReader{}}, contextPath, ctxFor(keys.PrefixEk, "a@x")); code(t, rec) != "invalid_key_type" {
		t.Errorf("ek code = %q", code(t, rec))
	}
}

func TestUserOnlySeesOwnEvidence(t *testing.T) {
	f := &fakeReader{}
	for _, email := range []string{"a@x", "b@x"} {
		for _, qs := range []string{"", "?email=b@x", "?scope=all", "?user=a@x"} {
			get(t, Deps{Reader: f}, contextPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, workloadsPath+qs, ctxFor(keys.PrefixPk, email))
		}
	}
	if len(f.calls) != 16 {
		t.Fatalf("calls = %d", len(f.calls))
	}
	for i, c := range f.calls {
		want := "a@x"
		if i >= 8 {
			want = "b@x"
		}
		if c.email != want {
			t.Fatalf("call %d scope %q, want %q", i, c.email, want)
		}
	}
}

func TestUnconfiguredIsNotReady(t *testing.T) {
	for _, path := range []string{contextPath, workloadsPath} {
		rec := get(t, Deps{}, path, ctxFor(keys.PrefixPk, "a@x"))
		if rec.Code != 503 || code(t, rec) != "not_ready" {
			t.Fatalf("%s: code=%d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestMalformedInputIs400WithoutEvidenceCall(t *testing.T) {
	for _, qs := range []string{"?start=2026-13-01", "?end=nope", "?start=2026-10-10&end=2026-10-01", "?start=2026-01-01&end=2026-10-01"} {
		f := &fakeReader{}
		rec := get(t, Deps{Reader: f}, contextPath+qs, ctxFor(keys.PrefixPk, "a@x"))
		if rec.Code != 400 || code(t, rec) != "invalid_argument" || len(f.calls) != 0 {
			t.Errorf("%s: code=%d calls=%d", qs, rec.Code, len(f.calls))
		}
	}
}

type resolverFunc func(string) (*keystore.KeyInfo, error)

func (f resolverFunc) Resolve(_ context.Context, p string) (*keystore.KeyInfo, error) { return f(p) }

// Through the real Authn middleware, as server.go mounts it: the credential is
// resolved, never trusted from the request, and only a pk_ reaches evidence.
func TestThroughRealAuthn(t *testing.T) {
	// ports: none — exercise the real Authn chain, not an injected context
	res := resolverFunc(func(p string) (*keystore.KeyInfo, error) {
		switch p {
		case "pk-ana":
			return &keystore.KeyInfo{KeyID: "1", KeyType: keys.PrefixPk, OwnerEmail: "ana@x"}, nil
		case "ek-ana":
			return &keystore.KeyInfo{KeyID: "2", KeyType: keys.PrefixEk, OwnerEmail: "ana@x"}, nil
		}
		return nil, nil // unknown, revoked or expired: Authn answers 401
	})
	f := &fakeReader{}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authn(res, nil, nil, middleware.AuthnOptions{
			Headers: []middleware.CredentialHeader{{Name: "x-ach-key", Mode: middleware.ModeResolve}},
		}))
		Mount(r, Deps{Reader: f})
	})
	call := func(hdr map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, contextPath+"?email=bob@x", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for name, hdr := range map[string]map[string]string{
		"none":    nil,
		"unknown": {"x-ach-key": "pk-nobody"},
		"ek":      {"x-ach-key": "ek-ana"},
		"foreign": {"Authorization": "Bearer sk-litellm-raw"},
	} {
		if got := call(hdr); got != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, got)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("rejected callers reached evidence: %+v", f.calls)
	}
	if got := call(map[string]string{"x-ach-key": "pk-ana"}); got != http.StatusOK {
		t.Fatalf("pk: status %d", got)
	}
	if len(f.calls) != 1 || f.calls[0].email != "ana@x" {
		t.Errorf("calls = %+v, want one scoped to ana@x", f.calls)
	}
}

func TestContextWindowAndErrors(t *testing.T) {
	f := &fakeReader{}
	rec := get(t, Deps{Reader: f}, contextPath+"?start=2026-10-01&end=2026-10-07", ctxFor(keys.PrefixPk, "a@x"))
	if rec.Code != 200 || len(f.calls) != 1 || !f.calls[0].w.End.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("code=%d calls=%+v", rec.Code, f.calls)
	}
	if rec := get(t, Deps{Reader: f}, contextPath+"?start=bad", ctxFor(keys.PrefixPk, "a@x")); rec.Code != 400 || len(f.calls) != 1 {
		t.Errorf("bad date: code=%d calls=%d", rec.Code, len(f.calls))
	}
	if rec := get(t, Deps{Reader: &fakeReader{listErr: evidence.ErrSourceUnavailable}}, contextPath, ctxFor(keys.PrefixPk, "a@x")); rec.Code != 503 || strings.Contains(rec.Body.String(), "source unavailable:") {
		t.Errorf("source error: code=%d", rec.Code)
	}
}
