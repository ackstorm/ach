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
//	ErrEnvironmentNotFound   → reserved for a future strict variant;
//	                           precheck currently narrows missing-env
//	                           to ErrUnauthorizedResource (D-15)
var (
	ErrInvalidKeyType       = errors.New("precheck: invalid or missing key type")
	ErrUnauthorizedResource = errors.New("precheck: unauthorized resource (name not in bound environment)")
	ErrUnauthorizedTeam     = errors.New("precheck: unauthorized team (no environment grants caller access to this name)")
	ErrLiteLLMUnreachable   = errors.New("precheck: litellm unreachable during teams resolve")
	ErrEnvironmentNotFound  = errors.New("precheck: environment not found")
)
