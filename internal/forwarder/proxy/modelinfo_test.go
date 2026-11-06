// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

type fakeCaps struct{}

func (fakeCaps) ListDeploymentCapabilities(context.Context) (map[string]litellm.ModelCaps, error) {
	vision, tokens := true, 524288.0
	return map[string]litellm.ModelCaps{
		"vertex.flash": {SupportsVision: &vision, MaxInputTokens: &tokens},
	}, nil
}

func (fakeCaps) ModelGroupAliases(context.Context) (map[string]string, error) {
	return map[string]string{"ackstorm.smart": "vertex.flash", "ackstorm.broken": "gone"}, nil
}

// TestModelInfo_Alias drives GET /v2/model/info through HandlerModelInfo
// against a LiteLLM stand-in that answers each name as production does: 200
// with no row for a granted alias, 403 for a name the caller is not granted.
func TestModelInfo_Alias(t *testing.T) {
	const a2aOnly = `{"data":[{"model_name":"a2a/agent","model_info":{}}],"total_count":1,"current_page":1,"size":50,"total_pages":1}`
	const passed = `{"data":[{"model_name":"ackstorm.smart","model_info":{"max_input_tokens":1}}],"total_count":1}`
	const denied = `{"data":[],"error":{"message":"team not allowed to access model"}}` // only the status may decide

	for _, tc := range []struct {
		name, query, upstream string
		status                int
		wantRewrite           bool
	}{
		{"granted alias gets the target's caps", "model=ackstorm.smart", a2aOnly, http.StatusOK, true},
		{"alias the caller is not granted stays denied", "model=ackstorm.smart", denied, http.StatusForbidden, false},
		{"real model passes through", "model=vertex.flash", passed, http.StatusOK, false},
		{"alias LiteLLM returned itself passes through", "model=ackstorm.smart", passed, http.StatusOK, false},
		{"alias with an unknown target passes through", "model=ackstorm.broken", a2aOnly, http.StatusOK, false},
		{"unfiltered listing passes through", "", a2aOnly, http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.RawQuery; got != tc.query {
					t.Errorf("upstream query %q, want %q", got, tc.query)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.wantRewrite && r.Header.Get("Accept-Encoding") != "" {
					w.Header().Set("Content-Encoding", "gzip")
					w.WriteHeader(tc.status)
					gz := gzip.NewWriter(w)
					_, _ = gz.Write([]byte(tc.upstream))
					_ = gz.Close()
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.upstream))
			}))
			defer upstream.Close()
			deps := mkDeps(t, upstream, &mockSigner{}, precheck.Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{}}, newBIPResolver())
			deps.Deps.Caps = litellm.NewCapsCache(fakeCaps{}, slog.Default())

			path := "/v2/model/info"
			if tc.query != "" {
				path += "?" + tc.query
			}
			kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e"}
			r := requestWithKC(t, http.MethodGet, path, kc, "")
			if tc.wantRewrite {
				r.Header.Set("Accept-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			HandlerModelInfo(deps)(w, r)

			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if !tc.wantRewrite {
				if w.Body.String() != tc.upstream {
					t.Fatalf("body changed:\n%s", w.Body)
				}
				return
			}
			var body struct {
				Data []struct {
					ModelName     string          `json:"model_name"`
					LiteLLMParams json.RawMessage `json:"litellm_params"`
					ModelInfo     struct {
						SupportsVision *bool    `json:"supports_vision"`
						MaxInputTokens *float64 `json:"max_input_tokens"`
						SupportsPDF    *bool    `json:"supports_pdf_input"`
					} `json:"model_info"`
				} `json:"data"`
				TotalCount int `json:"total_count"`
				Size       int `json:"size"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", w.Body, err)
			}
			if len(body.Data) != 1 || body.Data[0].ModelName != "ackstorm.smart" || body.TotalCount != 1 {
				t.Fatalf("want only the alias row, got %s", w.Body)
			}
			info := body.Data[0].ModelInfo
			if info.SupportsVision == nil || !*info.SupportsVision || info.MaxInputTokens == nil || *info.MaxInputTokens != 524288 {
				t.Fatalf("caps %s", w.Body)
			}
			if info.SupportsPDF != nil {
				t.Fatalf("a flag LiteLLM did not report must stay unknown: %s", w.Body)
			}
			if body.Data[0].LiteLLMParams != nil {
				t.Fatalf("litellm_params leaked: %s", w.Body)
			}
			if body.Size != 50 {
				t.Fatalf("envelope lost: %s", w.Body)
			}
		})
	}
}
