// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// modelInfoPayload builds a valid GET /v1/model/info body with n entries
// sized like the real thing (~4.4 KB/entry, so 433 models ≈ 1.9 MB).
func modelInfoPayload(t *testing.T, n int) []byte {
	t.Helper()
	entries := make([]map[string]any, 0, n)
	for i := range n {
		entries = append(entries, map[string]any{
			"model_id":   fmt.Sprintf("id-%d", i),
			"model_name": fmt.Sprintf("model-%d", i),
			"litellm_params": map[string]any{
				"api_base": "https://upstream.example.com/v1",
				"filler":   strings.Repeat("p", 4000),
			},
		})
	}
	body, err := json.Marshal(map[string]any{"data": entries})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

// serveBody starts a mock returning body with HTTP 200 on every path.
func serveBody(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestMakeRequestReadsBodiesOverOneMiB — regression for the silent
// truncation that stalled every Environment at SubConditionsNotReady.
// makeRequest capped bodies at 1 MiB via io.LimitReader with no overflow
// check, so a complete 1.9 MB /v1/model/info response was cut mid-JSON
// and surfaced as "decode ...: unexpected end of JSON input" — a bogus
// decode error blamed on an upstream that had answered correctly.
func TestMakeRequestReadsBodiesOverOneMiB(t *testing.T) {
	body := modelInfoPayload(t, 433)
	if len(body) <= 1<<20 {
		t.Fatalf("payload must exceed 1 MiB to exercise the regression; got %d bytes", len(body))
	}

	c := newTestClient(t, serveBody(t, body).URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels on a valid %d-byte body: %v", len(body), err)
	}
	if len(models) != 433 {
		t.Errorf("models: want 433, got %d", len(models))
	}
}

// TestMakeRequestRejectsOverCapBody — past respMaxBytes the client must
// fail loudly. A silent truncate is what caused the outage above; an
// explicit size error names the real problem at the real layer.
func TestMakeRequestRejectsOverCapBody(t *testing.T) {
	oversized := make([]byte, respMaxBytes+1024)
	for i := range oversized {
		oversized[i] = 'x'
	}

	c := newTestClient(t, serveBody(t, oversized).URL)
	_, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("ListModels on an over-cap body: want error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("want an explicit size error, got: %v", err)
	}
}
