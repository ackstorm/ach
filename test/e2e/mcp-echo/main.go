// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	echojwt "github.com/ackstorm/ach/test/e2e/mcp-echo/jwt"
)

func main() {
	addr := envOr("MOCK_BIND_ADDRESS", ":9090")
	jwksURL := mustEnv("ACH_JWKS_URL")
	expectIss := mustEnv("ACH_EXPECTED_ISS")
	expectAud := splitCSV(mustEnv("ACH_EXPECTED_AUD"))

	keys := echojwt.NewKeyCache(jwksURL)
	verifier := echojwt.NewVerifier(keys, echojwt.Expectations{
		Issuer:   expectIss,
		Audience: expectAud,
	})
	sink := newCapture()

	mcpSrv := server.NewMCPServer("ach-mcp-echo", "0.1.0")
	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Echoes text; payload includes the verified JWT claims."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo back verbatim.")),
	)
	mcpSrv.AddTool(echoTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := req.GetString("text", "")
		claims, _ := claimsFromContext(ctx)
		payload := map[string]any{
			"echoed":     text,
			"jwt_claims": claims,
		}
		b, _ := json.Marshal(payload)
		return mcp.NewToolResultText(string(b)), nil
	})

	streamable := server.NewStreamableHTTPServer(mcpSrv,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if v, ok := claimsFromContext(r.Context()); ok {
				return context.WithValue(ctx, claimsCtxKey, v)
			}
			return ctx
		}),
	)
	guarded := requireJWT(verifier, sink)(streamable)

	mux := http.NewServeMux()
	mux.Handle("/", guarded)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/__capture/last", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sink.snapshot())
	})
	mux.HandleFunc("/__capture/reset", func(w http.ResponseWriter, _ *http.Request) {
		sink.reset()
		w.WriteHeader(http.StatusNoContent)
	})

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("ach-mcp-echo listening addr=%s jwks=%s iss=%s aud=%v",
		addr, jwksURL, expectIss, expectAud)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		log.Fatalf("ach-mcp-echo: required env %q not set", key)
	}
	return v
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
