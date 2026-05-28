// SPDX-License-Identifier: Apache-2.0

// External-reference refresh helper shared by the Plugin / Prompt /
// Artifact reconcilers. Implements §10.3 steps 1-7 (resolve auth Secret,
// dispatch via sources.For, Fetch, conditional-GET shortcut, .tmp staging
// + LimitReader cap + fsync + atomic rename(2), DB UpsertExternalRef)
// behind a single materializeExternalRef function so per-kind
// reconcilers stay thin.
//
// D-04 / D-05: fetcher returns a stream; reconciler owns .tmp lifecycle.
// D-11: Secret reads via the controller-runtime cached client (informer).
// D-12: Plugin size cap via io.LimitReader BEFORE rename(2). No torn-byte
//       oversized file ever reaches the cache PVC — overshoot deletes the
//       staging file and surfaces OversizeError to the caller.

package ach

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/pluginpack"
	"github.com/ackstorm/ach/internal/sources/registry"
)

// FetcherFactory is the indirection the reconciler uses to construct a
// [sources.Fetcher] per [sources.SourceSpec]. Production wires
// registry.For via the per-reconciler struct's Fetchers field (or nil to
// default — see materializeExternalRef); envtest injects an in-memory
// fake fetcher for deterministic assertions on the §10.3 staging /
// rename(2) / UPSERT branches.
type FetcherFactory func(sources.SourceSpec) (sources.Fetcher, error)

// OversizeError is returned by [materializeExternalRef] when the Plugin
// size cap is exceeded. [classifyFetchError] maps it to
// [ReasonPluginTooLarge]. The two byte counts are the observed staging
// length (always > Cap by definition) and the cap itself in bytes.
type OversizeError struct {
	Bytes int64
	Cap   int64
}

// Error formats the human-readable status.message. Both numbers are byte
// counts (Cap = ACH_PLUGIN_MAX_SIZE_MIB << 20); the message MUST NOT
// echo Secret values or auth header contents (threat T-02-05-04).
func (e *OversizeError) Error() string {
	return fmt.Sprintf("staged %d bytes exceeds ACH_PLUGIN_MAX_SIZE_MIB cap of %d bytes", e.Bytes, e.Cap)
}

// ExternalRefRefreshDeps bundles the per-reconciler dependencies the
// shared [materializeExternalRef] needs. Plugin / Prompt / Artifact
// reconcilers build one per Reconcile call.
type ExternalRefRefreshDeps struct {
	// Client is the controller-runtime cached client used to Get the
	// auth Secret from the deployment namespace (D-11). Reads after
	// informer warmup are sub-millisecond.
	Client client.Client

	// Namespace is the deployment namespace — the only namespace the
	// Operator watches per MULTI-01. Secret references resolve here.
	Namespace string

	// DB is the Postgres pool; nil-tolerant for the Phase 1 envtest path
	// (the reconciler decides at its callsite whether the DB UPSERT is
	// skipped). When non-nil, materializeExternalRef calls
	// [achdb.UpsertExternalRef] after a successful rename(2).
	DB *pgxpool.Pool

	// CacheRoot is the PVC mount root. Staging files live under
	// CacheRoot+/.tmp; final paths live under CacheRoot+/{plugin,prompt,
	// artifact}/<name>[.tar.gz].
	CacheRoot string

	// Kind is one of "plugin" / "prompt" / "artifact" — drives the
	// external_refs.kind column.
	Kind string

	// Name is the CR metadata.name — drives the external_refs.name
	// column AND the final path leaf.
	Name string

	// SourceSpec is the discriminator + per-type subobject built from
	// cr.Spec by the per-kind reconciler.
	SourceSpec sources.SourceSpec

	// AuthSecretRef is the resolved per-source-type AuthSecretRef from
	// the matching subobject. Nil for HTTPSource without authentication
	// (the only source-type whose AuthSecretRef is itself optional).
	AuthSecretRef *achv1alpha1.SourceAuthSecretRef

	// Refresh carries spec.refresh.{interval, maxStaleness}. NextRefreshAt
	// is computed from Interval (when set) or maxStaleness/2 (fallback).
	Refresh achv1alpha1.RefreshBlock

	// PriorRev is the upstream_rev recorded by the most recent successful
	// refresh. Read from external_refs.upstream_rev by the per-kind
	// reconciler before calling. Empty on first reconcile.
	PriorRev string

	// SizeCapBytes enforces the Plugin size cap when > 0. 0 = no cap
	// (Prompt / Artifact). Plugin reconciler passes
	// int64(PluginMaxSizeMiB) << 20.
	SizeCapBytes int64

	// FinalPath is the absolute path the rename(2) targets. The per-kind
	// reconciler computes it via [computeFinalPath] before calling — the
	// helper does NOT know the per-kind suffix conventions.
	FinalPath string

	// Fetchers is the FetcherFactory. nil → defaults to registry.For
	// (the production dispatcher). Tests inject a fake.
	Fetchers FetcherFactory

	// Log carries the per-reconcile logger; materializeExternalRef
	// currently does not log on the success path (the per-kind
	// reconciler logs once per Reconcile after success) but reserves
	// the field for future structured-error logging.
	Log logr.Logger
}

// MaterializeResult is the output [materializeExternalRef] returns to the
// per-kind reconciler.
type MaterializeResult struct {
	// UpstreamRev is the value to write into external_refs.upstream_rev
	// AND status.upstreamRev. Empty on Err != nil; equal to deps.PriorRev
	// when NotModified is true.
	UpstreamRev string

	// NotModified is true when the conditional fetch hit (HTTP 304 / SHA
	// equality / generation equality). When true, the caller MUST skip
	// the rename(2) and keep the prior cached file unchanged. The
	// last_successful_refresh wall-clock SHOULD still be bumped via DB
	// UpsertExternalRef so the staleness predicate stays accurate, but
	// the helper itself does not perform the UPSERT in the NotModified
	// branch — that decision belongs to the caller (which may want to
	// preserve UPSERT-clears-force-refresh semantics).
	NotModified bool

	// Err is non-nil on failure. Wraps a [sources.Err*] sentinel where
	// possible so [classifyFetchError] can map to a [Reason*] string.
	Err error
}

// materializeExternalRef runs §10.3 steps 1-7 for one CR:
//
//  1. Resolve auth Secret via deps.Client.Get.
//  2. Dispatch via deps.Fetchers (nil → registry.For).
//  3. fetcher.Fetch(ctx, FetchRequest{Spec, Secret, PriorRev}).
//  4. NotModified shortcut → return early without staging or UPSERT.
//  5. Stage at deps.CacheRoot/.tmp/stg-<random> via os.CreateTemp.
//     (issue #26) When deps.Kind == "plugin", apply pluginpack.Filter
//     to result.Body BEFORE the size-cap copy so the cap measures the
//     filtered bytes. Prompt/Artifact paths are byte-identical to
//     pre-issue-26 behavior.
//  6. Wrap body in io.LimitReader when SizeCapBytes > 0; io.Copy; check
//     overshoot (overshoot → delete staging file → OversizeError); fsync.
//  7. Atomic rename(2) staging → deps.FinalPath.
//  8. db.UpsertExternalRef (when deps.DB != nil).
//
// Crash safety: if the operator dies between rename(2) (step 7) and
// UpsertExternalRef (step 8), the file lives at the published path but
// the DB row carries the prior upstream_rev (or no row at all). The next
// reconcile fetches fresh, stages, renames over the existing file
// (atomic per POSIX), and UPSERTs — idempotent.
func materializeExternalRef(ctx context.Context, deps ExternalRefRefreshDeps) MaterializeResult {
	// ─── Step 1: resolve auth Secret (D-11 informer cache). ───
	var secretObj corev1.Secret
	if deps.AuthSecretRef != nil {
		key := types.NamespacedName{Namespace: deps.Namespace, Name: deps.AuthSecretRef.Name}
		if err := deps.Client.Get(ctx, key, &secretObj); err != nil {
			if apierrors.IsNotFound(err) {
				return MaterializeResult{Err: fmt.Errorf("auth Secret %q: %w", deps.AuthSecretRef.Name, sources.ErrUnauthorized)}
			}
			return MaterializeResult{Err: fmt.Errorf("Secret Get %q: %w", deps.AuthSecretRef.Name, err)}
		}
	}

	// ─── Step 2: dispatch to the per-source-type fetcher. ───
	factory := deps.Fetchers
	if factory == nil {
		factory = registry.For
	}
	fetcher, err := factory(deps.SourceSpec)
	if err != nil {
		return MaterializeResult{Err: err}
	}

	// ─── Step 3: fetch. ───
	var secretArg *corev1.Secret
	if deps.AuthSecretRef != nil {
		secretArg = &secretObj
	}
	result, err := fetcher.Fetch(ctx, sources.FetchRequest{
		Spec:     deps.SourceSpec,
		Secret:   secretArg,
		PriorRev: deps.PriorRev,
	})
	if err != nil {
		return MaterializeResult{Err: err}
	}

	// ─── Step 4: NotModified shortcut. ───
	if result.NotModified {
		// Body is nil on 304 per FetchResult contract; nothing to close.
		return MaterializeResult{NotModified: true, UpstreamRev: deps.PriorRev}
	}

	// ─── Step 5: stage at .tmp/stg-<random>. ───
	if result.Body == nil {
		return MaterializeResult{Err: fmt.Errorf("fetcher returned nil body without NotModified: %w", sources.ErrUpstreamInvalid)}
	}
	defer result.Body.Close()

	// ─── Step 5.5: kind-specific body transform (issue #26). ───
	// TODO(#26-followup): generalize to BodyTransform once marketplace path consumes the same filter.
	// For Plugin CRs only, run the upstream tarball through the
	// pluginpack content filter before the size-cap copy. This means
	// the existing io.LimitReader cap below operates on FILTERED bytes
	// — the "size cap applies POST-filter" decision locked in
	// CONTEXT.md. Prompt and Artifact paths are byte-identical to
	// pre-issue-26 behavior.
	if deps.Kind == "plugin" {
		// Defense in depth: bound the raw upstream read BEFORE the
		// filter consumes it. The user-visible SizeCapBytes applies to
		// the filtered output (locked decision); pluginRawIngressCap
		// is a separate operator-memory guard against a multi-GB
		// tarball reaching this path (most acute for type: http where
		// the fetcher has no body cap). Sized to mirror
		// gitDefaultMaxCloneBytes (512 MiB) — the existing ceiling on
		// git-transport clones.
		const pluginRawIngressCap = 512 << 20
		raw, err := io.ReadAll(io.LimitReader(result.Body, pluginRawIngressCap+1))
		if err != nil {
			return MaterializeResult{Err: fmt.Errorf("plugin filter: read body: %w", err)}
		}
		if int64(len(raw)) > pluginRawIngressCap {
			return MaterializeResult{Err: &OversizeError{Bytes: int64(len(raw)), Cap: pluginRawIngressCap}}
		}
		filtered, err := pluginpack.Filter(raw)
		if err != nil {
			// The error already wraps sources.ErrUpstreamInvalid via
			// ErrManifestMissing or the traversal-rejection path;
			// classifyFetchError below maps it to ReasonUpstreamInvalid.
			return MaterializeResult{Err: err}
		}
		// Rebind result.Body so the existing Step 6 LimitReader sees
		// the filtered bytes. The deferred Close() above was registered
		// against the ORIGINAL result.Body (Go defer receivers are
		// evaluated at defer-time), so the upstream reader is still
		// closed on every exit path; the fresh NopCloser is never
		// explicitly closed (Close on a bytes.Reader-backed NopCloser
		// is a no-op).
		result.Body = io.NopCloser(bytes.NewReader(filtered))
	}

	tmpFile, err := os.CreateTemp(filepath.Join(deps.CacheRoot, ".tmp"), "stg-")
	if err != nil {
		return MaterializeResult{Err: fmt.Errorf("create staging file: %w", err)}
	}
	stagingPath := tmpFile.Name()
	// Use a closeOnce-style flag so explicit Close before rename(2) does
	// not double-close at defer time (os.File.Close on a closed file
	// returns ErrInvalid, which would mask the rename's err).
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
	}()

	// ─── Step 6: copy with optional size cap. ───
	var n int64
	var copyErr error
	if deps.SizeCapBytes > 0 {
		// LimitReader bound to Cap+1 so we can detect overshoot exactly:
		// if io.Copy returns Cap+1, the upstream advertised >Cap bytes.
		limited := io.LimitReader(result.Body, deps.SizeCapBytes+1)
		n, copyErr = io.Copy(tmpFile, limited)
	} else {
		n, copyErr = io.Copy(tmpFile, result.Body)
	}
	if copyErr != nil {
		_ = os.Remove(stagingPath)
		return MaterializeResult{Err: fmt.Errorf("staging copy: %w", copyErr)}
	}
	if deps.SizeCapBytes > 0 && n > deps.SizeCapBytes {
		_ = os.Remove(stagingPath)
		return MaterializeResult{Err: &OversizeError{Bytes: n, Cap: deps.SizeCapBytes}}
	}

	// fsync before rename so a power cut does not leave a zero-length file
	// at the published path. (rename(2) preserves whatever data was on
	// disk at the moment of the rename — if the buffer never reached the
	// platter, the post-crash file is short.)
	if err := tmpFile.Sync(); err != nil {
		_ = os.Remove(stagingPath)
		return MaterializeResult{Err: fmt.Errorf("staging fsync: %w", err)}
	}
	// Close explicitly so platforms with strict rename-of-open-fd semantics
	// (Windows; some FUSE FS) behave correctly. POSIX allows rename of an
	// open file, but explicit close is portable and harmless on Linux.
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return MaterializeResult{Err: fmt.Errorf("staging close: %w", err)}
	}
	closed = true

	// ─── Step 7: atomic rename(2). ───
	if err := os.Rename(stagingPath, deps.FinalPath); err != nil {
		// Rename failure is typically ENOSPC / EACCES / cross-filesystem
		// (which can't happen here — both paths live under CacheRoot, but
		// FUSE/bind-mount setups have caught us before). errno wrapping
		// per OP-04; classifyFetchError defaults to ReasonUnreachable.
		_ = os.Remove(stagingPath)
		return MaterializeResult{Err: fmt.Errorf("§10.3 rename(2): %w", err)}
	}

	// ─── Step 8: UPSERT external_refs (when DB available). ───
	if deps.DB != nil {
		now := time.Now()
		next := now.Add(requeueDurationFromRefresh(deps.Refresh))
		ref := achdb.ExternalRef{
			Kind:                  deps.Kind,
			Name:                  deps.Name,
			StorageLocation:       deps.FinalPath,
			UpstreamRev:           result.UpstreamRev,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         next,
			MaxStalenessSeconds:   int64(deps.Refresh.MaxStaleness.Duration.Seconds()),
		}
		if err := achdb.UpsertExternalRef(ctx, deps.DB, ref); err != nil {
			return MaterializeResult{Err: fmt.Errorf("db upsert: %w", err)}
		}
	}

	return MaterializeResult{UpstreamRev: result.UpstreamRev}
}

// classifyFetchError maps an error returned by [materializeExternalRef]
// to a (reason, message) pair drawn from the Hub §6.6 closed enum.
//
// Staleness escalation (OP-04): when refresh has succeeded at least once
// AND now − lastRefresh > maxStaleness AND the latest fetch failed with
// a transient classification, the reason flips from ReasonUnreachable
// (or whatever the underlying classification would have been) to
// ReasonStaleCacheExpired so Content Service can decide whether to serve
// the stale bytes or 503 stale_cache_expired.
//
// The OversizeError, Unauthorized, NotFound, and UpstreamInvalid cases
// do NOT escalate to StaleCacheExpired — those are configuration-derived
// or terminal-upstream errors; the staleness predicate applies only to
// transient reachability failures.
func classifyFetchError(err error, refresh achv1alpha1.RefreshBlock, lastRefresh time.Time) (reason, message string) {
	if err == nil {
		return ReasonSynced, ""
	}
	var oe *OversizeError
	switch {
	case errors.As(err, &oe):
		return ReasonPluginTooLarge, err.Error()
	case errors.Is(err, sources.ErrUnauthorized):
		return ReasonUnauthorized, err.Error()
	case errors.Is(err, sources.ErrNotFound):
		return ReasonNotFound, err.Error()
	case errors.Is(err, sources.ErrUpstreamInvalid):
		return ReasonUpstreamInvalid, err.Error()
	}
	// Default: transient / Unreachable. Apply staleness escalation if a
	// prior successful refresh exists AND the staleness window has
	// elapsed.
	if !lastRefresh.IsZero() && refresh.MaxStaleness.Duration > 0 {
		age := time.Since(lastRefresh)
		if age > refresh.MaxStaleness.Duration {
			return ReasonStaleCacheExpired, fmt.Sprintf("upstream unreachable for %s; cache expired: %v", age.Round(time.Second), err)
		}
	}
	return ReasonUnreachable, err.Error()
}

// computeFinalPath derives the §10.3 publish path per the cache layout
// table in 02-CONTEXT.md:
//
//   - plugin   → CacheRoot/plugin/<name>.tar.gz
//   - prompt   → CacheRoot/prompt/<name>            (raw bytes)
//   - artifact + scope=object    → CacheRoot/artifact/<name>
//   - artifact + scope=directory → CacheRoot/artifact/<name>.tar.gz
//
// Returns the empty string on an unrecognized (kind, scope) tuple; the
// caller should treat that as a fatal bug (only reachable if the per-
// kind reconciler passes an unknown kind, which it never does).
func computeFinalPath(cacheRoot, kind, name, scope string) string {
	switch kind {
	case "plugin":
		return filepath.Join(cacheRoot, "plugin", name+".tar.gz")
	case "prompt":
		return filepath.Join(cacheRoot, "prompt", name)
	case "artifact":
		switch scope {
		case "object":
			return filepath.Join(cacheRoot, "artifact", name)
		case "directory":
			return filepath.Join(cacheRoot, "artifact", name+".tar.gz")
		}
	}
	return ""
}

// buildSourceSpec constructs a [sources.SourceSpec] discriminator from
// the per-source-type pointer fields the Plugin / Prompt / Artifact
// specs expose. Exactly one pointer should be non-nil per specType (CRD
// admission enforces this via CEL XValidation per CRD-03); this helper
// does NOT enforce the invariant — it forwards every pointer and lets
// [registry.For] do the defensive nil check.
func buildSourceSpec(specType string, github *achv1alpha1.GitHubSource, gitlab *achv1alpha1.GitLabSource, bitbucket *achv1alpha1.BitbucketSource, s3 *achv1alpha1.S3Source, gcs *achv1alpha1.GCSSource, http *achv1alpha1.HTTPSource) sources.SourceSpec {
	return sources.SourceSpec{
		Type:      specType,
		GitHub:    github,
		GitLab:    gitlab,
		Bitbucket: bitbucket,
		S3:        s3,
		GCS:       gcs,
		HTTP:      http,
	}
}

// extractAuthSecretRef returns the AuthSecretRef from whichever per-type
// subobject is non-nil for specType. HTTPSource / GitHubSource /
// GitLabSource / BitbucketSource carry pointer-typed AuthSecretRef
// (auth is optional — Phase 02.1 removed the requirement on SCM sources
// to support anonymous public-repo fetches); S3 / GCS embed it by value
// (cloud-storage providers do not admit anonymous mode in v1alpha1).
//
// Returns nil when (a) specType matches a kind whose AuthSecretRef
// pointer is nil (anonymous fetch is the operator's intent), or (b)
// specType matches a kind whose matching subobject is nil (the CR is
// malformed; registry.For will catch it shortly).
func extractAuthSecretRef(specType string, github *achv1alpha1.GitHubSource, gitlab *achv1alpha1.GitLabSource, bitbucket *achv1alpha1.BitbucketSource, s3 *achv1alpha1.S3Source, gcs *achv1alpha1.GCSSource, httpSrc *achv1alpha1.HTTPSource) *achv1alpha1.SourceAuthSecretRef {
	switch specType {
	case "github":
		if github == nil {
			return nil
		}
		return github.AuthSecretRef
	case "gitlab":
		if gitlab == nil {
			return nil
		}
		return gitlab.AuthSecretRef
	case "bitbucket":
		if bitbucket == nil {
			return nil
		}
		return bitbucket.AuthSecretRef
	case "s3":
		if s3 == nil {
			return nil
		}
		return &s3.AuthSecretRef
	case "gcs":
		if gcs == nil {
			return nil
		}
		return &gcs.AuthSecretRef
	case "http":
		if httpSrc == nil {
			return nil
		}
		return httpSrc.AuthSecretRef
	}
	return nil
}

// shouldSkipFetch decides whether the per-kind reconciler may skip the
// materializeExternalRef call this turn and return immediately with
// ctrl.Result{RequeueAfter} pointing at the next interval boundary.
//
// Skip iff ALL of:
//   - lastRefresh is non-zero (CR has at least one successful prior fetch)
//   - now < lastRefresh + interval (within the refresh window)
//   - observedGeneration == generation (no spec change since last reconcile)
//   - "ach.ackstorm.ai/force-refresh" annotation is absent
//
// Rationale: the operator was burning ~3 GitHub REST calls per CR per
// reconcile event (GetCommit + GetArchiveLink + tarball GET), and the
// reconciler is triggered by every status update, pod restart, spec
// re-apply, and periodic RequeueAfter. With three CRs and a handful of
// dev cycles per hour that exceeds GitHub's anonymous 60 req/hour/IP
// ceiling. Gating on the refresh window cuts steady-state burn ~10x
// without changing correctness — spec change, annotation, and the
// next-interval timer still trigger fresh fetches.
//
// Pure: no I/O, no logging. Caller computes time.Now() at the same
// instant as cr.Get() to avoid TOCTOU between gate eval and Fetch call.
func shouldSkipFetch(
	refresh achv1alpha1.RefreshBlock,
	lastRefresh time.Time,
	observedGen, generation int64,
	annotations map[string]string,
	now time.Time,
) bool {
	if lastRefresh.IsZero() {
		return false
	}
	if observedGen != generation {
		return false
	}
	if _, hasForce := annotations["ach.ackstorm.ai/force-refresh"]; hasForce {
		return false
	}
	window := requeueDurationFromRefresh(refresh)
	return now.Before(lastRefresh.Add(window))
}

// requeueDurationFromRefresh returns the RequeueAfter duration to use
// after a successful (or terminal-fail) reconcile. When spec.refresh.interval
// is set, use that; otherwise fall back to maxStaleness/2 so the reconciler
// stays ahead of the staleness predicate. Used by both materializeExternalRef
// (for NextRefreshAt computation) and the per-kind Reconcile bodies (for
// ctrl.Result{RequeueAfter}).
func requeueDurationFromRefresh(r achv1alpha1.RefreshBlock) time.Duration {
	if r.Interval != nil && r.Interval.Duration > 0 {
		return r.Interval.Duration
	}
	if r.MaxStaleness.Duration > 0 {
		return r.MaxStaleness.Duration / 2
	}
	// Defensive default: 1h. CRD-04 requires maxStaleness so this branch
	// is effectively unreachable for admission-validated CRs, but envtest
	// fixtures that bypass admission may hit it.
	return time.Hour
}
