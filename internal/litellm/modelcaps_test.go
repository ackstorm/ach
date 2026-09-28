// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListDeploymentCapabilities_PagesAndFirstWins(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/model/info" || r.Header.Get("Authorization") != "Bearer "+testMasterKey {
			t.Errorf("path %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"total_pages":2,"data":[{"model_name":"gemini.flash","litellm_params":{"api_key":"sk-up"},
				"model_info":{"mode":"chat","max_input_tokens":1048576,"supports_pdf_input":true,"cache_read_input_token_cost":7.5e-08,"supports_audio_input":null}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"total_pages":2,"data":[{"model_name":"gemini.flash","model_info":{"max_input_tokens":1}},
			{"model_name":"openai.gpt","model_info":{"mode":"chat","supports_vision":true}}]}`))
	}))
	defer srv.Close()
	caps, err := newTestClient(t, srv.URL).ListDeploymentCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g := caps["gemini.flash"]
	if strings.Join(pages, ",") != "1,2" || len(caps) != 2 || *g.MaxInputTokens != 1048576 || !*g.SupportsPDFInput ||
		g.SupportsAudioInput != nil || *g.CacheReadInputTokenCost != 7.5e-08 || !*caps["openai.gpt"].SupportsVision {
		t.Fatalf("pages=%v caps=%+v", pages, caps)
	}
}

func TestModelGroupAliases_KeepsOnlyTheAliasMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"callbacks":[{"name":"langfuse","variables":{"LANGFUSE_SECRET_KEY":"x"}}],
			"router_settings":{"routing_strategy":"simple-shuffle","model_group_alias":{
			"ackstorm.smart":"gemini.flash","ackstorm.hidden":{"model":"openai.gpt","hidden":true},"bad":3}}}`))
	}))
	defer srv.Close()
	got, err := newTestClient(t, srv.URL).ModelGroupAliases(context.Background())
	if err != nil || len(got) != 2 || got["ackstorm.smart"] != "gemini.flash" || got["ackstorm.hidden"] != "openai.gpt" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestModelGroupAliases_NoRouterSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"callbacks":[]}`))
	}))
	defer srv.Close()
	if got, err := newTestClient(t, srv.URL).ModelGroupAliases(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}
