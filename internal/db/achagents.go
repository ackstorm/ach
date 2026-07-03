// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentsChannel is the Postgres NOTIFY channel emitted on every achagents
// projection write/delete. The gateway agentstore LISTENs on it. Single
// source of truth — referenced by the reconciler writer and the gateway
// reader alike.
const AgentsChannel = "ach_achagents_changed"

// ChannelSummary is the compact per-channel shape stored in achagents.channels
// (jsonb) for the UI. Source is only set for webhook channels.
type ChannelSummary struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
}

// AgentRow is the full achagents projection row (operator write side).
type AgentRow struct {
	Namespace       string
	Name            string
	ProfileRef      string
	ServiceName     string // "" when the agent has no Service (cron/queue-only)
	ServicePort     int32  // 0 when no Service
	HasWebhook      bool
	Ready           bool
	Channels        []ChannelSummary
	ResourceVersion string
	UpdatedAt       time.Time
}

// WebhookAgentRow is the narrow gateway read shape: only what /hook routing needs.
type WebhookAgentRow struct {
	Namespace   string
	Name        string
	ServiceName string
	ServicePort int32
}

const upsertAgentSQL = `
	INSERT INTO achagents
	    (namespace, name, profile_ref, service_name, service_port,
	     has_webhook, ready, channels, resource_version, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
	ON CONFLICT (namespace, name) DO UPDATE SET
	    profile_ref      = EXCLUDED.profile_ref,
	    service_name     = EXCLUDED.service_name,
	    service_port     = EXCLUDED.service_port,
	    has_webhook      = EXCLUDED.has_webhook,
	    ready            = EXCLUDED.ready,
	    channels         = EXCLUDED.channels,
	    resource_version = EXCLUDED.resource_version,
	    updated_at       = now()
`

// UpsertAgentTx inserts-or-updates an achagents row keyed by (namespace, name).
func UpsertAgentTx(ctx context.Context, tx pgx.Tx, row AgentRow) error {
	chJSON, err := json.Marshal(row.Channels)
	if err != nil {
		return fmt.Errorf("db: marshal channels for %s/%s: %w", row.Namespace, row.Name, err)
	}
	if row.Channels == nil {
		chJSON = []byte("[]")
	}
	if _, err := tx.Exec(ctx, upsertAgentSQL,
		row.Namespace, row.Name, row.ProfileRef, row.ServiceName, row.ServicePort,
		row.HasWebhook, row.Ready, chJSON, row.ResourceVersion,
	); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: UpsertAgent(%s/%s): %w", row.Namespace, row.Name, err)
	}
	return nil
}

// UpsertAgent is the pool-level convenience wrapper (tests / non-tx callers).
func UpsertAgent(ctx context.Context, pool *pgxpool.Pool, row AgentRow) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return UpsertAgentTx(ctx, tx, row)
	})
}

// DeleteAgentTx removes the achagents row keyed by (namespace, name). Absence
// is not an error.
func DeleteAgentTx(ctx context.Context, tx pgx.Tx, ns, name string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM achagents WHERE namespace = $1 AND name = $2`, ns, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: DeleteAgent(%s/%s): %w", ns, name, err)
	}
	return nil
}

// DeleteAgent is the pool-level convenience wrapper.
func DeleteAgent(ctx context.Context, pool *pgxpool.Pool, ns, name string) error {
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		return DeleteAgentTx(ctx, tx, ns, name)
	})
}

// ListWebhookAgents returns every routable webhook agent across all namespaces
// (the /hook route carries the namespace, so this is deliberately unscoped).
// Rows with an empty service_name are excluded — they cannot be routed.
func ListWebhookAgents(ctx context.Context, pool *pgxpool.Pool) ([]WebhookAgentRow, error) {
	const sql = `
		SELECT namespace, name, service_name, service_port
		  FROM achagents
		 WHERE has_webhook = TRUE AND service_name <> ''
	`
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: ListWebhookAgents: %w", err)
	}
	defer rows.Close()
	out := []WebhookAgentRow{}
	for rows.Next() {
		var r WebhookAgentRow
		if err := rows.Scan(&r.Namespace, &r.Name, &r.ServiceName, &r.ServicePort); err != nil {
			return nil, fmt.Errorf("db: scan webhook agent row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		if isTransientPgErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("db: iterate webhook agent rows: %w", err)
	}
	return out, nil
}
