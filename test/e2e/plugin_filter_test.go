//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Plugin tarball content-filter E2E (issue #26).
//
// Verifies the manifest-aware whitelist filter wired into
// materializeExternalRef (`internal/sources/pluginpack`) end-to-end on
// a live cluster, without any external GitHub fetch:
//
//  1. Build a deterministic synthetic .tar.gz in-test with a known
//     must-include and must-exclude set.
//  2. Mount it via a ConfigMap binaryData entry into a self-contained
//     in-cluster nginx Deployment + Service.
//  3. Apply a Plugin CR (type: http) pointing at the in-cluster URL.
//  4. Wait Synced=True.
//  5. Inspect the cached file inside the operator's content-service
//     container and assert the filter:
//       - filtered tarball is strictly smaller than the raw input
//       - filtered size ≤ a generous upper bound (cap-sanity)
//       - every must-include entry is present
//       - every must-exclude entry is absent
//
// Hermetic — no upstream network, no rate limits. Re-runnable: the
// external_refs row is cleared on entry so the within-interval and 304
// fast-paths never short-circuit the filter.
//
// Run via:
//
//	./scripts/dev.sh make e2e-focus RUN='TestPluginFilter'
//
// Runs whenever the e2e suite runs against a live cluster (no opt-in flag;
// mirrors phase5SuiteGuard).

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	pfNamespace      = "ach-system"
	pfPluginName     = "filter-e2e"
	pfFixtureName    = "plugin-filter-fixture"
	pfTarballKey     = "test-plugin.tar.gz"
	pfOperatorDeploy = "ach-operator"
	pfCSContainer    = "content-service"

	// pfMaxFilteredBytes is the loose upper bound for the filtered
	// tarball — a sanity cap, not a contract. The synthetic input is
	// ~10 KiB raw; the filtered output should be a few KiB.
	pfMaxFilteredBytes = 32 * 1024
)

// pfMustInclude is the set of entries that MUST appear in the filtered
// tarball. Files only — directory headers are checked separately because
// busybox tar lists trailing-slash entries that depend on platform.
var pfMustInclude = []string{
	".claude-plugin/plugin.json",
	"src/utils/helper.js",
	"agents/agent-a.md",
	"commands/cmd-a.md",
	"skills/skill-a.md",
	"hooks/hook-a.js",
	"LICENSE",
	"README.md",
}

// pfMustExclude is the set of entries that MUST NOT appear in the
// filtered tarball. Each is real noise from real-world Claude plugin
// repos (caveman, anthropic-templates).
var pfMustExclude = []string{
	"AGENTS.md",
	"GEMINI.md",
	".codex/foo.md",
	".junie/bar.md",
	"tests/test_a.py",
}

func TestPluginFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	raw := pfBuildSyntheticTarball(t)
	t.Logf("synthesized input tarball: %d bytes, %d entries (incl. noise)", len(raw), len(pfMustInclude)+len(pfMustExclude))

	pfSetupFixture(t, ctx, raw)
	t.Cleanup(func() { pfTeardownFixture(t) })

	pfClearPriorState(t, ctx)

	pfApplyPluginCR(t, ctx)
	t.Cleanup(func() { pfDeletePluginCR(t) })

	pfWaitSynced(t, ctx)
	pfVerifyCache(t, ctx, int64(len(raw)))
}

// pfBuildSyntheticTarball assembles the input fixture: a gzipped tar
// containing pfMustInclude + pfMustExclude entries plus a manifest that
// references src/utils/helper.js via ${CLAUDE_PLUGIN_ROOT}.
func pfBuildSyntheticTarball(t *testing.T) []byte {
	t.Helper()

	manifest := `{
  "name": "filter-e2e-plugin",
  "version": "0.0.1",
  "description": "Synthetic fixture for issue #26 E2E.",
  "commands": [
    {
      "name": "test-cmd",
      "source": "${CLAUDE_PLUGIN_ROOT}/src/utils/helper.js"
    }
  ]
}
`
	noisePad := strings.Repeat("noise-padding ", 100) // ~1.4 KiB per noise entry

	entries := []struct {
		name string
		body string
	}{
		// must-include
		{".claude-plugin/plugin.json", manifest},
		{"src/utils/helper.js", "// referenced by manifest\nmodule.exports = () => 'hello';\n"},
		{"agents/agent-a.md", "# agent-a\n"},
		{"commands/cmd-a.md", "# cmd-a\n"},
		{"skills/skill-a.md", "# skill-a\n"},
		{"hooks/hook-a.js", "// hook-a\n"},
		{"LICENSE", "Apache-2.0\n"},
		{"README.md", "# filter-e2e-plugin\n"},
		// must-exclude (padded so raw > filtered is clearly observable)
		{"AGENTS.md", noisePad},
		{"GEMINI.md", noisePad},
		{".codex/foo.md", noisePad},
		{".junie/bar.md", noisePad},
		{"tests/test_a.py", noisePad},
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("write body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// pfSetupFixture creates the ConfigMap (binaryData), Service, and
// Deployment that serves the synthetic tarball at
// http://plugin-filter-fixture.ach-system.svc.cluster.local/test-plugin.tar.gz.
// Waits until the Deployment is Ready before returning.
func pfSetupFixture(t *testing.T, ctx context.Context, tarball []byte) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString(tarball)

	manifest := fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[2]s
binaryData:
  %[3]s: %[4]s
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  selector:
    app: %[1]s
  ports:
    - name: http
      port: 80
      targetPort: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 80
              name: http
          readinessProbe:
            httpGet:
              path: /%[3]s
              port: 80
            initialDelaySeconds: 1
            periodSeconds: 1
          volumeMounts:
            - mountPath: /usr/share/nginx/html
              name: html
              readOnly: true
      volumes:
        - name: html
          configMap:
            name: %[1]s
`, pfFixtureName, pfNamespace, pfTarballKey, b64)

	pfKubectlApplyStdin(t, ctx, manifest)
	pfKubectlRolloutStatus(t, ctx, pfFixtureName, 120*time.Second)
}

// pfTeardownFixture removes the Deployment, Service, and ConfigMap.
func pfTeardownFixture(t *testing.T) {
	t.Helper()
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, kind := range []string{"deployment", "service", "configmap"} {
		_ = pfKubectlRun(bgCtx, "-n", pfNamespace, "delete", kind, pfFixtureName, "--ignore-not-found", "--wait=false")
	}
}

// pfClearPriorState ensures the test starts from a known-clean state by
// deleting any prior Plugin CR with the same name. The --wait=true blocks
// until the §10.3 finalizer drain completes, which hard-deletes the
// external_refs projection row (plugin_controller.go reconcileDeletion →
// achdb.DeleteExternalRef) — so the within-interval / 304 fast-paths can't
// short-circuit the filter on re-runs without a separate SQL DELETE. The
// operator is gated Ready by cluster.sh verify_all before any test runs, so
// the cascade is reliable; an orphaned row could only survive an
// operator-down delete, which the e2e harness rules out.
func pfClearPriorState(t *testing.T, ctx context.Context) {
	t.Helper()
	_ = pfKubectlRun(ctx, "-n", pfNamespace, "delete", "plugin", pfPluginName, "--ignore-not-found", "--wait=true")
}

// pfApplyPluginCR applies a Plugin CR pointing at the in-cluster
// fixture Service via type: http.
func pfApplyPluginCR(t *testing.T, ctx context.Context) {
	t.Helper()
	manifest := fmt.Sprintf(`---
apiVersion: ach.ackstorm.ai/v1alpha1
kind: Plugin
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  type: http
  refresh:
    interval: 10m
    maxStaleness: 1h
  http:
    url: http://%[3]s.%[2]s.svc.cluster.local/%[4]s
`, pfPluginName, pfNamespace, pfFixtureName, pfTarballKey)

	pfKubectlApplyStdin(t, ctx, manifest)
}

// pfDeletePluginCR removes the Plugin CR (best-effort, background ctx).
func pfDeletePluginCR(t *testing.T) {
	t.Helper()
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = pfKubectlRun(bgCtx, "-n", pfNamespace, "delete", "plugin", pfPluginName, "--ignore-not-found", "--wait=false")
}

// pfWaitSynced blocks until plugin/<name>'s Synced condition is True.
func pfWaitSynced(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := pfKubectlRun(ctx, "-n", pfNamespace, "wait",
		"--for=jsonpath={.status.conditions[?(@.type==\"Synced\")].status}=True",
		"plugin/"+pfPluginName, "--timeout=180s"); err != nil {
		out, _, _ := kubectlExec(ctx, pfNamespace, pfOperatorDeploy, "manager",
			"sh", "-c", "echo conditions:; date")
		t.Fatalf("plugin/%s did not reach Synced=True: %v\n%s", pfPluginName, err, out)
	}
}

// pfVerifyCache reads the operator's cache file via the content-service
// container and runs the filter assertions.
func pfVerifyCache(t *testing.T, ctx context.Context, rawSize int64) {
	t.Helper()

	cachePath := fmt.Sprintf("/var/cache/ach/plugin/%s.tar.gz", pfPluginName)

	sizeOut, _, err := kubectlExec(ctx, pfNamespace, pfOperatorDeploy, pfCSContainer,
		"stat", "-c", "%s", cachePath)
	if err != nil {
		t.Fatalf("stat cached tarball: %v", err)
	}
	filteredSize, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		t.Fatalf("parse cache size %q: %v", sizeOut, err)
	}
	t.Logf("filtered cache size: %d bytes (raw input was %d bytes)", filteredSize, rawSize)

	if filteredSize >= rawSize {
		t.Errorf("filtered size %d ≥ raw size %d — filter did not reduce", filteredSize, rawSize)
	}
	if filteredSize > pfMaxFilteredBytes {
		t.Errorf("filtered size %d > sanity cap %d", filteredSize, pfMaxFilteredBytes)
	}

	listOut, _, err := kubectlExec(ctx, pfNamespace, pfOperatorDeploy, pfCSContainer,
		"sh", "-c", "tar tzf "+cachePath)
	if err != nil {
		t.Fatalf("tar tzf cached tarball: %v", err)
	}
	entries := pfParseTarList(listOut)
	t.Logf("filtered cache entries (%d): %v", len(entries), entries)

	for _, want := range pfMustInclude {
		if !contains(entries, want) {
			t.Errorf("MISSING expected entry: %q (filter dropped a must-include)", want)
		}
	}
	for _, banned := range pfMustExclude {
		if contains(entries, banned) {
			t.Errorf("PRESENT banned entry: %q (filter failed to drop a must-exclude)", banned)
		}
	}
}

// pfParseTarList returns the entry names sorted, with trailing slashes
// preserved as written by tar so the grep set matches verbatim.
func pfParseTarList(out string) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		cleaned = append(cleaned, l)
	}
	sort.Strings(cleaned)
	return cleaned
}

// contains reports whether haystack has needle (exact match).
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// pfKubectlApplyStdin pipes manifest YAML to `kubectl apply -f -`.
func pfKubectlApplyStdin(t *testing.T, ctx context.Context, manifest string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("kubectl apply -f -: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
}

// pfKubectlRolloutStatus blocks until the named Deployment is Ready.
func pfKubectlRolloutStatus(t *testing.T, ctx context.Context, name string, timeout time.Duration) {
	t.Helper()
	if err := pfKubectlRun(ctx, "-n", pfNamespace, "rollout", "status",
		"deployment/"+name, fmt.Sprintf("--timeout=%s", timeout)); err != nil {
		t.Fatalf("rollout status %s: %v", name, err)
	}
}

// pfKubectlRun runs `kubectl <args...>` and returns an error wrapping
// stderr on non-zero exit. Stdout is discarded — callers that need it
// use kubectlExec directly.
func pfKubectlRun(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
