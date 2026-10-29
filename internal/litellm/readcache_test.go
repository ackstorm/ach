// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestReadCache — the operator's GET cache: a repeat GET is served from
// memory, any write drops it (so a read-back after a write is live),
// /tag/info does not, errors are never cached, and entries expire.
func TestReadCache(t *testing.T) {
	var captured []capturedRequest
	fail := false
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		if fail {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.EnableReadCache(time.Hour)
	ctx := context.Background()
	get := func() { _, _ = c.makeRequest(ctx, "GET", "/v1/access_group", nil) }

	get()
	get() // hit
	_, _ = c.makeRequest(ctx, "POST", "/tag/info", nil)
	get() // still a hit: /tag/info only reads
	_, _ = c.makeRequest(ctx, "POST", "/v1/access_group", nil)
	get() // miss: the write dropped the cache
	want := "/v1/access_group,/tag/info,/v1/access_group,/v1/access_group"
	if got := strings.Join(paths(captured), ","); got != want {
		t.Fatalf("requests = %s, want %s", got, want)
	}

	captured, fail = nil, true
	_, _ = c.makeRequest(ctx, "GET", "/team/info?team_id=x", nil)
	fail = false
	_, _ = c.makeRequest(ctx, "GET", "/team/info?team_id=x", nil) // error was not cached
	if len(captured) != 2 {
		t.Fatalf("an error must not be cached, got %d requests", len(captured))
	}

	captured = nil
	c.EnableReadCache(time.Nanosecond)
	get()
	time.Sleep(time.Millisecond)
	get() // expired
	if len(captured) != 2 {
		t.Fatalf("an expired entry must be refetched, got %d requests", len(captured))
	}
}

// TestReadCacheOffByDefault — every other service (platform-api, forwarder,
// content-service) builds RESTClient without the cache and stays live.
func TestReadCacheOffByDefault(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	for range 2 {
		_, _ = c.makeRequest(context.Background(), "GET", "/v1/access_group", nil)
	}
	if len(captured) != 2 {
		t.Fatalf("no cache unless enabled, got %d requests", len(captured))
	}
}
