// SPDX-License-Identifier: Apache-2.0

package render_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

const wantContentType = "application/json; charset=utf-8"

// TestJSONSuccessEnvelope asserts the §15.5 success-side shape:
// status, Content-Type, and a body that decodes back to the input
// (with nullable next_cursor preserved).
func TestJSONSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	render.JSON(rec, http.StatusOK, map[string]any{
		"items":       []string{"a"},
		"next_cursor": nil,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != wantContentType {
		t.Fatalf("Content-Type = %q, want %q", got, wantContentType)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 || items[0] != "a" {
		t.Fatalf("items = %#v, want []any{\"a\"}", body["items"])
	}
	if v, present := body["next_cursor"]; !present {
		t.Fatalf("next_cursor key missing (must be present even when null)")
	} else if v != nil {
		t.Fatalf("next_cursor = %#v, want nil", v)
	}
}

// TestErrorEnvelope asserts the §15.5 error-side shape: status,
// Content-Type, and the verbatim envelope
// {"error":{"code":..,"message":..},"request_id":..}.
func TestErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	render.Error(rec, http.StatusUnauthorized, "expired_or_revoked", "key not valid", "req_abc")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Header().Get("Content-Type"); got != wantContentType {
		t.Fatalf("Content-Type = %q, want %q", got, wantContentType)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if body.Error.Code != "expired_or_revoked" {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, "expired_or_revoked")
	}
	if body.Error.Message != "key not valid" {
		t.Fatalf("error.message = %q, want %q", body.Error.Message, "key not valid")
	}
	if body.RequestID != "req_abc" {
		t.Fatalf("request_id = %q, want %q", body.RequestID, "req_abc")
	}
}

// TestJSONContentTypeBeforeWriteHeader asserts the Content-Type
// header is set BEFORE WriteHeader flushes the status — proven by
// observing it on rec.Header() (Go's http.ResponseWriter contract:
// header changes after WriteHeader are no-ops for the actual
// response).
func TestJSONContentTypeBeforeWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	render.JSON(rec, http.StatusOK, map[string]any{"ok": true})
	if got := rec.Header().Get("Content-Type"); got != wantContentType {
		t.Fatalf("Content-Type = %q, want %q (must be set before WriteHeader)", got, wantContentType)
	}
}

// failingWriter is an http.ResponseWriter whose Write always returns
// an error after a single header capture. Used to prove the
// encoder-error path is swallowed (status already flushed).
type failingWriter struct {
	header http.Header
	status int
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (f *failingWriter) WriteHeader(s int)         { f.status = s }

// TestJSONSwallowsEncodeError asserts json.Encoder write failures do
// not panic — the helper is best-effort once WriteHeader has flushed.
func TestJSONSwallowsEncodeError(t *testing.T) {
	w := &failingWriter{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("render.JSON panicked on failing writer: %v", r)
		}
	}()
	render.JSON(w, http.StatusOK, map[string]any{"k": "v"})
	if w.status != http.StatusOK {
		t.Fatalf("WriteHeader not called with 200 before Write error; got %d", w.status)
	}
}

// TestErrorSwallowsEncodeError asserts the same on the Error path.
func TestErrorSwallowsEncodeError(t *testing.T) {
	w := &failingWriter{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("render.Error panicked on failing writer: %v", r)
		}
	}()
	render.Error(w, http.StatusInternalServerError, "internal_error", "boom", "req_x")
	if w.status != http.StatusInternalServerError {
		t.Fatalf("WriteHeader not called with 500 before Write error; got %d", w.status)
	}
}

// TestErrorOutcomeCodeCompatibility asserts that the audit.Outcome*
// enum is wire-format-compatible with the error envelope's `code`
// field. This is the cross-package contract: handler plans pass
// audit.Outcome* constants directly to render.Error so logs and HTTP
// responses share vocabulary. The test-time import of internal/audit
// proves the vocabulary works without introducing a production cycle.
func TestErrorOutcomeCodeCompatibility(t *testing.T) {
	rec := httptest.NewRecorder()
	render.Error(rec, http.StatusUnauthorized, audit.OutcomeExpiredOrRevoked, "key not valid", "req_x")

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if body.Error.Code != "expired_or_revoked" {
		t.Fatalf("error.code = %q, want %q (audit enum literal must round-trip)",
			body.Error.Code, "expired_or_revoked")
	}
}
