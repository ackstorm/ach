// SPDX-License-Identifier: Apache-2.0

// Package store ships the Postgres-backed reader helpers for Platform API
// handlers (issue #34 / Phase B). Every platform-api read of an Environment
// projection row goes through this package so the spec-v4 §5.2 cache-served
// discipline is observable in one place.
//
// The constructor accepts a *pgxpool.Pool — the caller (cmd/ach/cmd/platform_api.go)
// opens the pool via db.Open and threads it through. Reads bind the
// namespace from the constructor as the first $-parameter (MULTI-01
// invariant carried over from the informer-era Store).
//
// Read-only invariant: Store offers ONLY Get + List against the projection
// tables. It MUST NOT expose Create/Update/Delete — platform-api's only
// write surface that touches CR-owned state is the /admin/refresh helper
// (see admin.ForceRefreshHandler), which sets the external_refs / marketplace_plugins
// force_refresh_requested_at marker and fires NOTIFY ach_refresh for the
// Operator's refreshsignal listener.
package store
