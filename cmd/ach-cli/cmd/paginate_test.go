// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// TestFetchAll follows next_cursor across two pages, stops on a JSON null
// cursor, and carries the previous cursor into the next request path.
func TestFetchAll(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"items":[1],"next_cursor":"c1"}`))
		case "c1":
			_, _ = w.Write([]byte(`{"items":[2],"next_cursor":null}`))
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	hc := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_test", HTTPClient: srv.Client()}
	got, err := fetchAll[int](context.Background(), hc, "", func(c string) string {
		if c == "" {
			return "/list"
		}
		return "/list?cursor=" + c
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("items = %v, want [1 2]", got)
	}
	if len(paths) != 2 || paths[0] != "/list" || paths[1] != "/list?cursor=c1" {
		t.Fatalf("paths = %v", paths)
	}
}
