// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// envelope is the §15.5 wire shape the CLI client decodes on non-2xx.
type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// writeEnvelope is a test helper that writes a §15.5 error envelope at
// the given status code.
func writeEnvelope(w http.ResponseWriter, status int, code, msg, reqID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": msg,
		},
		"request_id": reqID,
	})
}

// TestClient_Do_GET2xx asserts Test 1: a successful GET decodes the
// body into the provided out struct and verbose mode writes a redacted
// header dump to Stderr including "x-ach-key: pk-***".
func TestClient_Do_GET2xx(t *testing.T) {
	type body struct {
		Hello string `json:"hello"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-ach-key"); got != "pk-supersecretlong" {
			t.Errorf("server saw x-ach-key=%q, want pk-supersecretlong", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body{Hello: "world"})
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	c := &httpclient.Client{
		BaseURL: srv.URL,
		APIKey:  "pk-supersecretlong",
		Verbose: true,
		Stderr:  &stderr,
	}
	var out body
	if err := c.Do(context.Background(), http.MethodGet, "/ping", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Hello != "world" {
		t.Errorf("decoded %+v, want hello=world", out)
	}
	dump := stderr.String()
	if !strings.Contains(dump, "x-ach-key: pk-***") &&
		!strings.Contains(dump, "X-Ach-Key: pk-***") {
		t.Errorf("verbose dump missing redacted x-ach-key:\n%s", dump)
	}
	if strings.Contains(dump, "supersecretlong") {
		t.Errorf("verbose dump leaked plaintext:\n%s", dump)
	}
}

// TestClient_Do_PostBodyAndDecode asserts Test 2: POST with JSON body
// is decoded server-side, response is decoded client-side.
func TestClient_Do_PostBodyAndDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("server saw Content-Type=%q, want application/json", r.Header.Get("Content-Type"))
		}
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatalf("server decode: %v", err)
		}
		if in["name"] != "demo" {
			t.Errorf("server saw body %+v, want name=demo", in)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"echo": "demo"})
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	var out map[string]string
	if err := c.Do(context.Background(), http.MethodPost, "/echo", map[string]string{"name": "demo"}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out["echo"] != "demo" {
		t.Errorf("decoded %v, want echo=demo", out)
	}
}

// TestClient_Do_401_ServerError asserts Test 3: a 401 response decodes
// into *ServerError with the expected fields + Error() format.
func TestClient_Do_401_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusUnauthorized, "invalid_key", "x", "req_test")
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	err := c.Do(context.Background(), http.MethodGet, "/", nil, nil)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("Do returned %v, want *ServerError", err)
	}
	if sErr.Status != 401 || sErr.Code != "invalid_key" || sErr.Message != "x" || sErr.RequestID != "req_test" {
		t.Errorf("ServerError %+v, want {401 invalid_key x req_test}", sErr)
	}
	want := "401 invalid_key: x (request_id=req_test)"
	if sErr.Error() != want {
		t.Errorf("Error() = %q, want %q", sErr.Error(), want)
	}
}

// TestClient_Do_403_NotAdmin asserts Test 4: 403 with code "not_admin"
// surfaces correctly populated *ServerError.
func TestClient_Do_403_NotAdmin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusForbidden, "not_admin", "admin only", "req_a")
	}))
	defer srv.Close()
	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	err := c.Do(context.Background(), http.MethodGet, "/admin", nil, nil)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("Do returned %v, want *ServerError", err)
	}
	if sErr.Code != "not_admin" || sErr.Status != 403 {
		t.Errorf("ServerError %+v, want {403 not_admin ...}", sErr)
	}
}

// TestClient_Do_MalformedEnvelope asserts Test 5: malformed JSON body
// on 4xx returns *ServerError with Status set, Code/Message empty, and
// ErrEnvelopeDecode wrapped via Underlying.
func TestClient_Do_MalformedEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	err := c.Do(context.Background(), http.MethodGet, "/", nil, nil)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("Do returned %v, want *ServerError", err)
	}
	if sErr.Status != 400 {
		t.Errorf("Status = %d, want 400", sErr.Status)
	}
	if sErr.Code != "" || sErr.Message != "" {
		t.Errorf("expected zero-value Code/Message on malformed envelope, got %+v", sErr)
	}
	if !errors.Is(err, httpclient.ErrEnvelopeDecode) {
		t.Errorf("error chain missing ErrEnvelopeDecode: %v", err)
	}
}

// TestClient_Do_AdditiveEnvelopeField (CR-01) asserts that an error
// envelope carrying an UNKNOWN additive field (e.g. retry_after) still
// decodes into a fully-populated *ServerError — not an opaque
// ErrEnvelopeDecode — and that exit.MapServerError maps the 403 to AuthN.
// Guards the removal of decodeServerError's DisallowUnknownFields.
func TestClient_Do_AdditiveEnvelopeField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized_team","message":"no intersection"},"request_id":"req_x","retry_after":30}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	err := c.Do(context.Background(), http.MethodGet, "/", nil, nil)
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("Do returned %v, want *ServerError", err)
	}
	if errors.Is(err, httpclient.ErrEnvelopeDecode) {
		t.Fatalf("additive field must not trip ErrEnvelopeDecode: %v", err)
	}
	if sErr.Code != "unauthorized_team" || sErr.Status != 403 || sErr.RequestID != "req_x" {
		t.Errorf("ServerError %+v, want {403 unauthorized_team ... req_x}", sErr)
	}
	if got := exit.MapServerError(sErr); got != exit.AuthN {
		t.Errorf("MapServerError = %d, want AuthN (%d)", got, exit.AuthN)
	}
}

// TestClient_Do_AdditiveSuccessField is the env-list root-cause guard:
// a 2xx body carrying fields beyond the lean decode target must NOT error
// (the /platform/environments payload carries authorizedTeams / context /
// runtime / conditions / origin / locked beyond name+namespace+status).
func TestClient_Do_AdditiveSuccessField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"name":"demo","namespace":"ach-system","status":"Available","authorizedTeams":["default"],"origin":"cr","locked":true}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	var lean struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Status    string `json:"status"`
	}
	if err := c.Do(context.Background(), http.MethodGet, "/", nil, &lean); err != nil {
		t.Fatalf("Do with additive success fields returned %v, want nil", err)
	}
	if lean.Name != "demo" || lean.Namespace != "ach-system" || lean.Status != "Available" {
		t.Errorf("lean decode = %+v, want {demo ach-system Available}", lean)
	}
}

// TestClient_Do_TransportError asserts Test 6: transport error → a
// non-*ServerError error is returned.
func TestClient_Do_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediately close; subsequent dial fails

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	err := c.Do(context.Background(), http.MethodGet, "/", nil, nil)
	if err == nil {
		t.Fatalf("expected transport error, got nil")
	}
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		t.Errorf("transport error misclassified as *ServerError: %+v", sErr)
	}
}

// TestClient_Do_ExtraHeaders asserts Test 7: ExtraHeaders are
// forwarded on every Do call. Multiple values supported.
func TestClient_Do_ExtraHeaders(t *testing.T) {
	var sawAccept, sawCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccept = r.Header.Get("Accept-Encoding")
		sawCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := &httpclient.Client{
		BaseURL: srv.URL,
		APIKey:  "pk_x",
		ExtraHeaders: http.Header{
			"Accept-Encoding": []string{"gzip"},
			"X-Custom":        []string{"hello"},
		},
	}
	var out map[string]any
	if err := c.Do(context.Background(), http.MethodGet, "/", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if sawAccept != "gzip" {
		t.Errorf("server saw Accept-Encoding=%q, want gzip", sawAccept)
	}
	if sawCustom != "hello" {
		t.Errorf("server saw X-Custom=%q, want hello", sawCustom)
	}
}

// TestClient_DoRaw_2xx asserts Test 8 part A: DoRaw returns the live
// *http.Response with Body unread on 2xx so callers can io.Copy.
func TestClient_DoRaw_2xx(t *testing.T) {
	want := "raw stream bytes verbatim"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-ach-key") != "ek_y" {
			t.Errorf("server saw x-ach-key=%q, want ek_y", r.Header.Get("x-ach-key"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(want))
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "ek_y"}
	resp, err := c.DoRaw(context.Background(), http.MethodPost, "/stream", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("DoRaw: %v", err)
	}
	if resp == nil {
		t.Fatalf("DoRaw returned nil *http.Response on 2xx")
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != want {
		t.Errorf("DoRaw stream = %q, want %q", got, want)
	}
}

// TestClient_DoRaw_NonOk_ServerError asserts Test 8 part B: DoRaw
// returns nil + *ServerError on non-2xx.
func TestClient_DoRaw_NonOk_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusServiceUnavailable, "internal_error", "down", "req_dr")
	}))
	defer srv.Close()

	c := &httpclient.Client{BaseURL: srv.URL, APIKey: "pk_x"}
	resp, err := c.DoRaw(context.Background(), http.MethodGet, "/", nil)
	if resp != nil {
		t.Errorf("DoRaw returned non-nil *http.Response on non-2xx: %+v", resp)
	}
	var sErr *httpclient.ServerError
	if !errors.As(err, &sErr) {
		t.Fatalf("DoRaw returned %v, want *ServerError", err)
	}
	if sErr.Status != 503 || sErr.Code != "internal_error" {
		t.Errorf("ServerError %+v, want {503 internal_error ...}", sErr)
	}
}
