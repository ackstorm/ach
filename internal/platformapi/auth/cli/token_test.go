// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/audit"
)

// putPending writes a sentinel session and returns its id.
func putPending(t *testing.T, deps Deps, id string) {
	t.Helper()
	sess := Session{CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := Put(context.Background(), deps.Redis, id, sess, DefaultSessionTTL); err != nil {
		t.Fatalf("Put pending: %v", err)
	}
}

// putCompleted writes a completed session and returns its id.
func putCompleted(t *testing.T, deps Deps, id string) Session {
	t.Helper()
	sess := Session{
		KeyID:      "pkid_xxxxxxxxxxxxxxxxxxxxxxxxxx",
		Plaintext:  "pk_xxxxxxxxxxxxxxxxxxxxxxxxxx",
		OwnerEmail: "alice@example.com",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := Put(context.Background(), deps.Redis, id, sess, DefaultSessionTTL); err != nil {
		t.Fatalf("Put completed: %v", err)
	}
	return sess
}

// TestTokenHandlerPendingReturns202 — POST /token with a session_id
// that points at a pending sentinel returns 202 {status:"pending"}.
// The Redis key MUST still exist after the call (Peek + re-Put refresh
// pattern; Consume is reserved for the completed branch).
func TestTokenHandlerPendingReturns202(t *testing.T) {
	deps, auditBuf := newDeps(t)
	h := TokenHandler(deps)

	putPending(t, deps, "abc")
	body := strings.NewReader(`{"session_id":"abc"}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", body), "req_tok_a")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp TokenPendingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
	}
	if resp.Status != "pending" {
		t.Errorf("status field: got %q want 'pending'", resp.Status)
	}

	// Session is still in Redis (Peek succeeds with sentinel).
	sess, _, err := Peek(context.Background(), deps.Redis, "abc")
	if err != nil {
		t.Errorf("Peek after pending /token: %v", err)
	}
	if sess == nil || sess.KeyID != "" {
		t.Errorf("session lost or corrupted after pending /token: %+v", sess)
	}

	if c := auditCount(auditBuf, audit.ActionCliLogin); c != 0 {
		t.Errorf("pending /token must not emit platform.cli.login; got %d emissions", c)
	}
}

// TestTokenHandlerCompletedReturns200AndIsOneShot — POST /token with
// a session_id pointing at a completed session returns 200 with the
// {key_id, plaintext, owner_email}; second call returns 404
// session_not_found (GETDEL consumed the entry).
func TestTokenHandlerCompletedReturns200AndIsOneShot(t *testing.T) {
	deps, auditBuf := newDeps(t)
	h := TokenHandler(deps)

	want := putCompleted(t, deps, "xyz")

	body1 := strings.NewReader(`{"session_id":"xyz"}`)
	req1 := withReqID(httptest.NewRequest(http.MethodPost, "/token", body1), "req_tok_b1")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status: got %d want 200; body=%s", rec1.Code, rec1.Body.String())
	}
	var got TokenResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rec1.Body.String())
	}
	if got.KeyID != want.KeyID || got.Plaintext != want.Plaintext || got.OwnerEmail != want.OwnerEmail {
		t.Errorf("body mismatch: got %+v want %+v", got, want)
	}

	// Audit emission on success: action=platform.cli.login,
	// actor="<ns>/<email>", key.id=pkid_…, NO plaintext.
	auditStr := auditBuf.String()
	if !strings.Contains(auditStr, audit.ActionCliLogin) {
		t.Errorf("audit log missing platform.cli.login; got: %s", auditStr)
	}
	if !strings.Contains(auditStr, want.KeyID) {
		t.Errorf("audit log missing key.id %q; got: %s", want.KeyID, auditStr)
	}
	if !strings.Contains(auditStr, "ach-system/"+want.OwnerEmail) {
		t.Errorf("audit log missing actor 'ach-system/%s'; got: %s", want.OwnerEmail, auditStr)
	}
	if strings.Contains(auditStr, want.Plaintext) {
		t.Errorf("FATAL: pk_ plaintext leaked into audit log: %s", auditStr)
	}
	if !strings.Contains(auditStr, "req_tok_b1") {
		t.Errorf("audit log missing request_id req_tok_b1; got: %s", auditStr)
	}

	// Second call to /token with the same id → 404.
	body2 := strings.NewReader(`{"session_id":"xyz"}`)
	req2 := withReqID(httptest.NewRequest(http.MethodPost, "/token", body2), "req_tok_b2")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("second call status: got %d want 404; body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "session_not_found") {
		t.Errorf("second call body missing session_not_found code: %s", rec2.Body.String())
	}
}

// TestTokenHandlerAbsentSessionReturns404 — POST /token with a
// session_id that was never created returns 404 session_not_found.
func TestTokenHandlerAbsentSessionReturns404(t *testing.T) {
	deps, auditBuf := newDeps(t)
	h := TokenHandler(deps)

	body := strings.NewReader(`{"session_id":"never-existed"}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", body), "req_tok_c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "session_not_found") {
		t.Errorf("body missing session_not_found code: %s", rec.Body.String())
	}
	if c := auditCount(auditBuf, audit.ActionCliLogin); c != 0 {
		t.Errorf("absent /token must not emit platform.cli.login; got %d", c)
	}
}

// TestTokenHandlerEmptyBodyReturns400 — POST /token with empty body
// returns 400 invalid_argument.
func TestTokenHandlerEmptyBodyReturns400(t *testing.T) {
	deps, _ := newDeps(t)
	h := TokenHandler(deps)

	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", nil), "req_tok_d")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_argument") {
		t.Errorf("body missing invalid_argument code: %s", rec.Body.String())
	}
}

// TestTokenHandlerMissingSessionIDReturns400 — POST /token with a body
// that omits session_id returns 400 invalid_argument.
func TestTokenHandlerMissingSessionIDReturns400(t *testing.T) {
	deps, _ := newDeps(t)
	h := TokenHandler(deps)

	body := strings.NewReader(`{}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", body), "req_tok_e")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestTokenHandlerRejectsUnknownFields — strict decode applies.
func TestTokenHandlerRejectsUnknownFields(t *testing.T) {
	deps, _ := newDeps(t)
	h := TokenHandler(deps)

	body := strings.NewReader(`{"session_id":"abc","stray":"oops"}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", body), "req_tok_f")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestTokenHandlerPendingRefreshesTTL — pending polls re-Put the
// sentinel; TTL is restored to DefaultSessionTTL.
func TestTokenHandlerPendingRefreshesTTL(t *testing.T) {
	deps, _ := newDeps(t)
	h := TokenHandler(deps)

	// Pre-seed with a sentinel that has a shorter TTL than the default,
	// so we can observe the refresh restoring it.
	short := Session{CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := Put(context.Background(), deps.Redis, "pp", short, 30*time.Second); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := strings.NewReader(`{"session_id":"pp"}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/token", body), "req_tok_g")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rec.Code, rec.Body.String())
	}

	_, ttl, err := Peek(context.Background(), deps.Redis, "pp")
	if err != nil {
		t.Fatalf("Peek after pending /token: %v", err)
	}
	if ttl <= 4*time.Minute || ttl > DefaultSessionTTL {
		t.Errorf("ttl after pending /token: got %v, want (4m, 5m] (refreshed to DefaultSessionTTL)", ttl)
	}
}
