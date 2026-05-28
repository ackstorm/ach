// SPDX-License-Identifier: Apache-2.0

// Package envstore implements a Postgres-backed Environment cache for the
// forwarder precheck path (issue #34). Replaces the controller-runtime
// cached client's Get/List on Environment.
//
// Get/List read an atomic.Pointer[map[string]db.EnvironmentRow] keyed by
// name; precheck consumes the rows via the envProvider interface in
// internal/forwarder/precheck so it can be substituted with a tiny in-memory
// fake in unit tests.
//
// Run subscribes to ach_environments_changed for event-driven refresh AND
// ticks every 5 minutes as a safety net (db.Listener does not replay
// missed events when its conn drops).
package envstore
