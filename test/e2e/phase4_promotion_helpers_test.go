//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 promotion shared helpers — force-refresh round-trip, golden
// JSON shape diff with tolerated paths, ConfigMap-backed nginx
// fixture-server bring-up, BIP finalizer probes.

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// forceRefreshAndAssert drives one §11a-shape round trip for an
// external-reference CR (Prompt / Artifact / Skill):
//
//  1. Snapshot status.upstreamRev and status.lastSuccessfulRefresh.
//  2. kubectl annotate <kind>/<name> ach.ackstorm.ai/force-refresh=now --overwrite
//  3. Poll until BOTH:
//     a) annotation absent (reconciler cleared it per D-07)
//     b) status.lastSuccessfulRefresh > snapshot (RFC3339 string compare
//     works because ISO-8601 lex-sorts correctly).
//  4. Assert status.upstreamRev is UNCHANGED (upstream didn't move, so
//     no re-publish; UpsertExternalRef returns NotModified=true).
//
// timeout caps the wait at 30s — the upstream HEAD probe is sub-second
// against GitHub even cold, and the in-cluster controller-runtime
// requeue is immediate on annotation event. 30s is generous.
//
// Hard-fail (t.Fatalf) on:
//   - kubectl annotate failure
//   - annotation not cleared within timeout
//   - lastSuccessfulRefresh not advanced within timeout
//   - upstreamRev changed (would mean upstream moved mid-test; rerun)
func forceRefreshAndAssert(t *testing.T, kind, name string, timeout time.Duration) {
	t.Helper()

	priorRev := getCRJSONPath(t, kind, name, "{.status.upstreamRev}")
	priorRefresh := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
	if priorRefresh == "" {
		t.Fatalf("§11a %s/%s: status.lastSuccessfulRefresh is empty at snapshot — "+
			"CR has not reconciled to first success yet; apply + wait for "+
			"SourceReachable=True before driving force-refresh", kind, name)
	}

	if out, err := runCmd("kubectl", "annotate", "-n", namespace, kind+"/"+name,
		"ach.ackstorm.ai/force-refresh=now", "--overwrite"); err != nil {
		t.Fatalf("§11a %s/%s: kubectl annotate: %v\n%s", kind, name, err, out)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ann := getCRJSONPath(t, kind, name,
			"{.metadata.annotations.ach\\.ackstorm\\.ai/force-refresh}")
		curRefresh := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
		if ann == "" && curRefresh != "" && curRefresh > priorRefresh {
			// Both invariants met. Now check upstreamRev stability.
			curRev := getCRJSONPath(t, kind, name, "{.status.upstreamRev}")
			if curRev != priorRev {
				t.Fatalf("§11a %s/%s: upstreamRev moved during force-refresh "+
					"(prior=%q, current=%q) — upstream changed mid-test; rerun",
					kind, name, priorRev, curRev)
			}
			return
		}
		// In-loop rate-limit drift detection: skipIfRateLimited at the
		// call site only sees the snapshot BEFORE annotate. The Fetcher
		// then runs and may flip SourceReachable to False/Unauthorized
		// (GitHub anonymous-quota 403) — the wait loop must skip the
		// test in that case, not time out.
		st := getCRJSONPath(t, kind, name, "{.status.conditions[?(@.type==\"SourceReachable\")].status}")
		if st == "False" {
			reason := getCRJSONPath(t, kind, name, "{.status.conditions[?(@.type==\"SourceReachable\")].reason}")
			if reason == "Unauthorized" || reason == "RateLimited" {
				t.Skipf("§11a %s/%s force-refresh triggered SourceReachable=False reason=%s mid-flight — GitHub anonymous-quota rate-limit. Skipping (engineer-pending: provision GitHub PAT Secret + AuthSecretRef on examples/06,07,08).",
					kind, name, reason)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Diagnose.
	annNow := getCRJSONPath(t, kind, name,
		"{.metadata.annotations.ach\\.ackstorm\\.ai/force-refresh}")
	refreshNow := getCRJSONPath(t, kind, name, "{.status.lastSuccessfulRefresh}")
	dumpOperatorLogs(t)
	t.Fatalf("§11a %s/%s: force-refresh did not complete within %s.\n"+
		"  annotation present? %q (want empty)\n"+
		"  lastSuccessfulRefresh: prior=%q current=%q (want current > prior)",
		kind, name, timeout, annNow, refreshNow, priorRefresh)
}

// skipIfRateLimited waits for SourceReachable on the given CR. If the
// reconciler lands on SourceReachable=True, returns normally. If it
// lands on SourceReachable=False reason=Unauthorized (GitHub anonymous
// 60 req/h/IP rate-limit hit by the kind cluster during a hot suite
// run), the calling test is t.Skipf'd with an engineer-pending message.
// Any other terminal state is a real failure — t.Fatalf.
func skipIfRateLimited(t *testing.T, kind, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := getCRJSONPath(t, kind, name, "{.status.conditions[?(@.type==\"SourceReachable\")].status}")
		reason := getCRJSONPath(t, kind, name, "{.status.conditions[?(@.type==\"SourceReachable\")].reason}")
		switch st {
		case "True":
			return
		case "False":
			if reason == "Unauthorized" || reason == "RateLimited" {
				t.Skipf("§11: %s/%s SourceReachable=False reason=%s — GitHub anonymous-quota rate-limit. Skipping (engineer-pending: provision GitHub PAT Secret + AuthSecretRef on examples/06,07,08 OR wait 1h for quota reset).",
					kind, name, reason)
			}
			// Other False reason: not rate-limit, real bug — fail.
			msg := getCRJSONPath(t, kind, name, "{.status.conditions[?(@.type==\"SourceReachable\")].message}")
			t.Fatalf("§11: %s/%s SourceReachable=False reason=%s msg=%q", kind, name, reason, msg)
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("§11: %s/%s SourceReachable never reached terminal state within %s", kind, name, timeout)
}

// getCRJSONPath is a thin wrapper around `kubectl get <kind>/<name> -o
// jsonpath=...` returning trimmed stdout. Returns "" on kubectl error
// (subtest is then expected to fail on the empty-value assertion).
func getCRJSONPath(t *testing.T, kind, name, jsonpath string) string {
	t.Helper()
	out, err := runCmd("kubectl", "get", kind, name, "-n", namespace,
		"-o", fmt.Sprintf("jsonpath=%s", jsonpath))
	if err != nil {
		t.Logf("getCRJSONPath %s/%s %s: %v\n%s", kind, name, jsonpath, err, out)
		return ""
	}
	return strings.TrimSpace(out)
}

// (compareJSONShape / walkJSON / jsonScalarEqual were removed in #58 — their
// only caller was the deleted SC11e hydrate-golden subtest.)

// bipFinalizer is the canonical BIP finalizer name. Mirrors
// internal/controller/ach/finalizers.go.
const bipFinalizer = "backendidentitypolicies.ach.ackstorm.ai/finalizer"

// assertBIPFinalizerPresent asserts the BIP CR carries the BIP finalizer.
// Returns the parsed finalizer list for diagnostics.
func assertBIPFinalizerPresent(t *testing.T, name string) []string {
	t.Helper()
	out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
		"-o", "jsonpath={.metadata.finalizers}")
	if err != nil {
		t.Fatalf("§11b get bip/%s finalizers: %v\n%s", name, err, out)
	}
	if !strings.Contains(out, bipFinalizer) {
		t.Fatalf("§11b bip/%s missing finalizer %q; got=%q", name, bipFinalizer, out)
	}
	return strings.Fields(strings.Trim(out, "[]"))
}

// assertBIPNoNameConflict asserts the BIP's Synced condition (if any)
// does NOT carry the NameConflict reason. The alpha-FIRST winner of a
// duplicate (target.kind, target.name) pair stays clean per G15 — the
// runtime is forwarder-resolved and the winner owns the target.
func assertBIPNoNameConflict(t *testing.T, name string) {
	t.Helper()
	out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
		"-o", `jsonpath={.status.conditions[?(@.type=="Synced")].reason}`)
	if err != nil {
		t.Fatalf("§11b get bip/%s Synced reason: %v\n%s", name, err, out)
	}
	if strings.TrimSpace(out) == "NameConflict" {
		t.Fatalf("§11b bip/%s (alpha-first winner) MUST NOT carry "+
			"Synced/NameConflict (G15 — only the loser is shadowed)", name)
	}
}

// assertBIPNameConflict polls until the BIP carries the advisory
// Synced=False/NameConflict referencing winnerName, or fails after
// timeout. The loser's condition is reconciler-written on the
// sibling-create requeue, so it is eventually-consistent (G15).
func assertBIPNameConflict(t *testing.T, name, winnerName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastReason, lastStatus, lastMsg string
	for time.Now().Before(deadline) {
		lastReason, _ = runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Synced")].reason}`)
		lastStatus, _ = runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Synced")].status}`)
		lastMsg, _ = runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Synced")].message}`)
		if strings.TrimSpace(lastReason) == "NameConflict" &&
			strings.TrimSpace(lastStatus) == "False" &&
			strings.Contains(lastMsg, winnerName) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("§11b bip/%s never got Synced=False/NameConflict referencing %q "+
		"within %s (G15 shadow loser); last reason=%q status=%q msg=%q",
		name, winnerName, timeout, strings.TrimSpace(lastReason),
		strings.TrimSpace(lastStatus), strings.TrimSpace(lastMsg))
}

// (applyPhase4MarketplaceServer was removed alongside the SC11c / SC11f
// PluginMarketplace subtests: the PluginMarketplace kind is disabled behind
// featuregate.PluginsEnabled=false and its CRD is no longer shipped in the
// chart, so the in-cluster marketplace fixture-server has no consumer.)

// getOperatorPodUID returns the metadata.uid of the running ach-operator
// Pod. The Helm chart installs deploy/ach-operator into the release
// namespace (E2E_NAMESPACE, default ach-system). The label selector
// includes component=operator to disambiguate from the forwarder /
// platform-api / content-service Pods (all share name=ach).
func getOperatorPodUID(t *testing.T) string {
	t.Helper()
	out, err := runCmd("kubectl", "get", "pods", "-n", namespace,
		"-l", "app.kubernetes.io/name=ach,app.kubernetes.io/component=operator",
		"-o", "jsonpath={.items[0].metadata.uid}")
	if err != nil {
		t.Fatalf("§11d get operator pod uid: %v\n%s", err, out)
	}
	uid := strings.TrimSpace(out)
	if uid == "" {
		t.Fatalf("§11d: no operator pod found "+
			"(label app.kubernetes.io/name=ach,component=operator in ns %s)", namespace)
	}
	return uid
}

// waitForOperatorPodChanged polls until a pod with uid != prevUID is
// Running + Ready. Returns the new uid.
func waitForOperatorPodChanged(t *testing.T, prevUID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", "pods", "-n", namespace,
			"-l", "app.kubernetes.io/name=ach,app.kubernetes.io/component=operator",
			"-o", "jsonpath={range .items[?(@.status.phase=='Running')]}{.metadata.uid}{' '}{.status.conditions[?(@.type=='Ready')].status}{'\\n'}{end}")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 && parts[0] != prevUID && parts[1] == "True" {
					return parts[0]
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := runCmd("kubectl", "get", "pods", "-n", namespace,
		"-l", "app.kubernetes.io/name=ach,app.kubernetes.io/component=operator", "-o", "wide")
	t.Fatalf("§11d: no new Ready operator Pod within %s (prev uid=%s)\n%s",
		timeout, prevUID, out)
	return ""
}

// runCmdStdin runs an arbitrary shell pipeline with `stdin` piped in.
// Used for `... | kubectl apply -f -` patterns.
func runCmdStdin(cmdline, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdline)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// (driveHydrateAndCapture removed in #58 — it shelled out to the deleted
// examples/hydrate-demo.sh; the hydrate wire-path golden is covered by
// TestPhase6CLI via `ach-cli hydrate`.)

// queryACHPostgresCount runs a `SELECT count(*) ...` against the
// ach-postgres pod and returns the integer. The whereClause MUST NOT
// be user-tainted (this is a test helper, not a runtime path; SQL is
// shell-quoted as-is).
// execACHPostgres runs an arbitrary SQL statement against the ach Postgres via
// `kubectl exec sts/ach-postgres -- psql`. Used by the §11g takeover test to
// seed an origin='ui' draft row and to clean it up. The SQL must not contain a
// double-quote (it is passed inside a double-quoted psql -c argument).
func execACHPostgres(t *testing.T, sql string) {
	t.Helper()
	out, err := runCmd("kubectl", "exec", "-n", namespace,
		"sts/ach-postgres", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
	if err != nil {
		t.Fatalf("execACHPostgres %q: %v\n%s", sql, err, out)
	}
}

func queryACHPostgresCount(t *testing.T, table, whereClause string) int {
	t.Helper()
	sql := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, whereClause)
	out, err := runCmd("kubectl", "exec", "-n", namespace,
		"sts/ach-postgres", "--",
		"sh", "-c", `PGPASSWORD=ach psql -U ach -d ach -t -A -c "`+sql+`"`)
	if err != nil {
		t.Fatalf("§11f DB query %q: %v\n%s", sql, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("§11f DB query %q: non-integer result %q: %v", sql, out, err)
	}
	return n
}

// waitForACHPostgresCount polls until the count equals want, or t.Fatalf
// at the timeout. Used to assert finalizer cleanup completed (count→0).
func waitForACHPostgresCount(t *testing.T, table, whereClause string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if queryACHPostgresCount(t, table, whereClause) == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := queryACHPostgresCount(t, table, whereClause)
	t.Fatalf("§11f DB count: table=%s where=%q want=%d got=%d (timeout=%s)",
		table, whereClause, want, got, timeout)
}

// waitForBIPDeleted polls until `kubectl get bip <name>` returns
// NotFound. Asserts the finalizer was removed cleanly. timeout=30s.
func waitForBIPDeleted(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
			"--ignore-not-found", "-o", "jsonpath={.metadata.name}")
		if err == nil && strings.TrimSpace(out) == "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace, "-o", "yaml")
	t.Fatalf("§11b bip/%s: not deleted within %s; possibly stuck on finalizer\n%s",
		name, timeout, out)
}
