// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// bufferSink is a tiny logr.LogSink that writes every Info / Error call
// (plus its key=value fields) into an in-memory buffer. Used by the
// redaction-canary tests to assert that NO body / header / credential
// material ever reaches a log line under default settings.
type bufferSink struct {
	mu  sync.Mutex
	buf *bytes.Buffer
}

func (b *bufferSink) Init(info logr.RuntimeInfo)             {}
func (b *bufferSink) Enabled(level int) bool                 { return true }
func (b *bufferSink) WithValues(kv ...any) logr.LogSink      { return b }
func (b *bufferSink) WithName(name string) logr.LogSink      { return b }
func (b *bufferSink) Info(level int, msg string, kv ...any)  { b.write(msg, kv) }
func (b *bufferSink) Error(err error, msg string, kv ...any) { b.write(msg+" err="+errStr(err), kv) }

func (b *bufferSink) write(msg string, kv []any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fmt.Fprintf(b.buf, "%s", msg)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(b.buf, " %v=%v", kv[i], kv[i+1])
	}
	b.buf.WriteByte('\n')
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func (b *bufferSink) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// canaryMasterKey is the synthetic master-key string the redaction
// tests register. If this string EVER appears in captured log output
// under default settings, §9.1 is violated and the test fails.
const canaryMasterKey = "sk-canary-XYZ-12345-FAKE"

// nineResponseShapes returns an http.HandlerFunc that emits the 9
// response shapes the §9.1 canary exercises: 200, 200-empty, 400, 401,
// 404, 422, 500, 5xx-with-junk-body, and connection-reset (handled by
// the test via a separate hijacking handler).
func nineResponseShapes(t *testing.T) http.HandlerFunc {
	t.Helper()
	var n int32
	statuses := []int{200, 200, 400, 401, 404, 422, 500, 502}
	return func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomicInc(&n)) - 1
		if idx >= len(statuses) {
			idx = len(statuses) - 1
		}
		s := statuses[idx]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s)
		switch s {
		case 200:
			if idx == 1 {
				// empty body
				return
			}
			_, _ = w.Write([]byte(`{"hello":"` + canaryMasterKey + `-echoed"}`))
		case 401:
			_, _ = w.Write([]byte(`{"error":{"message":"Authentication Error, Invalid proxy server token passed. Received API Key = ` + canaryMasterKey + `","type":"token_not_found_in_db","param":"key","code":"401"}}`))
		default:
			_, _ = w.Write([]byte(`{"error":{"message":"junk","type":"x","param":null,"code":"x"}}`))
		}
	}
}

// atomicInc avoids the sync/atomic import by serializing via a mutex
// (the canary test fires requests sequentially, so contention is zero).
var atomicMu sync.Mutex

func atomicInc(p *int32) int32 {
	atomicMu.Lock()
	defer atomicMu.Unlock()
	*p++
	return *p
}

// TestNoCredentialLeak — §9.1 canary. With the DANGEROUSLY env var
// UNSET, no master-key string, request body, response body, or header
// value may appear in captured log output across 9 response shapes.
func TestNoCredentialLeak(t *testing.T) {
	t.Setenv(EnvDangerouslyLogBodies, "") // explicit: redaction ON

	srv := httptest.NewServer(nineResponseShapes(t))
	defer srv.Close()

	cap := &bytes.Buffer{}
	logger := logr.New(&bufferSink{buf: cap})
	client := newHTTPClient(logger)

	// 8 normal-status requests…
	for i := 0; i < 8; i++ {
		req, _ := http.NewRequest("GET", srv.URL+"/test", strings.NewReader(`{"req":"`+canaryMasterKey+`"}`))
		req.Header.Set("Authorization", "Bearer "+canaryMasterKey)
		resp, err := client.Do(req)
		if err == nil {
			drainAndClose(resp.Body)
		}
	}

	// …plus one "connection reset" request via a hijacking handler.
	resetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("hijack not supported")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer resetSrv.Close()
	req, _ := http.NewRequest("GET", resetSrv.URL+"/reset", nil)
	req.Header.Set("Authorization", "Bearer "+canaryMasterKey)
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		drainAndClose(resp.Body)
	}

	got := cap.String()
	if strings.Contains(got, canaryMasterKey) {
		t.Fatalf("§9.1 canary FAILED: master-key string leaked to logs.\nLogs:\n%s", got)
	}
	for _, k := range []string{"method=", "path=", "status="} {
		if !strings.Contains(got, k) {
			t.Errorf("expected log key %q in captured output (got: %q)", k, got)
		}
	}
}

// TestDangerouslyEnvFlipsRedaction — env var DOES flip redaction.
// With ACH_LITELLM_DANGEROUSLY_LOG_BODIES=true, the canary string
// SHOULD appear (because the response body contains it). Proves the
// opt-out works in both directions.
func TestDangerouslyEnvFlipsRedaction(t *testing.T) {
	t.Setenv(EnvDangerouslyLogBodies, "true")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"echo":"` + canaryMasterKey + `"}`))
	}))
	defer srv.Close()

	cap := &bytes.Buffer{}
	logger := logr.New(&bufferSink{buf: cap})
	client := newHTTPClient(logger)

	req, _ := http.NewRequest("GET", srv.URL+"/probe", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// Read body to assert byte-perfect restore semantics.
	body, _ := io.ReadAll(resp.Body)
	drainAndClose(resp.Body)
	if !strings.Contains(string(body), canaryMasterKey) {
		t.Fatalf("expected body restore to deliver canary; got: %s", body)
	}

	got := cap.String()
	if !strings.Contains(got, canaryMasterKey) {
		t.Fatalf("expected canary in logs with DANGEROUSLY=true; got: %s", got)
	}
}

// TestDrainAndClose — REL-04 reinforcement. Run 1000 sequential
// requests against a server returning a 1 MB body, defer drainAndClose
// every iteration, then assert the goroutine delta < 5. Failing this
// test means drain+close is leaking somewhere — equivalent to FD-stable
// over 1000 requests.
func TestDrainAndClose(t *testing.T) {
	t.Setenv(EnvDangerouslyLogBodies, "")

	payload := bytes.Repeat([]byte("A"), 1<<20) // 1 MB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	logger := logr.Discard()
	client := newHTTPClient(logger)

	// Settle goroutines from server startup.
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 1000; i++ {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		drainAndClose(resp.Body)
	}

	// Allow keepalive goroutines to settle / time out.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	delta := after - before
	if delta > 5 {
		t.Fatalf("goroutine leak: before=%d after=%d delta=%d (>5)", before, after, delta)
	}
}

// TestProcessLitellmError — feeds the literal 401 body shape recorded
// in 01-01-SUMMARY.md (spike Probe 8) and asserts processLitellmError
// extracts the code + a non-empty message.
func TestProcessLitellmError(t *testing.T) {
	literal := `{"error":{"message":"Authentication Error, Invalid proxy server token passed. Received API Key = sk-...-key","type":"token_not_found_in_db","param":"key","code":"401"}}`
	kind, msg, code := processLitellmError([]byte(literal))
	if code != "401" {
		t.Errorf("code: want 401, got %q", code)
	}
	if msg == "" {
		t.Errorf("message: want non-empty, got empty")
	}
	if kind == "" {
		t.Errorf("kind: want non-empty, got empty")
	}

	// Unparsable body returns kind="unparsed" + raw (capped) body.
	junk := []byte("not json at all")
	kind2, msg2, code2 := processLitellmError(junk)
	if kind2 != "unparsed" {
		t.Errorf("kind: want unparsed for junk, got %q", kind2)
	}
	if code2 != "" {
		t.Errorf("code: want empty for junk, got %q", code2)
	}
	if msg2 != "not json at all" {
		t.Errorf("message: want raw body for junk, got %q", msg2)
	}
}

