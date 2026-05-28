// SPDX-License-Identifier: Apache-2.0

package devicecode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestInit_HappyPath asserts that Init POSTs to
// /platform/auth/cli/init and decodes the four-field InitResponse.
func TestInit_HappyPath(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		if r.URL.Path != "/platform/auth/cli/init" {
			t.Errorf("path = %s; want /platform/auth/cli/init", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id":       "abc123",
			"verification_url": "https://hub.example/platform/auth/login?session_id=abc123",
			"poll_interval":    2,
			"expires_in":       300,
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := Init(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if resp.SessionID != "abc123" {
		t.Errorf("SessionID = %q; want abc123", resp.SessionID)
	}
	if !strings.Contains(resp.VerificationURL, "session_id=abc123") {
		t.Errorf("VerificationURL = %q; missing session_id=abc123", resp.VerificationURL)
	}
	if resp.PollInterval != 2 {
		t.Errorf("PollInterval = %d; want 2", resp.PollInterval)
	}
	if resp.ExpiresIn != 300 {
		t.Errorf("ExpiresIn = %d; want 300", resp.ExpiresIn)
	}
}

// TestInit_ServerError surfaces the §15.5 envelope as *ServerError.
func TestInit_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": "internal_error", "message": "boom"},
			"request_id": "req_test",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Init(ctx, srv.URL)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("Init err = %v; want *httpclient.ServerError", err)
	}
	if sErr.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d; want 500", sErr.Status)
	}
	if sErr.Code != "internal_error" {
		t.Errorf("Code = %q; want internal_error", sErr.Code)
	}
}

// TestPollToken_PendingThen200 — the canonical poll flow: 3x 202
// pending, then 200 with the pk_. Asserts the poll counter actually
// observed ≥ 3 polls before the 200.
func TestPollToken_PendingThen200(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/platform/auth/cli/token" {
			t.Errorf("path = %s; want /platform/auth/cli/token", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 4 {
			// 202 pending for the first 3 polls.
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		// 200 on the 4th poll.
		_ = json.NewEncoder(w).Encode(map[string]string{
			"key_id":      "pkid_abc",
			"plaintext":   "pk_secretsecret",
			"owner_email": "u@x",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, err := PollToken(ctx, srv.URL, "sess", 10*time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if tr.KeyID != "pkid_abc" {
		t.Errorf("KeyID = %q; want pkid_abc", tr.KeyID)
	}
	if tr.Plaintext != "pk_secretsecret" {
		t.Errorf("Plaintext = %q; want pk_secretsecret", tr.Plaintext)
	}
	if tr.OwnerEmail != "u@x" {
		t.Errorf("OwnerEmail = %q; want u@x", tr.OwnerEmail)
	}
	got := atomic.LoadInt32(&calls)
	if got < 4 {
		t.Errorf("polls = %d; want ≥ 4 (3 pending + 1 success)", got)
	}
}

// TestPollToken_SessionNotFound returns *ServerError immediately, no
// retry on terminal 4xx.
func TestPollToken_SessionNotFound(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      map[string]string{"code": "session_not_found", "message": "session not found"},
			"request_id": "req_test",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := PollToken(ctx, srv.URL, "sess", 10*time.Millisecond, 1*time.Second)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("PollToken err = %v; want *httpclient.ServerError", err)
	}
	if sErr.Status != http.StatusNotFound {
		t.Errorf("Status = %d; want 404", sErr.Status)
	}
	if sErr.Code != "session_not_found" {
		t.Errorf("Code = %q; want session_not_found", sErr.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("polls = %d; want 1 (no retry on terminal 4xx)", got)
	}
}

// TestPollToken_CtxCancel returns ctx.Err() promptly when ctx is
// cancelled mid-poll.
func TestPollToken_CtxCancel(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 30ms — well after the first 10ms poll tick but
	// before the natural totalTimeout below.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := PollToken(ctx, srv.URL, "sess", 10*time.Millisecond, 5*time.Second)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PollToken err = %v; want context.Canceled", err)
	}
	// Honor-cancellation contract: return within one pollInterval of
	// cancel; allow generous slack for wallclock noise in CI.
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v; want < 500ms (prompt cancel honor)", elapsed)
	}
}

// TestPollToken_TotalTimeout returns ErrLoginTimeout when the
// totalTimeout fires before the server returns 200.
func TestPollToken_TotalTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := PollToken(ctx, srv.URL, "sess", 10*time.Millisecond, 50*time.Millisecond)
	if !errors.Is(err, ErrLoginTimeout) {
		t.Fatalf("PollToken err = %v; want ErrLoginTimeout", err)
	}
}

// TestPollToken_RespectsPollInterval asserts the poll cadence
// approximates the configured interval. With pollInterval = 50ms and
// 4 polls completing, the wallclock should be ≥ ~3*50ms.
func TestPollToken_RespectsPollInterval(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 4 {
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"key_id":      "pkid_x",
			"plaintext":   "pk_xxxxxxxx",
			"owner_email": "u@x",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pollInterval := 50 * time.Millisecond
	start := time.Now()
	_, err := PollToken(ctx, srv.URL, "sess", pollInterval, 5*time.Second)
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	elapsed := time.Since(start)
	// Need ≥ 3 inter-poll sleeps before the 4th call succeeds: ~150ms
	// minimum. Allow a small wallclock-floor slack (-10%).
	min := time.Duration(float64(3*pollInterval) * 0.9)
	if elapsed < min {
		t.Errorf("elapsed = %v; want ≥ %v (poll interval honored)", elapsed, min)
	}
}

// TestOpener_SeamOverride asserts the package-level Opener var can be
// swapped out for tests. Tests of CLI subcommands rely on this to
// avoid spawning xdg-open / open during `go test`.
func TestOpener_SeamOverride(t *testing.T) {
	// NOT t.Parallel — mutating a package-level var.
	original := Opener
	t.Cleanup(func() { Opener = original })

	var called bool
	var seen string
	var mu sync.Mutex
	Opener = func(url string) error {
		mu.Lock()
		defer mu.Unlock()
		called = true
		seen = url
		return nil
	}

	if err := Opener("https://hub.example/foo?bar=baz"); err != nil {
		t.Fatalf("Opener: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Error("Opener override was not invoked")
	}
	if seen != "https://hub.example/foo?bar=baz" {
		t.Errorf("Opener saw url = %q; want https://hub.example/foo?bar=baz", seen)
	}
}
