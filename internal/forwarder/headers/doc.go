// SPDX-License-Identifier: Apache-2.0

// Package headers ships the FWD-04 / D-06 strip + D-07 write contract as a
// pure function. Centralizing the transform makes the test surface exhaustive
// (case-insensitive prefix matches, Connection-token-named strip per RFC 7230
// §6.1, hop-by-hop list, multi-value headers) and lets the per-route handlers
// stay thin. Stdlib only — no third-party deps.
package headers
