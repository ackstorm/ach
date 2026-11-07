// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestClient_Do_RedirectDropsKeyAcrossOrigins asserts x-ach-key reaches only
// the origin it was addressed to, while a same-origin redirect keeps it.
func TestClient_Do_RedirectDropsKeyAcrossOrigins(t *testing.T) {
	var other string
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other = r.Header.Get("x-ach-key")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer otherSrv.Close()
	var same string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, otherSrv.URL+"/x", http.StatusFound)
		case "/same":
			http.Redirect(w, r, "/landed", http.StatusFound)
		default:
			same = r.Header.Get("x-ach-key")
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk-secret"}
	var out struct{}
	if err := c.Do(context.Background(), http.MethodGet, "/cross", nil, &out); err != nil {
		t.Fatalf("cross: %v", err)
	}
	if other != "" {
		t.Errorf("x-ach-key reached another origin: %q", other)
	}
	if err := c.Do(context.Background(), http.MethodGet, "/same", nil, &out); err != nil {
		t.Fatalf("same: %v", err)
	}
	if same != "pk-secret" {
		t.Errorf("same-origin redirect lost x-ach-key: %q", same)
	}
}
