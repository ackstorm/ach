// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
)

// KeyLive reports whether the key row behind an authenticated request still
// authorises traffic: (false, nil) is a definite suspended, revoked, expired
// or missing row, and an error says nothing about the key.
//
// It reads the row by id, so every identity kind answers the same way: a
// pk_, an OAuth or trusted-IdP token (their oauth pk_ row), an ek_ and an
// exchange JWT (its ek_ row). The presented credential plays no part, which
// is why a token reaching its own exp does not read as a dead key. Grant loss
// reaches an exchange key through the operator revoking its row.
type KeyLive func(ctx context.Context, keyType keys.BearerPrefix, keyID string) (bool, error)

// NewKeyLiveDB is the production KeyLive.
func NewKeyLiveDB(pool *pgxpool.Pool) KeyLive {
	return keyLive(
		func(ctx context.Context, keyID string) (*db.PkKeyInfo, error) {
			return db.GetPersonalKey(ctx, pool, keyID)
		},
		func(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
			return db.GetEnvironmentKey(ctx, pool, keyID)
		})
}

func keyLive(
	readPK func(context.Context, string) (*db.PkKeyInfo, error),
	readEK func(context.Context, string) (*db.EkKeyInfo, error),
) KeyLive {
	return func(ctx context.Context, keyType keys.BearerPrefix, keyID string) (bool, error) {
		var status string
		var expires *time.Time
		if keyType == keys.PrefixPk {
			row, err := readPK(ctx, keyID)
			if err != nil || row == nil {
				return false, err
			}
			status, expires = row.Status, &row.ExpiresAt
		} else {
			row, err := readEK(ctx, keyID)
			if err != nil || row == nil {
				return false, err
			}
			status, expires = row.Status, row.ExpiresAt
		}
		return status == "active" && (expires == nil || time.Now().Before(*expires)), nil
	}
}
