// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func gateErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, body)
	}
	return env.Error.Code
}

// D-30 / AC-09: EkOwnerGate applies precheck.CheckEkOwner to every
// authenticated family so access loss also denies /v1 + /gemini model
// traffic, not just the prechecked /mcp + /a2a families.
func TestEkOwnerGate(t *testing.T) {
	env := makeEnvRow("demo", nil, nil, []string{"team-a"})

	t.Run("ek_ with access -> 200", func(t *testing.T) {
		deps := precheck.Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{teams: []string{"team-a"}}}
		kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
		r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, "")
		w := httptest.NewRecorder()
		EkOwnerGate(deps)(okHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body)
		}
	})

	t.Run("ek_ owner lost access -> 403 unauthorized_team", func(t *testing.T) {
		deps := precheck.Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{teams: []string{"team-z"}}}
		kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
		r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, "")
		w := httptest.NewRecorder()
		EkOwnerGate(deps)(okHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body)
		}
		if code := gateErrorCode(t, w.Body.Bytes()); code != "unauthorized_team" {
			t.Fatalf("code = %q, want unauthorized_team", code)
		}
	})

	t.Run("litellm down -> 503 litellm_unreachable, not a verdict", func(t *testing.T) {
		deps := precheck.Deps{EnvProvider: newEnvProvider(env), TeamsResolver: &mockTeamsResolver{err: errors.New("boom")}}
		kc := middleware.KeyContext{KeyType: keys.PrefixEk, OwnerEmail: "u@e", Environment: "demo"}
		r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, "")
		w := httptest.NewRecorder()
		EkOwnerGate(deps)(okHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body)
		}
		if code := gateErrorCode(t, w.Body.Bytes()); code != "litellm_unreachable" {
			t.Fatalf("code = %q, want litellm_unreachable", code)
		}
	})

	t.Run("pk_ passes untouched", func(t *testing.T) {
		deps := precheck.Deps{EnvProvider: newEnvProvider(), TeamsResolver: &mockTeamsResolver{err: errors.New("must not be consulted for pk_")}}
		kc := middleware.KeyContext{KeyType: keys.PrefixPk, OwnerEmail: "u@e"}
		r := requestWithKC(t, http.MethodPost, "/v1/chat/completions", kc, "")
		w := httptest.NewRecorder()
		EkOwnerGate(deps)(okHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body)
		}
	})

	t.Run("no ACH identity passes untouched", func(t *testing.T) {
		deps := precheck.Deps{}
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		EkOwnerGate(deps)(okHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body)
		}
	})
}
