// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerGemini_RouteAllowlist(t *testing.T) {
	const gen = ":generateContent"
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int // 0 = reaches upstream
	}{
		{"list models", "GET", "/gemini/v1beta/models", "", 0},
		{"get model", "GET", "/gemini/v1beta/models/gemini-3.7-flash", "", 0},
		{"generate", "POST", "/gemini/v1beta/models/m" + gen, `{"contents":[]}`, 0},
		{"stream with alt=sse", "POST", "/gemini/v1beta/models/m:streamGenerateContent?alt=sse", `{}`, 0},
		{"countTokens", "POST", "/gemini/v1beta/models/m:countTokens", `{}`, 0},
		{"embedContent", "POST", "/gemini/v1beta/models/m:embedContent", `{}`, 0},
		{"batchEmbedContents", "POST", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[{"model":"models/m"}]}`, 0},
		{"POST without body", "POST", "/gemini/v1beta/models/m" + gen, "", 0},

		{"v1 version", "POST", "/gemini/v1/models/m" + gen, `{}`, http.StatusNotFound},
		{"v1alpha version", "POST", "/gemini/v1alpha/models/m" + gen, `{}`, http.StatusNotFound},
		{"v1main version", "POST", "/gemini/v1main/models/m" + gen, `{}`, http.StatusNotFound},
		{"version case", "POST", "/gemini/V1beta/models/m" + gen, `{}`, http.StatusNotFound},
		{"tunedModels", "POST", "/gemini/v1beta/tunedModels/m" + gen, `{}`, http.StatusNotFound},
		{"files", "GET", "/gemini/v1beta/files", "", http.StatusNotFound},
		{"cachedContents", "POST", "/gemini/v1beta/cachedContents", `{}`, http.StatusNotFound},
		{"batches", "GET", "/gemini/v1beta/batches", "", http.StatusNotFound},
		{"batchGenerateContent", "POST", "/gemini/v1beta/models/m:batchGenerateContent", `{}`, http.StatusNotFound},
		{"predict", "POST", "/gemini/v1beta/models/m:predict", `{}`, http.StatusNotFound},
		{"method case", "POST", "/gemini/v1beta/models/m:GenerateContent", `{}`, http.StatusNotFound},
		{"extra segment", "POST", "/gemini/v1beta/models/a/b" + gen, `{}`, http.StatusNotFound},
		{"encoded slash", "POST", "/gemini/v1beta/models/..%2fx" + gen, `{}`, http.StatusNotFound},
		{"encoded slash lower-case hex in middle", "POST", "/gemini/v1beta/models/a%2Fb" + gen, `{}`, http.StatusNotFound},
		{"dot segment", "POST", "/gemini/v1beta/models/.." + gen, `{}`, http.StatusNotFound},
		{"encoded dot segment", "POST", "/gemini/v1beta/models/%2e%2e" + gen, `{}`, http.StatusNotFound},
		{"empty model", "POST", "/gemini/v1beta/models/" + gen, `{}`, http.StatusNotFound},
		{"GET with action", "GET", "/gemini/v1beta/models/m" + gen, "", http.StatusNotFound},
		{"POST without action", "POST", "/gemini/v1beta/models/m", `{}`, http.StatusNotFound},
		{"POST on list", "POST", "/gemini/v1beta/models", `{}`, http.StatusNotFound},
		{"DELETE model", "DELETE", "/gemini/v1beta/models/m", "", http.StatusNotFound},
		{"openai-compat subpath", "POST", "/gemini/v1beta/openai/chat/completions", `{}`, http.StatusNotFound},
		{"encoded percent", "POST", "/gemini/v1beta/models/m%25x" + gen, `{}`, http.StatusNotFound},
		{"encoded percent on GET", "GET", "/gemini/v1beta/models/m%25x", "", http.StatusNotFound},
		{"encoded question mark", "POST", "/gemini/v1beta/models/m%3Fx" + gen, `{}`, http.StatusNotFound},
		{"encoded hash", "GET", "/gemini/v1beta/models/m%23x", "", http.StatusNotFound},
		{"bare prefix", "GET", "/gemini/", "", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, upstreamPath, closeFn := pathSpy(t, nil)
			defer closeFn()
			r := requestWithKC(t, tc.method, tc.path, geminiKC(), tc.body)
			w := httptest.NewRecorder()
			HandlerGemini(deps)(w, r)
			if tc.want == 0 {
				if upstreamPath() == "" {
					t.Fatalf("status %d, want it forwarded", w.Code)
				}
				return
			}
			if w.Code != tc.want {
				t.Fatalf("status = %d; want %d", w.Code, tc.want)
			}
			if !strings.Contains(w.Body.String(), "route_not_allowed") {
				t.Fatalf("body %q lacks route_not_allowed", w.Body.String())
			}
			if upstreamPath() != "" {
				t.Fatalf("upstream was reached at %q", upstreamPath())
			}
		})
	}
}

func TestHandlerGemini_SlashedPrefix(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		prefixes []string
		want     bool // reaches upstream
	}{
		{"configured slashed prefix", "/gemini/v1beta/models/gemini/x:generateContent", []string{"gemini/"}, true},
		{"slashed prefix on get", "/gemini/v1beta/models/gemini/x", []string{"gemini/"}, true},
		{"unconfigured slash", "/gemini/v1beta/models/a/b:generateContent", []string{"gemini/"}, false},
		{"unconfigured slash, no prefixes", "/gemini/v1beta/models/a/b:generateContent", nil, false},
		{"slash left after the prefix", "/gemini/v1beta/models/gemini/a/b:generateContent", []string{"gemini/"}, false},
		{"dot segment after the prefix", "/gemini/v1beta/models/gemini/..:generateContent", []string{"gemini/"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, upstreamPath, closeFn := pathSpy(t, tc.prefixes)
			defer closeFn()
			method, body := "POST", `{}`
			if !strings.Contains(tc.path, ":") {
				method, body = "GET", ""
			}
			w := httptest.NewRecorder()
			HandlerGemini(deps)(w, requestWithKC(t, method, tc.path, geminiKC(), body))
			if reached := upstreamPath() != ""; reached != tc.want {
				t.Fatalf("reached upstream = %v (status %d); want %v", reached, w.Code, tc.want)
			}
		})
	}
}

func TestHandlerGemini_BodyModelMustMatchPath(t *testing.T) {
	prefixes := []string{"gemini.", "vertex_ai."}
	tests := []struct {
		name string
		path string
		body string
		want int // 0 = forwarded
	}{
		{"equal", "/gemini/v1beta/models/m:generateContent", `{"model":"m"}`, 0},
		{"equal with models/ prefix", "/gemini/v1beta/models/m:embedContent", `{"model":"models/m"}`, 0},
		{"equal after configured prefix strip", "/gemini/v1beta/models/m:generateContent", `{"model":"models/gemini.m"}`, 0},
		{"prefixed path, bare body", "/gemini/v1beta/models/gemini.m:generateContent", `{"model":"m"}`, 0},
		{"absent", "/gemini/v1beta/models/m:generateContent", `{"contents":[]}`, 0},
		{"different", "/gemini/v1beta/models/m:generateContent", `{"model":"other"}`, http.StatusBadRequest},
		{"different on embedContent", "/gemini/v1beta/models/m:embedContent", `{"model":"models/other"}`, http.StatusBadRequest},
		{"different on batchEmbedContents", "/gemini/v1beta/models/m:batchEmbedContents", `{"model":"other"}`, http.StatusBadRequest},
		{"different on countTokens", "/gemini/v1beta/models/m:countTokens", `{"model":"other"}`, http.StatusBadRequest},
		{"different on stream", "/gemini/v1beta/models/m:streamGenerateContent", `{"model":"other"}`, http.StatusBadRequest},
		{"target_model_names without model", "/gemini/v1beta/models/m:generateContent", `{"target_model_names":"other"}`, http.StatusBadRequest},
		{"target_model_names beside model", "/gemini/v1beta/models/m:generateContent", `{"model":"m","target_model_names":["other"]}`, http.StatusBadRequest},
		{"non-string model", "/gemini/v1beta/models/m:generateContent", `{"model":["m"]}`, http.StatusBadRequest},
		{"null model", "/gemini/v1beta/models/m:generateContent", `{"model":null}`, http.StatusBadRequest},
		{"form body", "/gemini/v1beta/models/m:generateContent", `model=other`, http.StatusBadRequest},
		{"batch entries match", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[{"model":"models/m"},{"model":"models/gemini.m"}]}`, 0},
		{"batch entry without model", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[{"model":"models/m"},{"content":{}}]}`, 0},
		{"batch entry differs", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[{"model":"models/m"},{"model":"models/other"}]}`, http.StatusBadRequest},
		{"batch entry non-string model", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[{"model":["m"]}]}`, http.StatusBadRequest},
		{"batch entry not an object", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":["m"]}`, http.StatusBadRequest},
		{"batch entry null", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":[null]}`, http.StatusBadRequest},
		{"batch requests not a list", "/gemini/v1beta/models/m:batchEmbedContents", `{"requests":{"model":"m"}}`, http.StatusBadRequest},
		{"requests on another action", "/gemini/v1beta/models/m:generateContent", `{"requests":[{"model":"other"}]}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, upstreamPath, closeFn := pathSpy(t, prefixes)
			defer closeFn()
			r := requestWithKC(t, "POST", tc.path, geminiKC(), tc.body)
			if strings.HasPrefix(tc.body, "model=") {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			w := httptest.NewRecorder()
			HandlerGemini(deps)(w, r)
			if tc.want == 0 {
				if upstreamPath() == "" {
					t.Fatalf("status %d (%s), want it forwarded", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != tc.want {
				t.Fatalf("status = %d; want %d", w.Code, tc.want)
			}
			if upstreamPath() != "" {
				t.Fatalf("upstream was reached at %q", upstreamPath())
			}
		})
	}
}

func TestHandlerGemini_BodyContentType(t *testing.T) {
	const b = "XBOUND"
	multipartBody := "--" + b + "\r\nContent-Disposition: form-data; name=\"contents\"\r\n\r\nv\r\n--" + b + "--\r\n"
	tests := []struct {
		name, ct, body string
		forwarded      bool
	}{
		{"json", "application/json", `{}`, true},
		{"json with parameters", "application/json; charset=utf-8", `{}`, true},
		{"json in upper case", "Application/JSON", `{}`, true},
		{"absent", "", `{}`, true},
		{"no body, any type", "application/x-www-form-urlencoded", "", true},
		{"urlencoded", "application/x-www-form-urlencoded", `{"contents":[]}`, false},
		{"multipart", "multipart/form-data; boundary=" + b, multipartBody, false},
		{"text", "text/plain", `{}`, false},
		{"another json type", "application/vnd.api+json", `{}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, upstreamPath, closeFn := pathSpy(t, nil)
			defer closeFn()
			r := requestWithKC(t, "POST", "/gemini/v1beta/models/m:generateContent", geminiKC(), tc.body)
			r.Header.Del("Content-Type")
			if tc.ct != "" {
				r.Header.Set("Content-Type", tc.ct)
			}
			w := httptest.NewRecorder()
			HandlerGemini(deps)(w, r)
			if tc.forwarded {
				if upstreamPath() == "" {
					t.Fatalf("status %d (%s), want it forwarded", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model_mismatch") {
				t.Fatalf("status %d (%s), want 400 model_mismatch", w.Code, w.Body.String())
			}
			if upstreamPath() != "" {
				t.Fatalf("upstream was reached at %q", upstreamPath())
			}
		})
	}
}

func TestHandlerGemini_ModelMismatchCode(t *testing.T) {
	deps, _, closeFn := pathSpy(t, nil)
	defer closeFn()
	r := requestWithKC(t, "POST", "/gemini/v1beta/models/m:generateContent", geminiKC(), `{"model":"other"}`)
	w := httptest.NewRecorder()
	HandlerGemini(deps)(w, r)
	if !strings.Contains(w.Body.String(), "model_mismatch") {
		t.Fatalf("body %q lacks model_mismatch", w.Body.String())
	}
}
