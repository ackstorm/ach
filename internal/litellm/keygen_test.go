// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestKeyGenerateEchoesCallerSuppliedKey asserts the Phase 3 D-13 invariant:
// ACH supplies the bearer plaintext via KeyGenerateRequest.Key, and the
// LiteLLM response echoes that same plaintext in KeyGenerateResponse.Key
// (LiteLLM stores ACH's prefix verbatim). Token is the LiteLLM-internal
// opaque hex (DISTINCT from Key) — Phase 3 stores Token in
// personal_keys.litellm_token / environment_keys.litellm_token.
func TestKeyGenerateEchoesCallerSuppliedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"key":"pk_abc","token":"litellm-hex-xyz","user_id":"u-1"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.KeyGenerate(context.Background(), &KeyGenerateRequest{
		Key:    "pk_abc",
		UserID: "u-1",
	})
	if err != nil {
		t.Fatalf("KeyGenerate: %v", err)
	}
	if got == nil {
		t.Fatal("KeyGenerate: nil response")
	}
	if got.Key != "pk_abc" {
		t.Errorf("Key: want pk_abc (caller-supplied), got %q", got.Key)
	}
	if got.Token != "litellm-hex-xyz" {
		t.Errorf("Token: want litellm-hex-xyz (LiteLLM-internal), got %q", got.Token)
	}
	if got.UserID != "u-1" {
		t.Errorf("UserID: want u-1, got %q", got.UserID)
	}
}

// TestKeyGenerateOmitsMaxBudgetWhenNil asserts the KEY-10 invariant at
// the wire level: when callers pass MaxBudget=nil, the field MUST be
// absent from the marshaled JSON (NOT serialized as "max_budget":null).
// This is the type-level enforcement of "ACH never sets max_budget on
// first-SSO LiteLLM user creation" — see Phase 3 D-25.
func TestKeyGenerateOmitsMaxBudgetWhenNil(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"key":"pk_abc","token":"t-1","user_id":"u-1"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.KeyGenerate(context.Background(), &KeyGenerateRequest{
		Key:       "pk_abc",
		UserID:    "u-1",
		MaxBudget: nil,
	})
	if err != nil {
		t.Fatalf("KeyGenerate: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("want 1 request, got %d", len(captured))
	}
	if captured[0].Method != "POST" || captured[0].Path != "/key/generate" {
		t.Errorf("wire: want POST /key/generate, got %s %s", captured[0].Method, captured[0].Path)
	}
	body := string(captured[0].Body)
	if strings.Contains(body, "max_budget") {
		t.Errorf("KEY-10 violation: max_budget MUST be absent when nil, got body: %s", body)
	}

	// Re-decode the body to confirm Key + UserID landed in the right
	// JSON fields (catches accidental tag drift).
	var decoded KeyGenerateRequest
	if err := json.Unmarshal(captured[0].Body, &decoded); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if decoded.Key != "pk_abc" || decoded.UserID != "u-1" {
		t.Errorf("body shape mismatch: got %+v", decoded)
	}
}

// TestKeyGenerateRequestSerializesDuration asserts Duration and TeamID land
// on the wire — the fix for pk_ keys minted with expires:None and no team.
func TestKeyGenerateRequestSerializesDuration(t *testing.T) {
	b, err := json.Marshal(&KeyGenerateRequest{UserID: "u", TeamID: "ach-user-a@b.com", Duration: "168h"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"duration":"168h"`) {
		t.Fatalf("duration missing from body: %s", b)
	}
	if !strings.Contains(string(b), `"team_id":"ach-user-a@b.com"`) {
		t.Fatalf("team_id missing from body: %s", b)
	}
}

// TestKeyGenerate401Propagation asserts REL-06 typed error flows through
// KeyGenerate the same way it does through every other RESTClient method.
func TestKeyGenerate401Propagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(litellmAuth401Body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.KeyGenerate(context.Background(), &KeyGenerateRequest{Key: "pk_x", UserID: "u"})
	if err == nil {
		t.Fatalf("want error on 401, got nil")
	}
	var auth401 *Auth401Error
	if !errors.As(err, &auth401) {
		t.Errorf("KeyGenerate 401: want *Auth401Error, got %T: %v", err, err)
	}
}
