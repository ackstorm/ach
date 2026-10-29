//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- Phase 4 SC3 (BIP alpha-first JWT mint) helpers ------------------------
//
// The forwarder bipcache resolves the winning BackendIdentityPolicy for a
// (targetKind, targetName) by sorting matches on metadata.name ASC and taking
// the alpha-FIRST row; if that row's forwardIdentityJWT is false it is an
// explicit opt-out and NO JWT is minted (internal/forwarder/bipcache/cache.go
// Resolve, frozen alpha-FIRST tiebreak per G15). These helpers apply/delete
// throwaway BIPs on a shared target and assert which one won by reading the
// ach-mcp-echo /__capture/last endpoint.

// sc3ApplyBIP applies a BackendIdentityPolicy targeting the given MCPServer
// route with the given forwardIdentityJWT value, and registers a t.Cleanup
// that deletes it. The name is chosen by the caller so the alpha-first tiebreak
// is unambiguous.
func sc3ApplyBIP(t *testing.T, name, route string, forwardJWT bool) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: ach.ackstorm.ai/v1alpha1
kind: BackendIdentityPolicy
metadata:
  name: %s
  namespace: %s
spec:
  target:
    kind: MCPServer
    name: %s
  forwardIdentityJWT: %t
`, name, phase4Namespace, route, forwardJWT)
	if out, err := runCmdStdin("kubectl apply -f -", manifest); err != nil {
		t.Fatalf("sc3ApplyBIP %s: %v\n%s", name, err, out)
	}
	t.Cleanup(func() { sc3DeleteBIP(t, name) })
	sc3WaitBIPObserved(t, name)
}

// sc3DeleteBIP deletes a BIP by name (idempotent — ignores not-found).
func sc3DeleteBIP(t *testing.T, name string) {
	t.Helper()
	if out, err := runCmd("kubectl", "-n", phase4Namespace,
		"delete", "backendidentitypolicy", name, "--ignore-not-found"); err != nil {
		t.Logf("sc3DeleteBIP %s (cleanup, non-fatal): %v\n%s", name, err, out)
	}
}

// sc3WaitBIPObserved blocks until the operator has reconciled the BIP
// (status.observedGeneration == metadata.generation), mirroring cluster.sh
// wait_bip_reconciled. After this the projection row + NOTIFY have fired and
// the forwarder bipcache refreshes via its LISTEN within ~1s.
func sc3WaitBIPObserved(t *testing.T, name string) {
	t.Helper()
	gen, err := runCmd("kubectl", "-n", phase4Namespace, "get",
		"backendidentitypolicy", name, "-o", "jsonpath={.metadata.generation}")
	if err != nil {
		t.Fatalf("sc3WaitBIPObserved %s: read generation: %v\n%s", name, err, gen)
	}
	gen = strings.TrimSpace(gen)
	if out, err := runCmd("kubectl", "-n", phase4Namespace, "wait",
		"--for=jsonpath={.status.observedGeneration}="+gen, "--timeout=60s",
		"backendidentitypolicy/"+name); err != nil {
		t.Fatalf("sc3WaitBIPObserved %s: %v\n%s", name, err, out)
	}
}

// sc3AssertJWTPresentEventually drives an echo through the forwarder and reads
// the mcp-echo capture, retrying until jwt_present matches want or the bounded
// budget is exhausted (the bipcache LISTEN refresh is near-instant but the
// reconcile→project→NOTIFY→cache hop has a small lag). Bounded retry with an
// explicit failure path per the no-naked-loop rule. Returns the matching snap
// so the caller can assert claims.
func sc3AssertJWTPresentEventually(t *testing.T, gatewayLocal, mcpEchoLocal, pk, route string, want bool) captureSnap {
	t.Helper()
	const attempts = 30
	var snap captureSnap
	for i := 0; i < attempts; i++ {
		resetMcpEchoCapture(t, mcpEchoLocal)
		marker := fmt.Sprintf("sc3-%t-%d", want, i)
		resp := callEchoViaForwarder(t, gatewayLocal, pk, route, marker)
		if !strings.Contains(resp, marker) {
			t.Fatalf("sc3: echo did not round-trip marker %q on /mcp/%s: %s", marker, route, resp)
		}
		snap = readMcpEchoCapture(t, mcpEchoLocal)
		if snap.JWTPresent == want {
			return snap
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("sc3: jwt_present never became %t within %ds on /mcp/%s; last capture=%+v",
		want, attempts, route, snap)
	return snap
}
