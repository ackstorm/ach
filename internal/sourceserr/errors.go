// SPDX-License-Identifier: Apache-2.0

package sourceserr

import "errors"

// Sentinel errors classifying upstream-fetch failures. Per-source-type
// fetchers wrap these via `fmt.Errorf("...: %w", sourceserr.ErrXxx)` so
// callers can dispatch on the classification via [ReasonOf] without
// depending on per-SDK error types.
//
// The classification is intentionally narrow — the Hub §6.6
// SourceReachable.reason enum has exactly four reachability outcomes
// (Unauthorized, NotFound, Unreachable, UpstreamInvalid). The size-cap
// reason (PluginTooLarge) is the reconciler's concern (D-12) and the
// staleness reason (StaleCacheExpired) is the Content Service's concern
// (Phase 5); neither flows through fetchers and so neither has a sentinel
// in this package.
var (
	// ErrUnauthorized indicates an authentication or authorization failure
	// against the upstream (HTTP 401/403, SDK auth error, missing auth
	// secret key, etc.). Caller surfaces SourceReachable=False,
	// reason=Unauthorized.
	ErrUnauthorized = errors.New("sources: unauthorized")

	// ErrNotFound indicates the upstream entity does not exist (HTTP 404,
	// S3 NoSuchKey, GCS storage.ErrObjectNotExist, missing git ref).
	// Caller surfaces SourceReachable=False, reason=NotFound.
	ErrNotFound = errors.New("sources: not found")

	// ErrUnreachable indicates a transient network condition: TCP reset,
	// connection refused, DNS failure, context deadline exceeded, HTTP
	// 5xx, etc. Caller surfaces SourceReachable=False, reason=Unreachable
	// and the controller-runtime workqueue applies exponential backoff
	// for retry.
	ErrUnreachable = errors.New("sources: unreachable")

	// ErrUpstreamInvalid indicates the upstream is reachable and
	// authorized but produced a malformed response: the requested object
	// returned 200 OK with an unparsable body, or the configured URL/repo
	// has an invalid shape. NOTE: an HTTPSource plaintext http:// URL is
	// NOT upstream-invalid — it is a dev/e2e-only concession (G19 decision
	// D); production https:// is convention, not machine-enforced.
	// Caller surfaces SourceReachable=False, reason=UpstreamInvalid.
	ErrUpstreamInvalid = errors.New("sources: upstream invalid")

	// ErrUnknownSource indicates the Registry received a SourceSpec
	// whose Type is not one of the six enum values. Defense in depth
	// above the CRD CEL enum — admission should already have rejected
	// such a CR per the spec.type kubebuilder validation marker.
	ErrUnknownSource = errors.New("sources: unknown source type")
)
