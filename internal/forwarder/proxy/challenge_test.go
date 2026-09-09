// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func mustBase(t *testing.T, raw string) *url.URL {
	t.Helper()
	u := parsePublicBase(raw)
	if u == nil {
		t.Fatalf("parsePublicBase(%q) = nil", raw)
	}
	return u
}

// TestRewriteChallengeValue pins the rewrite contract from issue #177: swap the
// scheme+authority of the resource_metadata pointer for ACH's, preserve the
// path verbatim, and leave everything else — including every other auth-param —
// byte for byte.
func TestRewriteChallengeValue(t *testing.T) {
	base := mustBase(t, "https://ach.example.com")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "rewrites a well-known pointer",
			in:   `Bearer error="invalid_token", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/my-server"`,
			want: `Bearer error="invalid_token", resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/my-server"`,
		},
		{
			name: "preserves other auth-params byte for byte",
			in:   `Bearer realm="mcp", error="invalid_token", error_description="The access token expired", scope="read write", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s"`,
			want: `Bearer realm="mcp", error="invalid_token", error_description="The access token expired", scope="read write", resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/s"`,
		},
		{
			name: "rewrites scheme too (http upstream -> https ACH)",
			in:   `Bearer resource_metadata="http://api.example.com/.well-known/oauth-protected-resource/mcp/s"`,
			want: `Bearer resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/s"`,
		},
		{
			name: "no resource_metadata parameter — untouched",
			in:   `Bearer error="invalid_token", error_description="nope"`,
			want: `Bearer error="invalid_token", error_description="nope"`,
		},
		{
			name: "path lacks the well-known segment — untouched",
			in:   `Bearer resource_metadata="https://api.example.com/mcp/my-server"`,
			want: `Bearer resource_metadata="https://api.example.com/mcp/my-server"`,
		},
		{
			name: "unterminated quoted-string — untouched",
			in:   `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s`,
			want: `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s`,
		},
		{
			name: "parameter merely ENDING in resource_metadata — untouched",
			in:   `Bearer x_resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s"`,
			want: `Bearer x_resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s"`,
		},
		{
			name: "unparseable value — untouched",
			in:   `Bearer resource_metadata="ht tp://%zz/.well-known/oauth-protected-resource/x"`,
			want: `Bearer resource_metadata="ht tp://%zz/.well-known/oauth-protected-resource/x"`,
		},
		{
			name: "query string on the pointer is preserved",
			in:   `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s?v=2"`,
			want: `Bearer resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/s?v=2"`,
		},
		{
			name: "case-insensitive parameter name",
			in:   `Bearer Resource_Metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s"`,
			want: `Bearer Resource_Metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/s"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteChallengeValue(tc.in, base); got != tc.want {
				t.Errorf("rewriteChallengeValue:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestRewriteChallengeHost pins the response-level guards: status codes, header
// absence, a nil public base, and multi-value WWW-Authenticate.
func TestRewriteChallengeHost(t *testing.T) {
	base := mustBase(t, "https://ach.example.com")
	const upstream = `Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/s"`
	const rewritten = `Bearer resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/s"`

	resp := func(status string, code int, hdr string) *http.Response {
		r := &http.Response{Status: status, StatusCode: code, Header: http.Header{}}
		if hdr != "" {
			r.Header.Set("Www-Authenticate", hdr)
		}
		return r
	}

	t.Run("401 is rewritten", func(t *testing.T) {
		r := resp("401", http.StatusUnauthorized, upstream)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != rewritten {
			t.Errorf("got %s want %s", got, rewritten)
		}
	})

	t.Run("403 is rewritten", func(t *testing.T) {
		r := resp("403", http.StatusForbidden, upstream)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != rewritten {
			t.Errorf("got %s want %s", got, rewritten)
		}
	})

	t.Run("200 is left alone", func(t *testing.T) {
		r := resp("200", http.StatusOK, upstream)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != upstream {
			t.Errorf("200 response was rewritten: %s", got)
		}
	})

	t.Run("no challenge header is a no-op", func(t *testing.T) {
		r := resp("401", http.StatusUnauthorized, "")
		rewriteChallengeHost(r, base)
		if len(r.Header.Values("Www-Authenticate")) != 0 {
			t.Errorf("header invented: %v", r.Header)
		}
	})

	t.Run("nil public base disables the rewrite", func(t *testing.T) {
		r := resp("401", http.StatusUnauthorized, upstream)
		rewriteChallengeHost(r, nil)
		if got := r.Header.Get("Www-Authenticate"); got != upstream {
			t.Errorf("rewrote with nil base: %s", got)
		}
	})

	t.Run("every value of a multi-value header is rewritten", func(t *testing.T) {
		r := resp("401", http.StatusUnauthorized, "")
		r.Header.Add("Www-Authenticate", `Basic realm="other"`)
		r.Header.Add("Www-Authenticate", upstream)
		rewriteChallengeHost(r, base)
		vals := r.Header.Values("Www-Authenticate")
		if len(vals) != 2 || vals[0] != `Basic realm="other"` || vals[1] != rewritten {
			t.Errorf("multi-value handling: %#v", vals)
		}
	})
}

// TestParsePublicBaseRejects pins that only an absolute http(s) URL enables the
// rewrite — anything else leaves ModifyResponse nil.
func TestParsePublicBaseRejects(t *testing.T) {
	for _, raw := range []string{"", "not a url", "/relative/path", "ach.example.com"} {
		if got := parsePublicBase(raw); got != nil {
			t.Errorf("parsePublicBase(%q) = %v, want nil", raw, got)
		}
	}
}

// TestResourcePathFor pins the reduction of a forwarded path to the RFC 9728
// resource identifier the insert branch advertises.
func TestResourcePathFor(t *testing.T) {
	tests := map[string]string{
		"/mcp/my-server":          "/mcp/my-server",
		"/mcp/my-server/tools":    "/mcp/my-server",
		"/mcp/my-server/":         "/mcp/my-server",
		"/a2a/agent-x":            "/a2a/agent-x",
		"/a2a/agent-x/tasks/send": "/a2a/agent-x",
		"/mcp/":                   "",
		"/mcp":                    "",
		"/v1/chat/completions":    "",
		"/.well-known/jwks.json":  "",
		"":                        "",
	}
	for in, want := range tests {
		if got := resourcePathFor(in); got != want {
			t.Errorf("resourcePathFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestInsertResourceMetadata pins the second branch of the #177 invariant: a
// challenge with no pointer LEAVES ACH carrying one, so the fix does not
// silently no-op once LiteLLM stops discarding the backend's challenge.
func TestInsertResourceMetadata(t *testing.T) {
	base := mustBase(t, "https://ach.example.com")
	const want = `resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/mcp/my-server"`

	newResp := func(code int, path string, hdrs ...string) *http.Response {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		r := &http.Response{StatusCode: code, Header: http.Header{}, Request: req}
		for _, h := range hdrs {
			r.Header.Add("Www-Authenticate", h)
		}
		return r
	}

	t.Run("appends to a bare Bearer challenge", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server",
			`Bearer error="invalid_token", error_description="expired", scope="read"`)
		rewriteChallengeHost(r, base)
		got := r.Header.Get("Www-Authenticate")
		if got != `Bearer error="invalid_token", error_description="expired", scope="read", `+want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("appends to a scheme-only Bearer challenge", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server", "Bearer")
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != "Bearer "+want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("synthesizes a challenge on a 401 with none", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server")
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != "Bearer "+want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("403 with no challenge keeps none", func(t *testing.T) {
		r := newResp(http.StatusForbidden, "/mcp/my-server")
		rewriteChallengeHost(r, base)
		if len(r.Header.Values("Www-Authenticate")) != 0 {
			t.Errorf("invented a challenge on 403: %v", r.Header)
		}
	})

	t.Run("subpath still advertises the bare resource", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server/tools", `Bearer error="invalid_token"`)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != `Bearer error="invalid_token", `+want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("a2a route", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/a2a/agent-x", `Bearer error="invalid_token"`)
		rewriteChallengeHost(r, base)
		want := `Bearer error="invalid_token", resource_metadata="https://ach.example.com/.well-known/oauth-protected-resource/a2a/agent-x"`
		if got := r.Header.Get("Www-Authenticate"); got != want {
			t.Errorf("got %s", got)
		}
	})

	t.Run("non-resource route is left alone", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/v1/chat/completions", `Bearer error="invalid_token"`)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != `Bearer error="invalid_token"` {
			t.Errorf("rewrote a non-resource route: %s", got)
		}
	})

	t.Run("Basic-only challenge is not touched", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server", `Basic realm="x"`)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != `Basic realm="x"` {
			t.Errorf("appended an OAuth param to a Basic challenge: %s", got)
		}
	})

	t.Run("present pointer still takes the rewrite branch", func(t *testing.T) {
		r := newResp(http.StatusUnauthorized, "/mcp/my-server",
			`Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp/my-server"`)
		rewriteChallengeHost(r, base)
		if got := r.Header.Get("Www-Authenticate"); got != "Bearer "+want {
			t.Errorf("got %s", got)
		}
	})
}
