// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// pathSpy records the request path the upstream actually received.
func pathSpy(t *testing.T, prefixes []string) (HandlerDeps, func() string, func()) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	deps := HandlerDeps{
		Deps: Deps{
			LiteLLMUpstream:          mustParseURL(t, srv.URL),
			Logger:                   slog.Default(),
			GeminiStripModelPrefixes: prefixes,
		},
		BaseURL: "https://ach.example.com",
	}
	return deps, func() string { return got }, srv.Close
}

func geminiKC() middleware.KeyContext {
	return middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "prod", KeyID: "ek_1"}
}

// The Director must actually apply the rewrite on /gemini — the pure
// function being correct proves nothing about it being wired in.
func TestHandlerGemini_StripsModelPrefixUpstream(t *testing.T) {
	deps, upstreamPath, closeFn := pathSpy(t, []string{"gemini.", "gemini/", "vertex_ai."})
	defer closeFn()

	r := requestWithKC(t, http.MethodPost,
		"/gemini/v1beta/models/gemini.gemini-3.7-flash:generateContent", geminiKC(), "")
	HandlerGemini(deps)(httptest.NewRecorder(), r)

	if got, want := upstreamPath(), "/gemini/v1beta/models/gemini-3.7-flash:generateContent"; got != want {
		t.Fatalf("upstream path = %q; want %q", got, want)
	}
}

// With nothing configured the path must reach LiteLLM byte-for-byte, which
// is the pre-feature contract every existing deployment relies on.
func TestHandlerGemini_UnconfiguredForwardsVerbatim(t *testing.T) {
	deps, upstreamPath, closeFn := pathSpy(t, nil)
	defer closeFn()

	const path = "/gemini/v1beta/models/gemini.gemini-3.7-flash:generateContent"
	r := requestWithKC(t, http.MethodPost, path, geminiKC(), "")
	HandlerGemini(deps)(httptest.NewRecorder(), r)

	if got := upstreamPath(); got != path {
		t.Fatalf("upstream path = %q; want it unchanged (%q)", got, path)
	}
}

// /v1 must NOT be rewritten: there the model travels in the JSON body, the
// Director never reads the body, and a path-shaped match would be a
// coincidence, not a model name.
func TestHandlerV1_IgnoresGeminiPrefixConfig(t *testing.T) {
	deps, upstreamPath, closeFn := pathSpy(t, []string{"gemini.", "gemini/"})
	defer closeFn()

	const path = "/v1/models/gemini.gemini-3.7-flash"
	r := requestWithKC(t, http.MethodGet, path, geminiKC(), "")
	HandlerV1(deps)(httptest.NewRecorder(), r)

	if got := upstreamPath(); got != path {
		t.Fatalf("/v1 path = %q; want it unchanged (%q)", got, path)
	}
}
