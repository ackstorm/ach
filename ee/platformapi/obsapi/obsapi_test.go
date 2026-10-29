// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package obsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	eeevidence "github.com/ackstorm/ach/ee/observability/evidence"
	"github.com/ackstorm/ach/internal/edition"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// Deps is what a test hands the routes: nil Reader is ClickHouse not configured.
type Deps struct{ Reader Reader }

type scopeCall struct {
	email   string
	q       eeevidence.TraceQuery
	trace   string
	project string
	w       evidence.Window
}

type fakeReader struct {
	calls     []scopeCall
	listErr   error
	detailErr error
	sessionQ  eeevidence.SessionQuery
}

func (f *fakeReader) Sessions(_ context.Context, s evidence.Scope, q eeevidence.SessionQuery) (eeevidence.SessionPage, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: q.Window})
	f.sessionQ = q
	return eeevidence.SessionPage{Sessions: []eeevidence.SessionSummary{}}, f.listErr
}

func (f *fakeReader) Session(_ context.Context, s evidence.Scope, id, project string, w evidence.Window) (eeevidence.SessionDetail, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), trace: id, project: project, w: w})
	return eeevidence.SessionDetail{SessionID: id, TurnsList: []eeevidence.SessionTurn{}}, f.detailErr
}

func (f *fakeReader) List(_ context.Context, s evidence.Scope, q eeevidence.TraceQuery) (eeevidence.TracePage, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), q: q})
	return eeevidence.TracePage{Traces: []eeevidence.TraceSummary{}}, f.listErr
}

func (f *fakeReader) Detail(_ context.Context, s evidence.Scope, trace, project string, w evidence.Window) (eeevidence.TraceDetail, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), trace: trace, project: project, w: w})
	return eeevidence.TraceDetail{TraceID: trace, Spans: []eeevidence.Span{}}, f.detailErr
}

func (f *fakeReader) Context(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.ContextReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.ContextReport{}, f.listErr
}

func (f *fakeReader) Workloads(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.WorkloadReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.WorkloadReport{}, f.listErr
}

func (f *fakeReader) Cost(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.CostReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.CostReport{}, f.listErr
}

func (f *fakeReader) Latency(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.LatencyReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.LatencyReport{}, f.listErr
}

func (f *fakeReader) Performance(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.PerformanceReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.PerformanceReport{}, f.listErr
}

func (f *fakeReader) Classification(_ context.Context, s evidence.Scope, w evidence.Window) (eeevidence.ClassificationReport, error) {
	f.calls = append(f.calls, scopeCall{email: s.Email(), w: w})
	return eeevidence.ClassificationReport{}, f.listErr
}

func ctxFor(t keys.BearerPrefix, email string) context.Context {
	return middleware.WithKeyContext(context.Background(),
		&keystore.KeyInfo{KeyID: "id", KeyType: t, OwnerEmail: email}, false)
}

func get(t *testing.T, d Deps, path string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	mount(r, d.Reader)
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
	listPath    = "/platform/observability/traces"
	costPath    = "/platform/observability/cost"
	latencyPath = "/platform/observability/latency"
	perfPath    = "/platform/observability/performance"
	classPath   = "/platform/observability/classification"
	contextPath = "/platform/observability/context"
	workPath    = "/platform/observability/workloads"
)

func TestRejectedCallersMakeZeroEvidenceCalls(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no key context (foreign credential passed through)": context.Background(),
		"ek credential": ctxFor(keys.PrefixEk, "a@x"),
	} {
		for _, path := range []string{listPath, listPath + "/t1", sessionsPath, contextPath, workPath, costPath, latencyPath, perfPath, classPath} {
			t.Run(name+path, func(t *testing.T) {
				f := &fakeReader{}
				rec := get(t, Deps{Reader: f}, path, ctx)
				if rec.Code != 401 || len(f.calls) != 0 {
					t.Fatalf("code=%d calls=%d", rec.Code, len(f.calls))
				}
			})
		}
	}
	if rec := get(t, Deps{Reader: &fakeReader{}}, listPath, ctxFor(keys.PrefixEk, "a@x")); code(t, rec) != "invalid_key_type" {
		t.Errorf("ek code = %q", code(t, rec))
	}
}

func TestUserOnlySeesOwnTraces(t *testing.T) {
	f := &fakeReader{}
	for _, email := range []string{"a@x", "b@x"} {
		for _, qs := range []string{"", "?email=b@x", "?scope=all", "?user=a@x"} {
			get(t, Deps{Reader: f}, listPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, listPath+"/t1"+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, costPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, latencyPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, perfPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, classPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, contextPath+qs, ctxFor(keys.PrefixPk, email))
			get(t, Deps{Reader: f}, workPath+qs, ctxFor(keys.PrefixPk, email))
		}
	}
	if len(f.calls) != 64 {
		t.Fatalf("calls = %d", len(f.calls))
	}
	for i, c := range f.calls {
		want := "a@x"
		if i >= 32 {
			want = "b@x"
		}
		if c.email != want {
			t.Fatalf("call %d scope %q, want %q", i, c.email, want)
		}
	}
}

func TestUnconfiguredIsNotReady(t *testing.T) {
	for _, path := range []string{listPath, sessionsPath, contextPath, workPath, costPath, latencyPath, perfPath, classPath} {
		rec := get(t, Deps{}, path, ctxFor(keys.PrefixPk, "a@x"))
		if rec.Code != 503 || code(t, rec) != "not_ready" {
			t.Fatalf("%s: code=%d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestMalformedInputIs400WithoutEvidenceCall(t *testing.T) {
	for _, qs := range []string{"?start=2026-13-01", "?end=nope", "?limit=x", "?cursor=-1", "?start=2026-10-10&end=2026-10-01", "?start=2026-01-01&end=2026-10-01", "?dir=sideways"} {
		f := &fakeReader{}
		rec := get(t, Deps{Reader: f}, listPath+qs, ctxFor(keys.PrefixPk, "a@x"))
		if rec.Code != 400 || code(t, rec) != "invalid_argument" || len(f.calls) != 0 {
			t.Errorf("%s: code=%d calls=%d", qs, rec.Code, len(f.calls))
		}
	}
}

func TestInclusiveEndBecomesExclusiveBoundary(t *testing.T) {
	f := &fakeReader{}
	get(t, Deps{Reader: f}, listPath+"?start=2026-10-01&end=2026-10-03&limit=7&cursor=14&status=error&search=ab&sort=cost&dir=asc", ctxFor(keys.PrefixPk, "a@x"))
	q := f.calls[0].q
	if q.Sort != "cost" || !q.Asc {
		t.Errorf("sort = %q asc=%v", q.Sort, q.Asc)
	}
	if !q.Window.Start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !q.Window.End.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) ||
		q.Window.Timezone != "UTC" || q.Limit != 7 || q.Cursor != 14 || q.Status != "error" || q.Search != "ab" {
		t.Errorf("query = %+v", q)
	}
	// defaults: last 30 calendar days including today, limit 25.
	f = &fakeReader{}
	get(t, Deps{Reader: f}, listPath, ctxFor(keys.PrefixPk, "a@x"))
	q = f.calls[0].q
	if q.Limit != 25 || q.Asc || q.Sort != "" || q.Window.End.Sub(q.Window.Start) != 30*24*time.Hour {
		t.Errorf("defaults = %+v", q)
	}
}

func TestDetailPassesProjectSelector(t *testing.T) {
	f := &fakeReader{}
	get(t, Deps{Reader: f}, listPath+"/t9?project=p2&start=2026-10-01&end=2026-10-02", ctxFor(keys.PrefixPk, "a@x"))
	if c := f.calls[0]; c.trace != "t9" || c.project != "p2" || !c.w.End.Equal(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("call = %+v", c)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{evidence.ErrProjectRequired, 409, "project_required"},
		{evidence.ErrNotFound, 404, "trace_not_found"},
		{evidence.ErrProjectNotAllowed, 404, "trace_not_found"},
		{evidence.ErrScopeMismatch, 503, "not_ready"},
		{evidence.ErrSourceUnavailable, 503, "not_ready"},
		{errors.New("clickhouse says s3cret"), 503, "not_ready"},
		{evidence.ErrInvalidArgument, 400, "invalid_argument"},
	} {
		rec := get(t, Deps{Reader: &fakeReader{detailErr: tc.err}}, listPath+"/t1", ctxFor(keys.PrefixPk, "a@x"))
		if rec.Code != tc.status || code(t, rec) != tc.code {
			t.Errorf("%v: code=%d %s", tc.err, rec.Code, rec.Body)
		}
		if b := rec.Body.String(); len(b) > 0 && contains(b, "s3cret") {
			t.Errorf("upstream text leaked: %s", b)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestListSuccessReturnsPage(t *testing.T) {
	rec := get(t, Deps{Reader: &fakeReader{}}, listPath, ctxFor(keys.PrefixPk, "a@x"))
	var got map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got["traces"] == nil {
		t.Fatalf("code=%d %s", rec.Code, rec.Body)
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
		mount(r, f)
	})
	call := func(hdr map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, listPath+"?email=bob@x", nil)
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

func TestThanosURL(t *testing.T) {
	t.Setenv("ACH_OBSERVABILITY_THANOS_URL", "")
	if got, err := thanosURL(); err != nil || got != "" {
		t.Fatalf("unset = %q %v", got, err)
	}
	t.Setenv("ACH_OBSERVABILITY_THANOS_URL", "http://thanos:10902/")
	if got, err := thanosURL(); err != nil || got != "http://thanos:10902" {
		t.Fatalf("thanos = %q %v", got, err)
	}
	t.Setenv("ACH_OBSERVABILITY_THANOS_URL", "thanos:10902")
	if _, err := thanosURL(); err == nil {
		t.Fatal("non-http thanos URL must be refused")
	}
}

func TestHookDisabledAnswersEnterpriseOnlyOnEveryRoute(t *testing.T) {
	on, off := chi.NewRouter(), chi.NewRouter()
	mountOn, err := Hook(edition.PlatformAPIEnv{Enterprise: true})
	if err != nil {
		t.Fatal(err)
	}
	mountOn(on)
	mountOff, err := Hook(edition.PlatformAPIEnv{})
	if err != nil {
		t.Fatal(err)
	}
	mountOff(off)
	n := 0
	_ = chi.Walk(on, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		n++
		rec := httptest.NewRecorder()
		off.ServeHTTP(rec, httptest.NewRequest(method, strings.NewReplacer("{trace_id}", "t1", "{session_id}", "s1").Replace(route), nil))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "enterprise_only") {
			t.Errorf("%s %s disabled = %d", method, route, rec.Code)
		}
		return nil
	})
	if n != 10 {
		t.Fatalf("walked %d routes, want 10", n)
	}
}

const sessionsPath = "/platform/observability/sessions"

func TestSessionsParsePagingAndPassProject(t *testing.T) {
	f := &fakeReader{}
	rec := get(t, Deps{Reader: f}, sessionsPath+"?start=2026-10-01&end=2026-10-03&limit=7&cursor=14&sort=turns&dir=asc", ctxFor(keys.PrefixPk, "a@x"))
	q := f.sessionQ
	if rec.Code != 200 || q.Limit != 7 || q.Cursor != 14 || q.Sort != "turns" || !q.Asc || !q.Window.End.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("code=%d query=%+v", rec.Code, q)
	}
	for _, qs := range []string{"?limit=x", "?cursor=-1", "?dir=up", "?end=nope"} {
		f := &fakeReader{}
		if rec := get(t, Deps{Reader: f}, sessionsPath+qs, ctxFor(keys.PrefixPk, "a@x")); rec.Code != 400 || len(f.calls) != 0 {
			t.Errorf("%s: code=%d calls=%d", qs, rec.Code, len(f.calls))
		}
	}
	f = &fakeReader{}
	get(t, Deps{Reader: f}, sessionsPath+"/s-1?project=p2", ctxFor(keys.PrefixPk, "a@x"))
	if c := f.calls[0]; c.trace != "s-1" || c.project != "p2" || c.email != "a@x" {
		t.Errorf("detail call = %+v", c)
	}
	// Rejected callers never reach the reader.
	f = &fakeReader{}
	if rec := get(t, Deps{Reader: f}, sessionsPath, ctxFor(keys.PrefixEk, "a@x")); rec.Code != 401 || len(f.calls) != 0 {
		t.Errorf("ek caller: code=%d calls=%d", rec.Code, len(f.calls))
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
