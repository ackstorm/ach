// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
)

// serviceRe / familyRe extract the resource a client holds: "/mcp/<name>"
// or "/a2a/<name>" for those route families, "/v1" or "/gemini" for
// the model API families; everything else is the API as a whole. Never the
// dialled path: streamable HTTP appends /messages and session segments, and
// RFC 9728 §3.2 has the client compare the document's `resource` against
// the server it configured — the root.
//
// Both anchor the tail on "/" or end-of-string. Without that anchor serviceRe
// matched a PREFIX of a malformed name: "/mcp/foo@bar" truncated to
// "/mcp/foo", so ChallengeFor pointed a client at a DIFFERENT service's
// document while the PRM handler's resourceRoot(rest) != rest check 404'd the
// same input. The two disagreed on what a well-formed name is; now they
// cannot. A malformed name resolves to "" — the API-root document — which is
// still a mismatch the client rejects per §3.2, but an honest one.
var (
	serviceRe = regexp.MustCompile(`^/(mcp|a2a)/([A-Za-z0-9._-]+)(/|$)`)
	familyRe  = regexp.MustCompile(`^/(v1|gemini)(/|$)`)
)

func resourceRoot(path string) string {
	if m := serviceRe.FindStringSubmatch(path); m != nil {
		return "/" + m[1] + "/" + m[2]
	}
	if m := familyRe.FindStringSubmatch(path); m != nil {
		return "/" + m[1]
	}
	return ""
}

// ChallengeFor composes the WWW-Authenticate value Authn attaches to a 401:
// the RFC 9728 pointer to the protected-resource document of the service
// root the request addressed.
func ChallengeFor(base string) func(*http.Request) string {
	base = strings.TrimRight(base, "/")
	return func(r *http.Request) string {
		return `Bearer resource_metadata="` + base + wellKnownPRMSegment + resourceRoot(r.URL.Path) + `"`
	}
}

// WellKnownHandler serves the two anonymous discovery documents: RFC 8414
// (jwt.ASMetadata) and RFC 9728 for the API root and for every
// /mcp/<name> and /a2a/<name>. Anything else under the PRM segment is 404.
//
// That 404 is a SHAPE test, never an existence test: a well-formed name no
// Environment declares still gets a 200 document, ON PURPOSE. This endpoint is
// anonymous by design — a client fetches it precisely because it holds no
// credential yet — so resolving the name here would hand any unauthenticated
// caller an existence oracle over every MCP server and a2a agent in the
// deployment: probe names, 200 means configured, 404 means not. The name set
// is caller-independent (precheck.EnvProvider.List walks all Environments),
// so there is no way to scope that disclosure to the asker. This is D-15
// (precheck/errors.go) applied to the anonymous surface; the handler takes no
// resolver, so it cannot drift into answering from state.
//
// The accepted cost: a client dialling a name that does not exist completes a
// full OAuth ceremony against a real AS, gets a real token, and only then
// fails the authenticated precheck with 403. It reads like an auth bug in
// support and it is not — it is this trade, chosen deliberately. Widening the
// document to 404 on unknown names buys that back with the oracle above.
func WellKnownHandler(base string) http.Handler {
	base = strings.TrimRight(base, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		switch {
		case r.URL.Path == "/.well-known/oauth-authorization-server":
			doc = jwt.ASMetadata(base)
		case strings.HasPrefix(r.URL.Path, wellKnownPRMSegment):
			rest := strings.TrimPrefix(r.URL.Path, wellKnownPRMSegment)
			if rest != "" && resourceRoot(rest) != rest {
				http.NotFound(w, r)
				return
			}
			doc = map[string]any{
				"resource":                 base + rest,
				"authorization_servers":    []string{base},
				"bearer_methods_supported": []string{"header"},
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(doc)
	})
}
