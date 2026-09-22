// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// tagsHeader is LiteLLM's per-request tag slot. Measured 2026-09-22: a tag
// presented here is budget-enforced on /v1, /gemini (both the passthrough
// and the OpenAI-compat v1beta shape) and /mcp — which is why ACH stamps
// tags in ONE place (the Director) instead of mutating JSON bodies. It
// replaces FWD-06's metadata.tags injection wholesale: never both at once,
// or a tag would be counted twice.
const tagsHeader = "X-Litellm-Tags"

// headerTags mirrors the stamped tags into a backend-observable header so
// the SC2 e2e can assert at the LLM backend that traffic was attributed.
// LiteLLM consumes tagsHeader itself and never forwards it.
//
// The name deliberately does NOT use the "x-ach-" prefix: the forwarder's
// own stripAndRewrite drops every "x-ach-*" header from the upstream
// request. "x-achtest-" sits outside that prefix yet stays "x-*", which is
// what LiteLLM's forward_client_headers_to_llm_api forwards to the backend.
// In production LiteLLM does not enable that setting, so the header is
// dropped before any real backend; only the test cluster forwards it.
const headerTags = "X-Achtest-Tags"

// TagsForContext returns the LiteLLM budget tags for the caller's ACH
// identity, in order: the owner's tag (caps pk_ AND every ek_ they own,
// across Environments), the Environment's tag (caps that Environment's
// pooled traffic), and the key's own tag (caps this one ek_). LiteLLM
// enforces each independently and blocks on the first one over budget.
// No ACH identity (passthrough credential, unresolved bearer) ⇒ no tags.
func TagsForContext(ctx context.Context) []string {
	kc, ok := middleware.KeyContextFromCtx(ctx)
	if !ok {
		return nil
	}
	var tags []string
	if kc.OwnerEmail != "" {
		tags = append(tags, litellm.UserBudgetTag(kc.OwnerEmail))
	}
	if kc.KeyType == keys.PrefixEk {
		if kc.Environment != "" {
			tags = append(tags, litellm.EnvironmentBudgetTag(kc.Environment))
		}
		if kc.KeyID != "" {
			tags = append(tags, litellm.KeyBudgetTag(kc.KeyID))
		}
	}
	return tags
}

// stampTags writes the tag header (and its test mirror) onto the upstream
// request. Fail-open by construction: with no tags it stamps none.
//
// It DELETES both headers first, unconditionally. They are ACH's own
// control plane — the budget ceilings the whole feature enforces are keyed
// off them — and stripAndRewrite forwards unknown x-litellm-* headers as
// they came. Without the delete, a caller with no ACH identity (a
// passthrough credential slot, or a raw LiteLLM key in Authorization) could
// present `x-litellm-tags: user:victim@corp` and book their spend against
// someone else's ceiling, pushing that ceiling over budget. A client never
// chooses its own attribution.
func stampTags(req *http.Request) {
	req.Header.Del(tagsHeader)
	req.Header.Del(headerTags)
	tags := TagsForContext(req.Context())
	if len(tags) == 0 {
		return
	}
	joined := strings.Join(tags, ",")
	req.Header.Set(tagsHeader, joined)
	req.Header.Set(headerTags, joined)
}
