// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

type call struct {
	Q    url.Values
	Body string
}

// scriptedCH serves the given bodies in order (the last repeats) and records calls.
func scriptedCH(t *testing.T, bodies ...string) (*core.ClickHouse, *[]call) {
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
	return core.NewClickHouse(core.Config{URL: srv.URL, User: "ro", Password: "pw"}, srv.Client()), calls
}

func testWindow() core.Window {
	s := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return core.Window{Start: s, End: s.AddDate(0, 0, 30), Timezone: "UTC"}
}

func validQuery() TraceQuery {
	return TraceQuery{Window: testWindow(), Limit: 25}
}

func scope(t *testing.T, email string) core.Scope {
	t.Helper()
	s, err := core.NewUserScope(email)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
