// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyListItem is the secret-free projection of one key (pk_ or ek_) for listing.
type KeyListItem struct {
	KeyID       string
	Type        string // "pk" | "ek"
	OwnerEmail  string
	Environment *string // nil for pk
	Name        *string // nil for pk
	Status      string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// KeyListFilter narrows ListKeys. Zero-value fields mean "no filter".
type KeyListFilter struct {
	OwnerEmail  *string // nil = all owners (admin scope)
	Type        string  // "" | "pk" | "ek"
	Status      string  // "" | "active" | "revoked" | "expired"
	Environment string  // "" = all (ek rows only)
}

// ListKeys returns a single cursor-paginated, created_at DESC / key_id DESC
// stream merging personal_keys and environment_keys via UNION ALL. The cursor
// encoding mirrors listEnvironmentKeys (decodeCursor/encodeCursor/clampLimit).
func ListKeys(ctx context.Context, pool *pgxpool.Pool, f KeyListFilter, limit int, cursor string) ([]KeyListItem, string, error) {
	limit = clampLimit(limit)
	cursorTs, cursorID, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}

	// Convert zero time.Time to *time.Time nil so postgres sees NULL for the
	// cursor branch ($5::timestamptz IS NULL skips the row-position filter).
	var cursorTsPtr *time.Time
	if !cursorTs.IsZero() {
		cursorTsPtr = &cursorTs
	}

	// $1 owner filter (nil=all), $2 type filter, $3 status filter,
	// $4 environment filter, $5 cursor ts (nil=no cursor), $6 cursor id, $7 limit+1.
	//
	// pk_ expiry is enforced ONLY by the PkCheckAndExtend auth predicate — nothing
	// ever writes status='expired' back to the column — so an expired pk_ still
	// reads status='active' on disk. Derive it here against the SAME predicate
	// (expires_at AND the created_at + 90d hard cap) so the listing never reports
	// a dead key as active and so ?status=expired is not a dead filter.
	//
	// The expiry DATE is deliberately not projected: the window slides on every
	// use, so any date shown to a user is stale the moment they read it. Liveness
	// is the only honest thing to report. environment_keys have no expires_at
	// column at all — an ek_ is perpetual, hence never 'expired'.
	const q = `
WITH combined AS (
    SELECT key_id, 'pk'::text AS key_type, owner_email,
           NULL::text AS environment, NULL::text AS name,
           CASE WHEN status = 'active'
                 AND LEAST(expires_at, created_at + interval '90 days') <= now()
                THEN 'expired' ELSE status END AS status,
           created_at, last_used_at, revoked_at
    FROM personal_keys
    UNION ALL
    SELECT key_id, 'ek'::text AS key_type, owner_email,
           environment, name,
           status, created_at, last_used_at, revoked_at
    FROM environment_keys
)
SELECT key_id, key_type, owner_email, environment, name,
       status, created_at, last_used_at, revoked_at
FROM combined
WHERE ($1::text IS NULL OR owner_email = $1)
  AND ($2::text = ''   OR key_type   = $2)
  AND ($3::text = ''   OR status     = $3)
  AND ($4::text = ''   OR environment = $4)
  AND ($5::timestamptz IS NULL OR (created_at, key_id) < ($5, $6))
ORDER BY created_at DESC, key_id DESC
LIMIT $7`

	rows, err := pool.Query(ctx, q,
		f.OwnerEmail, f.Type, f.Status, f.Environment,
		cursorTsPtr, cursorID, limit+1,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var items []KeyListItem
	for rows.Next() {
		var it KeyListItem
		if err := rows.Scan(
			&it.KeyID, &it.Type, &it.OwnerEmail, &it.Environment, &it.Name,
			&it.Status, &it.CreatedAt, &it.LastUsedAt, &it.RevokedAt,
		); err != nil {
			return nil, "", err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.KeyID)
		items = items[:limit]
	}
	return items, next, nil
}
