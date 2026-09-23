// SPDX-License-Identifier: Apache-2.0

package precheck

import "errors"

// Typed sentinel errors returned by CheckMCP / CheckA2A. The caller
// (Plan 04-07 per-route handlers) maps these to HTTP outcomes per
// FWD-03 / Hub §15.5:
//
//	ErrInvalidKeyType        → 401 invalid_key_type
//	ErrUnauthorizedResource  → 403 unauthorized_resource
//	ErrUnauthorizedTeam      → 403 unauthorized_team
//	ErrLiteLLMUnreachable    → 503 litellm_unreachable
//
// Missing-environment is deliberately narrowed to ErrUnauthorizedResource
// (D-15) — no separate not-found sentinel. Absent and unauthorized must stay
// indistinguishable to the caller: a distinguishable pair is an existence
// oracle over every name configured in the deployment.
//
// The same rule governs the ANONYMOUS discovery surface. The RFC 9728
// protected-resource document (proxy.WellKnownHandler) answers on the SHAPE of
// the requested path and never on existence, so it discloses nothing either —
// see the comment there for the cost that choice accepts.
var (
	ErrInvalidKeyType       = errors.New("precheck: invalid or missing key type")
	ErrUnauthorizedResource = errors.New("precheck: unauthorized resource (name not in bound environment)")
	ErrUnauthorizedTeam     = errors.New("precheck: unauthorized team (no environment grants caller access to this name)")
	ErrLiteLLMUnreachable   = errors.New("precheck: litellm unreachable during teams resolve")
)
