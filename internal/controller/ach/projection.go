// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	achdb "github.com/ackstorm/ach/internal/db"
)

// projectRow runs upsert + NOTIFY(channel, "ns/name") in one transaction.
// Nil pool is the envtest path (no projection). achdb.ErrOriginConflict is
// returned raw so the caller can flip to Synced=False/ConflictWithUIRow;
// every other error is wrapped with the non-secret label.
func projectRow(ctx context.Context, pool *pgxpool.Pool, channel, ns, name, label string, upsert func(pgx.Tx) error) error {
	if pool == nil {
		return nil
	}
	err := achdb.WithTxNotify(ctx, pool, channel, ns+"/"+name, upsert)
	if err == nil || errors.Is(err, achdb.ErrOriginConflict) {
		return err
	}
	return fmt.Errorf("db upsert %s projection: %w", label, err)
}
