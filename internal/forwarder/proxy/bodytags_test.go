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

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

func stripped(t *testing.T, ct, body string) ([]byte, *http.Request, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	err := stripBodyTags(r)
	got, _ := io.ReadAll(r.Body)
	return got, r, err
}

func TestStripBodyTags(t *testing.T) {
	cases := []struct {
		name, ct, in, want string // want "" = byte-identical
	}{
		{"top-level tags", "application/json", `{"model":"m","tags":["user:v"]}`, `{"model":"m"}`},
		{"metadata tags", "application/json; charset=utf-8", `{"metadata":{"tags":["x"],"trace":"t"}}`, `{"metadata":{"trace":"t"}}`},
		{"litellm_metadata tags", "application/json", `{"litellm_metadata":{"tags":["x"]}}`, `{"litellm_metadata":{}}`},
		{"no tags", "application/json", `{"model":"m",  "messages": []}`, ""},
		{"metadata not an object", "application/json", `{"metadata":"s"}`, ""},
		{"invalid json", "application/json", `{"tags":`, ""},
		{"non-json content type", "multipart/form-data; boundary=x", `{"tags":["x"]}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, r, err := stripped(t, c.ct, c.in)
			if err != nil {
				t.Fatal(err)
			}
			want := c.want
			if want == "" {
				want = c.in
			}
			if c.want == "" {
				if string(got) != want {
					t.Fatalf("body = %q, want byte-identical %q", got, want)
				}
				return
			}
			var g, w any
			if json.Unmarshal(got, &g) != nil || json.Unmarshal([]byte(want), &w) != nil {
				t.Fatalf("bad json %q / %q", got, want)
			}
			if !bytes.Equal(mustMarshal(g), mustMarshal(w)) {
				t.Fatalf("body = %s, want %s", got, want)
			}
			if r.ContentLength != int64(len(got)) || r.Header.Get("Content-Length") == "" {
				t.Fatalf("ContentLength = %d, header %q, body len %d", r.ContentLength, r.Header.Get("Content-Length"), len(got))
			}
		})
	}
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }

func TestStripBodyTagsTooLarge(t *testing.T) {
	big := `{"model":"` + strings.Repeat("a", maxTagScanBody) + `"}`
	_, _, err := stripped(t, "application/json", big)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("err = %v, want errBodyTooLarge", err)
	}
}

// End to end through the /v1 handler: the upstream sees no body tags and the
// stamped header tags are intact.
func TestHandlerV1StripsBodyTags(t *testing.T) {
	var gotBody, gotHdr string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotHdr = string(b), r.Header.Get("X-Litellm-Tags")
	}))
	defer upstream.Close()

	h := HandlerV1(HandlerDeps{Deps: newDepsWithUpstream(t, upstream)})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"m","tags":["user:victim@corp.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixEk, OwnerEmail: "p@e.com", Environment: "demo", KeyID: "ek_1",
	}))
	h(httptest.NewRecorder(), req)

	if gotBody != `{"model":"m"}` {
		t.Fatalf("upstream body = %q", gotBody)
	}
	if !strings.Contains(gotHdr, "user:p@e.com") || strings.Contains(gotHdr, "victim") {
		t.Fatalf("x-litellm-tags = %q", gotHdr)
	}

	// 413 on oversize, nothing forwarded.
	gotBody = "untouched"
	big := `{"model":"` + strings.Repeat("a", maxTagScanBody) + `"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || gotBody != "untouched" {
		t.Fatalf("code = %d, forwarded = %q", rec.Code, gotBody)
	}
}
