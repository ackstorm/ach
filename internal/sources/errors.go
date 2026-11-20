// SPDX-License-Identifier: Apache-2.0

package sources

import "github.com/ackstorm/ach/internal/sourceserr"

// Sentinel errors live in the k8s-free internal/sourceserr package so that
// internal/gitfetch (and the ach-cli binary) can classify fetch failures
// without importing this package — which pulls k8s.io/api/core/v1 via the
// *corev1.Secret in sources.go. These aliases keep every existing
// `sources.ErrXxx` call site compiling unchanged;
// identity is preserved, so errors.Is across the alias is exact.
var (
	ErrUnauthorized    = sourceserr.ErrUnauthorized
	ErrNotFound        = sourceserr.ErrNotFound
	ErrUnreachable     = sourceserr.ErrUnreachable
	ErrUpstreamInvalid = sourceserr.ErrUpstreamInvalid
	ErrUnknownSource   = sourceserr.ErrUnknownSource
)
