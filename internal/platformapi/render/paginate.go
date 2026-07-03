// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// PageParams parses the shared ?limit (default 100, cap 500) + ?cursor
// (opaque base64-of-decimal offset) pagination query parameters. On a bad
// parameter it writes the 400 invalid_argument envelope itself and returns
// ok=false. Shared by objects, environments, and admin inventory so the
// CLI cursor loop is uniform across endpoints.
func PageParams(w http.ResponseWriter, r *http.Request, reqID string) (limit, offset int, ok bool) {
	const defaultLimit, maxLimit = 100, 500
	limit = defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxLimit {
			Error(w, http.StatusBadRequest, "invalid_argument",
				"limit must be a positive integer no greater than "+strconv.Itoa(maxLimit), reqID)
			return 0, 0, false
		}
		limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid_argument",
				"cursor must be a valid base64-encoded value", reqID)
			return 0, 0, false
		}
		n, err := strconv.Atoi(string(decoded))
		if err != nil || n < 0 {
			Error(w, http.StatusBadRequest, "invalid_argument",
				"cursor must decode to a non-negative integer", reqID)
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

// PageWindow clamps [offset, offset+limit) to len(items) and returns the
// page plus the next_cursor value — base64 of the next offset, or nil when
// the page reached the end of the slice.
func PageWindow[T any](items []T, offset, limit int) (page []T, nextCursor any) {
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if end < total {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return items[offset:end], nextCursor
}
