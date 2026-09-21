// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"net/http"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// page is the paginated list envelope every platform-api list endpoint
// returns. A JSON null next_cursor decodes to "" (encoding/json leaves a
// string untouched on null), which is the loop-exit signal.
type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// fetchAll follows next_cursor from start until it comes back empty and
// returns every item. pathFor builds the request path for a cursor.
func fetchAll[T any](
	ctx context.Context, hc *httpclient.Client, start string, pathFor func(cursor string) string,
) ([]T, error) {
	all := make([]T, 0)
	cursor := start
	for {
		var resp page[T]
		if err := hc.Do(ctx, http.MethodGet, pathFor(cursor), nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if resp.NextCursor == "" {
			return all, nil
		}
		cursor = resp.NextCursor
	}
}
