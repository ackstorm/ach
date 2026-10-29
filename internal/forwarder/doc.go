// SPDX-License-Identifier: Apache-2.0

// Package forwarder assembles the §5.1 reverse-proxy plane (FWD-01) and
// the /.well-known/jwks.json publication (FWD-08) for the Hub. New(deps)
// returns the chi-backed traffic handler; NewHealthHandler(signer, sync)
// returns the health handler exposing /healthz, /livez, /readyz. Runnable
// owns both http.Server instances per D-03 (traffic on :8080, health on
// :8081) with the D-04 timeout matrix and the WriteTimeout=0 SSE carveout.
package forwarder
