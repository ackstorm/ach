// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"strings"
)

// IsDuplicateTeamErr reports whether err is LiteLLM's "Team id = <id> already
// exists" response to POST /team/new. Measured: HTTP 400 (NOT 409 like
// /user/new) with the phrase "already exists" in the body. Because ACH sets
// team_id == team_alias, a duplicate means the shell already exists with the
// id the caller already knows, so callers treat this as success.
func IsDuplicateTeamErr(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if strings.Contains(apiErr.Path, "/team/new") &&
			strings.Contains(string(apiErr.Body), "already exists") {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "/team/new") && strings.Contains(s, "already exists")
}
