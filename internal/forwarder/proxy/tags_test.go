// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

func ctxWith(kc middleware.KeyContext) context.Context { return ctxWithKeyAndJWT(kc, "") }

// TestTagsForContextEk — an ek_ carries all three tags, owner first.
func TestTagsForContextEk(t *testing.T) {
	got := TagsForContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixEk, OwnerEmail: "Pepe@Example.com",
		Environment: "demo", KeyID: "ek_123",
	}))
	want := []string{"user:pepe@example.com", "environment:demo", "key:ek_123"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestTagsForContextPk — a pk_ has no Environment and no per-key budget:
// only the user tag, which is what makes one ceiling cover pk_ + ek_.
func TestTagsForContextPk(t *testing.T) {
	got := TagsForContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixPk, OwnerEmail: "pepe@example.com", KeyID: "pk_9",
	}))
	if len(got) != 1 || got[0] != "user:pepe@example.com" {
		t.Fatalf("got %v, want [user:pepe@example.com]", got)
	}
}

// TestTagsForContextAnonymous — passthrough / unresolved credentials carry
// no ACH identity, so nothing is stamped and no ACH budget applies.
func TestTagsForContextAnonymous(t *testing.T) {
	if got := TagsForContext(context.Background()); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// TestTagsForContextOwnerlessEk — a row with no owner email still gets its
// environment and key tags (never drop a governance tag because one field
// is blank).
func TestTagsForContextOwnerlessEk(t *testing.T) {
	got := TagsForContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixEk, Environment: "demo", KeyID: "ek_1",
	}))
	if strings.Join(got, ",") != "environment:demo,key:ek_1" {
		t.Fatalf("got %v", got)
	}
}

// TestDirectorStampsTagHeader — every family gets x-litellm-tags from the
// KeyContext; the X-Achtest-Tags mirror keeps the SC2 backend assertion.
// The Director is the single stamping point, which is what puts the tags on
// /mcp, /a2a and /v2/model/info — families the old body injection never saw.
func TestDirectorStampsTagHeader(t *testing.T) {
	const want = "user:pepe@example.com,environment:demo,key:ek_1"
	kc := middleware.KeyContext{
		KeyType: keys.PrefixEk, OwnerEmail: "pepe@example.com",
		Environment: "demo", KeyID: "ek_1",
	}
	for _, path := range []string{"/v1/chat/completions", "/gemini/v1beta/models/m:generateContent",
		"/mcp/demo-mcp-nojwt", "/a2a/demo-agent", "/v2/model/info"} {
		t.Run(path, func(t *testing.T) {
			rp := New(Deps{LiteLLMUpstream: mustParseURL(t, "http://litellm.svc:4000"), Logger: nil})
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(ctxWith(kc))

			rp.Director(req)

			if got := req.Header.Get("x-litellm-tags"); got != want {
				t.Fatalf("x-litellm-tags = %q, want %q", got, want)
			}
			if got := req.Header.Get("X-Achtest-Tags"); got != want {
				t.Fatalf("X-Achtest-Tags mirror = %q, want %q", got, want)
			}
		})
	}
}

// TestDirectorStampsNoTagHeaderWithoutIdentity — a passthrough credential
// (raw LiteLLM key, unresolved bearer) has no ACH identity: no tags, and no
// header at all rather than an empty one. The caller's OWN tag headers are
// dropped: they are ACH's control plane, so a client can never attribute its
// spend to someone else's ceiling.
func TestDirectorStampsNoTagHeaderWithoutIdentity(t *testing.T) {
	rp := New(Deps{LiteLLMUpstream: mustParseURL(t, "http://litellm.svc:4000"), Logger: nil})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("X-Litellm-Tags", "user:victim@corp,environment:prod")
	req.Header.Set("X-Achtest-Tags", "user:victim@corp")

	rp.Director(req)

	for _, h := range []string{"X-Litellm-Tags", "X-Achtest-Tags"} {
		if _, ok := req.Header[h]; ok {
			t.Fatalf("%s must be absent, got %q", h, req.Header.Get(h))
		}
	}
}

// TestDirectorOverwritesSpoofedTagHeader — an identified caller does not get
// to append to, or keep any of, the tags ACH computes for them.
func TestDirectorOverwritesSpoofedTagHeader(t *testing.T) {
	rp := New(Deps{LiteLLMUpstream: mustParseURL(t, "http://litellm.svc:4000"), Logger: nil})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("X-Litellm-Tags", "user:victim@corp")
	req = req.WithContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixPk, OwnerEmail: "pepe@example.com", KeyID: "pk_9",
	}))

	rp.Director(req)

	if got := req.Header.Values("x-litellm-tags"); len(got) != 1 || got[0] != "user:pepe@example.com" {
		t.Fatalf("x-litellm-tags = %v, want exactly [user:pepe@example.com]", got)
	}
}

// TestDirectorLeavesBodyUnmodified — tags ride the header now; the request
// body is forwarded byte-for-byte (the 1 MiB JSON rewrite is gone).
func TestDirectorLeavesBodyUnmodified(t *testing.T) {
	const body = `{"model":"demo-model","metadata":{"tags":["mine"]}}`
	rp := New(Deps{LiteLLMUpstream: mustParseURL(t, "http://litellm.svc:4000"), Logger: nil})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxWith(middleware.KeyContext{
		KeyType: keys.PrefixEk, OwnerEmail: "p@e.com", Environment: "demo", KeyID: "ek_1",
	}))

	rp.Director(req)

	got := make([]byte, len(body)+16)
	n, _ := req.Body.Read(got)
	if string(got[:n]) != body {
		t.Fatalf("body = %q, want it untouched", got[:n])
	}
}
