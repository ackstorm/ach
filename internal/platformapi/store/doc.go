// SPDX-License-Identifier: Apache-2.0

// Package store ships the informer-backed reader helpers for Platform API
// handlers. Every Phase 3 read of an Environment / Plugin / Prompt / Artifact /
// PluginMarketplace / BackendIdentityPolicy CR goes through this package so
// the cache-served discipline (Hub §5.2) is observable in code review.
//
// The constructor accepts a controller-runtime client.Client — the caller
// (cmd/platform-api/main.go) MUST construct it via mgr.GetClient() AFTER
// mgr.GetCache() has completed initial list-and-watch sync; otherwise reads
// fall back to the API server, defeating the cache promise.
//
// Namespace scope per MULTI-01: the constructor takes ns from POD_NAMESPACE;
// all reads are scoped via client.InNamespace(ns).
//
// Read-only invariant: Store offers ONLY Get + List against the controller-
// runtime client. It MUST NOT expose Create/Update/Delete/Patch — Platform API's
// only K8s write surface is the force-refresh annotation patch (Plan 03-10
// admin handler), which lives outside this package.
package store
