// SPDX-License-Identifier: Apache-2.0

package headers

import (
	"net/http"
	"strings"
)

// prefixXAch: ACH's own header namespace (x-ach-key, x-ach-environment, …).
// Never leaves ACH — the credential slot is consumed by Authn, the rest is
// internal.
const prefixXAch = "x-ach-"

// StripAndRewrite prepares the outbound header set for LiteLLM:
//
//   - every x-ach-* header is dropped;
//   - x-litellm-api-key is SET to litellmAPIKey — the caller's own LiteLLM
//     virtual key (resolve) or the value they presented (passthrough). Set,
//     not Add: whatever the client put there is replaced, so this is the one
//     header a caller can never choose.
//
// Everything else passes as it came, including Authorization (Authn already
// consumed ours; anything left is the upstream provider's own credential)
// and the other x-litellm-* headers (LiteLLM enforces its own per-key
// permissions). Hop-by-hop headers are removed by httputil.ReverseProxy
// itself. The "Bearer " prefix LiteLLM's MCP key parser needs is applied on
// the /mcp route by the proxy Director; /v1, /gemini, /a2a take the
// bare value.
func StripAndRewrite(h http.Header, litellmAPIKey string) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), prefixXAch) {
			delete(h, k)
		}
	}
	h.Set("x-litellm-api-key", litellmAPIKey)
}
