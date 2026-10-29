// SPDX-License-Identifier: Apache-2.0

// Reason constants + shared condition-writer for the Plugin / Prompt /
// Artifact reconcilers' SourceReachable condition (Hub §6.6 closed set).
//
// PluginMarketplace's Synced condition reason vocabulary
// (UpstreamInvalid / InvalidConfig / UnsupportedPluginSource /
// DuplicateName) is owned by Plan 02-06 and is intentionally not
// declared here — the marketplace reconciler imports a superset of
// these constants AND its own.

package ach

import (
	"context"
	"fmt"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	// ReasonDuplicateName (handoff item 5) — a per-plugin SOFT skip when a
	// single marketplace.json lists the same plugin name more than once;
	// first-wins keeps the first, the rest are recorded in status.message.
	// Never flips Synced (not passed to markSyncedFalse) → no
	// reasonToConditionStates entry.
	ReasonDuplicateName = "DuplicateName"

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

// ReasonConflictWithUIRow is the canonical Reason once emitted when a
// projection UPSERT was blocked by a UI-origin row holding the same PK.
//
// G2 reversed that model to GitOps-wins: the operator is always authoritative
// and TAKES OVER a UI-owned row (origin 'ui'→'cr') rather than declining, so
// this Reason never trips. The environment reconcile no longer references it
// (the takeover SQL never returns ErrOriginConflict). It remains defined +
// referenced by the external-ref/BIP/litellm controllers, whose origin gate is
// unchanged and which produce no origin='ui' rows in v1 (the UI Objects API
// writes Environment only) — so it is dormant there too. The UI side enforces
// the inverse fence at the API: a UI write over an operator-owned row returns
// 403 immutable_via_ui / 409 conflict_with_kubernetes_object (see
// internal/platformapi/objects + internal/db/ui_objects.go), never this Reason.
const ReasonConflictWithUIRow = "ConflictWithUIRow"

// ConflictWithUIRowMessage is the canonical condition Message paired with
// ReasonConflictWithUIRow. See [setConflictWithUIRowCondition].
const ConflictWithUIRowMessage = "projection row owned by UI; operator declines to overwrite"

// ReasonNameConflict is the canonical Reason on the Synced condition when
// another CR of the same kind claims the same identity/target and loses the
// deterministic tiebreak. Standardized across kinds (G15 / G10 checklist).
const ReasonNameConflict = "NameConflict"

// setConflictWithUIRowCondition writes the canonical ConflictWithUIRow
// Status=False condition into a status conditions slice. condType varies
// per CR family — "Synced" (plugin/prompt/artifact/BIP),
// "AccessGroupSynced" (environment), or "Ready" (litellmconnection) — so
// the caller passes it explicitly; the Reason + Message are fixed.
func setConflictWithUIRowCondition(conds *[]metav1.Condition, condType string, observedGen int64) {
	apimeta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             metav1.ConditionFalse,
		Reason:             ReasonConflictWithUIRow,
		Message:            ConflictWithUIRowMessage,
		ObservedGeneration: observedGen,
		LastTransitionTime: metav1.Now(),
	})
}

// Condition type constants. Every external-ref-shaped resource
// (Plugin, Prompt, Artifact, PluginMarketplace) surfaces the same
// two-axis status model:
//
//   - SourceReachable: did we actually obtain the bytes we asked for?
//     True iff the fetch returned a usable response body. 401/403/404
//     count as NOT reachable — the auth gate and the 404 are part of
//     "can I get what I asked for?" Network failures and staleness
//     expiry on prior Unreachable cycles also force False. Pre-fetch
//     failures (config invalid, name conflict, unsupported source)
//     leave it Unknown — no fetch was attempted, so neither reachable
//     nor unreachable can be asserted.
//
//   - Synced: did the reconciler complete its full sweep (fetch → stage
//     → verify → publish → DB upsert)? True only on the terminal happy
//     path. False on every failure including post-fetch content errors
//     (UpstreamInvalid, PluginTooLarge) where SourceReachable is True
//     but the bytes were unusable.
//
// The matrix encoded in [reasonToConditionStates] is the source of
// truth — every reason maps to exactly one (SourceReachable, Synced)
// status pair.
const (
	ConditionSourceReachable = "SourceReachable"
	ConditionSynced          = "Synced"
)

// reasonToConditionStates maps a Hub §6.6 reason to the
// (SourceReachable, Synced) status pair. The matrix:
//
//	Reason                   SourceReachable  Synced
//	Synced                   True             True
//	Unreachable              False            False
//	Unauthorized             False            False
//	NotFound                 False            False
//	UpstreamInvalid          True             False   (got bytes, content bad)
//	PluginTooLarge           True             False   (got bytes, oversized)
//	StaleCacheExpired        False            False
//	InvalidConfig            Unknown          False   (no fetch attempted)
//	UnsupportedPluginSource  Unknown          False   (pre-fetch)
//	Initializing             Unknown          False   (no fetch yet)
//	<any other>              Unknown          False   (conservative default)
func reasonToConditionStates(reason string) (sourceReachable, synced metav1.ConditionStatus) {
	switch reason {
	case ReasonSynced:
		return metav1.ConditionTrue, metav1.ConditionTrue
	case ReasonUnreachable, ReasonUnauthorized, ReasonNotFound, ReasonStaleCacheExpired:
		return metav1.ConditionFalse, metav1.ConditionFalse
	case ReasonUpstreamInvalid, ReasonPluginTooLarge:
		return metav1.ConditionTrue, metav1.ConditionFalse
	default:
		// InvalidConfig, UnsupportedPluginSource, Initializing, and any
		// reason the closed enum gains in the future without a matrix
		// update fall here. Unknown is the conservative SourceReachable
		// answer when no fetch was attempted or the post-fetch outcome
		// doesn't pin connectivity either way.
		return metav1.ConditionUnknown, metav1.ConditionFalse
	}
}

// applyReconcileConditions writes BOTH SourceReachable and Synced for
// the given reason+message tuple. The single classification call site
// keeps the two conditions in lockstep so a reconciler can't
// accidentally update one and forget the other.
//
// message is propagated to both conditions verbatim — the diagnostic
// text answers "why isn't it reachable?" and "why isn't it synced?"
// identically when both flip together (the common case). Reconcilers
// that want different per-condition messages can still call
// setExternalRefCondition directly, but the unified call is preferred
// because diverging messages quickly drift out of sync.
func applyReconcileConditions(conds *[]metav1.Condition, reason, message string, observedGen int64) {
	srStatus, syncStatus := reasonToConditionStates(reason)
	setExternalRefCondition(conds, ConditionSourceReachable, srStatus, reason, message, observedGen)
	setExternalRefCondition(conds, ConditionSynced, syncStatus, reason, message, observedGen)
}

// Transport label constants surfaced on the SourceReachable / Synced
// condition message.
const (
	transportLabelGit = "git"
	transportLabelNA  = "n/a"
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
// served the request. Returns:
//
//	"git"  — github/gitlab/bitbucket source (git is the only transport).
//	"n/a"  — s3 / gcs / http source (no git transport applies).
func resolveTransportName(sourceSpec sources.SourceSpec) string {
	switch sourceSpec.Type {
	case sourceTypeGitHub, sourceTypeGitLab, sourceTypeBitbucket:
		return transportLabelGit
	default:
		return transportLabelNA
	}
}

// sourceReachableMessage returns the condition.Message format used by
// the per-kind reconcilers on success: "transport=<git|n/a>".
// Keeps the format string centrally so the per-kind controllers stay
// surgical.
func sourceReachableMessage(sourceSpec sources.SourceSpec) string {
	return "transport=" + resolveTransportName(sourceSpec)
}

// retryStatusUpdate wraps c.Status().Update in retry-on-conflict and a
// fresh Get-per-attempt so concurrent reconcilers cannot lose status
// updates to apiserver 409 races. Generic over any CR type T whose
// pointer satisfies client.Object.
//
// Apply callback runs on a freshly-Got copy of the CR; it re-applies
// the caller's desired status fields onto `fresh`. On success
// `cr.ResourceVersion` is mirrored back so subsequent metadata writes
// (e.g. force-refresh annotation clear) carry the post-Update version
// and do not 409 themselves.
//
// Issue #18 + sister project alitellm-operator's writeStatus pattern.
// Before this helper, every reconciler's `r.Status().Update(ctx, &cr)`
// was a naked best-effort write that intermittently lost the race to
// the suite-level reconciler under envtest, producing
// `TestPMR_Stage1_ParseFails`-class flakes:
//
//	Operation cannot be fulfilled on …: the object has been modified;
//	please apply your changes to the latest version and try again
//
// All reconcilers call this helper at every site that previously called
// `r.Status().Update` directly.
func retryStatusUpdate[T any, PT interface {
	*T
	client.Object
}](
	ctx context.Context,
	c client.Client,
	cr PT,
	apply func(fresh PT),
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var obj T
		fresh := PT(&obj)
		if err := c.Get(ctx, client.ObjectKeyFromObject(cr), fresh); err != nil {
			return err
		}
		apply(fresh)
		if err := c.Status().Update(ctx, fresh); err != nil {
			return err
		}
		cr.SetResourceVersion(fresh.GetResourceVersion())
		return nil
	})
}

// stageFailure is the per-entry stage-2 failure record aggregated into
// status.message via formatStageFailures.
type stageFailure struct {
	name   string
	reason string
}

// formatStageFailures renders the D-10 structured one-line summary of
// per-entry stage-2 failures:
//
//	"stage-2: <N> <noun>(s) failed: <n1>: <r1>, <n2>: <r2>, ... [, +<M> more]"
//
// First 5 failures listed verbatim; if more, append ", +<M> more". Returns
// the empty string on zero failures. Bounded to ~500 chars typical — well
// under Kubernetes' 4096-char status.message limit (names are pre-validated
// DNS-1123 subdomains; reasons are the §12.4 enum, so no adversarial-name
// content — T-02-06-08 mitigation).
func formatStageFailures(failures []stageFailure, noun string) string {
	if len(failures) == 0 {
		return ""
	}
	const verbatim = 5
	var b strings.Builder
	fmt.Fprintf(&b, "stage-2: %d %s(s) failed: ", len(failures), noun)
	n := len(failures)
	shown := min(n, verbatim)
	for i := 0; i < shown; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %s", failures[i].name, failures[i].reason)
	}
	if n > verbatim {
		fmt.Fprintf(&b, ", +%d more", n-verbatim)
	}
	return b.String()
}

// syncedFalseResult maps a marketplace Synced=False reason to the shared
// reconcile-result policy: terminal/configuration-derived reasons requeue
// without error (no hot-loop); transient reasons surface originalErr so
// the workqueue applies exponential backoff. Superset switch — a reason a
// given kind never produces simply never matches.
func syncedFalseResult(reason string, requeue time.Duration, originalErr error) (ctrl.Result, error) {
	switch reason {
	case ReasonInvalidConfig,
		ReasonUnauthorized,
		ReasonNotFound,
		ReasonUpstreamInvalid,
		ReasonUnsupportedPluginSource,
		ReasonPluginTooLarge:
		return ctrl.Result{RequeueAfter: requeue}, nil
	default:
		// Unreachable / StaleCacheExpired → backoff via workqueue.
		if originalErr != nil {
			return ctrl.Result{}, originalErr
		}
		return ctrl.Result{}, nil
	}
}
