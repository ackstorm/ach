// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"net/http"
	"strings"
)

// IsDuplicateUserErr reports whether err is LiteLLM's "user_id already
// exists" response to POST /user/new.
//
// Signature captured against prod LiteLLM v1.83 on 2026-06-04 (recreating an
// existing user_id): HTTP 409 with an envelope body
//
//	{"error":{"message":"User with id <id> already exists",...,"code":"409"}}
//
// rendered by the client as the typed *APIError{StatusCode:409,
// Path:"/user/new"}. Detection prefers the typed error (errors.As) — matching
// status 409 on the /user/new path, with the body's "already exists" phrase as
// a secondary signal — and falls back to a string match for wrapped errors.
// This makes the duplicate-create recovery robust to the §9.1 wrapper that
// keeps the body out of Error().
func IsDuplicateUserErr(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusConflict && strings.Contains(apiErr.Path, "/user/new") {
			return true
		}
		if strings.Contains(string(apiErr.Body), "already exists") {
			return true
		}
	}
	// Fallback for non-typed wraps: the Error() string carries the status +
	// path even though the body is stripped (§9.1).
	s := err.Error()
	return strings.Contains(s, "/user/new") &&
		(strings.Contains(s, "409") || strings.Contains(s, "already exists"))
}
