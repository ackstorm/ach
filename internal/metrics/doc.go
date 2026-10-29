// SPDX-License-Identifier: Apache-2.0

// Package metrics is the Phase 5 cross-component Prometheus surface for
// the Hub (OBS-03..06 from Phase 5 REQUIREMENTS.md). It owns:
//
//   - NewRegistry / Handler — D-09: a freshly-constructed process-local
//     prometheus.Registry per service, deliberately NOT
//     prometheus.DefaultRegisterer. Each long-running mode (operator,
//     platform-api, forwarder, content-service) builds its own Registry
//     so controller-runtime's default-registry collectors (workqueue,
//     leader-election, etc.) do not bleed onto the chi /metrics mux,
//     and so unit tests can construct isolated Registries without
//     re-register panics.
//   - Handler — D-10: returns the chi-mountable http.Handler from
//     promhttp.HandlerFor. Every Hub service mounts /metrics on the
//     same chi mux as its traffic listener (forwarder :8080, platform
//     API :8083, content-service :8082); the operator keeps the
//     controller-runtime metricsserver on :8443 and ADDS the ACH-
//     namespaced collectors there via metrics.Registry().MustRegister.
//   - Typed collector factories (forwarder.go, contentservice.go) —
//     D-09: per-service ForwarderCollectors / ContentServiceCollectors
//     structs whose Inc/Observe methods accept only typed strings
//     bound to the §18.5 normative label-value enums. Raw label
//     strings NEVER appear at the call site; if a call site needs a
//     new outcome value, it adds a constant in internal/audit or here
//     first. T-05-01-04 cardinality DoS is mitigated by this
//     type-level pinning.
//   - Shared cross-service collector (shared.go) —
//     MustRegisterLitellmUnreachable returns the single
//     ach_litellm_unreachable_total CounterVec; each service holds the
//     returned pointer and calls
//     .WithLabelValues("forwarder"|"content_service"|
//     "platform_api"|"operator").Inc().
//     ONE collector per process; OBS-05.
//   - Bucket constants (buckets.go) — D-11: ForwarderDurationBuckets
//     = prometheus.DefBuckets (forwarder is a thin proxy; tail beyond
//     10s is upstream LiteLLM's problem). ContentServiceDurationBuckets
//     extends DefBuckets to 30s + 60s to absorb the artifact-tarball
//     tail without falling into the +Inf bucket on every multi-MB
//     stream.
//
// Cardinality discipline (OBS-06): No request_id label, no owner_email
// label, no plaintext bearer fragment. Label values are §18.5 closed
// enums; new values added additively within an API version. See
// references/security/govulncheck-acknowledged.md for the
// prometheus/client_golang supply-chain posture (T-05-01-SC: accept;
// version v1.23.2 already vendored as indirect dep via
// controller-runtime, promoted to direct in Phase 5).
package metrics
