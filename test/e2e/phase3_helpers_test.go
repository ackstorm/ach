//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 3 invariants helpers — Plan 03-12.
//
// Shared helpers used by phase3_invariants_test.go to drive the deployed
// Platform API binary running in the kind cluster set up by
// scripts/cluster.sh. The suite follows the same shape as
// phase2_invariants_test.go: stdlib testing, kubectl orchestration,
// no Ginkgo (per Phase 02.3 decision — [feedback_023_tier_framework_rejected]).
//
// Suite-wide invariant guards
// ---------------------------
//
// The Phase 1 Platform API Deployment manifest (config/deployments/
// platform-api_deployment.yaml) does NOT set the Phase 3 env vars
// (ACH_DEX_*, ACH_REDIS_*, ACH_BASE_URL, etc.). Phase 7's Helm chart will
// wire them. Until then, the deployed binary cannot start as a Phase 3
// process — every SC subtest first calls phase3SuiteGuard(t) which
// inspects the running Pod's env-var set and t.Skipf's with a clear
// engineer-pending message when Phase 3 vars are absent.
//
// This mirrors Phase 02.2's `uat-g1.sh` "engineer-pending verification
// debt" pattern: the suite is mechanically correct and the audit-shape
// assertions are stable; once the Platform API Deployment is
// Phase-3-promoted (Phase 7 / DIST-04), the subtests run end-to-end on
// the live cluster.

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// phase3FixturesDir is the test/e2e/phase3_fixtures path relative to
// this test file (test/e2e/).
const phase3FixturesDir = "../../test/e2e/phase3_fixtures"

// phase3PlatformAPIDeployment is the Deployment name + container name of
// the Platform API in the kind cluster (per
// config/deployments/platform-api_deployment.yaml).
const (
	phase3PlatformAPIDeployment = "ach-platform-api"
	phase3PlatformAPIContainer  = "platform-api"
)

// phase3RequiredEnvVars is the subset of Phase 3 env vars (per
// Plan 03-11 SUMMARY) that MUST appear on the deployed Pod before any SC
// subtest can run end-to-end. The list is intentionally permissive — we
// check ACH_DEX_ISSUER_URL alone is sufficient as a phase gate since
// validateConfig refuses to start without ALL four ACH_DEX_* vars.
var phase3RequiredEnvVars = []string{"ACH_DEX_ISSUER_URL"}

// phase3SuiteGuard inspects the deployed Platform API Pod's env vars.
// If any phase3RequiredEnvVars entry is missing, t.Skipf's the calling
// subtest with an engineer-pending message. This is the canonical
// "manifest gap" detector — Phase 7 (Helm) closes it.
func phase3SuiteGuard(t *testing.T) {
	t.Helper()

	// `kubectl get deploy ach-platform-api -n ach-system -o
	// jsonpath={..env[*].name}` returns a space-separated list of env-var
	// names across all containers in the spec.
	out, err := runCmd("kubectl", "get", "deploy", phase3PlatformAPIDeployment,
		"-n", namespace, "-o", "jsonpath={.spec.template.spec.containers[*].env[*].name}",
	)
	if err != nil {
		t.Skipf("Phase 3 suite guard: kubectl get deploy %s failed (cluster up? config/e2e applied?): %v\n%s",
			phase3PlatformAPIDeployment, err, out)
		return
	}
	envSet := make(map[string]struct{})
	for _, name := range strings.Fields(out) {
		envSet[name] = struct{}{}
	}
	for _, want := range phase3RequiredEnvVars {
		if _, ok := envSet[want]; !ok {
			t.Skipf(
				"Phase 3 suite guard: deployed Platform API is NOT Phase-3-promoted "+
					"(missing env var %q; deployment ships Phase 1 stub). "+
					"Phase 7 / DIST-04 (Helm chart) will wire the Phase 3 env vars; "+
					"this is engineer-pending live verification debt per ROADMAP. "+
					"Manual verification path: scripts/uat-phase3.sh — drives an "+
					"in-binary Platform API process against the live cluster's "+
					"Postgres + Redis + LiteLLM + Dex.",
				want)
			return
		}
	}
}

// phase3AuditRecord is the parsed shape of a single Platform API audit
// line per Hub §18.2 + Plan 03-02 audit.Event composition contract.
// Fields are pointers so JSON omitempty round-trips correctly.
type phase3AuditRecord struct {
	Timestamp  string `json:"time"`        // slog default time field
	Audit      bool   `json:"audit"`       // top-level audit=true predicate
	Action     string `json:"action"`      // platform.* action constant
	Outcome    string `json:"outcome"`     // §18.2 outcome enum value
	Actor      string `json:"actor"`       // "<namespace>/<email>"
	RequestID  string `json:"request_id"`  // "req_<ulid>"
	KeyID      string `json:"key.id"`      // "pkid_..."/"ekid_..." or empty
	TargetKind string `json:"target.kind"` // optional
	TargetName string `json:"target.name"` // optional
	// raw line preserved for substring scans (plaintext, credential_hash
	// invariants).
	rawLine string
}

// phase3ParseAuditLines parses captured stdout from the Platform API
// process (typically `kubectl logs deploy/ach-platform-api ...`).
// Returns only the lines that have `"audit":true` at the top level —
// operational/debug logs are skipped silently. Returned records carry
// the raw JSON line via the unexported rawLine field so callers can run
// substring scans without re-marshaling.
func phase3ParseAuditLines(stdout []byte) []phase3AuditRecord {
	var records []phase3AuditRecord
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Cheap pre-filter — every audit line carries "audit":true; lines
		// without that substring cannot be audit records.
		if !strings.Contains(line, `"audit":true`) {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if v, _ := raw["audit"].(bool); !v {
			continue
		}
		rec := phase3AuditRecord{rawLine: line}
		if v, ok := raw["time"].(string); ok {
			rec.Timestamp = v
		}
		rec.Audit = true
		if v, ok := raw["action"].(string); ok {
			rec.Action = v
		}
		if v, ok := raw["outcome"].(string); ok {
			rec.Outcome = v
		}
		if v, ok := raw["actor"].(string); ok {
			rec.Actor = v
		}
		if v, ok := raw["request_id"].(string); ok {
			rec.RequestID = v
		}
		if v, ok := raw["key.id"].(string); ok {
			rec.KeyID = v
		}
		if v, ok := raw["target.kind"].(string); ok {
			rec.TargetKind = v
		}
		if v, ok := raw["target.name"].(string); ok {
			rec.TargetName = v
		}
		records = append(records, rec)
	}
	return records
}

// phase3PkPlaintextRe matches a base32-no-pad-lowercase pk_ bearer
// plaintext substring inside a JSON value. The (?:") boundary anchors
// it to JSON string positions only, so the pkid_ key_id form does
// not match (pkid_<ulid> uses Crockford ULID base32 which can include
// the pk_ substring inside its prefix, but with the JSON-string anchor
// the match scope is "field value starts with pk_<exactly-26-lowercase-base32>").
var phase3PkPlaintextRe = regexp.MustCompile(`"pk_[a-z2-7]{26}"`)

// phase3EkPlaintextRe — analog for ek_<26-base32-lowercase>.
var phase3EkPlaintextRe = regexp.MustCompile(`"ek_[a-z2-7]{26}"`)

// phase3AssertAuditOBS02 asserts the OBS-02 invariant on a single audit
// record: required fields present, field shapes valid, no plaintext or
// credential_hash leaked. This is the load-bearing assertion of SC#6.
//
// Required fields per Hub §18.2:
//   - timestamp (slog "time")
//   - actor — non-empty, "<ns>/<email>" form (contains "/")
//   - action — non-empty
//   - outcome — non-empty
//   - request_id — non-empty, "req_" prefix
//
// Optional fields when present:
//   - key.id — must use "pkid_" or "ekid_" prefix
//
// Hard-forbidden substrings in the raw line:
//   - pk_<26 base32 lowercase> (plaintext bearer)
//   - ek_<26 base32 lowercase> (plaintext bearer)
//   - "credential_hash":  (HMAC hash MUST NEVER appear in audit channel)
func phase3AssertAuditOBS02(t *testing.T, rec phase3AuditRecord) {
	t.Helper()
	if rec.Timestamp == "" {
		t.Errorf("OBS-02: audit record missing timestamp: %s", rec.rawLine)
	}
	if rec.Actor == "" {
		t.Errorf("OBS-02: audit record missing actor: %s", rec.rawLine)
	} else if !strings.Contains(rec.Actor, "/") {
		t.Errorf("OBS-02: actor must be <namespace>/<email>, got %q: %s",
			rec.Actor, rec.rawLine)
	}
	if rec.Action == "" {
		t.Errorf("OBS-02: audit record missing action: %s", rec.rawLine)
	}
	if rec.Outcome == "" {
		t.Errorf("OBS-02: audit record missing outcome: %s", rec.rawLine)
	}
	if rec.RequestID == "" {
		t.Errorf("OBS-02: audit record missing request_id: %s", rec.rawLine)
	} else if !strings.HasPrefix(rec.RequestID, "req_") {
		t.Errorf("OBS-02: request_id must use req_ prefix, got %q: %s",
			rec.RequestID, rec.rawLine)
	}
	if rec.KeyID != "" {
		if !strings.HasPrefix(rec.KeyID, "pkid_") && !strings.HasPrefix(rec.KeyID, "ekid_") {
			t.Errorf("OBS-02: key.id MUST use pkid_/ekid_ form, got %q: %s",
				rec.KeyID, rec.rawLine)
		}
	}
	phase3AssertNoPlaintextInLine(t, rec.rawLine)
}

// phase3AssertNoPlaintextInLine scans a raw audit line for pk_/ek_
// plaintext + credential_hash substrings. Failure here is the OBS-02
// no-leak invariant — bearer values and HMAC hashes MUST NEVER appear
// in audit output.
func phase3AssertNoPlaintextInLine(t *testing.T, raw string) {
	t.Helper()
	if phase3PkPlaintextRe.MatchString(raw) {
		t.Errorf("OBS-02 no-leak: pk_ plaintext bearer found in audit line: %s", raw)
	}
	if phase3EkPlaintextRe.MatchString(raw) {
		t.Errorf("OBS-02 no-leak: ek_ plaintext bearer found in audit line: %s", raw)
	}
	if strings.Contains(raw, `"credential_hash":`) {
		t.Errorf("OBS-02 no-leak: credential_hash field found in audit line: %s", raw)
	}
}

// phase3CapturePlatformAPILogs runs `kubectl logs deploy/ach-platform-api
// -c platform-api --tail=<n>` and returns the combined stdout/stderr
// bytes. Best-effort: returns an empty slice on kubectl error so the
// caller can phase-skip cleanly.
func phase3CapturePlatformAPILogs(t *testing.T, tail int) []byte {
	t.Helper()
	if tail <= 0 {
		tail = 500
	}
	out, err := runCmd("kubectl", "logs", "-n", namespace,
		"deployment/"+phase3PlatformAPIDeployment,
		"-c", phase3PlatformAPIContainer,
		"--tail", fmt.Sprintf("%d", tail),
	)
	if err != nil {
		t.Logf("kubectl logs deploy/%s failed (audit capture best-effort): %v\n%s",
			phase3PlatformAPIDeployment, err, out)
		return nil
	}
	return []byte(out)
}

// phase3WaitForPlatformAPIReady polls the deployed Platform API's
// /readyz endpoint via `kubectl exec` against a curl Pod, OR via an
// `http.Get` against a port-forward set up by the caller. The current
// implementation uses `kubectl rollout status` as the readiness proxy
// (the Deployment's own readinessProbe is the source of truth).
func phase3WaitForPlatformAPIReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	out, err := runCmdLonger(timeout, "kubectl", "rollout", "status",
		"-n", namespace,
		"deployment/"+phase3PlatformAPIDeployment,
		"--timeout", timeout.String(),
	)
	if err != nil {
		t.Fatalf("phase3WaitForPlatformAPIReady: rollout status: %v\n%s", err, out)
	}
}

// phase3ApplyEnvironment applies a Phase 3 fixture Environment CR via
// `kubectl apply -f`. Registers t.Cleanup to delete the CR on subtest
// end. Used by SC#2 (hydrate), SC#3 (env-keys create), and SC#5 (admin).
func phase3ApplyEnvironment(t *testing.T, fixturePath string) {
	t.Helper()
	if out, err := runCmd("kubectl", "apply", "-f", fixturePath); err != nil {
		t.Fatalf("phase3ApplyEnvironment %s: %v\n%s", fixturePath, err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "-f", fixturePath,
			"--wait=false", "--ignore-not-found")
	})
}

// phase3CountAuditByAction is the SC-subtest predicate counting audit
// records with a specific action constant. SC#6 cross-cutting check
// uses this to count "platform.pk.extend" events (which MUST be zero
// per OBS-01).
func phase3CountAuditByAction(records []phase3AuditRecord, action string) int {
	n := 0
	for _, r := range records {
		if r.Action == action {
			n++
		}
	}
	return n
}

// phase3ContainsToken scans a slice (e.g. db.ListActiveACHKeyTokens
// return) for a specific litellm_token value. Returns true on
// substring/exact match. SC#3 D-02 closure uses this to verify the
// orphan-loop's precise enumeration helper observes Phase 3 INSERTs.
func phase3ContainsToken(tokens []string, want string) bool {
	for _, tok := range tokens {
		if tok == want {
			return true
		}
	}
	return false
}

// ─── Below: deployment-time port-forwarding + HTTP probe helpers ──────
//
// SC subtests that drive real HTTP calls against the deployed Platform
// API use port-forwarding via `kubectl port-forward` (best for kind +
// in-cluster Service). The helpers below set up + tear down the
// port-forward and provide a typed HTTP client.

// phase3PlatformAPIBaseURL returns the URL the test client should hit.
// When ACH_PLATFORM_API_BASE_URL is set in the test env (e.g. by
// scripts/uat-phase3.sh), use it verbatim; otherwise default to
// http://localhost:8080 (the canonical port-forward target).
func phase3PlatformAPIBaseURL() string {
	if v := envOr("ACH_PLATFORM_API_BASE_URL", ""); v != "" {
		return v
	}
	return "http://localhost:8080"
}

// phase3StartPortForward kicks off `kubectl port-forward
// svc/ach-platform-api 8080:8080` in the background and returns a
// cleanup func the caller defers. Best-effort: if the Service doesn't
// exist yet the t.Skipf path applies via phase3SuiteGuard.
func phase3StartPortForward(t *testing.T) func() {
	t.Helper()
	cmd := exec.Command("kubectl", "port-forward",
		"-n", namespace,
		"svc/ach-platform-api",
		// Service exposes port 80 → targetPort 8080 (Phase 9 Helm chart
		// convention; ach-old used 8080:8080 directly on the Service).
		// The local port stays 8080 so phase3PlatformAPIBaseURL's
		// http://localhost:8080 default keeps working.
		"8080:80",
	)
	if err := cmd.Start(); err != nil {
		t.Skipf("phase3StartPortForward: cannot start kubectl port-forward (service absent?): %v", err)
		return func() {}
	}
	// Give the port-forward a moment to establish.
	time.Sleep(2 * time.Second)
	return func() {
		_ = cmd.Process.Kill()
	}
}

// phase3HTTPClient returns an http.Client with a short timeout for SC
// HTTP probes. Disables compression so audit-line capture sees raw
// bytes.
func phase3HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}
}

// phase3PostJSON is a tiny POST helper used by SC subtests. The bearer
// argument (when non-empty) is sent as the `x-ach-key` header per
// Hub §3.
func phase3PostJSON(t *testing.T, client *http.Client, urlStr, bearer string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("phase3PostJSON: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("x-ach-key", bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("phase3PostJSON %s: %v", urlStr, err)
	}
	return resp
}

// phase3URL joins the base URL with a path component, returning the
// composed string. Avoids importing net/url for one-liner code paths.
func phase3URL(path string) string {
	u, err := url.Parse(phase3PlatformAPIBaseURL())
	if err != nil {
		return phase3PlatformAPIBaseURL() + path
	}
	u.Path = path
	return u.String()
}
