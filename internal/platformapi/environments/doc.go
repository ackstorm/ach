// SPDX-License-Identifier: Apache-2.0

// Package environments hosts the GET /platform/environments endpoint pair
// (Plan 03-09): a paginated list filtered by team intersection (admin sees
// all) and a single-environment get (with the same filtering).
//
// Design summary (Hub §15.5, API-08):
//
//   - ListHandler — pk_ only; reads keyCtx.IsAdmin (populated by middleware.Authn
//     against the deployment admin allowlist per BLK-02) and looks up caller
//     team memberships via internal/platformapi/teams.LookupCallerTeams
//     (the canonical helper per WARN-06; Phase 4 will swap it for a Redis-
//     cached implementation). Calls store.ListAuthorizedEnvironments to
//     enforce the team-intersection filter on the informer cache. Applies
//     opaque ?cursor + ?limit pagination (default 100, hard cap 500).
//     Returns {items: [<EnvironmentView>], next_cursor: <string or nil>}.
//
//   - GetHandler — pk_ only; reads a single Environment via
//     store.GetEnvironment. Non-admins must intersect at least one team
//     with the env's spec.authorizedTeams (otherwise 403 unauthorized_team).
//     404 when the env is absent.
//
// Response semantics:
//
//   - conditions[] are carried verbatim from .status.conditions per API-08 /
//     Hub §6.6 closed set (the EnvironmentView projection is the canonical
//     shape — Plan 03-06 owns it).
//   - All array fields ALWAYS present in the response envelope; empty values
//     serialize as `[]` (NOT `null`) per API-04 convention.
//   - The endpoint is read-only — no audit emission per OBS-01 (environment
//     listing is not an event surface; ActionEnvironmentLifecycle is the
//     Operator's emission point, not the Platform API's).
//
// All reads are O(1) cache lookups served from the controller-runtime
// informer cache — no API-server round trips per D-21.
package environments
