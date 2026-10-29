// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPResult_SSEAndJSON(t *testing.T) {
	sse := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[]}}\n\n"
	r, err := mcpResult("text/event-stream", []byte(sse))
	if err != nil || r["tools"] == nil {
		t.Fatalf("sse: %v %v", r, err)
	}
	r, err = mcpResult("application/json", []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[1]}}`))
	if err != nil || r["tools"] == nil {
		t.Fatalf("json: %v %v", r, err)
	}
	if _, err = mcpResult("application/json", []byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-1}}`)); err == nil {
		t.Fatal("error envelope must fail")
	}
}

func fakeForwarder(t *testing.T, status string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/mcp/")
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":%q}}}\n\n", key)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "s1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"_meta\":{\"litellm.ai/server_outcomes\":{%q:{\"status\":%q}}},\"tools\":[]}}\n\n", key, status)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

func TestProbeGrant(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   probeOutcome
	}{
		{status: "ok", want: probeOK},
		{status: "auth_required", want: probeAuthRequired},
		{status: "error", want: probeOther},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f := newAS(t)
			installFakePKs(f)
			f.deps.Now = time.Now
			fw := fakeForwarder(t, tc.status)
			defer fw.Close()
			f.deps.Issuer = fw.URL
			f.deps.HTTPClient = fw.Client()
			if got := f.deps.probeGrant(context.Background(), "kilgore@kilgore.trout", "u1", "svc"); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}

	f := newAS(t)
	installFakePKs(f)
	f.deps.Now = time.Now
	f.deps.Issuer = "http://127.0.0.1:1"
	if got := f.deps.probeGrant(context.Background(), "k@x", "u1", "svc"); got != probeOther {
		t.Fatalf("unreachable: %s", got)
	}
	f403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer f403.Close()
	f.deps.Issuer = f403.URL
	if got := f.deps.probeGrant(context.Background(), "k@x", "u1", "svc"); got != probeOther {
		t.Fatalf("403: %s", got)
	}
}
