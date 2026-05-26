// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package sources

import "errors"

// Sentinel errors classifying upstream-fetch failures. Per-source-type
// fetchers wrap these via `fmt.Errorf("...: %w", sources.ErrXxx)` so
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
	// returned 200 OK with an unparsable body, the configured URL/repo
	// has an invalid shape, or the HTTPSource URL is not https://.
	// Caller surfaces SourceReachable=False, reason=UpstreamInvalid.
	ErrUpstreamInvalid = errors.New("sources: upstream invalid")

	// ErrUnknownSource indicates the [Registry] received a SourceSpec
	// whose Type is not one of the six enum values. Defense in depth
	// above the CRD CEL enum — admission should already have rejected
	// such a CR per the spec.type kubebuilder validation marker.
	ErrUnknownSource = errors.New("sources: unknown source type")
)

// ReasonOf classifies err into a Hub §6.6 SourceReachable.reason enum
// value. The returned string MUST be passed verbatim into
// apimeta.SetStatusCondition's Reason field by Plan 02-05/02-06
// reconcilers — the value is part of the wire contract with kubectl-
// describe consumers and any downstream alerting that filters on the
// reason enum.
//
// Dispatch order is deterministic and uses errors.Is against the
// sentinels:
//
//  1. [ErrUnauthorized]     → "Unauthorized"
//  2. [ErrNotFound]          → "NotFound"
//  3. [ErrUpstreamInvalid]   → "UpstreamInvalid"
//  4. [ErrUnreachable]       → "Unreachable"
//  5. (default, incl. nil)   → "Unreachable" — conservative; caller
//     retries. The default mirrors how transport errors (no wrapped
//     sentinel) are treated as transient/network issues.
//
// Callers should never see nil err pass through ReasonOf in practice —
// the function is invoked only when a Fetch returned a non-nil error —
// but the nil branch is defensive against caller bugs.
func ReasonOf(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "Unauthorized"
	case errors.Is(err, ErrNotFound):
		return "NotFound"
	case errors.Is(err, ErrUpstreamInvalid):
		return "UpstreamInvalid"
	case errors.Is(err, ErrUnreachable):
		return "Unreachable"
	default:
		return "Unreachable"
	}
}
