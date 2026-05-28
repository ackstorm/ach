// SPDX-License-Identifier: Apache-2.0

// Package devicecode is the CLI-side client for the device-code login
// flow against the Platform API endpoints under
// /platform/auth/cli/{init,token} (W1-P2 server contract per
// 06-CONTEXT.md D-02 + 06-02-SUMMARY.md).
//
// The poll cadence + 5-min total timeout map to the server's
// init.poll_interval + expires_in fields. The shape mirrors RFC 8628
// (OAuth 2.0 Device Authorization Grant) loosely — ACH does NOT
// implement RFC 8628 strictly; it borrows the pattern (two anonymous
// endpoints, polling client) so existing users of gh/gcloud/aws sso
// recognize the UX.
//
// Discipline:
//
//   - Stdlib net/http only (no third-party HTTP wrappers). The
//     anonymous request shape does not need internal/cli/httpclient's
//     x-ach-key carrier — bare http.Client is correct here.
//   - Error-envelope decode reuses *httpclient.ServerError to keep
//     downstream cmd/ach/main.go's errors.As branch unchanged.
//   - The opener-of-the-browser is a swap-in seam (Opener var) so
//     unit tests can override the side-effecting xdg-open / open /
//     rundll32 dispatch.
//   - ctx.Done() is honored on every sleep tick — no naked
//     time.Sleep that ignores cancellation.
package devicecode
