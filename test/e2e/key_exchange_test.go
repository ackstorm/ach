//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Key exchange e2e: a pod-bound ServiceAccount token reviewed by the kind API
// server buys an ACH JWT for kilgore's `demo` access through a KeyGrant; the
// JWT works wherever an ek_ does, and stops working once the grant is gone.
package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/audit"
)

const (
	kxNamespace = "ach-e2e-exchange"
	kxSA        = "agent-e2e"
	kxGrant     = "e2e-exchange"
)

func kxApply(t *testing.T, manifest string) {
	t.Helper()
	if out, err := runCmdStdin("kubectl apply -f -", manifest); err != nil {
		t.Fatalf("kubectl apply: %v\n%s", err, out)
	}
}

func kxSetup(t *testing.T) {
	t.Helper()
	kxApply(t, fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[2]s
  namespace: %[1]s
---
apiVersion: v1
kind: Pod
metadata:
  name: %[2]s
  namespace: %[1]s
spec:
  serviceAccountName: %[2]s
  automountServiceAccountToken: false
  containers:
  - name: idle
    image: ach-mock:e2e
    imagePullPolicy: IfNotPresent
    args: ["a2a"]
    env:
    - name: MOCK_BIND_ADDRESS
      value: ":9090"
`, kxNamespace, kxSA))
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "delete", "keygrant", kxGrant, "-n", namespace, "--ignore-not-found", "--wait=false")
		_, _ = runCmd("kubectl", "delete", "rolebinding", "e2e-kx-admin", "e2e-kx-edit", "-n", namespace, "--ignore-not-found")
		_, _ = runCmd("kubectl", "delete", "namespace", kxNamespace, "--ignore-not-found", "--wait=false")
	})
	if out, err := runCmdLonger(150*time.Second, "kubectl", "wait", "--for=condition=Ready",
		"pod/"+kxSA, "-n", kxNamespace, "--timeout=120s"); err != nil {
		t.Fatalf("agent pod never Ready: %v\n%s", err, out)
	}
}

func kxToken(t *testing.T, audience string, podBound bool) string {
	t.Helper()
	args := []string{"create", "token", kxSA, "-n", kxNamespace, "--audience", audience, "--duration", "10m"}
	if podBound {
		args = append(args, "--bound-object-kind", "Pod", "--bound-object-name", kxSA)
	}
	out, err := runCmd("kubectl", args...)
	if err != nil {
		t.Fatalf("kubectl create token: %v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

func kxExchange(t *testing.T, base, bearer string) (int, map[string]any) {
	t.Helper()
	body := `{"identity":"` + ekStateUserEmail + `","environment":"demo"}`
	req, err := http.NewRequest(http.MethodPost, base+"/platform/keys/exchange", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func kxErrCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// kxForwarderBearer is forwarderStatus with the credential in Authorization.
func kxForwarderBearer(t *testing.T, base, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"exchange e2e probe"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forwarder: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestKeyExchange(t *testing.T) {
	phase4SuiteGuard(t)
	base := "http://" + phase4GatewayAuthority(t)
	audience := base                          // ACH_BASE_URL of the e2e release
	userJWT := oauthLogin(t, base, "").Access // kilgore logs in first: teams enrolled, grant can be Ready
	kxSetup(t)
	bound := kxToken(t, audience, true)

	t.Run("guards_and_unbound_tokens", func(t *testing.T) {
		for name, bearer := range map[string]string{
			"ek_ key":        "ek-" + strings.Repeat("0", 64),
			"user OAuth JWT": userJWT,
			"wrong audience": kxToken(t, "https://elsewhere.example", true),
			"unbound token":  kxToken(t, audience, false),
		} {
			if code, m := kxExchange(t, base, bearer); code != http.StatusUnauthorized || kxErrCode(m) != "invalid_token" {
				t.Errorf("%s: %d %v", name, code, m)
			}
		}
	})

	t.Run("no_grant", func(t *testing.T) {
		_, _ = runCmd("kubectl", "delete", "keygrant", kxGrant, "-n", namespace, "--ignore-not-found")
		if code, m := kxExchange(t, base, bound); code != http.StatusForbidden || kxErrCode(m) != "not_granted" {
			t.Fatalf("%d %v", code, m)
		}
	})

	kxApply(t, fmt.Sprintf(`apiVersion: ach.ackstorm.ai/v1alpha1
kind: KeyGrant
metadata:
  name: %s
  namespace: %s
spec:
  identity: %s
  environments: [demo]
  requesters:
    serviceAccounts: [%s/%s]
`, kxGrant, namespace, ekStateUserEmail, kxNamespace, kxSA))
	if out, err := runCmdLonger(150*time.Second, "kubectl", "wait", "--for=condition=Ready",
		"keygrant/"+kxGrant, "-n", namespace, "--timeout=120s"); err != nil {
		t.Fatalf("KeyGrant never Ready: %v\n%s", err, out)
	}

	var keyID, first string
	t.Run("exchange_and_reuse", func(t *testing.T) {
		code, m := kxExchange(t, base, bound)
		if code != http.StatusOK {
			t.Fatalf("%d %v", code, m)
		}
		keyID, _ = m["key_id"].(string)
		first, _ = m["token"].(string)
		if !strings.HasPrefix(keyID, "ekid_") || m["expires_in"] != float64(900) || first == "" {
			t.Fatalf("%v", m)
		}
		code, m = kxExchange(t, base, bound)
		if code != http.StatusOK || m["key_id"] != keyID || m["token"] == first {
			t.Fatalf("a second exchange must reuse %s with a new JWT: %d %v", keyID, code, m)
		}
		k := keysAPI{t: t, base: base, jwt: userJWT}
		if s, r := k.state(keyID); s != "active" {
			t.Fatalf("exchange key state %s %v", s, r)
		}
	})
	if keyID == "" {
		t.Fatal("no exchange key; later subtests depend on it")
	}

	t.Run("jwt_works_where_an_ek_does", func(t *testing.T) {
		if got := forwarderStatus(t, base, first); got != http.StatusOK {
			t.Errorf("forwarder x-ach-key: %d", got)
		}
		if got := kxForwarderBearer(t, base, first); got != http.StatusOK {
			t.Errorf("forwarder Authorization: %d", got)
		}
		if got := hydrateStatus(t, base, first, "demo"); got != http.StatusOK {
			t.Errorf("platform-api hydrate: %d", got)
		}
		if got := contentStatus(t, base, first, "demo", demoPromptName); got != http.StatusOK {
			t.Errorf("content-service: %d", got)
		}
	})

	t.Run("audit_without_credentials", func(t *testing.T) {
		logs := phase3CapturePlatformAPILogs(t, 3000)
		issued, denied := 0, 0
		for _, rec := range phase3ParseAuditLines(logs) {
			if rec.Action != audit.ActionEkExchange {
				continue
			}
			switch rec.Outcome {
			case audit.OutcomeIssued:
				issued++
			case audit.OutcomeNotGranted:
				denied++
			}
		}
		if issued < 2 || denied < 1 {
			t.Fatalf("platform.ek.exchange events: issued=%d not_granted=%d", issued, denied)
		}
		for _, secret := range []string{bound, first} {
			if strings.Contains(string(logs), secret) {
				t.Fatal("a credential reached the platform-api logs")
			}
		}
	})

	t.Run("admin_and_edit_cannot_write_keygrants", func(t *testing.T) {
		sa := "system:serviceaccount:" + kxNamespace + ":" + kxSA
		for _, role := range []string{"admin", "edit"} {
			rb := "e2e-kx-" + role
			if out, err := runCmd("kubectl", "create", "rolebinding", rb, "--clusterrole="+role,
				"--serviceaccount="+kxNamespace+":"+kxSA, "-n", namespace); err != nil {
				t.Fatalf("bind %s: %v\n%s", role, err, out)
			}
			t.Cleanup(func() { _, _ = runCmd("kubectl", "delete", "rolebinding", rb, "-n", namespace, "--ignore-not-found") })
			out, _ := runCmd("kubectl", "auth", "can-i", "create", "keygrants.ach.ackstorm.ai", "-n", namespace, "--as="+sa)
			if strings.TrimSpace(out) != "no" {
				t.Errorf("%s may create KeyGrants: %q", role, out)
			}
			out, _ = runCmd("kubectl", "auth", "can-i", "create", "configmaps", "-n", namespace, "--as="+sa)
			if strings.TrimSpace(out) != "yes" {
				t.Errorf("control check: the %s binding is not effective: %q", role, out)
			}
		}
	})

	t.Run("grant_removal_cuts_access_and_revokes", func(t *testing.T) {
		if out, err := runCmd("kubectl", "delete", "keygrant", kxGrant, "-n", namespace, "--timeout=60s"); err != nil {
			t.Fatalf("delete KeyGrant: %v\n%s", err, out)
		}
		within(t, 90*time.Second, "exchange JWT refused once its grant is gone", func() bool {
			return forwarderStatus(t, base, first) == http.StatusUnauthorized
		})
		k := keysAPI{t: t, base: base, jwt: userJWT}
		within(t, 60*time.Second, "operator revokes the exchange key", func() bool {
			s, _ := k.state(keyID)
			return s == "revoked"
		})
		if code, m := kxExchange(t, base, bound); code != http.StatusForbidden || kxErrCode(m) != "not_granted" {
			t.Fatalf("exchange after grant removal: %d %v", code, m)
		}
	})
}
