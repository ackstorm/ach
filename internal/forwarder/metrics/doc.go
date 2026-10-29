// SPDX-License-Identifier: Apache-2.0

// Package metrics declares the forwarder-side counter-hook surface the
// forwarder calls from middleware + per-route handlers: IncRequests,
// IncJWTSigned, IncJWTSuppressed, IncLiteLLMUnreachable, plus the
// forward-compat ObserveRequestDuration. Phase 5 (this commit) ships
// the nil-tolerant delegation; bodies forward to
// internal/metrics.ForwarderCollectors set at cmd-level via
// InitCollectors. Phase 4 call sites are unchanged — D-19 thin-shim
// invariant.
package metrics
