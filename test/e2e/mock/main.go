// SPDX-License-Identifier: Apache-2.0

// ach-mock is the e2e mock-backend binary. Two subcommands:
//
//	ach-mock litellm   — LiteLLM-shaped chat-completion / embeddings mock
//	                     Captures the last request body + headers for tests
//	                     to inspect via GET /__capture/last
//	ach-mock mcp       — MCP server echo backend
//	                     Echoes Authorization header (JWT) in response and
//	                     captures it for tests via GET /__capture/last
//
// Both modes share the same in-memory capture surface and listen on
// MOCK_BIND_ADDRESS (default :9090). Tests reach into the mock via
// /__capture/last after driving traffic through the Forwarder.
//
// Stdlib only (no new go.mod entries). Single-replica; per-request state
// guarded by mu. Designed for in-cluster Service exposure on a kind
// cluster — NOT production code.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// capture is the singleton record of the last request the mock saw.
type capture struct {
	mu      sync.Mutex
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    json.RawMessage     `json:"body"`
	BodyRaw string              `json:"body_raw"`
	At      time.Time           `json:"at"`
}

func (c *capture) record(r *http.Request, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Method = r.Method
	c.Path = r.URL.Path
	c.Headers = filterAndCopyHeaders(r.Header)
	c.BodyRaw = string(raw)
	c.Body = nil
	if json.Valid(raw) {
		c.Body = append(c.Body[:0], raw...)
	}
	c.At = time.Now().UTC()
}

// captureView is the lock-free wire representation of capture for
// /__capture/last responses. Returned by snapshot under the mutex,
// then encoded without lock copying (which would trip go vet).
type captureView struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    json.RawMessage     `json:"body"`
	BodyRaw string              `json:"body_raw"`
	At      time.Time           `json:"at"`
}

func (c *capture) snapshot() captureView {
	c.mu.Lock()
	defer c.mu.Unlock()
	hcopy := make(map[string][]string, len(c.Headers))
	for k, v := range c.Headers {
		hcopy[k] = append([]string{}, v...)
	}
	return captureView{
		Method:  c.Method,
		Path:    c.Path,
		Headers: hcopy,
		Body:    append(json.RawMessage(nil), c.Body...),
		BodyRaw: c.BodyRaw,
		At:      c.At,
	}
}

// filterAndCopyHeaders preserves all headers the Forwarder may set or
// strip — tests inspect the captured map directly via /__capture/last.
func filterAndCopyHeaders(in http.Header) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string{}, v...)
	}
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ach-mock <litellm|mcp>")
		os.Exit(2)
	}
	mode := os.Args[1]
	addr := envOr("MOCK_BIND_ADDRESS", ":9090")

	cap := &capture{}
	mux := http.NewServeMux()

	// Common capture readback endpoint.
	mux.HandleFunc("/__capture/last", func(w http.ResponseWriter, r *http.Request) {
		snap := cap.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	// Common reset endpoint — tests call this between scenarios.
	mux.HandleFunc("/__capture/reset", func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		*cap = capture{}
		cap.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	// Healthz so a kind Pod can pass readiness.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	switch mode {
	case "litellm":
		mountLiteLLM(mux, cap)
	case "mcp":
		mountMCP(mux, cap)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want litellm | mcp)\n", mode)
		os.Exit(2)
	}

	log.Printf("ach-mock starting mode=%s addr=%s", mode, addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

// mountLiteLLM wires the LiteLLM-shaped chat-completion + embeddings
// routes. Captures the request and replies with a minimal chat
// completion envelope so the Forwarder's pass-through is observable.
func mountLiteLLM(mux *http.ServeMux, cap *capture) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		cap.record(r, raw)

		// Refuse when x-litellm-api-key is absent — same shape as real
		// LiteLLM so SC#1 can detect header-rewrite failure via 401.
		if r.Header.Get("x-litellm-api-key") == "" {
			http.Error(w, `{"error":{"code":"unauthorized","message":"missing x-litellm-api-key"}}`,
				http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id": "chatcmpl-mock-001",
  "object": "chat.completion",
  "model": "mock-model",
  "choices": [{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}`))
	}
	mux.HandleFunc("/v1/chat/completions", handler)
	mux.HandleFunc("/v1/embeddings", handler)
	mux.HandleFunc("/v1/", handler)
	mux.HandleFunc("/gemini/", handler)
}

// mountMCP wires the MCP echo handler. Records the request and replies
// with a JSON envelope that echoes the Authorization header verbatim —
// SC#3 assertions decode this to confirm the JWT minted by the Forwarder
// reached the backend.
func mountMCP(mux *http.ServeMux, cap *capture) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		cap.record(r, raw)

		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"mcp_echo": true,
			// authorization_seen lets SC#3 assert "Bearer <jwt>" round-tripped.
			"authorization_seen": auth,
			"path":               r.URL.Path,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
	mux.HandleFunc("/mcp/", handler)
	mux.HandleFunc("/a2a/", handler)
	mux.HandleFunc("/", handler) // catch-all so any path the Forwarder rewrites lands here
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
