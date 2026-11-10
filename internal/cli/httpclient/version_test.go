// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestVersionWarning(t *testing.T) {
	for _, tc := range []struct {
		name, cli, server string
		warns             bool
	}{
		{"cli older", "1.1.2", "1.2.0", true},
		{"compared as numbers", "1.1.9", "1.1.10", true},
		{"cli newer", "1.3.0", "1.2.0", false},
		{"snapshot cli", "1.1.11-SNAPSHOT-abc", "1.2.0", false},
		{"equal", "1.2.0", "1.2.0", false},
		{"equal with v prefix", "v1.2.0", "1.2.0", false},
		{"dev cli", "dev", "1.2.0", false},
		{"dev server", "1.2.0", "dev", false},
		{"server sends none", "1.2.0", "", false},
		{"no cli version", "", "1.2.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.server != "" {
					w.Header().Set(serverVersionHeader, tc.server)
				}
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"x","message":"y"}}`))
			}))
			defer srv.Close()
			versionWarning, CLIVersion = sync.Once{}, tc.cli
			t.Cleanup(func() { CLIVersion = "" })

			var stderr bytes.Buffer
			c := &Client{BaseURL: srv.URL, Stderr: &stderr}
			for range 2 {
				if err := c.Do(context.Background(), http.MethodGet, "/", nil, nil); err == nil {
					t.Fatal("want the server's 403 back")
				}
			}

			if got := strings.Count(stderr.String(), "warning: ach-cli"); got != map[bool]int{true: 1, false: 0}[tc.warns] {
				t.Fatalf("warned %d times, want warns=%v: %q", got, tc.warns, stderr.String())
			}
			if tc.warns && !strings.Contains(stderr.String(), "hint: upgrade with `ach-cli upgrade`") {
				t.Fatalf("warning without the upgrade hint: %q", stderr.String())
			}
		})
	}
}
