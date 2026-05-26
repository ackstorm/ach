// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package litellm is the ACH Operator's LiteLLM 1.83.10 REST client.
//
// Type definitions and the error-shape parser are derivative works from
// bbdsoftware/litellm-operator (Apache-2.0; see NOTICE at the repository
// root). The HTTP transport is freshly written to satisfy spec §5.1 / §7.7
// / §9.1 contracts.
//
// The package exposes the Client interface (consumed by every domain
// reconciler) and two implementations: RESTClient (production) and
// NoopClient (unit tests + Phase 1 carry-forward). Reconcilers MUST type
// their litellm dependency as Client — the swap from NoopClient to
// RESTClient is a wiring-only edit in cmd/operator/main.go (Plan 09).
//
// Invariants enforced at the package level:
//
//   - REL-04: every Response.Body is drained and closed via
//     defer drainAndClose immediately after http.Client.Do returns success.
//   - REL-05: list-returning helpers length-check len(list.Data) before
//     indexing and return ErrNotFound on empty results.
//   - REL-06: 401 responses are classified as *Auth401Error so the
//     reconciler's §7.7 fast-path can route them via errors.As.
//   - §9.1: with ACH_LITELLM_DANGEROUSLY_LOG_BODIES unset, no request
//     body, response body, header, or master-key string ever enters a log
//     line. The redacting RoundTripper records only
//     {method, path, status, latency_ms}.
//
// Environment variables consumed at NewRESTClient construction time:
//
//   - ACH_LITELLM_AUTH_HEADER (optional; default "Authorization: Bearer";
//     override to "x-litellm-api-key" as an escape hatch).
//   - ACH_LITELLM_DANGEROUSLY_LOG_BODIES (optional; default unset means
//     redaction ON; set to "true" to flip request + response body logging
//     ON for short-lived live debugging only).
//
// Endpoint and master key are sourced from the LiteLLMConnection/default
// custom resource: the reconciler in
// internal/controller/ach/litellmconnection_controller.go reads
// spec.endpoint and the Secret referenced by spec.masterKeySecretRef,
// constructs a RESTClient via NewRESTClient, and publishes it into the
// connection.Cache snapshot consumed by all domain reconcilers via the
// delegating connection.Client in internal/connection.
//
// The connection-probe endpoint is GET /models (NOT the legacy
// spec-§6.1 key-info path). LiteLLM 1.83.10's master key, when set only
// via env var on the LiteLLM Pod, is not stored in the LiteLLM key
// database, so the legacy probe always 404s; GET /models validates
// LiteLLM-reachable AND master-key-authoritative in one call.
package litellm
