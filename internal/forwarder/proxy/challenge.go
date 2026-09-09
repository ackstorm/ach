// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// wellKnownPRMSegment is the RFC 9728 §3 path segment that marks a
// protected-resource metadata document. The rewrite below fires ONLY on a
// pointer whose path carries it — the client FOLLOWS this URL, so a broader
// rewrite would be a redirect-injection primitive rather than a fix.
const wellKnownPRMSegment = "/.well-known/oauth-protected-resource"

// resourceMetadataRe matches the RFC 9728 §5.1 `resource_metadata` auth-param
// inside a WWW-Authenticate challenge, capturing the boundary before it, the
// parameter name + opening quote, the quoted value, and the closing quote.
//
// The leading boundary group ((^|[\s,])) prevents matching a parameter whose
// name merely ENDS in "resource_metadata" (Go's regexp has no lookbehind, so
// the boundary is captured and re-emitted verbatim). The value is [^"]*, so an
// unterminated quoted-string simply fails to match and is left alone.
var resourceMetadataRe = regexp.MustCompile(`(?i)(^|[\s,])(resource_metadata\s*=\s*")([^"]*)(")`)

// parsePublicBase parses ACH_BASE_URL into the scheme+authority the challenge
// rewrite stamps onto outbound pointers. Returns nil when the value is empty or
// unusable, which disables the rewrite entirely (ModifyResponse stays nil, and
// the proxy behaves exactly as it did before this existed).
func parsePublicBase(raw string) *url.URL {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}

// rewriteChallengeValue rewrites the `resource_metadata` pointer inside one
// WWW-Authenticate value so it names ACH instead of whatever upstream composed
// it. Every other auth-param — error, error_description, scope, realm — and all
// surrounding whitespace survive byte for byte, because only the quoted value
// itself is substituted.
//
// The value is left untouched when the parameter is absent, when the value does
// not parse as a URL, or when its path is not an RFC 9728 metadata path.
func rewriteChallengeValue(value string, publicBase *url.URL) string {
	return resourceMetadataRe.ReplaceAllStringFunc(value, func(match string) string {
		groups := resourceMetadataRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		boundary, prefix, raw, suffix := groups[1], groups[2], groups[3], groups[4]

		u, err := url.Parse(raw)
		if err != nil || !strings.Contains(u.Path, wellKnownPRMSegment) {
			return match
		}
		// Authority only. The path tail names the backend the document
		// describes and is upstream's to choose; only the front door is wrong.
		u.Scheme = publicBase.Scheme
		u.Host = publicBase.Host
		return boundary + prefix + u.String() + suffix
	})
}

// resourcePathFor reduces a forwarded path to the RFC 9728 resource identifier
// it belongs to — "/mcp/{name}" or "/a2a/{name}" — or "" when the path names no
// such resource. The tail beyond {name} is dropped: the resource is the server,
// not the sub-path a particular call used.
func resourcePathFor(path string) string {
	for _, prefix := range []string{"/mcp/", "/a2a/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		name := strings.TrimPrefix(path, prefix)
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return ""
		}
		return prefix + name
	}
	return ""
}

// hasResourceMetadata reports whether any challenge value already carries the
// resource_metadata auth-param.
func hasResourceMetadata(values []string) bool {
	for _, v := range values {
		if resourceMetadataRe.MatchString(v) {
			return true
		}
	}
	return false
}

// metadataURLFor builds the pointer ACH advertises for a resource path — the
// same document the forwarder itself serves at that path (see server.go).
// RFC 9728 §3 puts the well-known segment BETWEEN authority and resource path,
// so this is a sibling of the resource, not a child.
func metadataURLFor(publicBase *url.URL, resourcePath string) string {
	u := *publicBase
	u.Path = wellKnownPRMSegment + resourcePath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// insertResourceMetadata appends the pointer to the first Bearer challenge in
// values, or — when there is no challenge at all on a 401 — synthesizes a
// minimal one. Returns the new values and whether anything changed.
//
// Only Bearer is touched: appending an OAuth auth-param to a Basic challenge
// would be meaningless.
func insertResourceMetadata(values []string, status int, pointer string) ([]string, bool) {
	param := `resource_metadata="` + pointer + `"`
	for i, v := range values {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "bearer") {
			continue
		}
		sep := ", "
		if strings.EqualFold(strings.TrimSpace(v), "Bearer") {
			sep = " " // bare "Bearer" scheme with no auth-params yet
		}
		out := append([]string{}, values...)
		out[i] = strings.TrimRight(v, " ,") + sep + param
		return out, true
	}
	// No Bearer challenge at all. RFC 7235 requires one on a 401, so supply
	// the minimum that carries the pointer. A 403 needs no challenge, so an
	// upstream that sent none keeps none.
	if status == http.StatusUnauthorized && len(values) == 0 {
		return []string{"Bearer " + param}, true
	}
	return values, false
}

// rewriteChallengeHost points a 401/403 auth challenge at ACH's own public base
// URL (issue #177).
//
// WHY THIS IS NEEDED AT ALL: on the /mcp path LiteLLM composes the challenge
// itself rather than relaying the backend's. Its pre-session probe loop
// (proxy/_experimental/mcp_server/server.py) discards the upstream's
// WWW-Authenticate and synthesizes a replacement from get_request_base_url —
// which returns PROXY_BASE_URL verbatim, BEFORE any X-Forwarded-* handling, so
// on any deployment that sets it (needed for the admin UI, OAuth callbacks and
// spend links) the forwarded public hostname is unreachable code. A backend
// that correctly selects its own `resource` therefore has no way to reach the
// client either: its header is replaced. Measured on LiteLLM v1.99.1.
//
// The forwarder is the last hop that knows which front door the client dialled,
// and it knows its own public name without trusting a header: ACH_BASE_URL,
// validated http(s):// at process start and already used as the JWT "iss".
//
// Two branches converge on ONE invariant: a challenge leaving ACH names an ACH
// document, always — whether upstream sent the wrong pointer, or none at all.
//
//  1. REWRITE — a resource_metadata pointer is present: swap its scheme and
//     authority for ACH's, keep the path.
//  2. INSERT — no pointer is present, and the route is /mcp/{name} or
//     /a2a/{name}: append one naming the document the forwarder itself serves.
//
// Branch 2 exists because branch 1 alone silently no-ops the day LiteLLM stops
// discarding the backend's challenge. A backend that cannot learn its front
// door correctly OMITS the pointer rather than advertising one a client will
// reject under RFC 9728 §3.2 — at which point ACH would pass a bare challenge
// through and leave the client to derive the metadata URL from the resource it
// dialled (§3.1). That derivation is legal and some clients do it, but it is
// not something to depend on, and the failure mode is the same silent one.
//
// Restricted on purpose to 401/403 responses, to the resource_metadata
// parameter, and to pointers whose path is an RFC 9728 metadata path.
func rewriteChallengeHost(resp *http.Response, publicBase *url.URL) {
	if publicBase == nil || resp == nil {
		return
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return
	}
	values := resp.Header.Values("Www-Authenticate")

	var out []string
	changed := false

	if hasResourceMetadata(values) {
		out = make([]string, 0, len(values))
		for _, v := range values {
			rewritten := rewriteChallengeValue(v, publicBase)
			if rewritten != v {
				changed = true
			}
			out = append(out, rewritten)
		}
	} else {
		// Insert branch. The route path comes from the OUTBOUND request, which
		// Director has already normalized to the bare "/mcp/<server>" form.
		if resp.Request == nil || resp.Request.URL == nil {
			return
		}
		resourcePath := resourcePathFor(resp.Request.URL.Path)
		if resourcePath == "" {
			return
		}
		out, changed = insertResourceMetadata(values, resp.StatusCode, metadataURLFor(publicBase, resourcePath))
	}

	if !changed {
		return
	}
	resp.Header.Del("Www-Authenticate")
	for _, v := range out {
		resp.Header.Add("Www-Authenticate", v)
	}
}
