// SPDX-License-Identifier: Apache-2.0

package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// paginate drains a limit+1 "peek" query into a page of at most `limit`
// rows + an opaque nextCursor. scanFn scans one row; cursorOf extracts the
// (createdAt, keyID) cursor key from a scanned row. When the +1 peek row is
// present the page is trimmed to `limit` and the cursor is taken from the
// last RETURNED row (out[limit-1]) — matching the hand-rolled list tails.
func paginate[T any](
	rows pgx.Rows, limit int,
	scanFn func(pgx.Rows) (T, error),
	cursorOf func(T) (time.Time, string),
) ([]T, string, error) {
	defer rows.Close()
	out := make([]T, 0, limit)
	for rows.Next() {
		v, err := scanFn(rows)
		if err != nil {
			return nil, "", fmt.Errorf("db: paginate scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("db: paginate iterate: %w", err)
	}
	var nextCursor string
	if len(out) > limit {
		last := out[limit-1]
		ts, id := cursorOf(last)
		nextCursor = encodeCursor(ts, id)
		out = out[:limit]
	}
	return out, nextCursor, nil
}
