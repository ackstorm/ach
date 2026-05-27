//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 promotion shared helpers — force-refresh round-trip, golden
// JSON shape diff with tolerated paths, ConfigMap-backed nginx
// fixture-server bring-up, BIP finalizer probes.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// forceRefreshAndAssert drives one §11a-shape round trip for an
// external-reference CR (Plugin / Prompt / Artifact / PluginMarketplace):
//
//  1. Snapshot status.upstreamRev and status.lastSuccessfulRefresh.
//  2. kubectl annotate <kind>/<name> ach.ackstorm.ai/force-refresh=now --overwrite
//  3. Poll until BOTH:
//       a) annotation absent (reconciler cleared it per D-07)
//       b) status.lastSuccessfulRefresh > snapshot (RFC3339 string compare
//          works because ISO-8601 lex-sorts correctly).
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

// compareJSONShape diffs an actual JSON document against a golden one
// with tolerated paths (dotted JSON path strings). On any path in
// `tolerated`, the helper checks only that the field is PRESENT and has
// the same type; the value is not compared. For every other path the
// values must match exactly.
//
// Returns a slice of human-readable error strings. Empty slice means
// equal (under tolerance).
//
// Used by §11e to ignore timestamp/storageLocation hash drift while
// still asserting the JSON shape + leaf values that matter
// (schemaVersion, environment, name, id, downloadUrl).
func compareJSONShape(actual, golden []byte, tolerated map[string]struct{}) []string {
	var a, g any
	if err := json.Unmarshal(actual, &a); err != nil {
		return []string{fmt.Sprintf("actual not JSON: %v", err)}
	}
	if err := json.Unmarshal(golden, &g); err != nil {
		return []string{fmt.Sprintf("golden not JSON: %v", err)}
	}
	var diffs []string
	walkJSON("$", a, g, tolerated, &diffs)
	return diffs
}

func walkJSON(path string, a, g any, tolerated map[string]struct{}, out *[]string) {
	if _, ok := tolerated[path]; ok {
		// Only require type-match at this path.
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", g) {
			*out = append(*out, fmt.Sprintf("%s: type drift actual=%T golden=%T", path, a, g))
		}
		return
	}
	switch gv := g.(type) {
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: golden is object, actual is %T", path, a))
			return
		}
		for k, gvv := range gv {
			avv, present := av[k]
			if !present {
				*out = append(*out, fmt.Sprintf("%s.%s: missing in actual", path, k))
				continue
			}
			walkJSON(path+"."+k, avv, gvv, tolerated, out)
		}
		for k := range av {
			if _, present := gv[k]; !present {
				*out = append(*out, fmt.Sprintf("%s.%s: unexpected in actual (not in golden)", path, k))
			}
		}
	case []any:
		av, ok := a.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: golden is array, actual is %T", path, a))
			return
		}
		if len(av) != len(gv) {
			*out = append(*out, fmt.Sprintf("%s: length actual=%d golden=%d", path, len(av), len(gv)))
			return
		}
		for i := range gv {
			walkJSON(fmt.Sprintf("%s[%d]", path, i), av[i], gv[i], tolerated, out)
		}
	default:
		if !jsonScalarEqual(a, g) {
			*out = append(*out, fmt.Sprintf("%s: value actual=%v golden=%v", path, a, g))
		}
	}
}

func jsonScalarEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

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

// assertBIPConditionsEmpty asserts that status.conditions is
// empty/missing on the BIP CR. Per memory feedback_bip_no_shadow_logic,
// the operator stays dumb on BIPs — no DuplicateTarget condition, no
// Synced churn. The Forwarder resolves duplicates at READ time.
func assertBIPConditionsEmpty(t *testing.T, name string) {
	t.Helper()
	out, err := runCmd("kubectl", "get", "backendidentitypolicy", name, "-n", namespace,
		"-o", "jsonpath={.status.conditions}")
	if err != nil {
		t.Fatalf("§11b get bip/%s conditions: %v\n%s", name, err, out)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed != "" && trimmed != "[]" && trimmed != "null" {
		t.Fatalf("§11b bip/%s: status.conditions MUST stay empty by design "+
			"(no DuplicateTarget reconciler — see feedback_bip_no_shadow_logic); "+
			"got=%q", name, trimmed)
	}
}

// applyPhase4MarketplaceServer brings up the in-cluster nginx-backed
// fixture server for §11c:
//  1. Create ConfigMap mkt-phase4-fixture with marketplace.json keyed
//     off the file at test/e2e/fixtures/phase4_marketplace_internal.json.
//  2. Apply Deployment + Service mkt-test-server (nginx:alpine, ports 80).
//  3. Wait for the Deployment Ready.
//
// Registers t.Cleanup to tear everything down. Idempotent — if the
// ConfigMap/Deployment already exists from a previous run, re-apply
// updates them in place.
//
// The namespace (E2E_NAMESPACE, default ach-system) MUST already exist
// (created by scripts/cluster.sh during cluster-up).
func applyPhase4MarketplaceServer(t *testing.T) {
	t.Helper()

	// Create-from-file pattern: kubectl create configmap with
	// --dry-run=client -o yaml | kubectl apply is the idempotent
	// pattern (plain `kubectl create` errors on AlreadyExists).
	cmYAML, err := runCmd("kubectl", "create", "configmap", "mkt-phase4-fixture",
		"-n", namespace,
		"--from-file=marketplace.json=../../test/e2e/fixtures/phase4_marketplace_internal.json",
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		t.Fatalf("§11c configmap dry-run: %v\n%s", err, cmYAML)
	}
	if out, err := runCmdStdin("kubectl apply -f -", cmYAML); err != nil {
		t.Fatalf("§11c configmap apply: %v\n%s", err, out)
	}

	// Deployment + Service yaml inline (avoids a second fixture file).
	srvYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: mkt-test-server
  namespace: ` + namespace + `
spec:
  replicas: 1
  selector:
    matchLabels: { app: mkt-test-server }
  template:
    metadata:
      labels: { app: mkt-test-server }
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
        ports: [{ containerPort: 80 }]
        volumeMounts:
        - { name: fixture, mountPath: /usr/share/nginx/html }
      volumes:
      - name: fixture
        configMap:
          name: mkt-phase4-fixture
---
apiVersion: v1
kind: Service
metadata:
  name: mkt-test-server
  namespace: ` + namespace + `
spec:
  selector: { app: mkt-test-server }
  ports: [{ port: 80, targetPort: 80 }]
`
	if out, err := runCmdStdin("kubectl apply -f -", srvYAML); err != nil {
		t.Fatalf("§11c server apply: %v\n%s", err, out)
	}

	if out, err := runCmdLonger(60*time.Second,
		"kubectl", "rollout", "status", "-n", namespace,
		"deployment/mkt-test-server", "--timeout=60s",
	); err != nil {
		t.Fatalf("§11c server rollout: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "deployment", "mkt-test-server",
			"-n", namespace, "--wait=false", "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "service", "mkt-test-server",
			"-n", namespace, "--wait=false", "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "configmap", "mkt-phase4-fixture",
			"-n", namespace, "--wait=false", "--ignore-not-found")
	})
}

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
