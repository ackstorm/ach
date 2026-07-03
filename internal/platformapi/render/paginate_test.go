// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
)

func TestPageParams(t *testing.T) {
	cases := []struct {
		name                  string
		query                 string
		wantLimit, wantOffset int
		wantOK                bool
	}{
		{"defaults", "", 100, 0, true},
		{"limit", "?limit=5", 5, 0, true},
		{"limit at cap", "?limit=500", 500, 0, true},
		{"limit over cap", "?limit=501", 0, 0, false},
		{"limit zero", "?limit=0", 0, 0, false},
		{"limit junk", "?limit=x", 0, 0, false},
		{"cursor", "?cursor=" + base64.StdEncoding.EncodeToString([]byte("7")), 100, 7, true},
		{"cursor bad b64", "?cursor=___", 0, 0, false},
		{"cursor negative", "?cursor=" + base64.StdEncoding.EncodeToString([]byte("-1")), 0, 0, false},
		{"cursor junk", "?cursor=" + base64.StdEncoding.EncodeToString([]byte("x")), 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/x"+c.query, nil)
			limit, offset, ok := PageParams(w, r, "req-1")
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				if w.Code != 400 {
					t.Errorf("status = %d, want 400", w.Code)
				}
				return
			}
			if limit != c.wantLimit || offset != c.wantOffset {
				t.Errorf("got (%d,%d), want (%d,%d)", limit, offset, c.wantLimit, c.wantOffset)
			}
		})
	}
}

func TestPageWindow(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	page, next := PageWindow(items, 0, 2)
	if len(page) != 2 || page[0] != 1 || next == nil {
		t.Errorf("first page wrong: %v next=%v", page, next)
	}
	page, next = PageWindow(items, 4, 2)
	if len(page) != 1 || page[0] != 5 || next != nil {
		t.Errorf("last page wrong: %v next=%v", page, next)
	}
	page, next = PageWindow(items, 99, 2) // offset past end clamps
	if len(page) != 0 || next != nil {
		t.Errorf("past-end wrong: %v next=%v", page, next)
	}
	if got := base64.StdEncoding.EncodeToString([]byte("2")); func() string {
		_, n := PageWindow(items, 0, 2)
		return n.(string)
	}() != got {
		t.Errorf("next cursor must be base64 of next offset")
	}
}
