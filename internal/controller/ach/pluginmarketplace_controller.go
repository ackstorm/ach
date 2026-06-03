// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/registry"
)

// marketplacePluginsChannel is the NOTIFY channel emitted on every
// per-plugin marketplace_plugins write/soft-delete (issue #34). Payload
// is "<marketplace_name>/<plugin_name>".
const marketplacePluginsChannel = "ach_marketplace_plugins_changed"

// marketplaceJSONMaxBytes is the hard cap on the marketplace.json upstream
// body itself (T-02-06-03 mitigation: bounded body read keeps adversarial
// upstreams from blowing memory). 5 MiB is generous — Hub §12.1 expects a
// few KiB to tens of KiB.
const marketplaceJSONMaxBytes = 5 << 20 // 5 MiB

// PluginMarketplaceReconciler reconciles a PluginMarketplace object via
// the Hub §12.4 three-stage refresh (Plan 02-06):
//
//   - Stage 1: fetch + parse marketplace.json, apply RE2 include/exclude
//     filters, perform intra-marketplace dedup (first-wins, normalized key).
//     ANY Stage-1 failure → Synced=False with the §12.4 reason; ZERO
//     marketplace_plugins writes or deletes.
//   - Stage 2: per-surviving-plugin materialization via the same §10.3
//     fetch→stage→fsync→rename(2)→UPSERT loop as the Plugin reconciler.
//     SERIAL (D-09); per-plugin failures recorded in status.message
//     (D-10 structured format with first-5-verbatim + +M more truncation)
//     but do NOT abort the stage.
//   - Stage 3: DELETE sweep — drop marketplace_plugins rows + cached files
//     for names absent from the current upstream catalog.
type PluginMarketplaceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// Phase 2 (Plan 02-09 wires these from cmd/operator/main.go):

	// DB is the Postgres pool used for marketplace_plugins UPSERT/LIST/DELETE.
	// Nil in envtest (Phase 1 finalizer test); steady-state branch skips
	// DB reads/writes when nil so the existing test stays green.
	DB *pgxpool.Pool

	// PluginMaxSizeMiB is the per-plugin size cap (D-12) — applied to
	// EVERY plugin tarball, including marketplace-sourced ones
	// (T-02-06-07 mitigation). When 0 the cap is "infinite" and
	// materializeMarketplacePlugin skips the LimitReader wrap. Plan 02-09
	// validates ACH_PLUGIN_MAX_SIZE_MIB > 0 at startup; envtest leaves it
	// at zero and exercises the no-cap branch.
	PluginMaxSizeMiB int

	// PluginMaxSizeMiBFn, when non-nil, overrides PluginMaxSizeMiB at
	// every cap read. envtest wires this so the suite-level reconciler
	// and a per-test reconciler can share a size cap via a shared atomic
	// (issue #18 follow-up); production always leaves it nil and reads
	// the immutable PluginMaxSizeMiB field directly.
	PluginMaxSizeMiBFn func() int

	// Fetchers is the FetcherFactory; nil → defaults to registry.For.
	// Tests inject a fake fetcher to exercise Stage-1 + Stage-2 dispatch
	// without live HTTPS traffic.
	Fetchers FetcherFactory

	// Issue #34 (A10/A11): see PluginReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent
}

// sizeCapMiB returns the per-plugin tarball cap in MiB. Prefers the
// test-only PluginMaxSizeMiBFn override; falls back to PluginMaxSizeMiB.
func (r *PluginMarketplaceReconciler) sizeCapMiB() int {
	if r.PluginMaxSizeMiBFn != nil {
		return r.PluginMaxSizeMiBFn()
	}
	return r.PluginMaxSizeMiB
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=pluginmarketplaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=pluginmarketplaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=pluginmarketplaces/finalizers,verbs=update

// Reconcile implements the Phase 2 PluginMarketplace lifecycle.
func (r *PluginMarketplaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("pluginmarketplace", req.NamespacedName)

	var cr achv1alpha1.PluginMarketplace
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Deletion path: §10.3 subtree cleanup + DB row sweep + finalizer remove. ───
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cr, pluginMarketplaceFinalizer) {
			// §10.3 cache layout: marketplace/<name>/<everything>
			if err := os.RemoveAll(filepath.Join(r.CacheRoot, "marketplace", cr.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return ctrl.Result{}, err
			}
			// OP-12 / Plan 02-06: drop every marketplace_plugins row under
			// this marketplace in sync with the cached subtree.
			if r.DB != nil {
				rows, err := achdb.ListMarketplacePlugins(ctx, r.DB, cr.Name)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("db list marketplace_plugins on delete: %w", err)
				}
				for _, row := range rows {
					if err := achdb.DeleteMarketplacePlugin(ctx, r.DB, cr.Name, row.Name); err != nil {
						return ctrl.Result{}, fmt.Errorf("db delete marketplace_plugin %s/%s: %w", cr.Name, row.Name, err)
					}
				}
			}
			controllerutil.RemoveFinalizer(&cr, pluginMarketplaceFinalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("§10.3 marketplace subtree + DB cleanup complete; finalizer removed", "name", cr.Name)
		}
		return ctrl.Result{}, nil
	}

	// ─── Finalizer-add path. ───
	if !controllerutil.ContainsFinalizer(&cr, pluginMarketplaceFinalizer) {
		controllerutil.AddFinalizer(&cr, pluginMarketplaceFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// ─── Phase 2 steady state: §12.4 three-stage refresh. ───
	spec := cr.Spec
	requeue := requeueDurationFromRefresh(spec.Refresh)

	// §10.3 within-interval gate: skip the stage-1 catalog fetch when the
	// CR was successfully refreshed within spec.refresh.interval and
	// nothing demands re-verification. Marketplace reads lastRefresh from
	// status (no external_refs row is persisted for the catalog file
	// itself — see stage-1 PriorRev comment below). Spec change, the
	// ach.ackstorm.ai/force-refresh annotation, and the next-interval
	// timer still trigger fresh fetches.
	var lastRefresh time.Time
	if cr.Status.LastSuccessfulRefresh != nil {
		lastRefresh = cr.Status.LastSuccessfulRefresh.Time
	}
	var forceRefreshRequestedAt time.Time
	if r.DB != nil {
		ts, err := achdb.MaxMarketplaceForceRefresh(ctx, r.DB, cr.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("db max marketplace force refresh: %w", err)
		}
		forceRefreshRequestedAt = ts
	}
	if shouldSkipFetch(spec.Refresh, lastRefresh, cr.Status.ObservedGeneration, cr.Generation, cr.Annotations, forceRefreshRequestedAt, time.Now()) {
		remaining := time.Until(lastRefresh.Add(requeue))
		if remaining < time.Second {
			remaining = time.Second
		}
		logger.V(1).Info("§10.3 within-interval gate: skipping stage-1 catalog fetch",
			"lastRefresh", lastRefresh, "requeueAfter", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// ─── Stage 1: fetch + parse + filter. ───

	// 1a: Build the marketplace-file SourceSpec + resolve auth Secret.
	sourceSpec := buildSourceSpec(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)
	authRef := extractAuthSecretRef(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)

	var marketplaceSecret *corev1.Secret
	if authRef != nil {
		var sec corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: authRef.Name}, &sec); err != nil {
			if apierrors.IsNotFound(err) {
				// WR-07: use the same fmt.Sprintf %q format as the
				// non-IsNotFound branch below so Condition.Message is
				// consistent across both Secret-fetch failure paths.
				return r.markSyncedFalse(ctx, &cr, ReasonUnauthorized,
					fmt.Sprintf("stage-1: marketplace auth Secret %q: not found", authRef.Name),
					requeue, nil)
			}
			return ctrl.Result{}, fmt.Errorf("stage-1: get marketplace auth Secret %q: %w", authRef.Name, err)
		}
		marketplaceSecret = &sec
	}

	// 1b: Dispatch to the per-source-type fetcher and Fetch marketplace.json.
	factory := r.Fetchers
	if factory == nil {
		factory = registry.For
	}
	fetcher, err := factory(sourceSpec)
	if err != nil {
		return r.markSyncedFalse(ctx, &cr, ReasonInvalidConfig, "stage-1: fetcher: "+err.Error(), requeue, nil)
	}
	fetchResult, err := fetcher.Fetch(ctx, sources.FetchRequest{
		Spec:   sourceSpec,
		Secret: marketplaceSecret,
		// PriorRev is empty: Phase 2 does not persist marketplace.json's
		// own UpstreamRev (the marketplace_plugins rows track per-plugin
		// revs). A future v1beta1 may add an external_refs row keyed by
		// (kind="pluginmarketplace", name=cr.Name) for conditional-GET on
		// the catalog file itself.
		PriorRev: "",
	})
	if err != nil {
		reason, msg := classifyFetchError(err, spec.Refresh, time.Time{})
		return r.markSyncedFalse(ctx, &cr, reason, "stage-1: "+msg, requeue, err)
	}
	if fetchResult.NotModified {
		// 304 on the catalog file → no Stage-2/3 work; refresh staleness
		// window stays implicit. Surface the transport that served the
		// fetch so operators can see which wire path was used.
		return r.markSyncedTrue(ctx, &cr, sourceReachableMessage(sourceSpec), requeue)
	}
	if fetchResult.Body == nil {
		return r.markSyncedFalse(ctx, &cr, ReasonUpstreamInvalid, "stage-1: fetcher returned nil body without NotModified", requeue, nil)
	}
	defer fetchResult.Body.Close()

	// Body reshape: git-tarball source types (github/gitlab/bitbucket)
	// return the full repo archive — extract `<root>/.claude-plugin/
	// marketplace.json` before parsing. s3/gcs/http return the
	// marketplace.json bytes directly.
	var body []byte
	if isTarballSourceType(spec.Type) {
		body, err = extractMarketplaceJSON(io.LimitReader(fetchResult.Body, marketplaceJSONMaxBytes))
		if err != nil {
			reason, _ := classifyFetchError(err, spec.Refresh, time.Time{})
			return r.markSyncedFalse(ctx, &cr, reason, "stage-1 extract: "+err.Error(), requeue, err)
		}
	} else {
		body, err = io.ReadAll(io.LimitReader(fetchResult.Body, marketplaceJSONMaxBytes))
		if err != nil {
			return r.markSyncedFalse(ctx, &cr, ReasonUnreachable, "stage-1: marketplace.json read: "+err.Error(), requeue, err)
		}
	}

	mkt, err := parseClaudeCodeMarketplace(body)
	if err != nil {
		reason, _ := classifyFetchError(err, spec.Refresh, time.Time{})
		return r.markSyncedFalse(ctx, &cr, reason, "stage-1 parse: "+err.Error(), requeue, err)
	}

	// 1c: Compile + apply RE2 include/exclude filters.
	var includeRes, excludeRes []*regexp.Regexp
	var includeListed bool
	if spec.Filters != nil {
		includeListed = len(spec.Filters.Include) > 0
		if includeRes, err = compileAnchored(spec.Filters.Include); err != nil {
			return r.markSyncedFalse(ctx, &cr, ReasonInvalidConfig, "stage-1 filters.include: "+err.Error(), requeue, nil)
		}
		if excludeRes, err = compileAnchored(spec.Filters.Exclude); err != nil {
			return r.markSyncedFalse(ctx, &cr, ReasonInvalidConfig, "stage-1 filters.exclude: "+err.Error(), requeue, nil)
		}
	}
	filtered, includeMatched := applyFilters(mkt.Plugins, includeRes, excludeRes)
	if includeListed && !includeMatched {
		return r.markSyncedFalse(ctx, &cr, ReasonUpstreamInvalid, "stage-1 filters.include matched zero plugins", requeue, nil)
	}

	// ─── Stage 2: serial per-plugin materialization (D-09). ───

	var failures []pluginFailure
	// successful collects the per-entry (name, upstreamRev) pairs that
	// passed Stage-2 — feeds status.plugins[] so operators can `kubectl
	// get pluginmarketplace -o yaml` to see exactly what materialized.
	// Capacity = len(filtered) is the upper bound; the actual length will
	// be smaller when there are filters or per-entry failures.
	successful := make([]achv1alpha1.MarketplacePluginRef, 0, len(filtered))

	// Intra-marketplace dedup (handoff item 5): first-wins over the ordered
	// candidate slice, normalized key (case-fold + trim) so Foo/foo variants
	// collide HERE, not at the late path write. Empty name → UpstreamInvalid.
	seen := make(map[string]struct{}, len(filtered))
	deduped := make([]ClaudeCodeMarketplacePlugin, 0, len(filtered))
	for _, entry := range filtered {
		if strings.TrimSpace(entry.Name) == "" {
			failures = append(failures, pluginFailure{name: entry.Name, reason: ReasonUpstreamInvalid})
			continue
		}
		key := strings.ToLower(strings.TrimSpace(entry.Name))
		if _, dup := seen[key]; dup {
			failures = append(failures, pluginFailure{name: entry.Name, reason: ReasonDuplicateName})
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, entry)
	}
	for i := range deduped {
		entry := deduped[i]
		// Per-entry Kind="" → unsupported source wire-format shape (e.g.
		// an old npm-shaped entry). Short-circuit before the fetcher so
		// no live git remote is touched.
		if entry.Source.Kind == "" {
			failures = append(failures, pluginFailure{name: entry.Name, reason: ReasonUnsupportedPluginSource})
			continue
		}
		// Per-plugin auth Secret: marketplace plugin entries do NOT carry
		// their own AuthSecretRef in v1alpha1. Reuse the marketplace's
		// auth Secret (which may be nil for anonymous-HTTPS marketplaces).
		// This is acceptable because the entries fetched are typically
		// hosted by the same identity that hosts the marketplace.json.
		upstreamRev, perr := r.materializeMarketplacePlugin(ctx, &cr, entry, marketplaceSecret)
		if perr != nil {
			reason, _ := classifyFetchErrorMarketplace(perr, spec.Refresh, time.Time{})
			failures = append(failures, pluginFailure{name: entry.Name, reason: reason})
			continue
		}
		successful = append(successful, achv1alpha1.MarketplacePluginRef{
			Name:        entry.Name,
			UpstreamRev: upstreamRev,
		})
	}

	// ─── Stage 3: DELETE sweep of vanished names. ───
	if r.DB != nil {
		priorRows, err := achdb.ListMarketplacePlugins(ctx, r.DB, cr.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("stage-3 list: %w", err)
		}
		// currentNames is the set of all deduplicated plugin names present
		// in the upstream catalog — both materialized and failed Stage-2
		// entries are retained so a prior cached file is still served while
		// the failure is reported in status.message.
		currentNames := make(map[string]struct{}, len(deduped))
		for _, entry := range deduped {
			currentNames[entry.Name] = struct{}{}
		}
		for _, row := range priorRows {
			if _, kept := currentNames[row.Name]; kept {
				continue
			}
			// Vanished: remove cached file then drop DB row.
			// WR-06: on a non-IsNotExist filesystem error (EBUSY,
			// EACCES, EIO, etc.) we MUST NOT proceed to the DB delete.
			// The previous code logged and continued — leaving the
			// orphan file on disk after the DB row was gone, where
			// the Content Service would no longer serve it but cache
			// size accounting would still count it. Returning the
			// error here triggers controller-runtime's exponential
			// backoff so a transient filesystem fault (e.g. EBUSY
			// from a slow unmount) retries naturally; a permanent
			// fault (EACCES from a manual chmod) surfaces loudly via
			// the next reconcile's status condition.
			cachePath := filepath.Join(r.CacheRoot, "marketplace", cr.Name, "plugin", row.Name+".tar.gz")
			if err := os.Remove(cachePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return ctrl.Result{}, fmt.Errorf("stage-3 cache file remove %s/%s: %w", cr.Name, row.Name, err)
			}
			if err := achdb.DeleteMarketplacePlugin(ctx, r.DB, cr.Name, row.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("stage-3 db delete %s/%s: %w", cr.Name, row.Name, err)
			}
		}
	}

	// ─── Status + RequeueAfter. ───

	// Format the partial-failure message regardless of overall outcome.
	msg := formatStage2Message(failures)
	if msg != "" {
		logger.Info("stage-2 partial failures", "summary", msg)
	}

	// Sort the successful entries by Name for stable status output —
	// otherwise the listMapKey marshal order tracks reconcile iteration
	// order and reads like a flicker between successive reconciles.
	sort.Slice(successful, func(i, j int) bool { return successful[i].Name < successful[j].Name })

	// transport=<…> plugins=<N> [stage-2 partial-failure summary]
	finalMsg := fmt.Sprintf("%s plugins=%d", sourceReachableMessage(sourceSpec), len(successful))
	if msg != "" {
		finalMsg = finalMsg + " " + msg
	}
	cr.Status.Plugins = successful
	cr.Status.PluginsCount = len(successful)
	if _, err := r.markSyncedTrue(ctx, &cr, finalMsg, requeue); err != nil {
		// WR-02: when markSyncedTrue's r.Status().Update fails (typically
		// 409 from a concurrent reconcile), cr.ResourceVersion is stale.
		// A follow-up r.Update for the annotation-clear would also 409;
		// skip and let the next reconcile retry both writes from a fresh
		// Get.
		logger.Error(err, "stage-final markSyncedTrue failed; skipping annotation-clear")
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	// D-07: clear force-refresh annotation if present.
	if _, has := cr.Annotations["ach.ackstorm.ai/force-refresh"]; has {
		delete(cr.Annotations, "ach.ackstorm.ai/force-refresh")
		if err := r.Update(ctx, &cr); err != nil {
			logger.Error(err, "force-refresh annotation removal failed")
		}
	}

	return ctrl.Result{RequeueAfter: requeue}, nil
}

// materializeMarketplacePlugin runs §10.3 (resolve auth → fetch → stage →
// fsync → rename(2) → UPSERT) for ONE plugin in a marketplace. It is the
// marketplace-specific counterpart to materializeExternalRef; the
// differences are:
//
//   - Kind/UpsertExternalRef are replaced by db.UpsertMarketplacePlugin
//     keyed by (marketplace_name, plugin_name).
//   - The final path is CacheRoot/marketplace/<mp.Name>/plugin/<entry.Name>.tar.gz
//     and the parent directory MUST exist before rename(2).
//   - SizeCapBytes is r.PluginMaxSizeMiB << 20 (T-02-06-07 mitigation:
//     marketplace-sourced plugins observe the same cap as Plugin CRDs).
//
// Returns the error from the underlying §10.3 step; callers use
// classifyFetchErrorMarketplace to map to a §12.4 reason string.
func (r *PluginMarketplaceReconciler) materializeMarketplacePlugin(
	ctx context.Context,
	mp *achv1alpha1.PluginMarketplace,
	entry ClaudeCodeMarketplacePlugin,
	secret *corev1.Secret,
) (string, error) {
	// ─── 1+2: dispatch + fetch via internal/sources/git ───
	body, upstreamRev, err := dispatchMarketplacePlugin(ctx, mp, entry, secret, r.CacheRoot)
	if err != nil {
		return "", err
	}
	defer body.Close()

	// ─── 3: ensure the per-marketplace plugin dir exists ───
	finalDir := filepath.Join(r.CacheRoot, "marketplace", mp.Name, "plugin")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return "", fmt.Errorf("plugin %q: mkdir final dir: %w", entry.Name, err)
	}
	finalPath := filepath.Join(finalDir, entry.Name+".tar.gz")

	// ─── 4: stage at .tmp/stg-<random> ───
	tmpFile, err := os.CreateTemp(filepath.Join(r.CacheRoot, ".tmp"), "stg-")
	if err != nil {
		return "", fmt.Errorf("plugin %q: create staging file: %w", entry.Name, err)
	}
	stagingPath := tmpFile.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
	}()

	// ─── 5: copy with optional cap (T-02-06-07) ───
	capBytes := int64(r.sizeCapMiB()) << 20
	var n int64
	var copyErr error
	if capBytes > 0 {
		limited := io.LimitReader(body, capBytes+1)
		n, copyErr = io.Copy(tmpFile, limited)
	} else {
		n, copyErr = io.Copy(tmpFile, body)
	}
	if copyErr != nil {
		_ = os.Remove(stagingPath)
		return "", fmt.Errorf("plugin %q: staging copy: %w", entry.Name, copyErr)
	}
	if capBytes > 0 && n > capBytes {
		_ = os.Remove(stagingPath)
		return "", &OversizeError{Bytes: n, Cap: capBytes}
	}

	// ─── 6: fsync + close ───
	if err := tmpFile.Sync(); err != nil {
		_ = os.Remove(stagingPath)
		return "", fmt.Errorf("plugin %q: staging fsync: %w", entry.Name, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return "", fmt.Errorf("plugin %q: staging close: %w", entry.Name, err)
	}
	closed = true

	// ─── 6.5: post-fetch plugin.json existence check ───
	// Stream the staged tar to verifyPluginManifest before rename(2).
	// The verifier looks at the tar root because git.tarSubtree strips
	// the subtree prefix — see marketplace_manifest.go header.
	stagedForVerify, openErr := os.Open(stagingPath)
	if openErr != nil {
		_ = os.Remove(stagingPath)
		return "", fmt.Errorf("plugin %q: open staged tar: %w", entry.Name, openErr)
	}
	verifyErr := verifyPluginManifest(stagedForVerify)
	_ = stagedForVerify.Close()
	if verifyErr != nil {
		_ = os.Remove(stagingPath)
		return "", verifyErr
	}

	// ─── 7: atomic rename(2) ───
	if err := os.Rename(stagingPath, finalPath); err != nil {
		_ = os.Remove(stagingPath)
		return "", fmt.Errorf("plugin %q: §10.3 rename(2): %w", entry.Name, err)
	}

	// ─── 8: UPSERT marketplace_plugins (when DB available) ───
	// Issue #34: project + NOTIFY atomically on ach_marketplace_plugins_changed.
	// Payload is "<marketplace_name>/<plugin_name>". ErrOriginConflict
	// propagates raw — the Stage-2 caller treats UI-conflict as a
	// per-plugin failure recorded in the partial-failure summary.
	if r.DB != nil {
		now := time.Now()
		next := now.Add(requeueDurationFromRefresh(mp.Spec.Refresh))
		row := achdb.MarketplacePlugin{
			MarketplaceName:       mp.Name,
			Name:                  entry.Name,
			StorageLocation:       finalPath,
			UpstreamRev:           upstreamRev,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         next,
			MaxStalenessSeconds:   int64(mp.Spec.Refresh.MaxStaleness.Duration.Seconds()),
		}
		payload := fmt.Sprintf("%s/%s", mp.Name, entry.Name)
		err := achdb.WithTxNotify(ctx, r.DB, marketplacePluginsChannel, payload, func(tx pgx.Tx) error {
			return achdb.UpsertMarketplacePluginTx(ctx, tx, row)
		})
		if err != nil {
			return "", fmt.Errorf("plugin %q: db upsert: %w", entry.Name, err)
		}
	}
	return upstreamRev, nil
}

// classifyFetchErrorMarketplace is the marketplace-side fork of
// classifyFetchError: it understands errUnsupportedPluginSource (npm) and
// OversizeError in addition to the standard sources.Err* sentinels.
//
// Returned reason is one of the marketplace status enum:
// {Synced, Unreachable, Unauthorized, NotFound, UpstreamInvalid,
//
//	InvalidConfig, PluginTooLarge, UnsupportedPluginSource, StaleCacheExpired}.
func classifyFetchErrorMarketplace(err error, refresh achv1alpha1.RefreshBlock, lastRefresh time.Time) (reason, message string) {
	if err == nil {
		return ReasonSynced, ""
	}
	// errUnsupportedPluginSource is now intercepted in the Reconcile body
	// before dispatchMarketplacePlugin runs — the explicit Kind=="" gate
	// short-circuits there. This function still handles all other
	// sources.Err* sentinels via classifyFetchError.
	return classifyFetchError(err, refresh, lastRefresh)
}

// formatStage2Message renders the D-10 structured one-line summary of
// per-plugin failures:
//
//	"stage-2: <N> plugin(s) failed: <n1>: <r1>, <n2>: <r2>, ... [, +<M> more]"
//
// First 5 failures listed verbatim; if more, append ", +<M> more". Returns
// the empty string on zero failures.
//
// Bounded to ~500 chars typical (5 entries × ~80 chars + suffix) — well
// under Kubernetes' 4096-char status.message limit. Plugin names are
// pre-validated as DNS-1123 subdomains (~63 chars max) by
// parseClaudeCodeMarketplace; reason strings are bounded by the §12.4
// enum (max ~24 chars). T-02-06-08 mitigation: no adversarial-name
// content in the message because parseClaudeCodeMarketplace rejected
// path-traversal names.
func formatStage2Message(failures []pluginFailure) string {
	if len(failures) == 0 {
		return ""
	}
	const verbatim = 5
	var b strings.Builder
	fmt.Fprintf(&b, "stage-2: %d plugin(s) failed: ", len(failures))
	n := len(failures)
	max := n
	if max > verbatim {
		max = verbatim
	}
	for i := 0; i < max; i++ {
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

// pluginFailure is the per-plugin failure record aggregated into the
// status.message via formatStage2Message. Declared at package level so
// formatStage2Message can be tested independently of Reconcile.
type pluginFailure struct {
	name   string
	reason string
}

// markSyncedTrue writes Synced=True with the supplied message and updates
// the CR's status subresource. The reason is always ReasonSynced.
// Returns (ctrl.Result{RequeueAfter: requeue}, err) so callers can return
// directly.
func (r *PluginMarketplaceReconciler) markSyncedTrue(ctx context.Context, cr *achv1alpha1.PluginMarketplace, message string, requeue time.Duration) (ctrl.Result, error) {
	// Record successful catalog refresh (including 304 NotModified) so the
	// §10.3 within-interval gate can skip the next reconcile within the
	// refresh window. Mirrors the Plugin/Prompt/Artifact reconciler
	// semantics where LastSuccessfulRefresh advances on both fresh
	// fetches and NotModified shortcuts.
	now := metav1.Now()
	// Issue #18: retry on apiserver 409 conflicts. Without the retry the
	// suite-level reconciler and the per-test reconciler race on the same
	// CR's resourceVersion under envtest; the loser sees
	// "object has been modified" and the corresponding status condition
	// never lands → TestPMR_Stage1_* envtest flake. Mirror the
	// retry-on-conflict pattern used by sister project alitellm-operator's
	// writeStatus helpers.
	desiredGen := cr.Generation
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh achv1alpha1.PluginMarketplace
		if err := r.Get(ctx, client.ObjectKeyFromObject(cr), &fresh); err != nil {
			return err
		}
		applyReconcileConditions(&fresh.Status.Conditions, ReasonSynced, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		fresh.Status.LastSuccessfulRefresh = &now
		// Carry the caller-computed discovery set onto the fresh copy —
		// the Reconcile body set these on `cr` and the fresh Get would
		// otherwise drop them (issue #53 regression introduced by c28eeff).
		fresh.Status.Plugins = cr.Status.Plugins
		fresh.Status.PluginsCount = cr.Status.PluginsCount
		if u := r.Status().Update(ctx, &fresh); u != nil {
			return u
		}
		cr.Status = fresh.Status
		cr.ResourceVersion = fresh.ResourceVersion
		return nil
	})
	if err != nil {
		return ctrl.Result{RequeueAfter: requeue}, err
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// markSyncedFalse writes Synced=False with the supplied reason + message
// and updates the CR's status subresource. The originalErr param drives
// the failure-dispatch retry policy:
//
//   - terminal/configuration-derived reasons (InvalidConfig, Unauthorized,
//     NotFound, UpstreamInvalid, UnsupportedPluginSource,
//     PluginTooLarge) return (Result{RequeueAfter}, nil) so the reconciler
//     does not hot-loop on errors that won't change by retrying.
//   - transient reasons (Unreachable, StaleCacheExpired) return
//     (Result{}, originalErr) so controller-runtime's workqueue applies
//     exponential backoff.
func (r *PluginMarketplaceReconciler) markSyncedFalse(ctx context.Context, cr *achv1alpha1.PluginMarketplace, reason, message string, requeue time.Duration, originalErr error) (ctrl.Result, error) {
	// Issue #18: retry on apiserver 409 conflicts. See markSyncedTrue for
	// rationale — the suite + per-test reconciler race under envtest, and
	// without the retry the failure-classification status (UpstreamInvalid,
	// Unauthorized, Unreachable, …) never lands; drainReconcileUntil then
	// times out → flake.
	desiredGen := cr.Generation
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh achv1alpha1.PluginMarketplace
		if err := r.Get(ctx, client.ObjectKeyFromObject(cr), &fresh); err != nil {
			return err
		}
		applyReconcileConditions(&fresh.Status.Conditions, reason, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		// Carry the caller's discovery set (issue #53 regression: c28eeff).
		// Failure paths pass through the prior value loaded at the top of
		// Reconcile.
		fresh.Status.Plugins = cr.Status.Plugins
		fresh.Status.PluginsCount = cr.Status.PluginsCount
		if u := r.Status().Update(ctx, &fresh); u != nil {
			return u
		}
		cr.Status = fresh.Status
		cr.ResourceVersion = fresh.ResourceVersion
		return nil
	}); err != nil {
		// Status update failure is logged by the controller-runtime
		// recorder via the Reconcile return; surface it directly.
		return ctrl.Result{}, err
	}
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

// SetupWithManager registers the reconciler with controller-runtime.
func (r *PluginMarketplaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.PluginMarketplace{}, builder.WithPredicates()).
		Named("ach-pluginmarketplace")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
