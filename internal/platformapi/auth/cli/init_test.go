// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// newDeps builds a Deps wired to miniredis with deterministic defaults.
// auditBuf captures audit emissions so tests can assert presence /
// absence of the platform.cli.login event.
func newDeps(t *testing.T) (Deps, *bytes.Buffer) {
	t.Helper()
	rc, _ := newTestRedis(t)
	var auditBuf bytes.Buffer
	return Deps{
		Redis:        rc,
		Audit:        audit.NewLogger(&auditBuf),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace:    "ach-system",
		BaseURL:      "https://ach.example.com",
		SessionTTL:   DefaultSessionTTL,
		PollInterval: DefaultPollInterval,
	}, &auditBuf
}

// withReqID injects a deterministic request_id for assertion.
func withReqID(req *http.Request, reqID string) *http.Request {
	ctx := middleware.WithRequestID(req.Context(), reqID)
	return req.WithContext(ctx)
}

// auditCount counts audit lines containing the given action.
func auditCount(buf *bytes.Buffer, action string) int {
	c := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if a, ok := rec["action"].(string); ok && a == action {
			c++
		}
	}
	return c
}

// TestInitHandlerEmptyBodyReturnsSession — POST /init with empty body
// returns 200 + JSON {session_id, verification_url, poll_interval,
// expires_in}.
func TestInitHandlerEmptyBodyReturnsSession(t *testing.T) {
	deps, _ := newDeps(t)
	h := InitHandler(deps)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/init", nil), "req_init_a")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got InitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if len(got.SessionID) != 32 {
		t.Errorf("session_id length: got %d want 32 (24 random bytes base64url'd)", len(got.SessionID))
	}
	wantVerifURLPrefix := "https://ach.example.com/platform/auth/login?session_id="
	if !strings.HasPrefix(got.VerificationURL, wantVerifURLPrefix) {
		t.Errorf("verification_url: got %q, want prefix %q", got.VerificationURL, wantVerifURLPrefix)
	}
	if !strings.HasSuffix(got.VerificationURL, got.SessionID) {
		t.Errorf("verification_url: must end with session_id %q; got %q", got.SessionID, got.VerificationURL)
	}
	if got.PollInterval != 2 {
		t.Errorf("poll_interval: got %d want 2", got.PollInterval)
	}
	if got.ExpiresIn != 300 {
		t.Errorf("expires_in: got %d want 300", got.ExpiresIn)
	}
}

// TestInitHandlerWritesPendingSentinel — POST /init writes a Redis
// "ach:cli-session:<id>" key with TTL ~5 minutes containing an EMPTY
// Session (KeyID == ""). TokenHandler reads this as "pending".
func TestInitHandlerWritesPendingSentinel(t *testing.T) {
	rc, mr := newTestRedis(t)
	var auditBuf bytes.Buffer
	deps := Deps{
		Redis:     rc,
		Audit:     audit.NewLogger(&auditBuf),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Namespace: "ach-system",
		BaseURL:   "https://ach.example.com",
	}
	h := InitHandler(deps)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/init", nil), "req_init_b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp InitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	key := sessionKeyPrefix + resp.SessionID
	if !mr.Exists(key) {
		t.Fatalf("expected redis key %q to exist after /init", key)
	}
	ttl := mr.TTL(key)
	if ttl <= 4*time.Minute || ttl > DefaultSessionTTL {
		t.Errorf("ttl: got %v, want (4m, 5m]", ttl)
	}

	// Read the value back through Peek — KeyID MUST be empty (sentinel).
	sess, _, err := Peek(context.Background(), rc, resp.SessionID)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if sess == nil {
		t.Fatalf("Peek returned nil session")
	}
	if sess.KeyID != "" {
		t.Errorf("sentinel KeyID: got %q want empty", sess.KeyID)
	}
	if sess.Plaintext != "" {
		t.Errorf("sentinel Plaintext: got %q want empty", sess.Plaintext)
	}
	if sess.CreatedAt == "" {
		t.Errorf("sentinel CreatedAt: must be set, got empty")
	}
	// RFC3339 sanity.
	if _, perr := time.Parse(time.RFC3339, sess.CreatedAt); perr != nil {
		t.Errorf("CreatedAt: %q does not parse as RFC3339: %v", sess.CreatedAt, perr)
	}
}

// TestInitHandlerRejectsUnknownFields — POST /init with a non-empty
// body carrying unknown fields returns 400 invalid_argument.
func TestInitHandlerRejectsUnknownFields(t *testing.T) {
	deps, _ := newDeps(t)
	h := InitHandler(deps)
	body := strings.NewReader(`{"unexpected":"value"}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/init", body), "req_init_c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Err struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rec.Body.String())
	}
	if env.Err.Code != "invalid_argument" {
		t.Errorf("error.code: got %q want invalid_argument", env.Err.Code)
	}
}

// TestInitHandlerAcceptsEmptyJSONObject — POST /init with body "{}"
// returns 200 (no unknown fields → no decode error).
func TestInitHandlerAcceptsEmptyJSONObject(t *testing.T) {
	deps, _ := newDeps(t)
	h := InitHandler(deps)
	body := strings.NewReader(`{}`)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/init", body), "req_init_d")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestInitHandlerEmitsNoAudit — D-19: init does NOT emit
// platform.cli.login. The audit event is reserved for the /token
// success path (Task 2 token tests).
func TestInitHandlerEmitsNoAudit(t *testing.T) {
	deps, auditBuf := newDeps(t)
	h := InitHandler(deps)
	req := withReqID(httptest.NewRequest(http.MethodPost, "/init", nil), "req_init_e")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if c := auditCount(auditBuf, audit.ActionCliLogin); c != 0 {
		t.Errorf("expected 0 platform.cli.login emissions on /init; got %d (buf=%s)", c, auditBuf.String())
	}
}
