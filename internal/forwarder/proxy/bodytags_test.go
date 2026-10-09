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
		{"metadata empty string", "application/json", `{"metadata":""}`, ""},
		{"metadata number", "application/json", `{"metadata":3}`, ""},
		{"chat body", "application/json", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`, ""},
		{"string metadata with tags", "application/json", `{"metadata":"{\"tags\":[\"user:v\"],\"t\":1}"}`, `{"metadata":{"t":1}}`},
		{"string litellm_metadata with tags", "application/json", `{"litellm_metadata":"{\"tags\":[\"x\"]}"}`, `{"litellm_metadata":{}}`},
		{"text/plain tagged", "text/plain", `{"model":"m","tags":["user:v"]}`, `{"model":"m"}`},
		{"missing content type tagged", "", `{"model":"m","tags":["user:v"]}`, `{"model":"m"}`},
		{"protobuf type tagged", "application/x-protobuf", `{"model":"m","tags":["user:v"]}`, `{"model":"m"}`},
		{"empty body", "application/json", ``, ""},
		{"whitespace body", "", " \n", ""},
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

func TestStripBodyTagsRejects(t *testing.T) {
	const b = "XBOUND"
	mp := func(field string) string {
		return "--" + b + "\r\nContent-Disposition: form-data; name=\"" + field + "\"\r\n\r\nv\r\n--" + b + "--\r\n"
	}
	mpRaw := func(cd string) string {
		return "--" + b + "\r\nContent-Disposition: " + cd + "\r\n\r\nv\r\n--" + b + "--\r\n"
	}
	cases := []struct {
		name, ct, in string
		want         error
	}{
		{"NaN body", "application/json", `{"temperature":NaN,"tags":["user:v"]}`, errInvalidBody},
		{"NaN body no content type", "", `{"temperature":NaN,"tags":["user:v"]}`, errInvalidBody},
		{"urlencoded metadata", "application/x-www-form-urlencoded", "model=m&metadata=%7B%7D", errClientTags},
		{"multipart tags", "multipart/form-data; boundary=" + b, mp("tags"), errClientTags},
		{"multipart litellm_metadata", "multipart/form-data; boundary=" + b, mp("litellm_metadata"), errClientTags},
		{"NaN in string metadata", "application/json", `{"metadata":"{\"tags\":[\"user:v\"],\"x\":NaN}"}`, errInvalidBody},
		{"plain string metadata", "application/json", `{"metadata":"s"}`, errInvalidBody},
		{"urlencoded dup params", "application/x-www-form-urlencoded; a=1; a=1", "metadata=%7B%7D", errClientTags},
		{"top-level array", "application/json", `[{"tags":["x"]}]`, errInvalidBody},
		{"top-level string", "", `"metadata=x"`, errInvalidBody},
		{"top-level null", "application/json", `null`, errInvalidBody},
		{"attachment disposition", "multipart/form-data; boundary=" + b, mpRaw(`attachment; name="metadata"`), errClientTags},
		{"uppercase field", "multipart/form-data; boundary=" + b, mp("Metadata"), errClientTags},
		{"duplicate Content-Disposition", "multipart/form-data; boundary=" + b,
			"--" + b + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Disposition: form-data; name=\"metadata\"\r\n\r\nv\r\n--" + b + "--\r\n", errInvalidBody},
		{"duplicate name param", "multipart/form-data; boundary=" + b, mpRaw(`form-data; name="model"; name="metadata"`), errInvalidBody},
		{"zero-part multipart", "multipart/form-data; boundary=" + b, "--" + b + "--\r\n", errInvalidBody},
		{"junk after boundary", "multipart/form-data; boundary=" + b,
			"--" + b + "\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nv\r\n--" + b + "junk", errInvalidBody},
		{"multipart without boundary", "multipart/form-data", mp("model"), errInvalidBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := stripped(t, c.ct, c.in); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}

	// A canonical audio-upload form is forwarded byte-identical.
	in := "--" + b + "\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--" + b +
		"\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\nContent-Type: audio/wav\r\n\r\nRIFF\x00\x01\r\n--" + b + "--\r\n"
	got, _, err := stripped(t, "multipart/form-data; boundary="+b, in)
	if err != nil || string(got) != in {
		t.Fatalf("err = %v, body = %q, want byte-identical", err, got)
	}
}

// Handler level: a rejected body is a 400 and is never forwarded.
func TestHandlerV1RejectsUnparseableBody(t *testing.T) {
	forwarded := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true }))
	defer upstream.Close()
	h := HandlerV1(HandlerDeps{Deps: newDepsWithUpstream(t, upstream)})
	for _, c := range []struct{ ct, body, code string }{
		{"application/json", `{"temperature":NaN,"tags":["user:v"]}`, "invalid_request"},
		{"application/x-www-form-urlencoded", "metadata=x", "client_tags_not_allowed"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(c.body))
		req.Header.Set("Content-Type", c.ct)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.code) || forwarded {
			t.Fatalf("%s: code = %d body = %s forwarded = %v", c.ct, rec.Code, rec.Body.String(), forwarded)
		}
	}
}
