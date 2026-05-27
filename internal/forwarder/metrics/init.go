// SPDX-License-Identifier: Apache-2.0

// init.go wires the package-private collector vars from the cmd-level
// startup (cmd/ach/cmd/forwarder.go). Plan 05-06 D-19: the shim package
// keeps the existing Inc* signatures intact and delegates to typed
// collectors held in `internal/metrics`. InitCollectors is the single
// boundary between the cmd-level Registry build and the shim-call
// sites in internal/forwarder/{proxy,bip}/.
//
// Concurrency contract:
//
//   - Production: called ONCE in runForwarder before any traffic is
//     served. Subsequent reads from Inc* delegations see the assigned
//     values via process-start happens-before (no races possible
//     because nothing else has started yet).
//   - Tests: may call multiple times across test cases — last-init-wins
//     so per-test isolation is achievable via t.Cleanup that resets the
//     package vars to nil. NOT goroutine-safe to call concurrently
//     across goroutines (no atomic / mutex — the production startup is
//     single-threaded, and tests serialize within each function).
//
// Absence of an InitCollectors call yields zero-cost no-op delegations
// from counters.go — same behavior as Phase 4 stubs. This is by design
// per D-19: existing Phase 4 unit tests that import the shim without
// wiring it remain green.

package metrics

import (
	coremetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// InitCollectors sets the package-private collectors used by IncRequests,
// IncJWTSigned, IncJWTSuppressed, IncLiteLLMUnreachable, and
// ObserveRequestDuration.
//
// Either argument MAY be nil — passing nil leaves the corresponding
// surface in no-op mode (useful for tests that only exercise a subset
// of the shim). Production wiring in cmd/ach/cmd/forwarder.go MUST
// pass both non-nil values.
//
// Returns no error — the caller has already obtained the collectors
// via metrics.NewForwarderCollectors and metrics.MustRegisterLitellmUnreachable
// which panic on registry conflicts; this function is purely a
// pointer assignment.
func InitCollectors(c *coremetrics.ForwarderCollectors, lu *prometheus.CounterVec) {
	collectors = c
	litellmUnreachable = lu
}
