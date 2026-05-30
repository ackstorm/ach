// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newJSONRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	return req
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

// TG1: empty JSON gets {"metadata":{"tags":["environment:prod"]}}.
func TestInjectEnvironmentTag_TG1_Empty(t *testing.T) {
	req := newJSONRequest(t, `{}`)
	if err := InjectEnvironmentTag(req, "prod"); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	body := readBody(t, req)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	meta, _ := doc["metadata"].(map[string]any)
	tags, _ := meta["tags"].([]any)
	if len(tags) != 1 || tags[0] != "environment:prod" {
		t.Errorf("tags = %v; want [environment:prod]", tags)
	}
	if req.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d; want %d", req.ContentLength, len(body))
	}
}

// TG2: existing tags are preserved; new tag appended.
func TestInjectEnvironmentTag_TG2_ExistingTags(t *testing.T) {
	req := newJSONRequest(t, `{"metadata":{"tags":["existing"]}}`)
	if err := InjectEnvironmentTag(req, "prod"); err != nil {
		t.Fatalf("err = %v", err)
	}
	body := readBody(t, req)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	meta, _ := doc["metadata"].(map[string]any)
	tags, _ := meta["tags"].([]any)
	if len(tags) != 2 || tags[0] != "existing" || tags[1] != "environment:prod" {
		t.Errorf("tags = %v; want [existing environment:prod]", tags)
	}
}

// TG3: sibling metadata fields preserved.
func TestInjectEnvironmentTag_TG3_SiblingMetadataFields(t *testing.T) {
	req := newJSONRequest(t, `{"metadata":{"user_id":"u1"}}`)
	if err := InjectEnvironmentTag(req, "prod"); err != nil {
		t.Fatalf("err = %v", err)
	}
	body := readBody(t, req)
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	meta, _ := doc["metadata"].(map[string]any)
	if meta["user_id"] != "u1" {
		t.Errorf("metadata.user_id = %v; want u1", meta["user_id"])
	}
	tags, _ := meta["tags"].([]any)
	if len(tags) != 1 || tags[0] != "environment:prod" {
		t.Errorf("tags = %v", tags)
	}
}

// TG5: existing non-array tags → fail-open, body unchanged.
func TestInjectEnvironmentTag_TG5_TagsNotArray(t *testing.T) {
	original := `{"metadata":{"tags":"oldformat"}}`
	req := newJSONRequest(t, original)
	err := InjectEnvironmentTag(req, "prod")
	if err == nil {
		t.Fatal("expected error on non-array tags")
	}
	body := readBody(t, req)
	if string(body) != original {
		t.Errorf("body mutated on fail-open: got %s; want %s", body, original)
	}
}

// TG7: malformed JSON → fail-open, body restored.
func TestInjectEnvironmentTag_TG7_MalformedJSON(t *testing.T) {
	original := `{not json`
	req := newJSONRequest(t, original)
	if err := InjectEnvironmentTag(req, "prod"); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	body := readBody(t, req)
	if string(body) != original {
		t.Errorf("body changed: got %s", body)
	}
}

// TG8: non-JSON Content-Type → errNotJSON, body unchanged.
func TestInjectEnvironmentTag_TG8_NonJSONContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("blob"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=---")
	err := InjectEnvironmentTag(req, "prod")
	if !errors.Is(err, errNotJSON) {
		t.Errorf("err = %v; want errNotJSON", err)
	}
}

// TG9: missing Content-Type → errNotJSON.
func TestInjectEnvironmentTag_TG9_NoContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("{}"))
	err := InjectEnvironmentTag(req, "prod")
	if !errors.Is(err, errNotJSON) {
		t.Errorf("err = %v; want errNotJSON", err)
	}
}

// TG10: oversized body → errBodyTooLarge, no mutation.
func TestInjectEnvironmentTag_TG10_OversizedBody(t *testing.T) {
	big := strings.Repeat("a", maxBodyForTagInjection+1024)
	body := `{"junk":"` + big + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/x", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	err := InjectEnvironmentTag(req, "prod")
	if !errors.Is(err, errBodyTooLarge) {
		t.Errorf("err = %v; want errBodyTooLarge", err)
	}
}

// TG11: empty environmentName → errEmptyEnvironment.
func TestInjectEnvironmentTag_TG11_EmptyEnv(t *testing.T) {
	req := newJSONRequest(t, `{}`)
	if err := InjectEnvironmentTag(req, ""); !errors.Is(err, errEmptyEnvironment) {
		t.Errorf("err = %v; want errEmptyEnvironment", err)
	}
}

// TG13: nil body → returns nil, no panic.
func TestInjectEnvironmentTag_TG13_NilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Content-Type", "application/json")
	if err := InjectEnvironmentTag(req, "prod"); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

// TG14: success path also sets the X-Ach-Tags mirror header to the injected
// tag value (backend-observable proxy used by the SC2 e2e).
func TestInjectEnvironmentTag_TG14_HeaderSetOnSuccess(t *testing.T) {
	req := newJSONRequest(t, `{"metadata":{"tags":["existing"]}}`)
	if err := InjectEnvironmentTag(req, "demo"); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if got := req.Header.Get(headerTags); got != "environment:demo" {
		t.Errorf("%s = %q; want environment:demo", headerTags, got)
	}
}

// TG15: fail-open paths must NOT set the mirror header — header presence
// must stay coupled to body-tag injection (malformed JSON + non-array tags).
func TestInjectEnvironmentTag_TG15_HeaderAbsentOnFailOpen(t *testing.T) {
	cases := map[string]string{
		"malformed":      `{not json`,
		"tags_not_array": `{"metadata":{"tags":"oldformat"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := newJSONRequest(t, body)
			if err := InjectEnvironmentTag(req, "demo"); err == nil {
				t.Fatal("expected fail-open error")
			}
			if got := req.Header.Get(headerTags); got != "" {
				t.Errorf("%s = %q; want empty on fail-open", headerTags, got)
			}
		})
	}
}

// TG16: empty environmentName → no header (mirrors errEmptyEnvironment).
func TestInjectEnvironmentTag_TG16_HeaderAbsentOnEmptyEnv(t *testing.T) {
	req := newJSONRequest(t, `{}`)
	if err := InjectEnvironmentTag(req, ""); !errors.Is(err, errEmptyEnvironment) {
		t.Fatalf("err = %v; want errEmptyEnvironment", err)
	}
	if got := req.Header.Get(headerTags); got != "" {
		t.Errorf("%s = %q; want empty", headerTags, got)
	}
}
