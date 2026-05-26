// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// ---------- helpers ----------

// writeTempFile creates a temp file under t.TempDir() with the given
// content + filename suffix and returns its path. Cleanup is automatic
// via t.TempDir().
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// newWarnBuf returns a slog.Logger that writes to a buffer the caller can
// inspect for WARN emissions (L-4, L-5).
func newWarnBuf() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// newAuditBuf returns an audit logger writing to a buffer so M-* tests
// can assert audit emissions.
func newAuditBuf() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return audit.NewLogger(&buf), &buf
}

// adminCallContextOpts is a minimal builder for a request whose ctx
// already carries a KeyContext + RequestID (the chain would normally
// install these via RequestID + Authn middleware).
type adminCallOpts struct {
	keyType    keys.BearerPrefix
	ownerEmail string
	reqID      string
}

func newAdminReq(opts adminCallOpts) *http.Request {
	r := httptest.NewRequest("POST", "/platform/admin/keys/revoke", nil)
	ctx := r.Context()
	ctx = middleware.WithRequestID(ctx, opts.reqID)
	info := &keystore.KeyInfo{
		KeyID:      "pkid_test",
		KeyType:    opts.keyType,
		OwnerEmail: opts.ownerEmail,
		Status:     "active",
	}
	ctx = middleware.WithKeyContext(ctx, info, false /* isAdmin is unused by AdminOnly */)
	return r.WithContext(ctx)
}

// ---------- LoadAllowlist tests (L-1..L-7) ----------

// L-1: happy path — 2 valid emails + 1 comment + 1 blank line.
func TestLoadAllowlistHappyPath(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "alice@example.com\n# leading comment\n\nbob@example.com\n")
	logger, _ := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d (%v)", len(got), got)
	}
	if _, ok := got["alice@example.com"]; !ok {
		t.Fatalf("missing alice@example.com")
	}
	if _, ok := got["bob@example.com"]; !ok {
		t.Fatalf("missing bob@example.com")
	}
}

// L-2: case-sensitive comparison (file has "Admin@x"; lookup for
// "admin@x" must fail).
func TestLoadAllowlistCaseSensitive(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "Admin@example.com\n")
	logger, _ := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := got["Admin@example.com"]; !ok {
		t.Fatalf("uppercase entry missing")
	}
	if _, ok := got["admin@example.com"]; ok {
		t.Fatalf("lowercase lookup unexpectedly succeeded (case-insensitive)")
	}
}

// L-3: leading/trailing whitespace trim.
func TestLoadAllowlistWhitespaceTrim(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "  user@example.com  \n")
	logger, _ := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := got["user@example.com"]; !ok {
		t.Fatalf("trimmed entry missing; got %v", got)
	}
}

// L-4: missing file → empty map + nil error + WARN log.
func TestLoadAllowlistMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "does-not-exist.txt")
	logger, buf := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("expected nil err for missing file, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
	if !strings.Contains(buf.String(), "admin allowlist file missing") {
		t.Fatalf("expected WARN log; got %q", buf.String())
	}
}

// L-5: empty file → empty map + nil error + WARN log.
func TestLoadAllowlistEmptyFile(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "")
	logger, buf := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
	if !strings.Contains(buf.String(), "zero admins") {
		t.Fatalf("expected zero-admins WARN log; got %q", buf.String())
	}
}

// L-6: line with non-email content stored verbatim (no email validation
// per D-22 verbatim comparison).
func TestLoadAllowlistVerbatimNonEmail(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "not-an-email-just-some-text\n")
	logger, _ := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := got["not-an-email-just-some-text"]; !ok {
		t.Fatalf("verbatim entry missing; got %v", got)
	}
}

// L-7: CRLF line endings — TrimSpace must strip the CR.
func TestLoadAllowlistCRLF(t *testing.T) {
	p := writeTempFile(t, "admins.txt", "user@example.com\r\n# comment\r\n")
	logger, _ := newWarnBuf()
	got, err := LoadAllowlist(p, logger)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, ok := got["user@example.com"]; !ok {
		t.Fatalf("CRLF-stripped entry missing; got %v", got)
	}
}

// ---------- AdminOnly tests (M-1..M-6) ----------

// innerInvocationCounter is a handler that increments a counter so tests
// can assert whether the inner handler ran (M-2, M-3, M-4, M-6).
func innerInvocationCounter(t *testing.T) (http.Handler, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	return h, &n
}

func readErrorEnvelope(t *testing.T, body []byte) (code string) {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, string(body))
	}
	return env.Error.Code
}

// M-1: pk_ caller in allowlist → middleware passes through.
func TestAdminOnlyHappyPath(t *testing.T) {
	auditLog, _ := newAuditBuf()
	allow := map[string]struct{}{"alice@example.com": {}}
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(allow, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixPk, ownerEmail: "alice@example.com", reqID: "req_m1"})
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected inner invoked once, got %d", calls.Load())
	}
}

// M-2: ek_ caller rejected — 401 invalid_key_type + audit + inner not
// called.
func TestAdminOnlyRejectsEkType(t *testing.T) {
	auditLog, auditBuf := newAuditBuf()
	allow := map[string]struct{}{"workload@example.com": {}} // even if in allowlist
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(allow, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixEk, ownerEmail: "workload@example.com", reqID: "req_m2"})
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if code := readErrorEnvelope(t, rec.Body.Bytes()); code != audit.OutcomeInvalidKeyType {
		t.Fatalf("expected envelope code=invalid_key_type, got %q", code)
	}
	if calls.Load() != 0 {
		t.Fatalf("inner must not be called; got %d calls", calls.Load())
	}
	if !strings.Contains(auditBuf.String(), `"outcome":"invalid_key_type"`) {
		t.Fatalf("expected audit outcome=invalid_key_type; got %s", auditBuf.String())
	}
}

// M-3: pk_ caller NOT in allowlist → 403 not_admin + audit + inner not
// called.
func TestAdminOnlyRejectsNonAdmin(t *testing.T) {
	auditLog, auditBuf := newAuditBuf()
	allow := map[string]struct{}{"alice@example.com": {}}
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(allow, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixPk, ownerEmail: "bob@example.com", reqID: "req_m3"})
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if code := readErrorEnvelope(t, rec.Body.Bytes()); code != audit.OutcomeNotAdmin {
		t.Fatalf("expected code=not_admin, got %q", code)
	}
	if calls.Load() != 0 {
		t.Fatalf("inner must not be called; got %d calls", calls.Load())
	}
	if !strings.Contains(auditBuf.String(), `"outcome":"not_admin"`) {
		t.Fatalf("expected audit outcome=not_admin; got %s", auditBuf.String())
	}
}

// M-4: empty allowlist (zero admins per D-23) rejects every pk_ caller.
func TestAdminOnlyEmptyAllowlistRejectsAll(t *testing.T) {
	auditLog, _ := newAuditBuf()
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(map[string]struct{}{}, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixPk, ownerEmail: "anyone@example.com", reqID: "req_m4"})
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (zero admins), got %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("inner must not be called; got %d calls", calls.Load())
	}
}

// M-5: audit event includes actor=<ns>/<email>.
func TestAdminOnlyEmitsActor(t *testing.T) {
	auditLog, auditBuf := newAuditBuf()
	allow := map[string]struct{}{} // empty → 403, but audit still fires
	inner, _ := innerInvocationCounter(t)
	h := AdminOnly(allow, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixPk, ownerEmail: "alice@example.com", reqID: "req_m5"})
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(auditBuf.String(), `"actor":"ach-system/alice@example.com"`) {
		t.Fatalf("expected actor=ach-system/alice@example.com; got %s", auditBuf.String())
	}
}

// M-6: on ek_ rejection, inner handler must observe ZERO invocations
// (verifies AdminOnly runs BEFORE inner per the API-12 ordering rule).
func TestAdminOnlyRunsBeforeRoute(t *testing.T) {
	auditLog, _ := newAuditBuf()
	allow := map[string]struct{}{"workload@example.com": {}}
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(allow, auditLog, "ach-system")(inner)

	// Run with an ek_ caller — must be rejected; inner.Add must NOT run.
	rec := httptest.NewRecorder()
	r := newAdminReq(adminCallOpts{keyType: keys.PrefixEk, ownerEmail: "workload@example.com", reqID: "req_m6"})
	h.ServeHTTP(rec, r)

	if calls.Load() != 0 {
		t.Fatalf("inner handler ran despite AdminOnly rejection (calls=%d)", calls.Load())
	}
}

// ----- defensive: missing KeyContext → 401 (unreachable in prod path)
func TestAdminOnlyMissingKeyContext(t *testing.T) {
	auditLog, _ := newAuditBuf()
	inner, calls := innerInvocationCounter(t)
	h := AdminOnly(map[string]struct{}{}, auditLog, "ach-system")(inner)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil) // no ctx.KeyContext
	r = r.WithContext(middleware.WithRequestID(context.Background(), "req_defensive"))
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 defensive path, got %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("inner must not be called; got %d", calls.Load())
	}
}
