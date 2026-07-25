// SPDX-License-Identifier: Apache-2.0

package headers

import (
	"net/http"
	"net/textproto"
	"strings"
)

// Prefix constants for the D-06 case-insensitive strip pass. Both prefixes
// are matched against strings.ToLower(key) of every key in the incoming
// http.Header. Keys whose lowercased form starts with either prefix are
// dropped before the D-07 write pass.
//
// Hub spec §5.1 + FWD-04 mandate the strip on EVERY route — the function is
// shared by /v1, /v2, /gemini, /mcp/{name}, /a2a/{name}.
const (
	prefixXLiteLLM = "x-litellm-"
	prefixXAch     = "x-ach-"
)

// hopByHopSet is the RFC 7230 §6.1 static hop-by-hop header set, built
// once at package init for O(1) per-request lookup (D-06). They are
// stripped on every route regardless of whether the client also named
// them in a Connection token list. Keys are the canonical-case form
// (textproto.CanonicalMIMEHeaderKey) http.Header stores internally.
var hopByHopSet = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// authHeader is the canonical-case form of the client Authorization header
// the forwarder unconditionally strips per D-06 (T-04-01-03 mitigation).
const authHeader = "Authorization"

// googAPIKeyHeader is the native-Gemini credential header (canonical case).
// Clients authenticate to the forwarder via x-ach-key, never x-goog-api-key,
// so any incoming value is stripped on every route; the proxy Director re-sets
// it with the caller's own LiteLLM virtual key for the /gemini route only.
const googAPIKeyHeader = "X-Goog-Api-Key" //nolint:gosec // G101 false positive: HTTP header name, not a credential value

// StripAndRewrite mutates h in place per the D-06 (strip) + D-07 (write)
// contract:
//
//  1. Read the incoming Connection token list (every entry of h.Values("Connection"),
//     split on ",", trim whitespace, skip empties). Each token is canonicalized
//     with textproto.CanonicalMIMEHeaderKey so the resulting set matches the
//     canonical-case keys http.Header stores.
//  2. STRIP PASS — iterate h and delete every key matching any of:
//     - canonical "Authorization"
//     - canonical "X-Goog-Api-Key" (native-Gemini credential; the /gemini
//     Director re-sets it with the caller's own key)
//     - case-insensitive prefix "x-litellm-"
//     - case-insensitive prefix "x-ach-"
//     - canonical-case form is in the static hopByHopSet
//     - canonical-case form is in the Connection-named set
//  3. WRITE PASS — h.Set("x-litellm-api-key", litellmAPIKey). http.Header.Set
//     canonicalizes to "X-Litellm-Api-Key".
//
// TESTING-PHASE (reverts FIX01 §A.6 / D-13): the value written is the CALLER's
// own LiteLLM virtual key (litellmAPIKey) — NOT the shared master key — so
// LiteLLM attributes the request 1:1 to the user. The x-litellm-key-id
// delegation header is no longer written (we authenticate as the user's own
// key). An empty litellmAPIKey writes an empty header (callers with no stored
// material — pre-migration keys — fail upstream, by design).
//
// The function NEVER writes Authorization — JWT attach for /mcp + /a2a is the
// per-route handler's job AFTER this generic transform runs.
//
// Pure: no I/O, no logging, no panics on adversarial Connection token shapes
// (empty value, whitespace-only, comma-only, multi-value entries).
func StripAndRewrite(h http.Header, litellmAPIKey string) {
	// 1. Collect Connection-named tokens into a canonical-case set BEFORE the
	//    strip pass so the iteration below can drop them. h.Values traverses
	//    every entry (h.Add may have appended multiple Connection headers).
	var connNamed map[string]struct{}
	for _, raw := range h.Values("Connection") {
		for _, tok := range strings.Split(raw, ",") {
			t := strings.TrimSpace(tok)
			if t == "" {
				continue
			}
			if connNamed == nil {
				connNamed = make(map[string]struct{})
			}
			connNamed[textproto.CanonicalMIMEHeaderKey(t)] = struct{}{}
		}
	}

	// 2. Strip pass.
	for k := range h {
		if k == authHeader {
			delete(h, k)
			continue
		}
		if k == googAPIKeyHeader {
			delete(h, k)
			continue
		}
		kl := strings.ToLower(k)
		if strings.HasPrefix(kl, prefixXLiteLLM) || strings.HasPrefix(kl, prefixXAch) {
			delete(h, k)
			continue
		}
		// k is already canonical-case (http.Header stores canonical keys);
		// textproto.CanonicalMIMEHeaderKey on a canonical key is a no-op.
		if _, ok := hopByHopSet[k]; ok {
			delete(h, k)
			continue
		}
		if _, ok := connNamed[k]; ok {
			delete(h, k)
			continue
		}
	}

	// 3. Write pass. h.Set canonicalizes the key.
	// TESTING-PHASE (reverts FIX01 §A.6 / D-13): the caller's own LiteLLM
	// virtual key is written here for ALL routes. The "Bearer " prefix that
	// LiteLLM's MCP key parser (user_api_key_auth_mcp.py) requires is applied
	// ONLY on the /mcp route by the proxy Director — /v1, /v2, /gemini, and /a2a
	// take the bare value. The x-litellm-key-id delegation header is no longer
	// written.
	h.Set("x-litellm-api-key", litellmAPIKey)
}
