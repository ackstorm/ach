// SPDX-License-Identifier: Apache-2.0

// Package db helpers for the key_grants projection (KeyGrant CRs, migration
// 000026). The operator is the only writer; the exchange handler and the
// exchange-JWT resolvers read it per request.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyGrantRow is one projected KeyGrant. Identity is lower-case (the CRD
// admits nothing else). MaxBudget is nil when the grant sets no budget.
type KeyGrantRow struct {
	Namespace       string
	Name            string
	Identity        string
	Environments    []string
	ServiceAccounts []string
	MaxBudget       *float64
	BudgetDuration  string
}

// UpsertKeyGrant writes the row of one KeyGrant CR.
func UpsertKeyGrant(ctx context.Context, pool *pgxpool.Pool, row KeyGrantRow) error {
	const sql = `
		INSERT INTO key_grants
		    (namespace, name, identity, environments, service_accounts, max_budget, budget_duration, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (namespace, name) DO UPDATE SET
		    identity         = EXCLUDED.identity,
		    environments     = EXCLUDED.environments,
		    service_accounts = EXCLUDED.service_accounts,
		    max_budget       = EXCLUDED.max_budget,
		    budget_duration  = EXCLUDED.budget_duration,
		    updated_at       = now()
	`
	if _, err := pool.Exec(ctx, sql, row.Namespace, row.Name, row.Identity,
		row.Environments, row.ServiceAccounts, row.MaxBudget, row.BudgetDuration); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertKeyGrant(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// DeleteKeyGrant removes the row; an absent row is not an error.
func DeleteKeyGrant(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	if _, err := pool.Exec(ctx, `DELETE FROM key_grants WHERE namespace = $1 AND name = $2`, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteKeyGrant(%s/%s): %w", ns, name, err)
	}
	return nil
}

const keyGrantColumns = `namespace, name, identity, environments, service_accounts, max_budget, budget_duration`

// ListKeyGrants returns every grant in ns, ordered by name.
func ListKeyGrants(ctx context.Context, pool *pgxpool.Pool, ns string) ([]KeyGrantRow, error) {
	return queryKeyGrants(ctx, pool, "ListKeyGrants",
		`SELECT `+keyGrantColumns+` FROM key_grants WHERE namespace = $1 ORDER BY name`, ns)
}

// ListKeyGrantsForIdentity returns the grants in ns naming identity, ordered by name.
func ListKeyGrantsForIdentity(ctx context.Context, pool *pgxpool.Pool, ns, identity string) ([]KeyGrantRow, error) {
	return queryKeyGrants(ctx, pool, "ListKeyGrantsForIdentity",
		`SELECT `+keyGrantColumns+` FROM key_grants WHERE namespace = $1 AND identity = $2 ORDER BY name`, ns, identity)
}

func queryKeyGrants(ctx context.Context, pool *pgxpool.Pool, op, sql string, args ...any) ([]KeyGrantRow, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: %s: %w", op, err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (KeyGrantRow, error) {
		var g KeyGrantRow
		err := r.Scan(&g.Namespace, &g.Name, &g.Identity, &g.Environments, &g.ServiceAccounts, &g.MaxBudget, &g.BudgetDuration)
		return g, err
	})
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: %s: %w", op, err)
	}
	return out, nil
}
