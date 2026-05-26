// SPDX-License-Identifier: Apache-2.0

package platformapi

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/db"
)

// envkeysDBAdapter wires the internal/db package helpers behind the
// envkeys.dbOps structural interface. The interface is unexported on
// the envkeys package side but structural — Go's assignability lets
// this concrete type satisfy it without an explicit declaration.
type envkeysDBAdapter struct {
	pool *pgxpool.Pool
}

func newEnvkeysDB(pool *pgxpool.Pool) *envkeysDBAdapter {
	return &envkeysDBAdapter{pool: pool}
}

func (a *envkeysDBAdapter) InsertEnvironmentKey(ctx context.Context, row db.EkInsertRow) error {
	return db.InsertEnvironmentKey(ctx, a.pool, row)
}

func (a *envkeysDBAdapter) GetEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
	return db.GetEnvironmentKey(ctx, a.pool, keyID)
}

func (a *envkeysDBAdapter) RevokeEnvironmentKey(ctx context.Context, keyID string) (*db.EkKeyInfo, error) {
	return db.RevokeEnvironmentKey(ctx, a.pool, keyID)
}

func (a *envkeysDBAdapter) ListEnvironmentKeysByOwner(
	ctx context.Context, ownerEmail string, limit int, cursor string,
) ([]db.EkKeyInfo, string, error) {
	return db.ListEnvironmentKeysByOwner(ctx, a.pool, ownerEmail, limit, cursor)
}

func (a *envkeysDBAdapter) ListEnvironmentKeysByOwnerWithFilter(
	ctx context.Context, ownerEmailFilter *string, limit int, cursor string,
) ([]db.EkKeyInfo, string, error) {
	return db.ListEnvironmentKeysByOwnerWithFilter(ctx, a.pool, ownerEmailFilter, limit, cursor)
}

// redisDelAdapter wraps *redis.Client.Del to satisfy the envkeys.redisOps
// interface (`Del(ctx, key) error`). The wrapper translates the
// IntCmd's .Err() into a plain error for the handler's best-effort
// invalidation path.
type redisDelAdapter struct {
	client *redis.Client
}

func newRedisDelAdapter(c *redis.Client) *redisDelAdapter {
	return &redisDelAdapter{client: c}
}

func (a *redisDelAdapter) Del(ctx context.Context, key string) error {
	if a.client == nil {
		return errors.New("redisDelAdapter: nil client")
	}
	return a.client.Del(ctx, key).Err()
}
