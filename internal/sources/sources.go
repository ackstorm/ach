// SPDX-License-Identifier: Apache-2.0

// Package sources declares the source-type-agnostic upstream-fetch
// contract consumed by the Plugin / Prompt / Artifact / PluginMarketplace
// reconcilers (Hub §10, §11, §12).
//
// Each per-source-type subpackage (`github`, `gitlab`, `bitbucket`, `s3`,
// `gcs`, `http`) implements the [Fetcher] interface declared here and is
// reached via [For] in registry.go. Fetchers return a streaming
// io.ReadCloser plus a [FetchResult.UpstreamRev] discriminator — they do
// NOT touch the filesystem, do NOT enforce size caps, and do NOT update
// the database. Those are the reconciler's responsibilities per D-05
// (see .planning/phases/02-external-refs-marketplace-operator-reconciliation/02-CONTEXT.md).
//
// Plug-in size cap enforcement (D-12) is the RECONCILER's responsibility:
// the Plugin reconciler wraps [FetchResult.Body] in `io.LimitReader(body,
// max+1)` and deletes the staging file on overshoot. Prompt / Artifact /
// PluginMarketplace reconcilers do not enforce a per-resource cap in
// v1alpha1 — that keeps this package universal.
package sources

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// SourceSpec is the discriminated union the [Registry] uses to dispatch.
// The reconciler builds one from a Plugin / Prompt / Artifact /
// PluginMarketplace CR's spec block. Exactly one of the per-type
// subobject pointers is non-nil per Type — the matching subobject MUST
// be populated when Type == "<kind>". CRD admission (CEL XValidation
// rule per CRD-03) enforces this at the API server; [For] performs the
// same check defensively so the reconciler can report InvalidConfig
// instead of nil-dereferencing if a CEL bug ever allows a malformed CR
// through.
type SourceSpec struct {
	// Type is one of "github", "gitlab", "bitbucket", "s3", "gcs", "http".
	Type string

	GitHub    *achv1alpha1.GitHubSource
	GitLab    *achv1alpha1.GitLabSource
	Bitbucket *achv1alpha1.BitbucketSource
	S3        *achv1alpha1.S3Source
	GCS       *achv1alpha1.GCSSource
	HTTP      *achv1alpha1.HTTPSource
}

// FetchRequest is the per-reconcile-cycle input handed to [Fetcher.Fetch].
type FetchRequest struct {
	// Spec carries the source-type discriminator + the matching per-type
	// subobject (typically derived from the originating CR's spec).
	Spec SourceSpec

	// Secret is the resolved Kubernetes Secret carrying upstream
	// credentials. The reconciler resolves it via the controller-runtime
	// cached client.Get against spec.AuthSecretRef. May be nil for
	// HTTPSource without auth (the only source-type whose AuthSecretRef
	// is itself optional).
	Secret *corev1.Secret

	// PriorRev is the UpstreamRev returned by the most recent successful
	// [Fetcher.Fetch] (typically read from the external_refs row's
	// upstream_rev column). Empty on first reconcile.
	//
	// Fetchers use this for conditional-fetch semantics:
	//   - git fetchers (github/gitlab/bitbucket) compare against the
	//     resolved commit SHA at HEAD of spec.Ref; equal → NotModified.
	//   - S3 fetcher passes it as If-None-Match against the object ETag.
	//   - GCS fetcher compares against the object generation.
	//   - HTTP fetcher splits "ETag|Last-Modified" and sets both
	//     If-None-Match and If-Modified-Since.
	PriorRev string
}

// FetchResult is the output of [Fetcher.Fetch].
type FetchResult struct {
	// Body is the upstream payload stream. The caller MUST close it
	// (`defer body.Close()`) on every code path. Nil/empty when
	// NotModified is true. Reconcilers MUST drain the body in addition
	// to closing it (typically via `io.Copy(stagingFile, limited)`
	// which drains, or an explicit drainAndClose helper) — REL-04.
	Body io.ReadCloser

	// UpstreamRev is the source-type-specific revision identifier used
	// for conditional-fetch semantics on the next reconcile cycle:
	//   - github/gitlab/bitbucket: commit SHA at HEAD of spec.Ref.
	//   - s3:    object ETag (quotes stripped).
	//   - gcs:   object generation, decimal-formatted.
	//   - http:  "ETag|Last-Modified" composite — fetcher-internal
	//            convention used by the conditional-GET branch of
	//            internal/sources/http/Fetcher.Fetch.
	UpstreamRev string

	// NotModified is true when the conditional fetch hit (HTTP 304 / S3
	// 304-equivalent / SHA equality / generation equality). When true,
	// Body is nil and the caller MUST keep the prior cached file
	// unchanged. UpstreamRev is preserved verbatim from PriorRev so
	// callers may safely write it back to the external_refs row.
	NotModified bool
}

// Fetcher is the source-type-agnostic upstream-fetch contract. Every
// per-source-type subpackage's *Fetcher (created via its New(...) ctor)
// satisfies this interface — see [For] for the dispatch table.
type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error)
}
