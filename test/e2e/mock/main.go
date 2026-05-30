// SPDX-License-Identifier: Apache-2.0

// ach-mock is the e2e mock-backend binary. Three subcommands:
//
//	ach-mock model — OpenAI-compatible chat-completion / embeddings echo
//	                 backend. Sits BEHIND the real LiteLLM as the model
//	                 upstream (it is NOT a LiteLLM mock); "parrots" the last
//	                 user message so tests can assert the full
//	                 forwarder → LiteLLM → model round-trip.
//	ach-mock mcp   — MCP server echo backend. Echoes the Authorization header
//	                 (JWT) in the response and captures it for tests.
//	ach-mock a2a   — Agent-to-Agent echo backend.
//
// All three modes share the same in-memory capture surface and listen on
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
		fmt.Fprintln(os.Stderr, "usage: ach-mock <model|mcp|a2a>")
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
	//
	// Reset the DATA fields in place; do NOT do `*cap = capture{}`, which would
	// overwrite the embedded sync.Mutex with a fresh (unlocked) one while we
	// hold the old one — the following Unlock then fires "fatal error: sync:
	// unlock of unlocked mutex" and crashes the process (go vet copylocks).
	mux.HandleFunc("/__capture/reset", func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		cap.Method, cap.Path, cap.Headers = "", "", nil
		cap.Body, cap.BodyRaw, cap.At = nil, "", time.Time{}
		cap.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	// Healthz so a kind Pod can pass readiness.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	switch mode {
	case "model":
		mountModel(mux, cap)
	case "mcp":
		mountMCP(mux, cap)
	case "a2a":
		mountA2A(mux, cap)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want model | mcp | a2a)\n", mode)
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

// mountModel wires the OpenAI-compatible chat-completion + embeddings routes
// for the ach-mock-model echo backend. Captures the request (/__capture/last)
// and replies with a chat-completion envelope whose assistant content ECHOES
// the last user message — a "parrot" model, so e2e can assert keys work and
// the full data-plane round-trips (forwarder → LiteLLM → ach-mock-model → echo).
func mountModel(mux *http.ServeMux, cap *capture) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		cap.record(r, raw)

		// Require SOME upstream credential so a header-rewrite/auth failure
		// surfaces as 401. Accept either header: x-litellm-api-key (forwarder→mock
		// direct position) OR Authorization: Bearer (LiteLLM→mock model-backend
		// position, where LiteLLM forwards the model api_key as a Bearer token).
		if r.Header.Get("x-litellm-api-key") == "" && r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":{"code":"unauthorized","message":"missing upstream credential"}}`,
				http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-mock-001",
			"object": "chat.completion",
			"model":  "ach-mock-model",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": echoContent(raw)},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
		})
	}
	mux.HandleFunc("/v1/chat/completions", handler)
	mux.HandleFunc("/v1/embeddings", handler)
	mux.HandleFunc("/v1/", handler)
	mux.HandleFunc("/gemini/", handler)
}

// echoContent extracts the last message's content from an OpenAI
// chat-completion request body, for the parrot reply. Falls back to "ok"
// when the body has no parseable messages.
func echoContent(raw []byte) string {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &req); err == nil && len(req.Messages) > 0 {
		if c := req.Messages[len(req.Messages)-1].Content; c != "" {
			return c
		}
	}
	return "ok"
}

// mountA2A wires a SKELETON A2A (agent-to-agent) backend — captures the
// request (/__capture/last) and replies 200 with a minimal JSON-RPC envelope.
// Intentionally empty for now (full A2A surface — agent card, message/send,
// tasks — is a TODO); this just gives a deployable, reachable skeleton so
// demo-agent points at a real backend instead of a 404.
func mountA2A(mux *http.ServeMux, cap *capture) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		cap.record(r, raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"result":  map[string]any{"mock_a2a": true},
		})
	}
	mux.HandleFunc("/", handler)
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
