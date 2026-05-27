//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 promotion shared helpers — force-refresh round-trip, golden
// JSON shape diff with tolerated paths, ConfigMap-backed nginx
// fixture-server bring-up, BIP finalizer probes.

package e2e

import (
	"encoding/json"
	"fmt"
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
