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
// shared by /v1, /gemini, /mcp/{name}, /a2a/{name}.
const (
	prefixXLiteLLM = "x-litellm-"
	prefixXAch     = "x-ach-"
)

// hopByHop is the RFC 7230 §6.1 static list of hop-by-hop headers. They are
// stripped on every route per D-06 regardless of whether the client also
// named them in a Connection token list. Every entry is the canonical-case
// form returned by textproto.CanonicalMIMEHeaderKey so it matches the keys
// http.Header stores internally.
var hopByHop = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// authHeader is the canonical-case form of the client Authorization header
// the forwarder unconditionally strips per D-06 (T-04-01-03 mitigation).
const authHeader = "Authorization"

// StripAndRewrite mutates h in place per the D-06 (strip) + D-07 (write)
// contract:
//
//  1. Read the incoming Connection token list (every entry of h.Values("Connection"),
//     split on ",", trim whitespace, skip empties). Each token is canonicalized
//     with textproto.CanonicalMIMEHeaderKey so the resulting set matches the
//     canonical-case keys http.Header stores.
//  2. STRIP PASS — iterate h and delete every key matching any of:
//     - canonical "Authorization"
//     - case-insensitive prefix "x-litellm-"
//     - case-insensitive prefix "x-ach-"
//     - canonical-case form is in the static hopByHop list
//     - canonical-case form is in the Connection-named set
//  3. WRITE PASS — h.Set("x-litellm-api-key", sharedKey) and
//     h.Set("x-litellm-key-id", litellmToken). http.Header.Set canonicalizes
//     to "X-Litellm-Api-Key" / "X-Litellm-Key-Id".
//
// The function NEVER writes Authorization — JWT attach for /mcp + /a2a is the
// per-route handler's job AFTER this generic transform runs.
//
// Pure: no I/O, no logging, no panics on adversarial Connection token shapes
// (empty value, whitespace-only, comma-only, multi-value entries).
func StripAndRewrite(h http.Header, sharedKey, litellmToken string) {
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

	// hopByHopSet is computed once per call as a set for O(1) lookup.
	hopSet := make(map[string]struct{}, len(hopByHop))
	for _, k := range hopByHop {
		hopSet[k] = struct{}{}
	}

	// 2. Strip pass.
	for k := range h {
		if k == authHeader {
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
		if _, ok := hopSet[k]; ok {
			delete(h, k)
			continue
		}
		if _, ok := connNamed[k]; ok {
			delete(h, k)
			continue
		}
	}

	// 3. Write pass. h.Set canonicalizes the keys.
	h.Set("x-litellm-api-key", sharedKey)
	h.Set("x-litellm-key-id", litellmToken)
}
