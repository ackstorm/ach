// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestWellKnownHandler_Manifest(t *testing.T) {
	rec := httptest.NewRecorder()
	WellKnownHandler("https://ach.test/", "acme", "git+https://x/p.git#v1")(rec, httptest.NewRequest(http.MethodGet, "/.well-known/opencode", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"auth": map[string]any{"command": []any{"opencode", "--version"}, "env": ""},
		"config": map[string]any{"plugin": []any{[]any{"git+https://x/p.git#v1", map[string]any{
			"api": "https://ach.test/v1", "platform": "https://ach.test", "provider": "acme",
		}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %s", rec.Body)
	}
}
