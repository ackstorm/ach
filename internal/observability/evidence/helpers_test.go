// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type call struct {
	Q    url.Values
	Body string
}

// scriptedCH serves the given bodies in order (the last repeats) and records calls.
func scriptedCH(t *testing.T, bodies ...string) (*ClickHouse, *[]call) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]call{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		i := len(*calls)
		*calls = append(*calls, call{r.URL.Query(), string(b)})
		mu.Unlock()
		if len(bodies) == 0 {
			bodies = []string{`{"data":[]}`}
		}
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(srv.Close)
	return NewClickHouse(Config{URL: srv.URL, User: "ro", Password: "pw"}, srv.Client()), calls
}

func testWindow() Window {
	s := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return Window{Start: s, End: s.AddDate(0, 0, 30), Timezone: "UTC"}
}

func scope(t *testing.T, email string) Scope {
	t.Helper()
	s, err := NewUserScope(email)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func page(rows ...string) string { return `{"data":[` + strings.Join(rows, ",") + `]}` }
