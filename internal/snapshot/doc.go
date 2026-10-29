// SPDX-License-Identifier: Apache-2.0

// Package snapshot is the Phase 2 LiteLLM-side resource cache the
// EnvironmentReconciler reads on every reconcile to compute
// ExecutionResourcesResolved (OP-13 / Hub §6.4).
//
// The single exported type Snapshotter is a controller-runtime
// manager.Runnable that refreshes ListModels + ListMCPServers +
// ListA2AAgents from the LiteLLM REST API every 5 minutes (D-13,
// tuned to align with Hub §6.4 Environment requeue cadence).
//
// Reads are lock-free via atomic.Pointer[LiteLLMSnapshot]; writes are
// single-writer (the Runnable's own goroutine via the ticker loop).
// No reader ever blocks the writer; no writer ever blocks a reader.
//
// On LiteLLM-unreachable during a refresh tick, the prior snapshot
// is PRESERVED with Stale=true (D-14). Environments reconciling
// against a stale snapshot still write ExecutionResourcesResolved
// based on the cached data; the LiteLLMUnreachableCount counter
// increments on every failed tick (Phase 5 wires it into the
// ach_litellm_unreachable_total{caller="operator"} Prometheus counter).
//
// First-refresh failure produces an empty stale snapshot so callers
// get consistent "every spec entry unresolved + Stale=true" semantics
// (vs. the cold-start zero-value LiteLLMSnapshot which produces
// "every spec entry unresolved + Stale=false" — distinct codepath
// worth distinguishing in logs).
//
// The snapshot package depends only on internal/litellm (for the
// Client interface), logr (for logging), and stdlib (sync/atomic,
// time, errors).
package snapshot
