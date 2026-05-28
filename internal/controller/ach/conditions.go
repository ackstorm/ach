// SPDX-License-Identifier: Apache-2.0

// Reason constants + shared condition-writer for the Plugin / Prompt /
// Artifact reconcilers' SourceReachable condition (Hub §6.6 closed set).
//
// PluginMarketplace's Synced condition reason vocabulary
// (NameConflict / UpstreamInvalid / InvalidConfig /
// UnsupportedPluginSource) is owned by Plan 02-06 and is intentionally
// not declared here — the marketplace reconciler imports a superset of
// these constants AND its own.

package ach

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ackstorm/ach/internal/sources"
)

// Hub §6.6 closed-set reason vocabulary for the SourceReachable
// condition emitted by Plugin / Prompt / Artifact reconcilers.
const (
	// ReasonSynced is the terminal positive outcome — the most recent
	// reconcile fetched the upstream, staged via .tmp/<random>, fsync'd,
	// rename(2)'d into the published path, and UPSERTed the external_refs
	// row successfully.
	ReasonSynced = "Synced"

	// ReasonUnreachable indicates a transient network condition or a
	// non-classifiable upstream error (TCP reset, DNS failure, context
	// deadline, HTTP 5xx, rename(2) errno). Caller surfaces
	// SourceReachable=False, reason=Unreachable. controller-runtime
	// workqueue exponential backoff applies.
	ReasonUnreachable = "Unreachable"

	// ReasonUnauthorized indicates an auth failure: HTTP 401/403, SDK auth
	// error, missing auth Secret, missing auth-data key inside the Secret.
	// Auth Secret rotation observed on the next informer event will flip
	// this back to Synced; the reconciler does NOT exponential-backoff on
	// this reason (returns RequeueAfter, not err).
	ReasonUnauthorized = "Unauthorized"

	// ReasonNotFound indicates the upstream entity does not exist (HTTP
	// 404, S3 NoSuchKey, GCS storage.ErrObjectNotExist, missing git ref).
	// Reconciler returns RequeueAfter — operator must fix the spec.
	ReasonNotFound = "NotFound"

	// ReasonUpstreamInvalid indicates 200 + malformed body, unparseable
	// content, or a constructor-rejected spec (e.g. http:// instead of
	// https://). Reconciler returns RequeueAfter — operator must fix the
	// upstream or the spec.
	ReasonUpstreamInvalid = "UpstreamInvalid"

	// ReasonInvalidConfig is reserved for PluginMarketplace RE2 compile
	// failure (Plan 02-06). Declared here so the closed-enum constants
	// live in one file; the Plugin/Prompt/Artifact reconcilers do not
	// emit this reason in v1alpha1.
	ReasonInvalidConfig = "InvalidConfig"

	// ReasonPluginTooLarge indicates the Plugin reconciler's size-cap
	// overshoot — io.LimitReader caught bytes > ACH_PLUGIN_MAX_SIZE_MIB
	// before rename(2), the staging file was deleted, and no oversized
	// file ever reached the cache PVC. Reconciler returns RequeueAfter
	// (no backoff — the spec must change for this to clear).
	ReasonPluginTooLarge = "PluginTooLarge"

	// ReasonStaleCacheExpired indicates a fetch failure escalated past
	// the staleness window: now − last_successful_refresh > maxStaleness
	// AND the latest fetch attempt failed. Distinct from Unreachable
	// because Content Service uses this reason to decide whether to
	// serve the stale cached bytes or return 503 stale_cache_expired.
	ReasonStaleCacheExpired = "StaleCacheExpired"

	// ReasonInitializing is the Phase 1 carry-forward reason — written
	// before the first reconcile fetch attempt. Phase 2's steady-state
	// branch flips this to Synced or one of the failure reasons; this
	// constant stays in the file for parity with the previous status
	// writes the deletion-path or pre-fetch path may produce.
	ReasonInitializing = "Initializing"

	// ReasonNameConflict (Plan 02-06) — PluginMarketplace Synced=False
	// when ANY of this marketplace's post-filter candidates lost the
	// cross-marketplace name-conflict resolution to an alphabetically-
	// earlier marketplace (OP-08 / Hub §12.3). The status.message lists
	// the specific plugin name(s) that lost; the winning marketplace
	// keeps Synced=True. Plugin-CRD-wins drops do NOT flip Synced —
	// they're recorded with ReasonPluginCRDPrecedence (below) as
	// informational status.message annotations only.
	ReasonNameConflict = "NameConflict"

	// ReasonPluginCRDPrecedence (Plan 02-06 / WR-09) — used in
	// per-entry pluginFailure records when a marketplace plugin entry
	// was dropped because a Plugin CRD owns the same name (Hub §12.3
	// conflict resolution: Plugin CRDs take precedence over
	// marketplace entries). The marketplace's Synced condition is NOT
	// flipped by this drop — only ReasonNameConflict (a
	// marketplace-loser to another marketplace) flips Synced.
	// status.message reports the dropped plugin with this distinct
	// reason so an operator reading
	// `kubectl describe pluginmarketplace` can tell Plugin-CRD-wins
	// drops apart from marketplace-loser drops at a glance.
	ReasonPluginCRDPrecedence = "PluginCRDPrecedence"

	// ReasonUnsupportedPluginSource (Plan 02-06 / issue #15) — a
	// marketplace entry resolved to Source.Kind="" because its wire
	// shape is not materializable. Covers:
	//
	//   - Known-but-unsupported discriminators (today: "npm" — the
	//     v1alpha1 operator is git-only).
	//   - Unknown discriminators (any other source.source value).
	//   - Required-field gaps the parser couldn't recover from
	//     (e.g. git-subdir without url+path, github without repo,
	//     local-path with path-traversal).
	//
	// Per-entry only — the marketplace's Synced condition stays True if
	// Stage-1 succeeded; the rejected entry is recorded in
	// status.message via the partial-failure path.
	ReasonUnsupportedPluginSource = "UnsupportedPluginSource"

	// ─── Hub §6.6 closed-set reasons for the Environment.Available
	// rollup (TODO §9). The Environment reconciler is the only writer.
	// ───────────────────────────────────────────────────────────────

	// ReasonAllSubConditionsTrue is the terminal positive outcome for
	// the Environment.Available rollup — every required sub-condition
	// (AccessGroupSynced, ExecutionResourcesResolved) is True. Mirrors
	// the §16 acceptance YAML shape (TODO.md:505) verbatim so the
	// validation gate compares against a stable string.
	ReasonAllSubConditionsTrue = "AllSubConditionsTrue"

	// ReasonSubConditionsNotReady is the degraded outcome — at least
	// one required sub-condition is False. message includes the failing
	// sub-condition names so operators reading `kubectl describe
	// environment` can pivot without re-querying.
	ReasonSubConditionsNotReady = "SubConditionsNotReady"

	// ReasonPendingSubConditions is the in-flight outcome — at least
	// one required sub-condition is Unknown and none are False.
	// Pre-§7 this is the steady-state because AccessGroupSynced is the
	// J.6 placeholder Unknown.
	ReasonPendingSubConditions = "PendingSubConditions"
)

// setExternalRefCondition writes a Hub §6.6 condition into a status
// conditions slice via apimeta.SetStatusCondition (which preserves
// LastTransitionTime when the (Type, Status) tuple does not change).
//
// Used by Plugin / Prompt / Artifact reconcilers as the single condition-
// write callsite; the reconciler-side r.Status().Update issuing the API
// PATCH is left to the caller so it can batch other status-field updates
// (StorageLocation, UpstreamRev, LastSuccessfulRefresh, ObservedGeneration)
// in the same round-trip.
//
// observedGen is the cr.Generation so consumers can detect "the
// reconciler has seen this revision."
func setExternalRefCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string, observedGen int64) {
	apimeta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: observedGen,
		LastTransitionTime: metav1.Now(),
	})
}

// Transport label constants surfaced on the SourceReachable / Synced
// condition message. The "rest" / "git" string values match the
// kubebuilder enum on GitHubSource.Transport / GitLabSource.Transport /
// BitbucketSource.Transport.
const (
	transportLabelGit  = "git"
	transportLabelRest = "rest"
	transportLabelNA   = "n/a"
)

// Source-type discriminators matching sources.SourceSpec.Type values
// and the registry dispatch enum. Extracted as constants because
// goconst flags 5+ occurrences across the resolveTransportName switch.
const (
	sourceTypeGitHub    = "github"
	sourceTypeGitLab    = "gitlab"
	sourceTypeBitbucket = "bitbucket"
)

// resolveTransportName reports the wire path the outer fetch took for
// the given SourceSpec. Surfaced on the SourceReachable / Synced
// condition message so operators can see which transport actually
// served the request during the one-release window in which both
// transports coexist (FIX_GIT.txt). Returns:
//
//	"git"  — github/gitlab/bitbucket source with Transport != "rest"
//	         (or empty, defaults to git per kubebuilder default).
//	"rest" — github/gitlab/bitbucket source with Transport == "rest"
//	         (the one-release legacy escape hatch).
//	"n/a"  — s3 / gcs / http source (no git transport applies).
func resolveTransportName(sourceSpec sources.SourceSpec) string {
	// Dispatch on Type — the registry (internal/sources/registry) also
	// dispatches by sourceSpec.Type, so the label reported here matches
	// the fetcher actually invoked. If CEL admission is ever bypassed
	// and multiple per-type pointers are non-nil, this avoids reporting
	// whichever pointer happened to be checked first instead of the
	// one Type actually selected. PR #9 follow-up review finding #8.
	switch sourceSpec.Type {
	case sourceTypeGitHub:
		if sourceSpec.GitHub != nil && sourceSpec.GitHub.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	case sourceTypeGitLab:
		if sourceSpec.GitLab != nil && sourceSpec.GitLab.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	case sourceTypeBitbucket:
		if sourceSpec.Bitbucket != nil && sourceSpec.Bitbucket.Transport == transportLabelRest {
			return transportLabelRest
		}
		return transportLabelGit
	default:
		return transportLabelNA
	}
}

// sourceReachableMessage returns the condition.Message format used by
// the per-kind reconcilers on success: "transport=<git|rest|n/a>".
// Keeps the format string centrally so the per-kind controllers stay
// surgical.
func sourceReachableMessage(sourceSpec sources.SourceSpec) string {
	return "transport=" + resolveTransportName(sourceSpec)
}
