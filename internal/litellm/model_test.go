// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// capturedRequest records the wire-level shape of a request the mock
// server saw. Used by path-string-verification tests across the package.
type capturedRequest struct {
	Method string
	Path   string
	Body   []byte
}

// captureMock returns an http.HandlerFunc that records every request
// into the provided slice (mutex-protected) and responds with the given
// status + body. Caller passes a function that produces the response
// for the i-th request, enabling status-sequence per call.
func captureMock(t *testing.T, captured *[]capturedRequest, respond func(i int, w http.ResponseWriter)) http.HandlerFunc {
	t.Helper()
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*captured = append(*captured, capturedRequest{
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Body:   body,
		})
		idx := len(*captured) - 1
		mu.Unlock()
		respond(idx, w)
	}
}

// TestGetModelInfoLengthCheck — REL-05. Three malformed empty shapes
// must all return ErrNotFound (NOT panic, NOT generic error).
func TestGetModelInfoLengthCheck(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty_data_array", `{"data":[]}`},
		{"null_data", `{"data":null}`},
		{"missing_data_key", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			got, err := c.GetModelInfo(context.Background(), "anything")
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("err: want ErrNotFound, got %v", err)
			}
			if got != nil {
				t.Errorf("result: want nil on empty, got %+v", got)
			}
		})
	}
}

// TestGetModelInfoReturnsFirstEntry — happy path: well-formed Data → first entry.
func TestGetModelInfoReturnsFirstEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[{"model_id":"m1","model_name":"foo","litellm_params":{},"model_info":{"id":"m1"}}]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.GetModelInfo(context.Background(), "m1")
	if err != nil {
		t.Fatalf("GetModelInfo: %v", err)
	}
	if got == nil || got.ModelID != "m1" {
		t.Errorf("result: want ModelID=m1, got %+v", got)
	}
}

// TestModelHelpers401Propagation — REL-06 propagation through every
// model helper that issues HTTP.
func TestModelHelpers401Propagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(litellmAuth401Body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	check := func(name string, err error) {
		t.Helper()
		var a *Auth401Error
		if !errors.As(err, &a) {
			t.Errorf("%s: want *Auth401Error, got %T: %v", name, err, err)
		}
	}

	_, err := c.GetModelInfo(context.Background(), "x")
	check("GetModelInfo", err)
}

// TestGetModelInfoByName_HappyPath — GetModelInfoByName with a
// matching entry in data[]. The helper must return the entry (not nil).
func TestGetModelInfoByName_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the query param is correct.
		if r.URL.Query().Get("model_name") != "my-model" {
			t.Errorf("expected model_name=my-model, got %q", r.URL.Query().Get("model_name"))
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[
			{"model_id":"uuid-1","model_name":"my-model","litellm_params":{},"model_info":{"id":"uuid-1"}},
			{"model_id":"uuid-2","model_name":"other-model","litellm_params":{},"model_info":{"id":"uuid-2"}}
		]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.GetModelInfoByName(context.Background(), "my-model")
	if err != nil {
		t.Fatalf("GetModelInfoByName: unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("GetModelInfoByName: want non-nil result, got nil")
	}
	if got.ModelName != "my-model" {
		t.Errorf("ModelName: want my-model, got %q", got.ModelName)
	}
	if got.ModelInfo.ID != "uuid-1" {
		t.Errorf("ModelInfo.ID: want uuid-1, got %q", got.ModelInfo.ID)
	}
}

// TestGetModelInfoByName_NotFound — GetModelInfoByName with empty data[]
// or 200 with no matching name must return (nil, nil) — NOT an error.
func TestGetModelInfoByName_NotFound(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty_data", `{"data":[]}`},
		{"no_name_match", `{"data":[{"model_id":"other","model_name":"other-model","litellm_params":{},"model_info":{"id":"other"}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			got, err := c.GetModelInfoByName(context.Background(), "my-model")
			if err != nil {
				t.Fatalf("GetModelInfoByName: unexpected error (want nil,nil for not-found): %v", err)
			}
			if got != nil {
				t.Errorf("GetModelInfoByName: want nil result on not-found, got %+v", got)
			}
		})
	}
}

// TestGetModelInfoByName_401 — GetModelInfoByName must return *Auth401Error on 401.
func TestGetModelInfoByName_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(litellmAuth401Body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.GetModelInfoByName(context.Background(), "my-model")
	if got != nil {
		t.Errorf("GetModelInfoByName: want nil result on 401, got %+v", got)
	}
	var a *Auth401Error
	if !errors.As(err, &a) {
		t.Errorf("GetModelInfoByName: want *Auth401Error on 401, got %T: %v", err, err)
	}
}

// TestGetModelInfoByName_5xx — GetModelInfoByName returns non-nil error on 5xx.
func TestGetModelInfoByName_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream","type":"upstream","param":null,"code":"503"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.GetModelInfoByName(context.Background(), "my-model")
	if err == nil {
		t.Fatal("GetModelInfoByName: want error on 5xx, got nil")
	}
	if got != nil {
		t.Errorf("GetModelInfoByName: want nil result on error, got %+v", got)
	}
	// Must NOT be an Auth401Error.
	var a *Auth401Error
	if errors.As(err, &a) {
		t.Errorf("GetModelInfoByName: 5xx should not be *Auth401Error")
	}
}

