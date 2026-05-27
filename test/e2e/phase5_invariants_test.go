//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 5 invariants — Plan 05-08.
//
// Live-cluster verification of ROADMAP Phase 5 SC#1..#5:
//   SC1 ContentSendfile        — sendfile(2) + identity transfer + header invariants
//   SC2 ErrorMatrix            — 9 §15.5 error envelopes (drift flag #2 lock-in)
//   SC3 PluginPrecedence       — §12.3 CTE (CRD wins, alphabetically-lowest mkt)
//   SC4 StalenessAndRename     — D-04 step 7 staleness + D-02 in-flight rename
//   SC5 MetricsTopology        — §18.5 metric set across operator/forwarder/CS/PAPI
//
// Stdlib testing only — no Ginkgo. Mirrors phase4_invariants_test.go.
//
// Run via:
//   ACH_E2E_PHASE5=1 \
//     ACH_CONTENT_SERVICE_URL=http://localhost:8082 \
//     ACH_PLATFORM_API_URL=http://localhost:8080 \
//     ACH_FORWARDER_URL=http://localhost:8081 \
//     ACH_OPERATOR_METRICS_URL=http://localhost:8083/metrics \
//     ACH_E2E_PK_FIXTURE=pk_... ACH_E2E_EK_FIXTURE_PROD=ek_... \
//     ./scripts/dev.sh make e2e-focus RUN='TestPhase5Invariants'

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// errEnvelope is the §15.5 error response shape returned by the
// Content Service handler for all 4xx/5xx paths.
type errEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// reqIDPattern is the ULID-style request_id format. CS handler emits
// `req_<26-char-base32>` per §15.5 + the audit handler convention.
var reqIDPattern = regexp.MustCompile(`^req_[A-Z0-9]{26}$`)

func TestPhase5Invariants(t *testing.T) {
	phase5SuiteGuard(t)
	t.Run("SC1_ContentSendfile", testPhase5SC1ContentSendfile)
	t.Run("SC2_ErrorMatrix", testPhase5SC2ErrorMatrix)
	t.Run("SC3_PluginPrecedence", testPhase5SC3PluginPrecedence)
	t.Run("SC4_StalenessAndRename", testPhase5SC4StalenessAndInFlightRename)
	t.Run("SC5_MetricsTopology", testPhase5SC5MetricsTopology)
}

// testPhase5SC1ContentSendfile verifies the D-01 streaming discipline:
// sendfile(2) zero-copy, identity transfer, no http.ServeContent
// Range/If-* honoring, CS-06 header set verbatim.
func testPhase5SC1ContentSendfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pk, _, env := seedPhase5Fixtures(t, ctx)

	// Sendfile syscall assertion (live via strace under kubectl debug).
	if !straceCSSendfile(t, ctx, "/content/plugin/foo", pk, env) {
		t.Fatalf("expected ≥1 sendfile/sendfile64 syscall during CS GET — none observed (CS-06 zero-copy violated)")
	}

	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/")
	baseReq := func(t *testing.T, extraHeaders map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, csURL+"/content/plugin/foo", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("x-ach-key", pk)
		req.Header.Set("x-ach-environment", env)
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		return resp
	}

	t.Run("HeadersAndIdentityTransfer", func(t *testing.T) {
		resp := baseReq(t, nil)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d want=200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
			t.Errorf("Content-Type=%q want=application/gzip", ct)
		}
		if cl := resp.Header.Get("Content-Length"); cl == "" {
			t.Errorf("Content-Length missing")
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control=%q want=no-store (drift flag #3 lock-in)", cc)
		}
		if te := resp.Header.Get("Transfer-Encoding"); te != "" {
			t.Errorf("Transfer-Encoding=%q want=empty (identity transfer per CS-06)", te)
		}
		if len(body) == 0 {
			t.Errorf("body empty")
		}
	})

	t.Run("RangeHeaderIgnored", func(t *testing.T) {
		// D-01: CS ignores Range — always serves 200 with full body, never 206.
		resp := baseReq(t, map[string]string{"Range": "bytes=0-99"})
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d want=200 (Range MUST be ignored, not honored as 206)", resp.StatusCode)
		}
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			t.Errorf("Content-Range=%q want=empty (Range MUST NOT be acknowledged)", cr)
		}
	})

	t.Run("IfNoneMatchIgnored", func(t *testing.T) {
		resp := baseReq(t, map[string]string{"If-None-Match": "*"})
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d want=200 (If-None-Match MUST be ignored, never 304)", resp.StatusCode)
		}
	})

	t.Run("IfModifiedSinceIgnored", func(t *testing.T) {
		resp := baseReq(t, map[string]string{"If-Modified-Since": "Wed, 21 Oct 2099 07:28:00 GMT"})
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d want=200 (If-Modified-Since MUST be ignored)", resp.StatusCode)
		}
	})
}

// testPhase5SC2ErrorMatrix verifies the 9 §15.5 error envelopes per
// the D-03 outcome table. Drift flag #2 lock-in: UnauthorizedContent
// MUST be 403, never 404, even when the named resource has no
// backing CRD — the cheaper authz check runs FIRST.
func testPhase5SC2ErrorMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pk, ek, env := seedPhase5Fixtures(t, ctx)
	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/")

	type call struct {
		name           string
		path           string
		key            string
		envHeader      string
		wantStatus     int
		wantCode       string
		extraHeaders   map[string]string
		skipReason     string
	}

	calls := []call{
		{
			name:       "MissingEnvironment",
			path:       "/content/plugin/foo",
			key:        pk,
			envHeader:  "", // explicit empty — header NOT set below
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_environment",
		},
		{
			name:       "InvalidKeyFormat_NoPrefix",
			path:       "/content/plugin/foo",
			key:        "garbage-no-prefix",
			envHeader:  env,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_key_format",
		},
		{
			name:       "InvalidKeyFormat_Empty",
			path:       "/content/plugin/foo",
			key:        "",
			envHeader:  env,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_key_format",
		},
		{
			name:       "ExpiredOrRevoked",
			path:       "/content/plugin/foo",
			key:        "pk_DEADBEEFDEADBEEFDEADBEEFDEADBEEF",
			envHeader:  env,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "expired_or_revoked",
		},
		{
			name:       "UnauthorizedTeam",
			path:       "/content/plugin/foo",
			key:        pk,
			envHeader:  env,
			wantStatus: http.StatusForbidden,
			wantCode:   "unauthorized_team",
			skipReason: "LiteLLM team-removal not scriptable in E2E harness — covered by integration test in Plan 05-05",
		},
		{
			name:       "WrongEnvironment",
			path:       "/content/plugin/foo",
			key:        ek, // ek bound to env=prod
			envHeader:  "staging",
			wantStatus: http.StatusForbidden,
			wantCode:   "wrong_environment",
		},
		{
			// Drift flag #2 lock-in: the name "forbidden-name" does NOT
			// exist as a Plugin CRD AND is NOT in env.context.plugins.
			// The cheaper authz check (env.context membership) fires
			// FIRST and returns 403 unauthorized_content. A naive
			// implementation would resolve-then-authz and return 404
			// content_not_found, which would leak existence.
			name:       "UnauthorizedContent",
			path:       "/content/plugin/forbidden-name",
			key:        pk,
			envHeader:  env,
			wantStatus: http.StatusForbidden,
			wantCode:   "unauthorized_content",
		},
		{
			name:       "EnvironmentNotFound",
			path:       "/content/plugin/foo",
			key:        pk,
			envHeader:  "nonexistent-env",
			wantStatus: http.StatusNotFound,
			wantCode:   "environment_not_found",
		},
		{
			name:       "ContentNotFound",
			path:       "/content/plugin/foo",
			key:        pk,
			envHeader:  env,
			wantStatus: http.StatusNotFound,
			wantCode:   "content_not_found",
			skipReason: "requires env.context to name a Plugin whose CRD doesn't exist; pre-seed step not in current fixture set — engineer-pending",
		},
	}

	for _, c := range calls {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.skipReason != "" {
				t.Skipf("%s", c.skipReason)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, csURL+c.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if c.key != "" {
				req.Header.Set("x-ach-key", c.key)
			}
			if c.envHeader != "" {
				req.Header.Set("x-ach-environment", c.envHeader)
			}
			for k, v := range c.extraHeaders {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, c.wantStatus, strings.TrimSpace(string(body)))
			}
			var env errEnvelope
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("json.Unmarshal: %v body=%s", err, strings.TrimSpace(string(body)))
			}
			if env.Error.Code != c.wantCode {
				t.Errorf("error.code=%q want=%q", env.Error.Code, c.wantCode)
			}
			if !reqIDPattern.MatchString(env.RequestID) {
				t.Errorf("request_id=%q does not match ULID pattern %s", env.RequestID, reqIDPattern)
			}
		})
	}
}

// envOrSkip fetches an env var or t.Skipf — phase5SuiteGuard already
// asserted presence, but per-subtest defense saves a noisy failure
// when subtests are run in isolation via -run.
func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

// testPhase5SC3PluginPrecedence — §12.3 CTE precedence (CRD wins;
// alphabetically-lowest marketplace fallback; deletion-drain).
// Body implemented in Task 4; stub here so the umbrella compiles.
func testPhase5SC3PluginPrecedence(t *testing.T) {
	t.Skipf("SC#3 §12.3 plugin precedence body lands in Plan 05-08 Task 4")
}

// testPhase5SC4StalenessAndInFlightRename — D-04 staleness + D-02
// rename. Body implemented in Task 4; stub here so the umbrella
// compiles.
func testPhase5SC4StalenessAndInFlightRename(t *testing.T) {
	t.Skipf("SC#4 staleness + in-flight rename body lands in Plan 05-08 Task 4")
}

// testPhase5SC5MetricsTopology — §18.5 metric set across operator,
// forwarder, content-service, platform-api. Body implemented in
// Task 4; stub here so the umbrella compiles.
func testPhase5SC5MetricsTopology(t *testing.T) {
	t.Skipf("SC#5 metrics topology body lands in Plan 05-08 Task 4")
}
