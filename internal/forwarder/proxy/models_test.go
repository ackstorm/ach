// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

const modelListBody = `{"object":"list","data":[
	{"id":"ackstorm.smart","object":"model","mode":"chat"},
	{"id":"legacy","object":"model","mode":"completion"},
	{"id":"custom","object":"model"},
	{"id":"embed","object":"model","mode":"embedding"},
	{"id":"whisper","object":"model","mode":"audio_transcription"},
	{"id":"veo","object":"model","mode":"video_generation"}]}`

// TestModelList_FiltersByMode drives GET /v1/models through HandlerV1 against
// a LiteLLM stand-in that gzips when asked, as LiteLLM does.
func TestModelList_FiltersByMode(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Accept-Encoding") != "" {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte(modelListBody))
			_ = gz.Close()
			return
		}
		_, _ = w.Write([]byte(modelListBody))
	}))
	defer upstream.Close()
	deps := mkDeps(t, upstream, &mockSigner{}, precheck.Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{}}, newBIPResolver())
	kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}

	for _, tc := range []struct {
		path, wantQuery string
		want            []string
	}{
		{"/v1/models", "", []string{"ackstorm.smart", "legacy", "custom"}},
		{"/v1/models/?user=u1", "user=u1", []string{"ackstorm.smart", "legacy", "custom"}},
		{"/v1/models?types=embedding,%20Audio_Transcription&user=u1", "user=u1", []string{"ackstorm.smart", "legacy", "custom", "embed", "whisper"}},
		{"/v1/models?types=all", "", []string{"ackstorm.smart", "legacy", "custom", "embed", "whisper", "veo"}},
	} {
		r := requestWithKC(t, http.MethodGet, tc.path, kc, "")
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		HandlerV1(deps)(w, r)

		var got struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		raw := w.Body.Bytes()
		if w.Header().Get("Content-Encoding") == "gzip" { // ?types=all passes through as the client asked
			zr, err := gzip.NewReader(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			if raw, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: body %q: %v", tc.path, raw, err)
		}
		var ids []string
		for _, d := range got.Data {
			ids = append(ids, d.ID)
		}
		if !slices.Equal(ids, tc.want) || got.Object != "list" {
			t.Errorf("%s: object=%q ids=%v, want list %v", tc.path, got.Object, ids, tc.want)
		}
		if gotQuery != tc.wantQuery {
			t.Errorf("%s: upstream query %q, want %q (types must not reach LiteLLM)", tc.path, gotQuery, tc.wantQuery)
		}
	}
}

// Anything that is not a 200 OpenAI model list passes through untouched.
func TestModelList_OtherBodiesPassThrough(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"nope"}}`, `{"data":"x"}`, `not json`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		deps := mkDeps(t, upstream, &mockSigner{}, precheck.Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{}}, newBIPResolver())
		w := httptest.NewRecorder()
		HandlerV1(deps)(w, requestWithKC(t, http.MethodGet, "/v1/models", middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}, ""))
		upstream.Close()
		if w.Body.String() != body {
			t.Errorf("body %q came back as %q", body, w.Body.String())
		}
	}
}
