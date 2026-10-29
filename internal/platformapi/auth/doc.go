// SPDX-License-Identifier: Apache-2.0

// Package auth is ACH's OAuth 2.1 authorization server (/platform/oauth/*)
// and the Dex + LiteLLM plumbing behind it. Every endpoint is
// UNAUTHENTICATED by nature — a client reaches it before holding a
// credential — and mounts OUTSIDE the Authn-gated chi.Group.
//
// Two ways in, one Dex leg (/platform/oauth/as-callback), one token shape
// (1h Ed25519 JWT + rotating refresh token, one purpose='oauth' pk_ row per
// user behind it):
//
//   - authorization code + PKCE (oauth_authorize.go, oauth_token.go): MCP
//     clients and ach-cli with a local browser; DCR in oauth_register.go;
//     an optional consent hop to a BIP-declared broker in oauth_chain.go.
//   - RFC 8628 device grant (oauth_device.go): a host with no browser
//     shows a code, the user confirms it on /platform/oauth/device from
//     any browser, the client polls /token.
//
// Transient state lives in Valkey (oauth_store.go). provisionUser (sso.go)
// creates/enrols the LiteLLM user on every login; MintPK (mint.go) mints
// the pk_ row. Default-team-missing (Hub §17 / API-02) is fail-loud: ACH
// never creates the default Team.
//
// Plaintext discipline (Hub §16.1): the pk_ plaintext minted for an OAuth
// row is discarded — the forwarder resolves the row by owner_email; the
// JWT is the credential. Nothing here logs a token.
//
// Dex configuration: ACH_DEX_ISSUER_URL / CLIENT_ID / CLIENT_SECRET are
// REQUIRED at process start (cmd/ach/cmd/platform_api.go). This package
// consumes the pre-constructed *oidc.Provider and *oauth2.Config via Deps.
package auth
