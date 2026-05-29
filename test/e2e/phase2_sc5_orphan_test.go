//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 02 SC#5 e2e — orphan-cleanup interval-floor enforcement +
// live revocation + audit-event wire format. Ports the assertions
// from the (now deleted) scripts/uat-sc5.sh and scripts/uat-g1.sh
// into the stdlib e2e suite against the kind+Helm cluster brought
// up by scripts/cluster.sh — the in-cluster LiteLLM Helm release
// makes the SC#5 path that the Phase 02.1 e2e overlay deliberately
// skipped (LiteLLM-unreachable by design) reachable for the first
// time without docker-compose.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/orphan"
)

const (
	sc5UserID       = "u-e2e-sc5"
	sc5KeyID        = "pkid_e2e_sc5"
	sc5LiteLLMNS    = "litellm-system"
	sc5LiteLLMSvc   = "svc/litellm"
	sc5LiteLLMDBSvc = "svc/litellm-postgresql"
	sc5ACHPgSvc     = "svc/ach-postgres"
	// sc5MasterKey matches test/e2e/cluster/01-base/litellm.values.yaml and
	// config/e2e/litellm_connection.yaml (Secret litellm-master-key).
	sc5MasterKey = "sk-test-master-key"
)

// TestPhase2SC5Orphan asserts Phase 02 ROADMAP Success Criterion #5 — the
// orphan-cleanup loop refuses an interval below the 5-minute floor at
// operator startup AND emits a structured audit event per revocation.
//
// IntervalFloor mutates the in-cluster operator Deployment env, asserts
// the new Pod logs the §18.4 / D-15 error, then restores. OrphanReapLive
// drives one TickOnce() in-process via kubectl port-forwards to the
// in-cluster LiteLLM and Postgres services so the audit JSON wire can
// be parsed and field-asserted directly.
//
// Subtests run sequentially. OrphanReapLive scales the in-cluster
// operator to 0 replicas while it runs so the operator's own ticker does
// not race the test for the orphan revocation.
func TestPhase2SC5Orphan(t *testing.T) {
	t.Run("IntervalFloor", testSC5IntervalFloor)
	t.Run("OrphanReapLive", testSC5OrphanReapLive)
}

// testSC5IntervalFloor — operator process MUST refuse
// ACH_ORPHAN_CLEANUP_INTERVAL=1m at startup with the OP-15 / D-15
// below-minimum error. Asserted by patching the Deployment env then
// grepping the new Pod's container logs.
func testSC5IntervalFloor(t *testing.T) {
	t.Helper()

	if out, err := runCmd("kubectl", "set", "env", "-n", namespace,
		"deployment/ach-operator", "-c", "manager",
		"ACH_ORPHAN_CLEANUP_INTERVAL=1m"); err != nil {
		t.Fatalf("SC#5.IntervalFloor set env: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "set", "env", "-n", namespace,
			"deployment/ach-operator", "-c", "manager",
			"ACH_ORPHAN_CLEANUP_INTERVAL=5m")
		_, _ = runCmdLonger(120*time.Second, "kubectl", "rollout", "status", "-n", namespace,
			"deployment/ach-operator", "--timeout=120s")
	})

	const want = "ACH_ORPHAN_CLEANUP_INTERVAL=1m0s is below minimum 5m0s"
	deadline := time.Now().Add(120 * time.Second)
	var lastLogs string
	for time.Now().Before(deadline) {
		out, err := runCmd("kubectl", "logs", "-n", namespace,
			"deployment/ach-operator", "-c", "manager", "--tail=200")
		if err == nil {
			lastLogs = out
			if strings.Contains(out, want) {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("SC#5.IntervalFloor: operator did not log %q within 120s\n=== last operator logs ===\n%s",
		want, lastLogs)
}

// testSC5OrphanReapLive — full live revocation path.
//
//  1. Scale operator to 0 (no race against its own ticker).
//  2. Port-forward in-cluster LiteLLM + ACH Postgres + LiteLLM Postgres.
//  3. INSERT personal_keys row carrying litellm_user_id=sc5UserID so
//     the user becomes ACH-managed.
//  4. POST /key/generate against in-cluster LiteLLM under sc5UserID
//     (the returned token is an orphan: no ACH row references it).
//  5. UPDATE LiteLLM_VerificationToken SET created_at = now - 11m to
//     push the token past the 10-minute OrphanAgeFloor.
//  6. Build orphan.NewRunnable wired against the same in-cluster
//     LiteLLM + Postgres and call TickOnce; capture audit JSON via
//     a bytes.Buffer-backed logger.
//  7. Parse each emitted line as JSON; assert one line has audit:true,
//     outcome:"revoked", user_id:sc5UserID.
//  8. Cleanup deletes the LiteLLM key and the personal_keys row;
//     restores the operator replica count.
func testSC5OrphanReapLive(t *testing.T) {
	t.Helper()

	if out, err := runCmd("kubectl", "scale", "-n", namespace,
		"deployment/ach-operator", "--replicas=0"); err != nil {
		t.Fatalf("SC#5 scale operator to 0: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = runCmd("kubectl", "scale", "-n", namespace,
			"deployment/ach-operator", "--replicas=1")
		_, _ = runCmdLonger(120*time.Second, "kubectl", "rollout", "status", "-n", namespace,
			"deployment/ach-operator", "--timeout=120s")
	})
	if out, err := runCmdLonger(60*time.Second, "kubectl", "wait", "-n", namespace,
		"--for=delete", "pods", "-l", "app.kubernetes.io/name=ach-operator",
		"--timeout=60s"); err != nil {
		t.Logf("SC#5: wait operator pods deleted (best-effort): %v\n%s", err, out)
	}

	litellmPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000)
	achPgPort := startPortForward(t, namespace, sc5ACHPgSvc, 5432)
	llDBPort := startPortForward(t, sc5LiteLLMNS, sc5LiteLLMDBSvc, 5432)

	litellmURL := fmt.Sprintf("http://127.0.0.1:%d", litellmPort)
	achDBURL := fmt.Sprintf("postgres://ach:ach@127.0.0.1:%d/ach?sslmode=disable", achPgPort)
	llDBURL := fmt.Sprintf("postgres://litellm:litellm@127.0.0.1:%d/litellm?sslmode=disable", llDBPort)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	seedPersonalKey(t, ctx, achDBURL, sc5KeyID, sc5UserID)
	t.Cleanup(func() { cleanupPersonalKey(achDBURL, sc5KeyID) })

	token := createLiteLLMKey(t, ctx, litellmURL, sc5MasterKey, sc5UserID)
	t.Cleanup(func() { _ = deleteLiteLLMKey(litellmURL, sc5MasterKey, token) })

	// Backdate by user_id, not by token: LiteLLM's /key/generate returns
	// the plaintext key in `.key`, but LiteLLM_VerificationToken.token
	// stores the SHA-256 hash of that plaintext. Matching by plaintext
	// against the hash column would update 0 rows silently. user_id is
	// stored as plaintext on the same table and uniquely identifies the
	// orphan key we just generated. Mirrors scripts/uat-sc5.sh (deleted)
	// which used the same WHERE user_id = '...' approach.
	backdateLiteLLMKeyByUser(t, ctx, llDBURL, sc5UserID, 11)

	pool, err := pgxpool.New(ctx, achDBURL)
	if err != nil {
		t.Fatalf("SC#5 pgxpool.New ach: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("SC#5 ach db ping: %v", err)
	}

	auditBuf := &bytes.Buffer{}
	auditLog := audit.NewLogger(auditBuf)
	client := litellm.NewRESTClient(litellmURL, sc5MasterKey, logr.Discard())
	r := orphan.NewRunnable(client, pool, auditLog, 5*time.Minute, logr.Discard())
	r.TickOnce(ctx)

	for _, line := range strings.Split(strings.TrimSpace(auditBuf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Logf("SC#5: non-JSON audit line skipped: %s", line)
			continue
		}
		if ev["audit"] == true &&
			ev["outcome"] == "revoked" &&
			ev["user_id"] == sc5UserID {
			t.Logf("SC#5 audit event: %s", line)
			return
		}
	}
	t.Fatalf("SC#5.OrphanReapLive: no audit event with audit:true + outcome:revoked + user_id:%s\n=== audit buffer ===\n%s",
		sc5UserID, auditBuf.String())
}

// ─── Helpers ───────────────────────────────────────────────────────────

// startPortForward spawns `kubectl port-forward` against the given Service
// on a random free local port and waits for the local end to accept TCP
// before returning. Registers a t.Cleanup that kills the process. The
// caller MUST issue all DB / HTTP calls before its own t.Cleanup-registered
// destructive helpers since cleanups run LIFO.
func startPortForward(t *testing.T, ns, target string, remotePort int) int {
	t.Helper()
	port := pickFreePort(t)
	cmd := exec.Command("kubectl", "port-forward", "-n", ns, target,
		fmt.Sprintf("%d:%d", port, remotePort))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start port-forward %s/%s: %v", ns, target, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return port
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("port-forward %s/%s never became reachable on 127.0.0.1:%d", ns, target, port)
	return 0
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func seedPersonalKey(t *testing.T, ctx context.Context, dbURL, keyID, userID string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New (seed): %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		INSERT INTO personal_keys
		    (key_id, credential_hash, owner_email, status, litellm_user_id, litellm_token, expires_at)
		VALUES
		    ($1, 'e2e-sc5-fake-hash-' || md5(random()::text), 'sc5@example.com',
		     'active', $2, 'placeholder-pending-litellm-generate',
		     now() + interval '7 days')
		ON CONFLICT (key_id) DO NOTHING
	`, keyID, userID); err != nil {
		t.Fatalf("seed personal_keys: %v", err)
	}
}

func cleanupPersonalKey(dbURL, keyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return
	}
	defer pool.Close()
	_, _ = pool.Exec(ctx, `DELETE FROM personal_keys WHERE key_id=$1`, keyID)
}

func createLiteLLMKey(t *testing.T, ctx context.Context, baseURL, masterKey, userID string) string {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(
		`{"user_id":%q,"duration":"24h","metadata":{"created_by":"e2e-sc5"}}`, userID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/key/generate", body)
	if err != nil {
		t.Fatalf("build /key/generate request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /key/generate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("/key/generate HTTP %d: %s", resp.StatusCode, b)
	}
	var parsed struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode /key/generate: %v", err)
	}
	if parsed.Key == "" {
		t.Fatalf("/key/generate response missing .key field")
	}
	return parsed.Key
}

func deleteLiteLLMKey(baseURL, masterKey, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body := strings.NewReader(fmt.Sprintf(`{"keys":[%q]}`, token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/key/delete", body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// backdateLiteLLMKeyByUser UPDATEs LiteLLM_VerificationToken.created_at
// for every key owned by userID, pushing it back by the supplied minute
// offset against the in-cluster LiteLLM Postgres so any key newer than
// OrphanAgeFloor (10m) reads as orphan-ripe. minutes is int to keep the
// rendered Postgres `interval` syntax trivial — fractional offsets are
// not needed for SC#5.
//
// Matches scripts/uat-sc5.sh (deleted) which used the same WHERE
// user_id = '...' approach. WHERE token = ... is unsafe because the
// token column stores a SHA-256 hash, not the plaintext key returned by
// /key/generate.
//
// Asserts at least one row was updated so a silently-empty UPDATE does
// not pass as success.
func backdateLiteLLMKeyByUser(t *testing.T, ctx context.Context, dbURL, userID string, minutes int) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New (litellm-db): %v", err)
	}
	defer pool.Close()
	tag, err := pool.Exec(ctx, fmt.Sprintf(
		`UPDATE "LiteLLM_VerificationToken" SET created_at = now() - interval '%d minutes' WHERE user_id = $1`,
		minutes), userID)
	if err != nil {
		t.Fatalf("backdate LiteLLM_VerificationToken: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatalf("backdate LiteLLM_VerificationToken matched 0 rows for user_id=%s; key was not registered with that user", userID)
	}
}
