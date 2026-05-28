// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	addr := envOr("MOCK_BIND_ADDRESS", ":9090")

	mcpSrv := server.NewMCPServer("ach-mcp-echo", "0.1.0")

	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Echoes the supplied text back to the caller."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo back verbatim.")),
	)

	mcpSrv.AddTool(echoTool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := req.GetString("text", "")
		return mcp.NewToolResultText(text), nil
	})

	streamable := server.NewStreamableHTTPServer(mcpSrv)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", streamable)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("ach-mcp-echo listening addr=%s", addr)
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
