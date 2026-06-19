// SPDX-License-Identifier: Apache-2.0

// Package db force-refresh signaller (issue #34 / Phase B2).
//
// SetForceRefresh is the Postgres-only replacement for the pre-issue-34
// Platform-API force-refresh path that PATCH'd an annotation on the target CR.
// Issue #34 makes Postgres the source of truth, so platform-api now signals a
// refresh by:
//
//  1. Looking up the projection row for the named (kind, name) to confirm
//     origin='cr' — UI-managed rows have no upstream to refresh, so the
//     handler must surface 400 ErrUIOriginRefreshUnsupported.
//
//  2. UPDATE external_refs / marketplace_plugins SET force_refresh_requested_at = now()
//     on the matching row (force_refresh_requested_at lives on the source
//     table — external_refs for plugin/prompt/artifact, marketplace_plugins
//     for pluginmarketplace).
//
//  3. NOTIFY ach_refresh '<kind>/<name>' via db.Emit. The operator's A11
//     refreshsignal listener (parallel subagent) picks up the notification
//     and enqueues the matching CR for reconcile; the periodic operator
//     resync (A10) is the safety net for any dropped notification.
//
// The function deliberately does NOT use db.WithTxNotify — the UPDATE and the
// NOTIFY are independent in the source-of-truth semantics: a successful
// UPDATE is observable to any future reader via the column; the NOTIFY is a
// liveness hint to the operator's reconcile queue. If the NOTIFY fails the
// caller still got the column update, and the operator's periodic resync
// (A10, ≤ 5 min) catches up — losing the NOTIFY does not lose work.

package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUIOriginRefreshUnsupported is returned by SetForceRefresh when the
// matched projection row carries origin='ui'. UI-managed rows have no
// upstream Git URL to refetch — the UI is the authoritative source for those
// rows and re-uploads directly. The handler should surface a 400 to the
// caller; retrying does not help.
var ErrUIOriginRefreshUnsupported = errors.New("db: cannot force-refresh a UI-managed row")

// refreshSignalChannel is the LISTEN/NOTIFY channel name the operator's
// A11 refreshsignal listener binds. Kept as a package-private constant so
// the signaller and the listener cannot drift.
const refreshSignalChannel = "ach_refresh"

// SetForceRefresh marks the named external-reference projection row as
// pending-refresh and fires NOTIFY ach_refresh '<kind>/<name>'. The
// operator's A11 listener picks up the NOTIFY and enqueues the matching CR
// for reconcile.
//
// Kind discipline:
//   - "plugin", "prompt", "artifact", "skill" → external_refs (kind, name) PK.
//   - "pluginmarketplace"            → marketplace_plugins (marketplace_name, name)
//     PK — the operator's marketplace reconciler
//     sweeps every plugin under the named
//     marketplace on the next pass, so the
//     UPDATE sets every row's marker.
//   - "skillmarketplace"             → skill_marketplace_skills (marketplace_name,
//     name) PK — mirrors pluginmarketplace; the
//     SkillMarketplace reconciler sweeps every
//     discovered skill on the next pass.
//
// Behavioural contract:
//   - Returns ErrUIOriginRefreshUnsupported when the row exists with
//     origin='ui'. UI-managed rows have no upstream to refresh.
//   - Returns a not-found error wrapped in the package convention when the
//     row is genuinely absent — the caller should surface 404.
//   - Pgconn 08/57 transients propagate raw so a controller-runtime workqueue
//     applies exponential backoff (platform-api callers are HTTP-side so the
//     handler converts these to 503).
//
// The UPDATE and the NOTIFY are issued as separate statements (no
// WithTxNotify). See package doc-comment for the no-transaction rationale.
func SetForceRefresh(ctx context.Context, pool *pgxpool.Pool, ns, kind, name string) error {
	switch kind {
	case "plugin", "prompt", "artifact", "skill":
		if err := setForceRefreshExternalRef(ctx, pool, kind, name); err != nil {
			return err
		}
	case "pluginmarketplace":
		if err := setForceRefreshMarketplaceTable(ctx, pool, "marketplace_plugins", "pluginmarketplace", name); err != nil {
			return err
		}
	case "skillmarketplace":
		if err := setForceRefreshMarketplaceTable(ctx, pool, "skill_marketplace_skills", "skillmarketplace", name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("db: SetForceRefresh: unknown kind %q", kind)
	}
	// Best-effort NOTIFY: a transient pgconn 08/57 error propagates so the
	// HTTP caller can retry; any other Emit failure is wrapped and surfaced
	// — the operator's periodic resync (A10) is the safety net either way.
	payload := kind + "/" + name
	if err := Emit(ctx, pool, refreshSignalChannel, payload); err != nil {
		return fmt.Errorf("db: SetForceRefresh(%s/%s): notify: %w", kind, name, err)
	}
	_ = ns // namespace is implicit in single-namespace deployments; reserved for future multi-ns indexing
	return nil
}

// setForceRefreshExternalRef sets force_refresh_requested_at = now() on the
// external_refs row keyed by (kind, name) iff origin='cr'. Returns
// ErrUIOriginRefreshUnsupported when the row exists with origin='ui'.
//
// Absence (no row at all) returns pgx.ErrNoRows wrapped — the platform-api
// admin handler maps that to 404. We deliberately do the origin check via
// the RETURNING clause: a UPDATE that matches no rows could mean "absent" or
// "wrong origin"; a separate SELECT first would race against a concurrent
// origin change. The two-step variant (SELECT origin, then conditional
// UPDATE) is the simpler-to-explain form and is what we ship — concurrent
// origin changes are not in scope until the UI ships.
func setForceRefreshExternalRef(ctx context.Context, pool *pgxpool.Pool, kind, name string) error {
	const checkSQL = `SELECT origin FROM external_refs WHERE kind = $1 AND name = $2`
	var origin string
	if err := pool.QueryRow(ctx, checkSQL, kind, name).Scan(&origin); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", kind, name, pgx.ErrNoRows)
		}
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", kind, name, err)
	}
	if origin != "cr" {
		return ErrUIOriginRefreshUnsupported
	}
	const updateSQL = `
		UPDATE external_refs
		   SET force_refresh_requested_at = now()
		 WHERE kind = $1 AND name = $2 AND origin = 'cr'
	`
	if _, err := pool.Exec(ctx, updateSQL, kind, name); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", kind, name, err)
	}
	return nil
}

// setForceRefreshMarketplaceTable sets force_refresh_requested_at = now() on
// every row of a marketplace projection table under marketplaceName iff
// origin='cr'. table and errLabel are trusted compile-time constants (never
// user input — only "marketplace_plugins"/"pluginmarketplace" and
// "skill_marketplace_skills"/"skillmarketplace"), so the fmt.Sprintf into SQL
// is safe. The Plugin/SkillMarketplace reconcilers sweep the whole marketplace
// on the next pass, so we mark all rows at once rather than per-row — the
// platform-api request is "refresh the marketplace as a whole".
//
// Returns ErrUIOriginRefreshUnsupported when any row under the marketplace has
// origin='ui'. Absent marketplace (no rows) returns pgx.ErrNoRows wrapped.
//
// Two-phase origin check + UPDATE; same rationale as setForceRefreshExternalRef.
func setForceRefreshMarketplaceTable(ctx context.Context, pool *pgxpool.Pool, table, errLabel, marketplaceName string) error {
	// Pre-check origin: the marketplace is "all-cr" only if every row is cr.
	// A mixed-origin marketplace (rare; would require UI-side row insertion
	// against a CR-managed marketplace) refuses the refresh — the UI must
	// decide what to do.
	checkSQL := fmt.Sprintf(`
		SELECT COUNT(*) FILTER (WHERE origin = 'cr'),
		       COUNT(*) FILTER (WHERE origin <> 'cr')
		  FROM %s
		 WHERE marketplace_name = $1`, table) // #nosec G201 -- table is a trusted constant
	var crCount, nonCrCount int
	if err := pool.QueryRow(ctx, checkSQL, marketplaceName).Scan(&crCount, &nonCrCount); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", errLabel, marketplaceName, err)
	}
	if crCount == 0 && nonCrCount == 0 {
		return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", errLabel, marketplaceName, pgx.ErrNoRows)
	}
	if nonCrCount > 0 {
		return ErrUIOriginRefreshUnsupported
	}
	updateSQL := fmt.Sprintf(`
		UPDATE %s
		   SET force_refresh_requested_at = now()
		 WHERE marketplace_name = $1 AND origin = 'cr'`, table) // #nosec G201 -- table is a trusted constant
	if _, err := pool.Exec(ctx, updateSQL, marketplaceName); err != nil {
		if isTransientPgErr(err) {
			return err
		}
		return fmt.Errorf("db: SetForceRefresh(%s/%s): %w", errLabel, marketplaceName, err)
	}
	return nil
}
