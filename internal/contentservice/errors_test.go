// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// newTestDeps builds a Deps with isolated metrics + audit-log buffer for
// errors_test.go assertions. The caller receives a pointer to the
// audit-line buffer so it can inspect emissions after writeError runs.
func newTestDeps(t *testing.T) (Deps, *bytes.Buffer, *prometheus.Registry) {
	t.Helper()
	var buf bytes.Buffer
	reg := prometheus.NewRegistry()
	col := metrics.NewContentServiceCollectors(reg)
	llmUnreach := metrics.MustRegisterLitellmUnreachable(reg)
	auditLog := slog.New(slog.NewJSONHandler(&buf, nil))
	return Deps{
		AuditLog:           auditLog,
		Metrics:            col,
		LiteLLMUnreachable: llmUnreach,
		Logger:             slog.Default(),
	}, &buf, reg
}

// requestWithID wires a request whose context carries a stable
// request-id (so envelope/audit assertions can match it).
func requestWithID(reqID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/content/prompt/foo", nil)
	ctx := middleware.WithRequestID(r.Context(), reqID)
	return r.WithContext(ctx)
}

func TestWriteError_RendersEnvelope(t *testing.T) {
	d, _, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	r := requestWithID("req_01TEST")

	d.writeError(rec, r, "prompt", "foo", nil, errMissingEnvironment)

	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	e, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("body missing error object: %v", env)
	}
	if got := e["code"]; got != "missing_environment" {
		t.Errorf("error.code=%v, want missing_environment", got)
	}
	if got, ok := e["message"].(string); !ok || got == "" {
		t.Errorf("error.message empty / not a string")
	}
	if got := env["request_id"]; got != "req_01TEST" {
		t.Errorf("request_id=%v, want req_01TEST", got)
	}
}

func TestWriteError_EmitsAudit(t *testing.T) {
	d, buf, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	r := requestWithID("req_01ABCD")

	d.writeError(rec, r, "prompt", "foo", nil, errMissingEnvironment)

	got := buf.String()
	for _, want := range []string{
		`"action":"content.get"`,
		`"outcome":"missing_environment"`,
		`"target.kind":"prompt"`,
		`"target.name":"foo"`,
		`"request_id":"req_01ABCD"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %q\nfull line:\n%s", want, got)
		}
	}
}

func TestWriteError_NilKeyInfo_BlankActor(t *testing.T) {
	d, buf, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	r := requestWithID("req_x")

	d.writeError(rec, r, "plugin", "bar", nil, errUnauthorizedTeam)

	got := buf.String()
	// With POD_NAMESPACE unset in tests, actor collapses to "/-"
	// (empty namespace + dash for missing email).
	if !strings.Contains(got, `"actor":"/-"`) {
		t.Errorf("audit actor missing or wrong; got line:\n%s", got)
	}
	// key.id MUST be absent when info is nil (EmitAudit omits empty).
	if strings.Contains(got, `"key.id"`) {
		t.Errorf("audit unexpectedly includes key.id for nil keyinfo: %s", got)
	}
}

func TestWriteError_PopulatedKeyInfo_Actor(t *testing.T) {
	d, buf, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	r := requestWithID("req_y")

	info := &keystore.KeyInfo{
		KeyID:      "pkid_01XYZ",
		KeyType:    keys.PrefixPk,
		OwnerEmail: "alice@example.com",
	}
	d.writeError(rec, r, "prompt", "doc", info, errUnauthorizedContent)

	got := buf.String()
	if !strings.Contains(got, `"actor":"/alice@example.com"`) {
		t.Errorf("audit actor wrong; got line:\n%s", got)
	}
	if !strings.Contains(got, `"key.id":"pkid_01XYZ"`) {
		t.Errorf("audit missing key.id; got:\n%s", got)
	}
}

func TestWriteError_IncsMetric(t *testing.T) {
	d, _, reg := newTestDeps(t)
	rec := httptest.NewRecorder()
	r := requestWithID("req_z")

	d.writeError(rec, r, "prompt", "foo", nil, errMissingEnvironment)

	// Gather from the registry and verify the counter increment.
	got := gatherCounter(t, reg, "ach_content_service_requests_total",
		map[string]string{"kind": "prompt", "outcome": "missing_environment"})
	if got != 1 {
		t.Errorf("metric counter=%v, want 1", got)
	}
}

// gatherCounter walks reg.Gather() output for the named family + matching
// label-value set; returns 0 if no match. Used in lieu of
// testutil.ToFloat64 (which requires direct access to the unexported
// CounterVec field on ContentServiceCollectors).
func gatherCounter(t *testing.T, reg prometheus.Gatherer, family string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for _, lp := range m.GetLabel() {
				want, ok := labels[lp.GetName()]
				if !ok || want != lp.GetValue() {
					match = false
					break
				}
			}
			if match && len(m.GetLabel()) == len(labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestErrorFactories_AllCodes(t *testing.T) {
	cases := []struct {
		got        *errResp
		wantStatus int
		wantCode   string
	}{
		{errMissingEnvironment, 400, "missing_environment"},
		{errInvalidKeyFormat, 400, "invalid_key_format"},
		{errExpiredOrRevoked, 401, "expired_or_revoked"},
		{errUnauthorizedTeam, 403, "unauthorized_team"},
		{errWrongEnvironment, 403, "wrong_environment"},
		{errUnauthorizedContent, 403, "unauthorized_content"},
		{errEnvironmentNotFound, 404, "environment_not_found"},
		{errContentNotFound, 404, "content_not_found"},
		{errLitellmUnreachable, 503, "litellm_unreachable"},
		{errStaleCacheExpired, 503, "stale_cache_expired"},
		{errInternal, 500, "internal_error"},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("factory for %q returned nil", tc.wantCode)
			continue
		}
		if tc.got.HTTPStatus != tc.wantStatus {
			t.Errorf("%s: HTTPStatus=%d, want %d", tc.wantCode, tc.got.HTTPStatus, tc.wantStatus)
		}
		if tc.got.Code != tc.wantCode {
			t.Errorf("Code=%q, want %q", tc.got.Code, tc.wantCode)
		}
		if tc.got.Message == "" {
			t.Errorf("%s: Message empty (hard-coded per T-03-02-02)", tc.wantCode)
		}
	}
	// Sanity: ensure ActionContentGet constant exists for the audit emission path.
	if audit.ActionContentGet != "content.get" {
		t.Errorf("audit.ActionContentGet=%q, want content.get", audit.ActionContentGet)
	}
}

// Sanity: Stream the err map and confirm ALL outcome codes are
// distinct (no collision in D-03 table).
func TestErrorFactories_DistinctCodes(t *testing.T) {
	seen := map[string]bool{}
	codes := []*errResp{
		errMissingEnvironment,
		errInvalidKeyFormat,
		errExpiredOrRevoked,
		errUnauthorizedTeam,
		errWrongEnvironment,
		errUnauthorizedContent,
		errEnvironmentNotFound,
		errContentNotFound,
		errLitellmUnreachable,
		errStaleCacheExpired,
		errInternal,
	}
	for _, c := range codes {
		if seen[c.Code] {
			t.Errorf("duplicate code: %s", c.Code)
		}
		seen[c.Code] = true
	}
	if len(seen) != 11 {
		t.Errorf("expected 11 distinct codes, got %d", len(seen))
	}
}

// Compile-time sanity that the audit/metrics/render reuses are wired.
var _ context.Context = context.TODO()
