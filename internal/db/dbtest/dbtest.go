// SPDX-License-Identifier: Apache-2.0

// Package dbtest holds what tests need on top of internal/db: each projection
// write committed in its own transaction, and row reads.
package dbtest

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

func UpsertArtifact(ctx context.Context, pool *pgxpool.Pool, row db.ArtifactRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertArtifactTx(ctx, tx, row) })
}

func DeleteArtifact(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteArtifactTx(ctx, tx, ns, name) })
}

func UpsertBIP(ctx context.Context, pool *pgxpool.Pool, row db.BIPRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertBIPTx(ctx, tx, row) })
}

func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, row db.EnvironmentRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertEnvironmentTx(ctx, tx, row) })
}

func UpsertExternalRef(ctx context.Context, pool *pgxpool.Pool, r db.ExternalRef) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertExternalRefTx(ctx, tx, r) })
}

func UpsertLiteLLMConnection(ctx context.Context, pool *pgxpool.Pool, row db.LiteLLMConnectionRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertLiteLLMConnectionTx(ctx, tx, row) })
}

func UpsertMarketplacePlugin(ctx context.Context, pool *pgxpool.Pool, p db.MarketplacePlugin) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertMarketplacePluginTx(ctx, tx, p) })
}

func DeleteMarketplacePlugin(ctx context.Context, pool *pgxpool.Pool, marketplaceName, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteMarketplacePluginTx(ctx, tx, marketplaceName, name) })
}

func UpsertMarketplace(ctx context.Context, pool *pgxpool.Pool, row db.MarketplaceRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertMarketplaceTx(ctx, tx, row) })
}

func DeleteMarketplace(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteMarketplaceTx(ctx, tx, ns, name) })
}

func UpsertPlugin(ctx context.Context, pool *pgxpool.Pool, row db.PluginRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertPluginTx(ctx, tx, row) })
}

func DeletePlugin(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeletePluginTx(ctx, tx, ns, name) })
}

func UpsertInstruction(ctx context.Context, pool *pgxpool.Pool, row db.InstructionRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertInstructionTx(ctx, tx, row) })
}

func DeleteInstruction(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteInstructionTx(ctx, tx, ns, name) })
}

func UpsertSkillMarketplaceSkill(ctx context.Context, pool *pgxpool.Pool, s db.SkillMarketplaceSkill) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertSkillMarketplaceSkillTx(ctx, tx, s) })
}

func DeleteSkillMarketplaceSkill(ctx context.Context, pool *pgxpool.Pool, marketplaceName, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteSkillMarketplaceSkillTx(ctx, tx, marketplaceName, name) })
}

func UpsertSkillMarketplace(ctx context.Context, pool *pgxpool.Pool, row db.SkillMarketplaceRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertSkillMarketplaceTx(ctx, tx, row) })
}

func DeleteSkillMarketplace(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteSkillMarketplaceTx(ctx, tx, ns, name) })
}

func UpsertSkill(ctx context.Context, pool *pgxpool.Pool, row db.SkillRow) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.UpsertSkillTx(ctx, tx, row) })
}

func DeleteSkill(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return db.DeleteSkillTx(ctx, tx, ns, name) })
}

// GetPluginByName returns the plugins row, or nil when there is none.
func GetPluginByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*db.PluginRow, error) {
	r := &db.PluginRow{}
	err := pool.QueryRow(ctx, `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM plugins
		 WHERE namespace = $1 AND name = $2`, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.ResourceVersion, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetSkillByName returns the skills row, or nil when there is none.
func GetSkillByName(ctx context.Context, pool *pgxpool.Pool, ns, name string) (*db.SkillRow, error) {
	r := &db.SkillRow{}
	err := pool.QueryRow(ctx, `
		SELECT namespace, name, storage_location,
		       last_successful_refresh, max_staleness_seconds,
		       resource_version, updated_at
		  FROM skills
		 WHERE namespace = $1 AND name = $2`, ns, name).Scan(
		&r.Namespace, &r.Name, &r.StorageLocation,
		&r.LastSuccessfulRefresh, &r.MaxStalenessSeconds,
		&r.ResourceVersion, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}
