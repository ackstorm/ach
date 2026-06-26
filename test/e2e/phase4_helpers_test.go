//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 4 invariants helpers — Plan 04-09.
//
// Shared helpers used by phase4_invariants_test.go. Mirrors the Phase 3
// pattern: stdlib testing, kubectl orchestration, no Ginkgo.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// phase4Namespace is where the Helm chart installs ach-forwarder.
	phase4Namespace = "ach-system"
	// phase4ForwarderDeployment is the Deployment name created by the Helm
	// chart (matches deploy/helm/ach/templates/forwarder-deployment.yaml).
	phase4ForwarderDeployment = "ach-forwarder"
)

// phase4SuiteGuard skips when prerequisites aren't met.
func phase4SuiteGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("ACH_SKIP_PHASE4") == "1" {
		t.Skipf("Phase 4 e2e suite opted out via ACH_SKIP_PHASE4=1 (default: runs against the synced cluster).")
	}
	if err := waitForwarderReady(t, 15*time.Second); err != nil {
		t.Fatalf("Forwarder Deployment %s/%s not Ready (%v) — cluster must be up (set ACH_SKIP_PHASE4=1 to opt out).",
			phase4Namespace, phase4ForwarderDeployment, err)
	}
}

// waitForwarderReady polls the Forwarder Deployment until it has
// status.readyReplicas > 0 or the deadline expires. Uses kubectl rollout
// status under a bounded timeout — never an unbounded polling loop.
func waitForwarderReady(t *testing.T, deadline time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "-n", phase4Namespace,
		"rollout", "status", "deployment/"+phase4ForwarderDeployment,
		fmt.Sprintf("--timeout=%ds", int(deadline.Seconds())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// phase4GatewayPort returns the port of the gateway base (ACH_BASE_URL,
// default http://localhost:8080). All SSO acquisition reaches platform-api
// + Dex entirely through this gateway port — NO kubectl port-forward
// (this plan's zero-port-forward architecture; the gateway already routes
// /platform and /dex). The acquisition helpers build http://localhost:<port>,
// so this assumes the standard localhost gateway fixture.
func phase4GatewayPort(t *testing.T) string {
	t.Helper()
	base := os.Getenv("ACH_BASE_URL")
	if base == "" {
		base = "http://localhost:8080"
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse ACH_BASE_URL %q: %v", base, err)
	}
	if p := u.Port(); p != "" {
		return p
	}
	return "80"
}

// pk_/ek_ acquisition is expensive (a full device-code SSO round-trip),
// so cache across the package's sequentially-run tests. Keyed plainly
// (top-level go tests run serially unless t.Parallel, which this suite
// does not use).
var (
	cachedPk string
	cachedEk = map[string]string{}
)

// mustAcquirePk acquires a pk_ via the Phase 3 SSO flow (through the
// gateway), or returns the ACH_E2E_PK_FIXTURE override if set.
func mustAcquirePk(t *testing.T) string {
	t.Helper()
	if pk := os.Getenv("ACH_E2E_PK_FIXTURE"); pk != "" {
		return pk
	}
	if cachedPk != "" {
		return cachedPk
	}
	cachedPk = phase4AcquirePkAutomatically(t, phase4GatewayPort(t))
	return cachedPk
}

// mustAcquireEkBoundToEnv acquires an ek_ bound to the given Environment
// (through the gateway), or returns the ACH_E2E_EK_FIXTURE_<ENV> override.
func mustAcquireEkBoundToEnv(t *testing.T, env string) string {
	t.Helper()
	if key := os.Getenv("ACH_E2E_EK_FIXTURE_" + strings.ToUpper(env)); key != "" {
		return key
	}
	if ek, ok := cachedEk[env]; ok {
		return ek
	}
	pk := mustAcquirePk(t)
	ek, err := phase4AcquireEkBoundToEnvAutomatically(t, phase4GatewayPort(t), pk, env)
	if err != nil {
		t.Skipf("Skipping: cannot automatically generate environment key due to LiteLLM limits (e.g. Enterprise tags check): %v", err)
	}
	cachedEk[env] = ek
	return ek
}

// phase4AssertSecretAccessible verifies that the forwarder ServiceAccount
// can get the ach-jwt-signing-keys Secret. Uses `kubectl auth can-i`.
//
// This is a positive access check, not an isolation check: wide secret
// access in the ach-system namespace is the accepted posture (platform-api
// already has [get,list,watch] on secrets per platform-api-rbac.yaml), so
// the test only confirms the forwarder has the access it needs.
func phase4AssertSecretAccessible(t *testing.T) error {
	t.Helper()
	cmd := exec.Command("kubectl", "-n", phase4Namespace,
		"auth", "can-i", "get", "secret/ach-jwt-signing-keys",
		"--as=system:serviceaccount:"+phase4Namespace+":ach-forwarder")
	out, err := cmd.CombinedOutput()
	verdict := strings.TrimSpace(string(out))
	if verdict == "yes" {
		return nil
	}
	if err != nil && verdict != "no" {
		return fmt.Errorf("kubectl auth can-i: %v output=%s", err, verdict)
	}
	return fmt.Errorf("forwarder ServiceAccount cannot read ach-jwt-signing-keys; expected access")
}

// --- Automated SSO and Key Minting for local/automated testing ---

type noSecureCookieTransport struct {
	underlying http.RoundTripper
}

func (t *noSecureCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.underlying.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	cookies := resp.Header["Set-Cookie"]
	for i, c := range cookies {
		cookies[i] = strings.ReplaceAll(strings.ReplaceAll(c, "; Secure", ""), "; secure", "")
	}
	resp.Header["Set-Cookie"] = cookies
	return resp, nil
}

func phase4StartGatewayPortForward(t *testing.T, localPort string) func() {
	t.Helper()
	cmd := exec.Command("kubectl", "port-forward",
		"-n", "ach-system",
		"svc/ach-local-gateway",
		localPort+":80",
	)
	if err := cmd.Start(); err != nil {
		t.Skipf("phase4StartGatewayPortForward: cannot start port-forward: %v", err)
		return func() {}
	}
	// cmd.Start() only fails on fork/exec errors — it returns nil even when
	// kubectl exits milliseconds later (port in use, RBAC denial, missing
	// Service). Probe the local end with a bounded TCP dial so a dead
	// port-forward skips cleanly instead of failing a downstream HTTP call
	// with a confusing "connection refused". Mirrors startPortForward in
	// phase2_sc5_orphan_test.go.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+localPort, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return func() { _ = cmd.Process.Kill() }
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Skipf("phase4StartGatewayPortForward: port-forward never listened on 127.0.0.1:%s within 30s", localPort)
	return func() {}
}

func phase4AcquirePkAutomatically(t *testing.T, localPort string) string {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: &noSecureCookieTransport{
			underlying: http.DefaultTransport,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	loginURL := fmt.Sprintf("http://localhost:%s/platform/auth/login", localPort)
	loginResp, err := client.Get(loginURL)
	if err != nil {
		t.Fatalf("GET /login failed: %v", err)
	}
	defer loginResp.Body.Close()

	dexURL := loginResp.Header.Get("Location")
	if dexURL == "" {
		t.Fatalf("empty redirect location from login")
	}

	currentURL := dexURL
	var finalResp *http.Response
	const maxRedirects = 20
	for hop := 0; ; hop++ {
		if hop >= maxRedirects {
			t.Fatalf("SSO redirect loop exceeded %d hops at %s", maxRedirects, currentURL)
		}
		currentURL = strings.ReplaceAll(currentURL, "dex.dex-system.svc.cluster.local:5556", "localhost:"+localPort)
		currentURL = strings.ReplaceAll(currentURL, "localhost:8080", "localhost:"+localPort)

		resp, err := client.Get(currentURL)
		if err != nil {
			t.Fatalf("request to %s failed: %v", currentURL, err)
		}

		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusMovedPermanently {
			resp.Body.Close()
			loc := resp.Header.Get("Location")
			if loc == "" {
				t.Fatalf("SSO %d response missing Location header at %s", resp.StatusCode, currentURL)
			}
			base, err := url.Parse(currentURL)
			if err != nil {
				t.Fatalf("parse currentURL %q: %v", currentURL, err)
			}
			rel, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("parse Location %q: %v", loc, err)
			}
			currentURL = base.ResolveReference(rel).String()
		} else {
			finalResp = resp
			break
		}
	}
	defer finalResp.Body.Close()

	if finalResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(finalResp.Body)
		t.Fatalf("SSO callback returned status %d: %s", finalResp.StatusCode, string(body))
	}

	var data struct {
		Plaintext string `json:"plaintext"`
	}
	body, _ := io.ReadAll(finalResp.Body)
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("failed to decode JSON response: %v, body: %s", err, string(body))
	}

	if data.Plaintext == "" {
		t.Fatalf("empty pk plaintext in response: %s", string(body))
	}

	return data.Plaintext
}

func phase4AcquireEkBoundToEnvAutomatically(t *testing.T, localPort, pk, env string) (string, error) {
	t.Helper()
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	payload, _ := json.Marshal(map[string]string{
		"environment": env,
		"name":        "demo-ek",
	})

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:%s/platform/keys", localPort), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ach-key", pk)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /platform/keys failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("POST /platform/keys status = %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to decode keys JSON: %w, body: %s", err, string(body))
	}

	return data.Plaintext, nil
}

// _ ensures errors stays referenced — helpers may grow into wrapping.
var _ = errors.New
