// SPDX-License-Identifier: Apache-2.0

// Package resync provides a manager.Runnable that re-Lists every ACH CR Kind
// every Interval and pushes a GenericEvent per item into the per-Kind
// chan event.GenericEvent the matching controller registered via
// Watches(&source.Channel{Source: ch}, &handler.EnqueueRequestForObject{}).
// Safety net for missed events, operator restart drift, and Postgres write
// failures swallowed by a single reconcile. Default Interval is 5 minutes.
package resync
