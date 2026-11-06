// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

const (
	testEvery   = 10 * time.Millisecond
	testPatient = 5 * time.Second // upper bound on anything that must happen
)

// syncBuffer lets a test read what the watcher goroutine logs.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func capturedLog() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitDone fails the test unless ctx ends in time.
func waitDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(testPatient):
		t.Fatal("context still running")
	}
}

// waitChecks blocks until n more checks have run.
func waitChecks(t *testing.T, checks <-chan struct{}, n int) {
	t.Helper()
	for range n {
		select {
		case <-checks:
		case <-time.After(testPatient):
			t.Fatal("the key is not being re-checked")
		}
	}
}

func TestWatchKey_KeyGoneEndsRequest(t *testing.T) {
	ctx, stop := watchKey(context.Background(), testEvery, "ekid_x",
		func(context.Context) (bool, error) { return false, nil }, quietLog())
	defer stop()
	waitDone(t, ctx)
	if got := context.Cause(ctx); !errors.Is(got, errKeyNotLive) {
		t.Fatalf("cause = %v, want errKeyNotLive", got)
	}
}

func TestWatchKey_LiveKeyKeepsRequest(t *testing.T) {
	checks := make(chan struct{}, 16)
	ctx, stop := watchKey(context.Background(), testEvery, "ekid_x", func(context.Context) (bool, error) {
		checks <- struct{}{}
		return true, nil
	}, quietLog())
	defer stop()
	waitChecks(t, checks, 3)
	if ctx.Err() != nil {
		t.Fatalf("live key ended the request: %v", ctx.Err())
	}
}

func TestWatchKey_StoreErrorKeepsRequestAndWarns(t *testing.T) {
	log, buf := capturedLog()
	var calls atomic.Int32
	ctx, stop := watchKey(context.Background(), testEvery, "ekid_x", func(context.Context) (bool, error) {
		if calls.Add(1) <= 3 {
			return false, errors.New("connection refused")
		}
		return false, nil // the store came back: a definite answer still ends it
	}, log)
	defer stop()

	waitDone(t, ctx)
	if got := calls.Load(); got != 4 {
		t.Fatalf("checks = %d, want 3 failures then the answer", got)
	}
	if got := buf.String(); strings.Count(got, "level=WARN") != 3 || !strings.Contains(got, "connection refused") {
		t.Fatalf("log = %q, want one Warn per failed check", got)
	}
}

func TestWatchKey_CheckIsBounded(t *testing.T) {
	left := make(chan time.Duration, 1)
	ctx, stop := watchKey(context.Background(), testEvery, "ekid_x", func(ctx context.Context) (bool, error) {
		var d time.Duration // zero means no deadline
		if deadline, ok := ctx.Deadline(); ok {
			d = time.Until(deadline)
		}
		left <- d
		return false, nil
	}, quietLog())
	defer stop()
	waitDone(t, ctx)
	if d := <-left; d <= 0 || d > keyRecheckInterval {
		t.Fatalf("time left for the check = %v, want a deadline within one interval", d)
	}
}

func TestWatchKey_ShortRequestRunsNoCheck(t *testing.T) {
	var calls atomic.Int32
	_, stop := watchKey(context.Background(), testEvery, "ekid_x", func(context.Context) (bool, error) {
		calls.Add(1)
		return false, nil
	}, quietLog())
	stop()
	time.Sleep(5 * testEvery)
	if got := calls.Load(); got != 0 {
		t.Fatalf("checks = %d, want none", got)
	}
}

func TestWatchKey_ParentEndWithoutStopEndsWatcher(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	left := make(chan struct{})
	_, stop := watchKey(parent, testEvery, "ekid_x", func(ctx context.Context) (bool, error) {
		close(entered)
		<-ctx.Done()
		defer close(left)
		return false, ctx.Err()
	}, quietLog())
	defer stop()

	<-entered
	cancel()
	select {
	case <-left:
	case <-time.After(testPatient):
		t.Fatal("check kept running after the request ended")
	}
}

// An answer that lands as the request finishes concerns a request that no
// longer exists: it must not read as a key loss or a store failure.
func TestWatchKey_AnswerAfterStopIsDropped(t *testing.T) {
	log, buf := capturedLog()
	for _, answer := range []error{nil, errors.New("connection refused")} {
		entered, release, answered := make(chan struct{}), make(chan struct{}), make(chan struct{})
		ctx, stop := watchKey(context.Background(), testEvery, "ekid_x", func(context.Context) (bool, error) {
			close(entered)
			<-release
			defer close(answered)
			return false, answer
		}, log)
		<-entered
		stop()
		close(release)
		<-answered
		time.Sleep(5 * testEvery)
		if got := context.Cause(ctx); errors.Is(got, errKeyNotLive) {
			t.Fatalf("cause = %v after stop", got)
		}
	}
	if got := buf.String(); got != "" {
		t.Fatalf("log = %q, want nothing", got)
	}
}

func TestWatchKey_StopLeaksNoGoroutine(t *testing.T) {
	const watchers = 64
	before := runtime.NumGoroutine()
	var entered sync.WaitGroup
	entered.Add(watchers)
	stops := make([]func(), watchers)
	for i := range stops {
		_, stops[i] = watchKey(context.Background(), testEvery, "ekid_x", func(ctx context.Context) (bool, error) {
			entered.Done()
			<-ctx.Done()
			return false, ctx.Err()
		}, quietLog())
	}
	entered.Wait() // every watcher is inside a blocking check
	for _, stop := range stops {
		stop()
	}
	deadline := time.Now().Add(testPatient)
	for runtime.NumGoroutine() > before+watchers/4 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before, %d after stop", before, runtime.NumGoroutine())
		}
		time.Sleep(testEvery)
	}
}

// keyState is what the fake store answers for the watched key.
type keyState struct {
	gone   atomic.Bool
	checks chan struct{}
}

func newKeyState() *keyState { return &keyState{checks: make(chan struct{}, 1024)} }

func (k *keyState) live(_ context.Context, _ keys.BearerPrefix, _ string) (bool, error) {
	select {
	case k.checks <- struct{}{}:
	default:
	}
	return !k.gone.Load(), nil
}

// watchedForwarder is a real server running the production wrappers around
// HandlerV1. The caller's token expired an hour ago, as a long stream's JWT
// does: only the key's own state may end a request.
func watchedForwarder(t *testing.T, upstream string, live keystore.KeyLive) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	tokenExp := time.Now().Add(-time.Hour)
	info := &keystore.KeyInfo{KeyID: "ekid_x", KeyType: keys.PrefixEk, OwnerEmail: "u@e", ExpiresAt: &tokenExp}
	h := middleware.RecoverPanic(nil, nil)(keyWatch(testEvery, live, quietLog())(
		HandlerV1(HandlerDeps{Deps: Deps{LiteLLMUpstream: u, Logger: quietLog()}})))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(middleware.WithKeyContext(r.Context(), info, false)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKeyWatch_ClosesUpgradedTunnel(t *testing.T) {
	backendClosed := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("backend hijack: %v", err)
			return
		}
		defer close(backendClosed)
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = rw.WriteString(line)
			_ = rw.Flush()
		}
	}))
	defer backend.Close()

	key := newKeyState()
	front := watchedForwarder(t, backend.URL, key.live)
	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(testPatient)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	_, _ = fmt.Fprint(conn, "GET /v1/realtime HTTP/1.1\r\nHost: ach\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	client := bufio.NewReader(conn)
	resp, err := http.ReadResponse(client, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	echo := func() error {
		if _, err := fmt.Fprint(conn, "ping\n"); err != nil {
			return err
		}
		_, err := client.ReadString('\n')
		return err
	}
	if err := echo(); err != nil {
		t.Fatalf("tunnel not up: %v", err)
	}

	waitChecks(t, key.checks, 3)
	if err := echo(); err != nil {
		t.Fatalf("a live key lost its tunnel: %v", err)
	}

	key.gone.Store(true)
	_, err = client.ReadString('\n')
	var netErr net.Error
	if err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		t.Fatalf("read after key loss = %v, want the tunnel closed", err)
	}
	select {
	case <-backendClosed:
	case <-time.After(testPatient):
		t.Fatal("upstream side of the tunnel stayed open")
	}
}

func TestKeyWatch_ClosesStreamingResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: first\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer backend.Close()

	key := newKeyState()
	front := watchedForwarder(t, backend.URL, key.live)
	ctx, cancel := context.WithTimeout(context.Background(), testPatient)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	client := bufio.NewReader(resp.Body)
	if line, err := client.ReadString('\n'); err != nil || line != "data: first\n" {
		t.Fatalf("first event = %q, %v", line, err)
	}

	waitChecks(t, key.checks, 3)
	key.gone.Store(true)
	_, err = io.ReadAll(client)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("read after key loss = %v (test deadline: %v), want the stream aborted", err, ctx.Err())
	}
}

// Nothing reached the client yet, so it gets an answer rather than an empty
// success.
func TestKeyWatch_RequestAwaitingUpstreamAnswers401(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // net/http watches for a hangup only past the body
		<-r.Context().Done()
	}))
	defer backend.Close()

	key := newKeyState()
	key.gone.Store(true)
	front := watchedForwarder(t, backend.URL, key.live)
	ctx, cancel := context.WithTimeout(context.Background(), testPatient)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "expired_or_revoked" {
		t.Fatalf("code = %q, want expired_or_revoked", env.Error.Code)
	}
}

// A raw LiteLLM key carries no ACH identity: there is nothing of ours to
// watch, and no check may run.
func TestKeyWatch_NoIdentityIsNotWatched(t *testing.T) {
	var calls atomic.Int32
	live := func(context.Context, keys.BearerPrefix, string) (bool, error) {
		calls.Add(1)
		return false, nil
	}
	h := keyWatch(testEvery, live, quietLog())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * testEvery)
		if r.Context().Err() != nil {
			t.Errorf("no-identity request ended: %v", r.Context().Err())
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if got := calls.Load(); got != 0 {
		t.Fatalf("checks = %d, want none", got)
	}
}

// The watcher reads the row of the key the request authenticated with.
func TestKeyWatch_ChecksTheCallersKey(t *testing.T) {
	type read struct {
		kt keys.BearerPrefix
		id string
	}
	got := make(chan read, 16)
	live := func(_ context.Context, kt keys.BearerPrefix, id string) (bool, error) {
		select {
		case got <- read{kt, id}:
		default:
		}
		return false, nil
	}
	h := keyWatch(testEvery, live, quietLog())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		waitDone(t, r.Context())
	}))
	info := &keystore.KeyInfo{KeyID: "pkid_oauth", KeyType: keys.PrefixPk}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.ServeHTTP(httptest.NewRecorder(), r.WithContext(middleware.WithKeyContext(r.Context(), info, false)))
	if first := <-got; first != (read{keys.PrefixPk, "pkid_oauth"}) {
		t.Fatalf("read %+v", first)
	}
}
