// SPDX-License-Identifier: Apache-2.0

package sources

import "github.com/ackstorm/ach/internal/sourceserr"

// Sentinel errors live in the k8s-free internal/sourceserr package so that
// internal/gitfetch (and the ach-cli binary) can classify fetch failures
// without importing this package — which pulls k8s.io/api/core/v1 via the
// *corev1.Secret in sources.go. These aliases keep every existing
// `sources.ErrXxx` / `sources.ReasonOf` call site compiling unchanged;
// identity is preserved, so errors.Is across the alias is exact.
var (
	ErrUnauthorized    = sourceserr.ErrUnauthorized
	ErrNotFound        = sourceserr.ErrNotFound
	ErrUnreachable     = sourceserr.ErrUnreachable
	ErrUpstreamInvalid = sourceserr.ErrUpstreamInvalid
	ErrUnknownSource   = sourceserr.ErrUnknownSource
)

// ReasonOf re-exports sourceserr.ReasonOf — see that package for the
// Hub §6.6 SourceReachable.reason contract.
func ReasonOf(err error) string { return sourceserr.ReasonOf(err) }
