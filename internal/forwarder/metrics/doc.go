// SPDX-License-Identifier: Apache-2.0

// Package metrics declares the Plan 04-01 (D-18) counter-hook stubs the
// forwarder calls from middleware + per-route handlers: IncRequests,
// IncJWTSigned, IncJWTSuppressed, IncLiteLLMUnreachable. Phase 5
// (OBS-03..06) replaces these no-op bodies with
// prometheus.CounterVec.WithLabelValues(...).Inc() calls; call sites do
// not change.
package metrics
