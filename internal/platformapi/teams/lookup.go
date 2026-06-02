// SPDX-License-Identifier: Apache-2.0

package teams

import (
	"context"
	"errors"
	"strings"

	"github.com/ackstorm/ach/internal/litellm"
)

// LookupCallerTeams returns the LiteLLM-side team memberships for the
// caller identified by their owner email. Phase 3 calls LiteLLM on every
// request; Phase 4 will replace this with a Redis-cached variant
// (60s TTL, same cache the keystore.Resolver uses).
//
// Semantics:
//
//   - LiteLLM returns 404 (errors.Is(err, litellm.ErrNotFound) OR the
//     wrapped error string contains "404"): caller has no LiteLLM user
//     yet → return (empty slice, nil). Downstream team-intersection
//     treats this as zero intersection → 403 unauthorized_team for
//     protected paths.
//   - LiteLLM returns transport / 5xx error: return (nil, err). The
//     handler emits 503 litellm_unreachable.
//   - LiteLLM returns 200 with `teams` array: return ([...team names],
//     nil). A nil or empty `teams` field is normalized to a zero-length
//     slice so downstream `HasIntersect` consumers always see a real
//     slice.
//
// The dual-branch 404 detection (errors.Is + string-match) reflects
// Plan 03-01's UserInfoByEmail design decision: that helper does NOT
// translate 404 → ErrNotFound at the type level (kept the makeRequest
// 4xx convention narrow). Phase 4 Forwarder may tighten this contract.
func LookupCallerTeams(ctx context.Context, ll litellm.Client, email string) ([]string, error) {
	info, err := ll.UserInfoByEmail(ctx, email)
	if err != nil {
		if isNotFound(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if info == nil || len(info.Teams) == 0 {
		return []string{}, nil
	}
	return info.Teams, nil
}

// isNotFound reports whether err represents a LiteLLM 404 — either the
// typed sentinel (errors.Is path; Phase 4 may move all helpers to this)
// or the legacy makeRequest 4xx wrapper that surfaces "404" in its
// message body (Phase 3 D-25 UserInfoByEmail contract).
func isNotFound(err error) bool {
	if errors.Is(err, litellm.ErrNotFound) {
		return true
	}
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "404")
}
