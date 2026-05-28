// SPDX-License-Identifier: Apache-2.0

// Package bipcache implements a Postgres-backed BackendIdentityPolicy cache
// for the forwarder JWT trust path (issue #34). Replaces the informer-backed
// internal/forwarder/bip package.
//
// Resolve mirrors internal/forwarder/bip.ResolveWinner semantics 1:1:
//  1. Take the alphabetically-LAST BIP matching (targetKind, targetName).
//  2. If that row's ForwardIdentityJWT is FALSE — explicit opt-out — return nil.
//  3. Otherwise return the row.
//
// Run subscribes to ach_backend_identity_policies_changed for event-driven
// refresh AND ticks every 5 minutes as a safety net (Listener does not replay
// missed events when its conn drops).
package bipcache
