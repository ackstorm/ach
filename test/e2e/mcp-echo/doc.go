// SPDX-License-Identifier: Apache-2.0

// Package main is the ach-mcp-echo e2e + reference MCP backend.
//
// It serves a single tool, "echo", over the MCP Streamable-HTTP
// transport. The endpoint is wrapped in middleware that verifies the
// Ed25519 JWT minted by the ACH Forwarder (FWD-07/08) using the JWKS
// published at /.well-known/jwks.json. The verified claims are
// returned inside the tool result and recorded into an in-process
// capture surface (/__capture/last) so test/e2e/phase4_jwt_validate_test.go
// can assert iss / aud / sub / kid round-tripped end-to-end.
//
// Intentionally NOT production code: single replica, no persistence,
// no rate limiting. The capture surface and the JWKS-fetch cache are
// process-local. Engineers reading this as a reference for their own
// backend should read README.md alongside this binary.
package main
