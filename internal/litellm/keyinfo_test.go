// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListUserKeysPaginates — LiteLLM's default /key/list page size is
// 10; a single request silently drops any key past the 10th. Mock serves
// 3 pages of 2 keys each and asserts every key is returned and every
// request carries the expected page/size query values.
func TestListUserKeysPaginates(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		page := i + 1
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"keys":[{"token":"tok-%d-a","user_id":"u1"},{"token":"tok-%d-b","user_id":"u1"}],"total_count":6,"current_page":%d,"total_pages":3}`, page, page, page)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListUserKeys(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListUserKeys: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("want 6 keys across 3 pages, got %d: %+v", len(got), got)
	}
	want := []string{"tok-1-a", "tok-1-b", "tok-2-a", "tok-2-b", "tok-3-a", "tok-3-b"}
	for i, w := range want {
		if got[i].Token != w {
			t.Errorf("key %d: want token %q, got %q", i, w, got[i].Token)
		}
	}

	if len(captured) != 3 {
		t.Fatalf("want 3 requests, got %d", len(captured))
	}
	for i, c := range captured {
		wantPage := fmt.Sprintf("page=%d", i+1)
		if !strings.Contains(c.Path, wantPage) {
			t.Errorf("request %d: want %q in path, got %q", i, wantPage, c.Path)
		}
		if !strings.Contains(c.Path, "size=100") {
			t.Errorf("request %d: want size=100 in path, got %q", i, c.Path)
		}
		if !strings.Contains(c.Path, "user_id=u1") {
			t.Errorf("request %d: want user_id=u1 in path, got %q", i, c.Path)
		}
	}
}

// TestListUserKeysSinglePage — total_pages == 1 (or 0) stops after the
// first request; also covers the "zero total_pages means one page"
// case from the doc comment.
func TestListUserKeysSinglePage(t *testing.T) {
	cases := []struct {
		name       string
		totalPages int
	}{
		{"total_pages_one", 1},
		{"total_pages_zero", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []capturedRequest
			srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
				w.WriteHeader(200)
				fmt.Fprintf(w, `{"keys":[{"token":"only","user_id":"u1"}],"total_count":1,"current_page":1,"total_pages":%d}`, tc.totalPages)
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			got, err := c.ListUserKeys(context.Background(), "u1")
			if err != nil {
				t.Fatalf("ListUserKeys: %v", err)
			}
			if len(got) != 1 || got[0].Token != "only" {
				t.Fatalf("want single key %q, got %+v", "only", got)
			}
			if len(captured) != 1 {
				t.Fatalf("want 1 request, got %d", len(captured))
			}
		})
	}
}

// TestListUserKeysStopsOnEmptyPage — a page with zero keys halts the
// loop even if total_pages claims more remain, bounding it against a
// malformed/inconsistent envelope.
func TestListUserKeysStopsOnEmptyPage(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		if i == 0 {
			fmt.Fprint(w, `{"keys":[{"token":"tok-1","user_id":"u1"}],"total_count":5,"current_page":1,"total_pages":5}`)
			return
		}
		fmt.Fprint(w, `{"keys":[],"total_count":5,"current_page":2,"total_pages":5}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ListUserKeys(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListUserKeys: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 key (loop halted on empty page), got %d: %+v", len(got), got)
	}
	if len(captured) != 2 {
		t.Fatalf("want 2 requests (stop after empty page), got %d", len(captured))
	}
}
