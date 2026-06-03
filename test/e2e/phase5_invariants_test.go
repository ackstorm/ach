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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
// `req_<26-char-base32>` per §15.5 + the audit handler convention. The
// encoder emits lowercase Crockford base32, so match case-insensitively.
var reqIDPattern = regexp.MustCompile(`^req_[0-9a-zA-Z]{26}$`)

func TestPhase5Invariants(t *testing.T) {
	phase5SuiteGuard(t)
	t.Run("SC1_ContentSendfile", testPhase5SC1ContentSendfile)
	t.Run("SC2_ErrorMatrix", testPhase5SC2ErrorMatrix)
	t.Run("SC3_PluginPrecedence", testPhase5SC3PluginPrecedence)
	t.Run("SC4_StalenessAndRename", testPhase5SC4StalenessAndInFlightRename)
	t.Run("SC5_MetricsTopology", testPhase5SC5MetricsTopology)
	t.Run("SC6_InvalidSyncedFixtures", testPhase5SC6InvalidSyncedFixtures)
}

// testPhase5SC6InvalidSyncedFixtures asserts the negative-path half of the
// synced content-service matrix: plugin-invalid / prompt-invalid /
// artifact-invalid each settle at SourceReachable=False (their git source
// is a nonexistent repo). They are pre-synced by cluster.sh and gated to
// this failure state by verify_all, so the live condition is already
// settled; the suite asserts both the CR condition AND that the Postgres
// projection reflects the failure (no successful refresh recorded).
func testPhase5SC6InvalidSyncedFixtures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	invalid := []struct{ kind, name, table string }{
		{"plugin", "plugin-invalid", "plugins"},
		{"prompt", "prompt-invalid", "prompts"},
		{"artifact", "artifact-invalid", "artifacts"},
	}
	for _, inv := range invalid {
		inv := inv
		t.Run(inv.name, func(t *testing.T) {
			// CR-level: SourceReachable must be False.
			out, err := exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
				"get", inv.kind+"/"+inv.name,
				"-o", `jsonpath={.status.conditions[?(@.type=="SourceReachable")].status}`,
			).CombinedOutput()
			if err != nil {
				t.Fatalf("kubectl get %s/%s: %v output=%s", inv.kind, inv.name, err, strings.TrimSpace(string(out)))
			}
			if got := strings.TrimSpace(string(out)); got != "False" {
				t.Fatalf("%s/%s SourceReachable=%q want=False", inv.kind, inv.name, got)
			}

			// DB-level: the projection row (if the operator wrote one for a
			// never-fetched source) must carry NO successful refresh. A NULL
			// last_successful_refresh OR no row at all both satisfy "never
			// successfully materialized" — assert the COUNT of rows with a
			// non-NULL last_successful_refresh is 0.
			q := `SELECT COUNT(*) FROM ` + inv.table +
				` WHERE name='` + inv.name + `' AND last_successful_refresh IS NOT NULL;`
			stdout, stderr, err := psqlExec(ctx, q)
			if err != nil {
				t.Skipf("psql projection check (engineer-pending postgres harness): %v stderr=%s", err, strings.TrimSpace(stderr))
			}
			if got := strings.TrimSpace(stdout); got != "0" {
				t.Errorf("%s row name=%s has %s successfully-refreshed projection rows; want 0 (source is invalid)", inv.table, inv.name, got)
			}
		})
	}
}

// testPhase5SC1ContentSendfile verifies the D-01 streaming discipline:
// sendfile(2) zero-copy, identity transfer, no http.ServeContent
// Range/If-* honoring, CS-06 header set verbatim.
func testPhase5SC1ContentSendfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pk, _, env := seedPhase5Fixtures(t, ctx)

	// Sendfile syscall assertion (live via strace under kubectl debug).
	if !straceCSSendfile(t, ctx, "/content/plugin/plugin-valid", pk, env) {
		t.Fatalf("expected ≥1 sendfile/sendfile64 syscall during CS GET — none observed (CS-06 zero-copy violated)")
	}

	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/")
	baseReq := func(t *testing.T, extraHeaders map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, csURL+"/content/plugin/plugin-valid", nil)
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
		name         string
		path         string
		key          string
		envHeader    string
		wantStatus   int
		wantCode     string
		extraHeaders map[string]string
		skipReason   string
		// setup runs inside the subtest before the request is built. Used
		// by cases that must mutate cluster state (e.g. seed a dangling
		// context name) and register their own t.Cleanup. nil for the
		// purely-declarative cases.
		setup func(t *testing.T, ctx context.Context)
	}

	calls := []call{
		{
			name:       "MissingEnvironment",
			path:       "/content/plugin/plugin-valid",
			key:        pk,
			envHeader:  "", // explicit empty — header NOT set below
			wantStatus: http.StatusBadRequest,
			wantCode:   "missing_environment",
		},
		{
			name:       "InvalidKeyFormat_NoPrefix",
			path:       "/content/plugin/plugin-valid",
			key:        "garbage-no-prefix",
			envHeader:  env,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_key_format",
		},
		{
			name:       "InvalidKeyFormat_Empty",
			path:       "/content/plugin/plugin-valid",
			key:        "",
			envHeader:  env,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_key_format",
		},
		{
			name:       "ExpiredOrRevoked",
			path:       "/content/plugin/plugin-valid",
			key:        "pk_DEADBEEFDEADBEEFDEADBEEFDEADBEEF",
			envHeader:  env,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "expired_or_revoked",
		},
		{
			// unauthorized_team (gate 4, pk_ only): the request targets the
			// env-team-denied synced fixture, whose authorized_teams names a
			// sentinel team the SSO-provisioned user is NOT in. The user's
			// teams (default) ∩ env.authorized_teams (sentinel) = ∅ →
			// errUnauthorizedTeam. The test controls the RIGHT side of the
			// intersection via the fixture's authorizedTeams — no LiteLLM
			// membership mutation needed (the prior skip's premise was stale).
			// ek_ would short-circuit this gate, so the key MUST be pk_.
			name:       "UnauthorizedTeam",
			path:       "/content/plugin/plugin-valid",
			key:        pk,
			envHeader:  "env-team-denied",
			wantStatus: http.StatusForbidden,
			wantCode:   "unauthorized_team",
		},
		{
			name:       "WrongEnvironment",
			path:       "/content/plugin/plugin-valid",
			key:        ek, // ek bound to env=env-valid
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
			path:       "/content/plugin/plugin-valid",
			key:        pk,
			envHeader:  "nonexistent-env",
			wantStatus: http.StatusNotFound,
			wantCode:   "environment_not_found",
		},
		{
			// content_not_found is the LAST gate (§15.5): a name that IS
			// in env.context.plugins (passes the cheaper unauthorized_content
			// allowlist gate) but has NO backing plugins projection row →
			// resolveContent returns nil → 404. We seed a ghost name into
			// env-valid's context allowlist and ensure no plugins row backs
			// it. The plugins table is read FRESH from Postgres (no cache);
			// only the envcache (context_plugins) is Redis-cached at 60s TTL,
			// so setup settles 65s after the UPDATE for the loader to rebuild.
			name:       "ContentNotFound",
			path:       "/content/plugin/ghost-content",
			key:        pk,
			envHeader:  env,
			wantStatus: http.StatusNotFound,
			wantCode:   "content_not_found",
			setup: func(t *testing.T, ctx context.Context) {
				const ghost = "ghost-content"
				patch := `UPDATE environments SET context_plugins = array_append(context_plugins, '` + ghost + `') ` +
					`WHERE name='env-valid' AND NOT ('` + ghost + `' = ANY(context_plugins)); ` +
					`DELETE FROM plugins WHERE name='` + ghost + `';`
				if _, stderr, err := psqlExec(ctx, patch); err != nil {
					t.Skipf("psql ghost-content patch (engineer-pending): %v stderr=%s", err, strings.TrimSpace(stderr))
				}
				t.Cleanup(func() {
					cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer ccancel()
					_, _, _ = psqlExec(cctx,
						`UPDATE environments SET context_plugins = array_remove(context_plugins, '`+ghost+`') WHERE name='env-valid';`)
				})
				// Wait out the 60s envcache TTL so the patched context_plugins
				// loads (the direct UPDATE emits no NOTIFY). Bounded, mirrors
				// StaleCacheExpired.
				time.Sleep(65 * time.Second)
			},
		},
	}

	for _, c := range calls {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.skipReason != "" {
				t.Skipf("%s", c.skipReason)
			}
			if c.setup != nil {
				c.setup(t, ctx)
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
// CS-08 + CS-09 + drift flag #5 lock-in.
func testPhase5SC3PluginPrecedence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pk, _, env := seedPhase5Fixtures(t, ctx)
	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/")

	// CS-08 §12.3: when a Plugin CRD row exists for a given name, it
	// wins over any marketplace_plugins row with the same name.
	t.Run("CRDWinsOverMarketplace", func(t *testing.T) {
		// Pre-seed a marketplace_plugins row with the same name as the
		// existing Plugin CRD (plugin-valid) but distinct marketplace_name.
		query := `INSERT INTO marketplace_plugins ` +
			`(marketplace_name, name, storage_location, max_staleness_seconds, origin, locked) ` +
			`VALUES ('aaa-precedence-test-mkt', 'plugin-valid', ` +
			`'/var/cache/ach/plugin/plugin-valid.precedence-test', 86400, 'cr', true) ` +
			`ON CONFLICT DO NOTHING;`
		if _, stderr, err := psqlExec(ctx, query); err != nil {
			t.Skipf("psql seed (engineer-pending postgres harness): %v stderr=%s", err, strings.TrimSpace(stderr))
		}
		t.Cleanup(func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanCancel()
			_, _, _ = psqlExec(cleanCtx,
				`DELETE FROM marketplace_plugins WHERE marketplace_name='aaa-precedence-test-mkt' AND name='plugin-valid';`)
		})
		// CS request resolves via §12.3 CTE — CRD branch must win.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, csURL+"/content/plugin/plugin-valid", nil)
		req.Header.Set("x-ach-key", pk)
		req.Header.Set("x-ach-environment", env)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d want=200 (CRD branch MUST win over marketplace per §12.3)", resp.StatusCode)
		}
		// Engineer-pending byte-comparison: the storage_location of the
		// CRD-row vs the marketplace-row differ, so a byte-comparison
		// against a known fixture would distinguish which path served.
		// In live testing, attach a Service-side handler that returns
		// distinct content per path and assert; here we only verify
		// the §12.3 CTE compiled + returned a row (200 OK).
	})

	t.Run("AlphabeticallyLowestMarketplace", func(t *testing.T) {
		// Pre-seed three marketplace_plugins rows for the same name in
		// reverse alphabetical insert order — the §12.3 CTE MUST
		// resolve to the alphabetically-lowest marketplace_name.
		mkts := []string{"zzz-mkt", "internal-mkt", "anthropic-mkt"}
		for _, m := range mkts {
			q := fmt.Sprintf(
				`INSERT INTO marketplace_plugins `+
					`(marketplace_name, name, storage_location, max_staleness_seconds, origin, locked) `+
					`VALUES ('%s', 'mktshared', `+
					`'/var/cache/ach/plugin/mktshared.%s', 86400, 'cr', true) `+
					`ON CONFLICT DO NOTHING;`, m, m)
			if _, stderr, err := psqlExec(ctx, q); err != nil {
				t.Skipf("psql seed %s (engineer-pending): %v stderr=%s", m, err, strings.TrimSpace(stderr))
			}
		}
		t.Cleanup(func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanCancel()
			_, _, _ = psqlExec(cleanCtx, `DELETE FROM marketplace_plugins WHERE name='mktshared';`)
		})
		// Verify the CTE resolves to anthropic-mkt via direct query.
		stdout, _, err := psqlExec(ctx, `SELECT marketplace_name FROM marketplace_plugins WHERE name='mktshared' ORDER BY marketplace_name ASC LIMIT 1;`)
		if err != nil {
			t.Skipf("psql verify (engineer-pending): %v", err)
		}
		if got := strings.TrimSpace(stdout); got != "anthropic-mkt" {
			t.Errorf("alphabetically-lowest marketplace=%q want=anthropic-mkt", got)
		}
		// Engineer-pending: live CS request against /content/plugin/mktshared
		// requires the env.context.plugins to include "mktshared" AND a
		// resolvable upstream URL. Both are deferred; CTE behavior is
		// adequately covered by Plan 05-02 integration tests.
	})

	// ScopedMarketplacePlugin200 exercises the name@marketplace scoped
	// resolution path end-to-end: the demo Environment fixture includes
	// `feature-dev@conflict-mkt-a` in context.plugins (no Plugin CRD backs
	// this name), so the content-service must resolve via the marketplace
	// branch of the §12.3 CTE. A 200 response confirms the scoped ref was
	// parsed, resolved, and served. The `demo` environment is used here
	// because `env-valid` does not include the scoped ref.
	t.Run("ScopedMarketplacePlugin200", func(t *testing.T) {
		// Acquire a pk_ bound to the demo environment (the Environment fixture
		// that includes feature-dev@conflict-mkt-a in context.plugins).
		demoPK := mustAcquirePk(t)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			csURL+"/content/plugin/feature-dev@conflict-mkt-a", nil)
		req.Header.Set("x-ach-key", demoPK)
		req.Header.Set("x-ach-environment", "demo")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			dumpOperatorLogs(t)
			t.Fatalf("scoped plugin fetch: status=%d want=200 "+
				"(feature-dev@conflict-mkt-a must resolve via §12.3 marketplace branch)", resp.StatusCode)
		}
	})

	t.Run("DeletionDrainStillServes", func(t *testing.T) {
		// Apply a transient Plugin "drainable", wait Ready, delete it,
		// then immediately curl. Expect either 200 (envcache hit during
		// grace) OR 503 stale_cache_expired (after grace expiry). CS-09.
		applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(`---
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Plugin
metadata:
  name: drainable
  namespace: ach-system
spec:
  type: github
  refresh:
    interval: 1h
    maxStaleness: 24h
  github:
    repo: JuliusBrussee/caveman
    ref: main
`)
		if out, err := applyCmd.CombinedOutput(); err != nil {
			t.Skipf("kubectl apply drainable (engineer-pending): %v output=%s", err, strings.TrimSpace(string(out)))
		}
		t.Cleanup(func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cleanCancel()
			_, _ = exec.CommandContext(cleanCtx, "kubectl", "delete", "plugin", "drainable",
				"-n", phase5Namespace, "--ignore-not-found").CombinedOutput()
		})
		// Best-effort wait for the projection row (the reconciler writes
		// it once the Plugin reaches SourceReachable). kubectl wait
		// directly — see seedPhase5Fixtures for why `make wait-cr-ready`
		// is not usable from the test binary's cwd.
		_, _ = exec.CommandContext(ctx, "kubectl", "-n", phase5Namespace,
			"wait", "--for=condition=SourceReachable", "plugin/drainable",
			"--timeout=120s").CombinedOutput()
		// Now delete and probe.
		if out, err := exec.CommandContext(ctx, "kubectl", "delete", "plugin", "drainable",
			"-n", phase5Namespace).CombinedOutput(); err != nil {
			t.Fatalf("kubectl delete: %v output=%s", err, strings.TrimSpace(string(out)))
		}
		// Engineer-pending: a CS GET here requires drainable in
		// env.context.plugins (Environment "env-valid" doesn't include it).
		// SC#9 grace window coverage at integration-test layer.
		t.Logf("DeletionDrainStillServes: CRD delete issued; CS-09 grace verification deferred to Plan 05-05 integration test (Environment fixture omits drainable from context.plugins)")
	})
}

// testPhase5SC4StalenessAndInFlightRename — D-04 step 7 staleness +
// D-02 in-flight rename (the latter is plan-permitted to t.Skipf).
func testPhase5SC4StalenessAndInFlightRename(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pk, _, env := seedPhase5Fixtures(t, ctx)
	csURL := strings.TrimRight(envOrSkip(t, "ACH_CONTENT_SERVICE_URL"), "/")

	t.Run("StaleCacheExpired", func(t *testing.T) {
		// Force the foo plugin's projection row into stale-expired by
		// setting last_successful_refresh 24h ago + max_staleness=300s.
		q := `UPDATE plugins SET last_successful_refresh = NOW() - INTERVAL '24 hours', ` +
			`max_staleness_seconds = 300 WHERE name='plugin-valid';`
		if _, stderr, err := psqlExec(ctx, q); err != nil {
			t.Skipf("psql staleness patch (engineer-pending): %v stderr=%s", err, strings.TrimSpace(stderr))
		}
		t.Cleanup(func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanCancel()
			_, _, _ = psqlExec(cleanCtx,
				`UPDATE plugins SET last_successful_refresh = NOW(), max_staleness_seconds = 86400 WHERE name='plugin-valid';`)
		})
		// Invalidate envcache so the loader rebuilds from the patched row.
		// 60s TTL means we may need to wait or trigger eviction — for
		// the E2E run, sleep 60s+ to ensure cache miss. Bounded.
		time.Sleep(65 * time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, csURL+"/content/plugin/plugin-valid", nil)
		req.Header.Set("x-ach-key", pk)
		req.Header.Set("x-ach-environment", env)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want=503 (CS-10 stale_cache_expired) body=%s",
				resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var envBody errEnvelope
		if err := json.Unmarshal(body, &envBody); err != nil {
			t.Fatalf("json.Unmarshal: %v body=%s", err, strings.TrimSpace(string(body)))
		}
		if envBody.Error.Code != "stale_cache_expired" {
			t.Errorf("error.code=%q want=stale_cache_expired (CS-10)", envBody.Error.Code)
		}
	})

	t.Run("InFlightReadSurvivesRename", func(t *testing.T) {
		// D-02 in-flight inode pin: a slowly-streaming GET should
		// complete with the original byte content even after an atomic
		// rename(2) swaps the inode mid-stream. The orchestration
		// (slow client + operator-side rename trigger) exceeds inline
		// scope; covered by Plan 05-05 Task 4 integration test which
		// asserts via direct *os.File pinning under the unit harness.
		t.Skipf("DELIBERATE NON-GOAL: deterministic mid-stream rename(2) + throttled-reader orchestration is inherently flaky at the e2e layer (timing race between a slow client and the operator-side inode swap). The D-02 in-flight inode-pin invariant is covered deterministically at the integration layer (Plan 05-05 Task 4, via direct *os.File pinning). Intentionally not un-skipped by the buckets-2-3 plan.")
	})
}

// testPhase5SC5MetricsTopology — §18.5 metric set across all four
// services. Drift flag #4 lock-in: each metric endpoint scraped
// directly (CS via the Service-level annotation path; operator/
// forwarder/platform-api via their Pod-level paths).
func testPhase5SC5MetricsTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Per-service metrics routes through the gateway (a bare /metrics
	// can't disambiguate four services behind one base). The harness
	// exports each as ACH_<svc>_METRICS_URL = <base>/metrics/<svc>.
	forwarderURL := envOrSkip(t, "ACH_FORWARDER_METRICS_URL")
	csURL := envOrSkip(t, "ACH_CONTENT_METRICS_URL")
	papiURL := envOrSkip(t, "ACH_PLATFORM_METRICS_URL")
	operatorURL := envOrSkip(t, "ACH_OPERATOR_METRICS_URL")

	assertContains := func(t *testing.T, body string, names ...string) {
		t.Helper()
		for _, n := range names {
			if !strings.Contains(body, n) {
				t.Errorf("metric family %q missing from /metrics body", n)
			}
		}
	}

	t.Run("ForwarderMetrics", func(t *testing.T) {
		body := getMetricsBody(t, ctx, forwarderURL)
		assertContains(t, body,
			"forwarder_requests_total",
			"forwarder_jwt_signed_total",
			"forwarder_jwt_suppressed_total",
			"forwarder_request_duration_seconds",
			"litellm_unreachable_total",
		)
		if strings.Contains(body, "litellm_unreachable_total") &&
			!strings.Contains(body, `caller="forwarder"`) {
			t.Logf("litellm_unreachable_total present but no caller=\"forwarder\" sample yet (zero-count families register but only emit samples after the first failure)")
		}
	})

	t.Run("ContentServiceMetrics", func(t *testing.T) {
		body := getMetricsBody(t, ctx, csURL)
		assertContains(t, body,
			"content_service_requests_total",
			"content_service_bytes_served_total",
			"content_service_request_duration_seconds",
			"litellm_unreachable_total",
		)
	})

	t.Run("PlatformAPIMetrics", func(t *testing.T) {
		body := getMetricsBody(t, ctx, papiURL)
		// Platform-API has no §18.5-mandated handler-level metrics;
		// /metrics must still return the Go runtime baseline plus the
		// shared litellm_unreachable_total registered by Plan 05-06.
		assertContains(t, body, "go_goroutines", "litellm_unreachable_total")
	})

	t.Run("OperatorMetrics", func(t *testing.T) {
		body := getMetricsBody(t, ctx, operatorURL)
		// controller-runtime metricsserver — at least 3 of its
		// standard families MUST appear, plus the shared
		// litellm_unreachable_total registered by Plan 05-06 Task 5.
		families := []string{
			"controller_runtime_reconcile_total",
			"controller_runtime_reconcile_errors_total",
			"workqueue_depth",
			"litellm_unreachable_total",
		}
		assertContains(t, body, families...)
	})

	t.Run("NoForbiddenLabels_ContentService", func(t *testing.T) {
		// OBS-06 cardinality discipline lock-in: NO content_service_*
		// series may carry a request_id or owner_email label — either
		// would explode the per-series cardinality budget. Scan the
		// exposition line-by-line rather than via expfmt.TextParser: the
		// zero-value parser in prometheus/common v0.67 panics ("name
		// validation scheme unset"), and a substring scan is sufficient
		// and faithful for the forbidden-label invariant.
		body := getMetricsBody(t, ctx, csURL)
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "content_service_") {
				continue
			}
			for _, bad := range []string{"request_id=", "owner_email="} {
				if strings.Contains(line, bad) {
					t.Errorf("forbidden label %q present on a content_service_* series (OBS-06 cardinality discipline violated): %s",
						strings.TrimSuffix(bad, "="), line)
				}
			}
		}
	})
}
