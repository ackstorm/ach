// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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

// skillMarketplaceSkillsChannel is the NOTIFY channel emitted on every
// per-skill skill_marketplace_skills write/delete. Payload is
// "<marketplace_name>/<skill_name>".
const skillMarketplaceSkillsChannel = "ach_skill_marketplace_skills_changed"

// skillMarketplacesChannel carries the NOTIFY for the skill_marketplaces
// projection (the marketplace OBJECT + its Synced status), emitted from the
// same tx as the UpsertSkillMarketplace write. Parity-only with the other
// projection channels — the admin inventory reads Postgres on demand.
const skillMarketplacesChannel = "ach_skill_marketplaces_changed"

// SkillMarketplaceReconciler reconciles a SkillMarketplace object via a
// three-stage refresh mirroring PluginMarketplace, with a convention-based
// discovery swap (agentskills.io has no marketplace.json index):
//
//   - Stage 1: fetch the upstream as ONE tar.gz and discoverSkillsInTree —
//     walk for every top-level dir with a valid SKILL.md (name == dir). ANY
//     Stage-1 failure → Synced=False; ZERO skill_marketplace_skills writes.
//   - Stage 2: per-discovered-skill sliceSkillSubtree → verifySkillContents →
//     rename(2) to skill-marketplace/<mkt>/<name>.tar.gz → UPSERT. Per-skill
//     failures recorded in status.message but do NOT abort the stage.
//   - Stage 3: DELETE sweep — drop rows + cached files for vanished names.
type SkillMarketplaceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// DB is the Postgres pool for skill_marketplace_skills + skill_marketplaces.
	// Nil in envtest (finalizer test); steady-state branch skips DB I/O when nil.
	DB *pgxpool.Pool

	// SkillMaxSizeMiB caps the WHOLE marketplace fetch (operator-memory guard).
	// When 0 a hard ingress cap (skillRawIngressCap) applies instead.
	SkillMaxSizeMiB int

	// SkillMaxSizeMiBFn, when non-nil, overrides SkillMaxSizeMiB at every cap
	// read (envtest shares a cap across reconcilers via an atomic).
	SkillMaxSizeMiBFn func() int

	// Fetchers is the FetcherFactory; nil → defaults to registry.For.
	Fetchers FetcherFactory

	// ResyncSource — see PluginMarketplaceReconciler.ResyncSource.
	ResyncSource chan event.GenericEvent
}

// ingressCapBytes returns the whole-marketplace fetch cap in bytes. Prefers the
// test-only SkillMaxSizeMiBFn override; falls back to SkillMaxSizeMiB; a zero
// cap falls back to the hard skillRawIngressCap operator-memory guard.
func (r *SkillMarketplaceReconciler) ingressCapBytes() int64 {
	mib := r.SkillMaxSizeMiB
	if r.SkillMaxSizeMiBFn != nil {
		mib = r.SkillMaxSizeMiBFn()
	}
	if mib <= 0 {
		return skillRawIngressCap
	}
	return int64(mib) << 20
}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skillmarketplaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skillmarketplaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=skillmarketplaces/finalizers,verbs=update

// Reconcile implements the SkillMarketplace lifecycle. The deletion, finalizer,
// and steady-state legs are split into helpers to stay within the gocyclo
// budget (mirrors the sister reconcilers).
func (r *SkillMarketplaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr achv1alpha1.SkillMarketplace
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cr)
	}

	if !controllerutil.ContainsFinalizer(&cr, skillMarketplaceFinalizer) {
		controllerutil.AddFinalizer(&cr, skillMarketplaceFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return r.reconcileRefresh(ctx, &cr)
}

// reconcileDelete runs the deletion leg: subtree cleanup + DB row sweep +
// finalizer remove.
func (r *SkillMarketplaceReconciler) reconcileDelete(ctx context.Context, cr *achv1alpha1.SkillMarketplace) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(cr, skillMarketplaceFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := os.RemoveAll(filepath.Join(r.CacheRoot, "skill-marketplace", cr.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ctrl.Result{}, err
	}
	if r.DB != nil {
		rows, err := achdb.ListSkillMarketplaceSkills(ctx, r.DB, cr.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("db list skill_marketplace_skills on delete: %w", err)
		}
		for _, row := range rows {
			if err := achdb.DeleteSkillMarketplaceSkill(ctx, r.DB, cr.Name, row.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("db delete skill_marketplace_skill %s/%s: %w", cr.Name, row.Name, err)
			}
		}
		if err := achdb.DeleteSkillMarketplace(ctx, r.DB, cr.Namespace, cr.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("db delete skill_marketplace %s/%s: %w", cr.Namespace, cr.Name, err)
		}
	}
	controllerutil.RemoveFinalizer(cr, skillMarketplaceFinalizer)
	if err := r.Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	log.FromContext(ctx).Info("skill-marketplace subtree + DB cleanup complete; finalizer removed", "name", cr.Name)
	return ctrl.Result{}, nil
}

// reconcileRefresh runs the steady-state three-stage refresh.
func (r *SkillMarketplaceReconciler) reconcileRefresh(ctx context.Context, cr *achv1alpha1.SkillMarketplace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("skillmarketplace", client.ObjectKeyFromObject(cr))
	spec := cr.Spec
	requeue := requeueDurationFromRefresh(spec.Refresh)

	var lastRefresh time.Time
	if cr.Status.LastSuccessfulRefresh != nil {
		lastRefresh = cr.Status.LastSuccessfulRefresh.Time
	}
	var forceRefreshRequestedAt time.Time
	if r.DB != nil {
		ts, err := achdb.MaxSkillMarketplaceForceRefresh(ctx, r.DB, cr.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("db max skill marketplace force refresh: %w", err)
		}
		forceRefreshRequestedAt = ts
	}
	if shouldSkipFetch(spec.Refresh, lastRefresh, cr.Status.ObservedGeneration, cr.Generation, cr.Annotations, forceRefreshRequestedAt, time.Now()) {
		remaining := time.Until(lastRefresh.Add(requeue))
		if remaining < time.Second {
			remaining = time.Second
		}
		logger.V(1).Info("within-interval gate: skipping stage-1 marketplace fetch",
			"lastRefresh", lastRefresh, "requeueAfter", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// ─── Stage 1: fetch the whole marketplace archive + discover skills. ───
	raw, rev, res, done, err := r.stage1Fetch(ctx, cr, requeue)
	if done {
		return res, err
	}
	// A SkillMarketplace is a DISCOVERY mechanism (mirrors PluginMarketplace), not
	// a narrow-at-fetch object: stage-1 fetches the WHOLE repo (path stripped — see
	// stage1Fetch) and spec.<git>.path is a post-fetch tree-walk hint (the
	// skills-root holding the skill dirs), NOT a fetch-layer subtree narrow (F1).
	subPath := skillMarketplaceSubPath(spec)
	archiveRoot, discovered, derr := discoverSkillsInTree(raw, subPath)
	if derr != nil {
		// Malformed archive (gzip/tar) → UpstreamInvalid (terminal, no backoff).
		return r.markSyncedFalse(ctx, cr, ReasonUpstreamInvalid, "stage-1 discover: "+derr.Error(), requeue, nil)
	}

	// ─── Stage 2: per-skill slice + verify + materialize. ───
	successful, failures := r.materializeDiscovered(ctx, cr, discovered, raw, archiveRoot, subPath, rev)

	// ─── Stage 3: DELETE sweep of vanished names. ───
	if err := r.sweepVanishedSkills(ctx, cr, discovered); err != nil {
		return ctrl.Result{}, err
	}

	// ─── Status + RequeueAfter. ───
	msg := formatSkillStage2Message(failures)
	if msg != "" {
		logger.Info("stage-2 partial failures", "summary", msg)
	}
	sort.Slice(successful, func(i, j int) bool { return successful[i].Name < successful[j].Name })

	finalMsg := fmt.Sprintf("%s skills=%d", sourceReachableMessage(
		buildSourceSpec(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)), len(successful))
	if msg != "" {
		finalMsg = finalMsg + " " + msg
	}
	cr.Status.Skills = successful
	cr.Status.SkillsCount = len(successful)
	if _, err := r.markSyncedTrue(ctx, cr, finalMsg, requeue); err != nil {
		logger.Error(err, "stage-final markSyncedTrue failed; skipping annotation-clear")
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	if _, has := cr.Annotations["ach.ackstorm.ai/force-refresh"]; has {
		delete(cr.Annotations, "ach.ackstorm.ai/force-refresh")
		if err := r.Update(ctx, cr); err != nil {
			logger.Error(err, "force-refresh annotation removal failed")
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// stage1Fetch resolves auth, dispatches the fetcher, and reads the whole
// marketplace archive into memory (every source type returns a single tar.gz
// body — git/REST repo archive, or a pointed-at .tar.gz for s3/gcs/http). When
// done is true the caller MUST return (res, err) verbatim (a terminal
// markSynced* outcome or a hard error). On success done is false and raw/rev are
// populated; the caller derives archiveRoot via discoverSkillsInTree.
func (r *SkillMarketplaceReconciler) stage1Fetch(ctx context.Context, cr *achv1alpha1.SkillMarketplace, requeue time.Duration) (raw []byte, rev string, res ctrl.Result, done bool, err error) {
	spec := cr.Spec
	// Discovery, not a narrow-at-fetch object: clear the git spec.path so the
	// fetcher returns the WHOLE repo (the per-provider fetchers otherwise narrow
	// to spec.path — F1). path is re-applied post-fetch as the discovery walk hint.
	sourceSpec := withoutGitPath(buildSourceSpec(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP))
	authRef := extractAuthSecretRef(spec.Type, spec.GitHub, spec.GitLab, spec.Bitbucket, spec.S3, spec.GCS, spec.HTTP)

	var marketplaceSecret *corev1.Secret
	if authRef != nil {
		var sec corev1.Secret
		if gerr := r.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: authRef.Name}, &sec); gerr != nil {
			if apierrors.IsNotFound(gerr) {
				res, err = r.markSyncedFalse(ctx, cr, ReasonUnauthorized,
					fmt.Sprintf("stage-1: marketplace auth Secret %q: not found", authRef.Name), requeue, nil)
				return nil, "", res, true, err
			}
			return nil, "", ctrl.Result{}, true, fmt.Errorf("stage-1: get marketplace auth Secret %q: %w", authRef.Name, gerr)
		}
		marketplaceSecret = &sec
	}

	factory := r.Fetchers
	if factory == nil {
		factory = registry.For
	}
	fetcher, ferr := factory(sourceSpec)
	if ferr != nil {
		res, err = r.markSyncedFalse(ctx, cr, ReasonInvalidConfig, "stage-1: fetcher: "+ferr.Error(), requeue, nil)
		return nil, "", res, true, err
	}
	fetchResult, ferr := fetcher.Fetch(ctx, sources.FetchRequest{Spec: sourceSpec, Secret: marketplaceSecret, PriorRev: ""})
	if ferr != nil {
		reason, msg := classifyFetchError(ferr, spec.Refresh, time.Time{})
		res, err = r.markSyncedFalse(ctx, cr, reason, "stage-1: "+msg, requeue, ferr)
		return nil, "", res, true, err
	}
	if fetchResult.NotModified {
		res, err = r.markSyncedTrue(ctx, cr, sourceReachableMessage(sourceSpec), requeue)
		return nil, "", res, true, err
	}
	if fetchResult.Body == nil {
		res, err = r.markSyncedFalse(ctx, cr, ReasonUpstreamInvalid, "stage-1: fetcher returned nil body without NotModified", requeue, nil)
		return nil, "", res, true, err
	}
	defer func() { _ = fetchResult.Body.Close() }()

	ingressCap := r.ingressCapBytes()
	raw, rerr := io.ReadAll(io.LimitReader(fetchResult.Body, ingressCap+1))
	if rerr != nil {
		res, err = r.markSyncedFalse(ctx, cr, ReasonUnreachable, "stage-1: read marketplace body: "+rerr.Error(), requeue, rerr)
		return nil, "", res, true, err
	}
	if int64(len(raw)) > ingressCap {
		res, err = r.markSyncedFalse(ctx, cr, ReasonPluginTooLarge,
			fmt.Sprintf("stage-1: marketplace archive exceeds %d bytes", ingressCap), requeue, nil)
		return nil, "", res, true, err
	}
	return raw, fetchResult.UpstreamRev, ctrl.Result{}, false, nil
}

// materializeDiscovered runs Stage-2 over every discovered skill, returning the
// successful refs (for status) and per-skill failures (for status.message).
func (r *SkillMarketplaceReconciler) materializeDiscovered(ctx context.Context, cr *achv1alpha1.SkillMarketplace, discovered []discoveredSkill, raw []byte, archiveRoot, subPath, rev string) ([]achv1alpha1.SkillMarketplaceSkillRef, []skillFailure) {
	var failures []skillFailure
	successful := make([]achv1alpha1.SkillMarketplaceSkillRef, 0, len(discovered))
	for i := range discovered {
		d := discovered[i]
		if err := r.materializeMarketplaceSkill(ctx, cr, d, raw, archiveRoot, subPath, rev); err != nil {
			reason, _ := classifyFetchError(err, cr.Spec.Refresh, time.Time{})
			failures = append(failures, skillFailure{name: d.Name, reason: reason})
			continue
		}
		successful = append(successful, achv1alpha1.SkillMarketplaceSkillRef{Name: d.Name, UpstreamRev: rev})
	}
	return successful, failures
}

// sweepVanishedSkills runs Stage-3: drop rows + cached files for names absent
// from the current discovered set. No-op when DB is nil.
func (r *SkillMarketplaceReconciler) sweepVanishedSkills(ctx context.Context, cr *achv1alpha1.SkillMarketplace, discovered []discoveredSkill) error {
	if r.DB == nil {
		return nil
	}
	priorRows, err := achdb.ListSkillMarketplaceSkills(ctx, r.DB, cr.Name)
	if err != nil {
		return fmt.Errorf("stage-3 list: %w", err)
	}
	currentNames := make(map[string]struct{}, len(discovered))
	for _, d := range discovered {
		currentNames[d.Name] = struct{}{}
	}
	for _, row := range priorRows {
		if _, kept := currentNames[row.Name]; kept {
			continue
		}
		cachePath := filepath.Join(r.CacheRoot, "skill-marketplace", cr.Name, row.Name+".tar.gz")
		if err := os.Remove(cachePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stage-3 cache file remove %s/%s: %w", cr.Name, row.Name, err)
		}
		if err := achdb.DeleteSkillMarketplaceSkill(ctx, r.DB, cr.Name, row.Name); err != nil {
			return fmt.Errorf("stage-3 db delete %s/%s: %w", cr.Name, row.Name, err)
		}
	}
	return nil
}

// materializeMarketplaceSkill slices ONE skill's subtree out of the already-
// fetched marketplace bytes, validates it, atomically publishes it to
// skill-marketplace/<mkt>/<name>.tar.gz, and UPSERTs the row + NOTIFY. Unlike
// the PluginMarketplace counterpart there is NO per-skill fetch — a skills repo
// bundles every skill in the one marketplace archive.
func (r *SkillMarketplaceReconciler) materializeMarketplaceSkill(
	ctx context.Context,
	mp *achv1alpha1.SkillMarketplace,
	d discoveredSkill,
	raw []byte,
	archiveRoot, subPath, upstreamRev string,
) error {
	// d.Dir is the skill dir name relative to the skills-root (subPath); the
	// full subtree path inside the whole-repo archive is subPath/<dir>.
	subtreePath := d.Dir
	if subPath != "" {
		subtreePath = path.Join(subPath, d.Dir)
	}
	sub, err := sliceSkillSubtree(raw, archiveRoot, subtreePath)
	if err != nil {
		return fmt.Errorf("skill %q: slice subtree: %w", d.Name, err)
	}
	// Re-validate the sliced subtree (defense-in-depth atop discovery).
	if err := verifySkillContents(bytes.NewReader(sub)); err != nil {
		return fmt.Errorf("skill %q: verify: %w", d.Name, err)
	}

	finalDir := filepath.Join(r.CacheRoot, "skill-marketplace", mp.Name)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return fmt.Errorf("skill %q: mkdir final dir: %w", d.Name, err)
	}
	finalPath := filepath.Join(finalDir, d.Name+".tar.gz")

	tmpDir := filepath.Join(r.CacheRoot, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("skill %q: mkdir tmp dir: %w", d.Name, err)
	}
	tmpFile, err := os.CreateTemp(tmpDir, "stg-")
	if err != nil {
		return fmt.Errorf("skill %q: create staging file: %w", d.Name, err)
	}
	stagingPath := tmpFile.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
	}()

	if _, err := io.Copy(tmpFile, bytes.NewReader(sub)); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("skill %q: staging copy: %w", d.Name, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("skill %q: staging fsync: %w", d.Name, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("skill %q: staging close: %w", d.Name, err)
	}
	closed = true

	if err := os.Rename(stagingPath, finalPath); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("skill %q: rename(2): %w", d.Name, err)
	}

	if r.DB != nil {
		now := time.Now()
		next := now.Add(requeueDurationFromRefresh(mp.Spec.Refresh))
		row := achdb.SkillMarketplaceSkill{
			MarketplaceName:       mp.Name,
			Name:                  d.Name,
			StorageLocation:       finalPath,
			UpstreamRev:           upstreamRev,
			LastSuccessfulRefresh: now,
			NextRefreshAt:         next,
			MaxStalenessSeconds:   int64(mp.Spec.Refresh.MaxStaleness.Duration.Seconds()),
		}
		payload := fmt.Sprintf("%s/%s", mp.Name, d.Name)
		if err := achdb.WithTxNotify(ctx, r.DB, skillMarketplaceSkillsChannel, payload, func(tx pgx.Tx) error {
			return achdb.UpsertSkillMarketplaceSkillTx(ctx, tx, row)
		}); err != nil {
			return fmt.Errorf("skill %q: db upsert: %w", d.Name, err)
		}
	}
	return nil
}

// skillMarketplaceSubPath returns the in-repo directory that holds the skill
// dirs (spec.<git>.path), or "" when skills sit at the repo root. s3/gcs/http
// sources point directly at a pre-archived tarball and carry no sub-path. Used
// as the discovery tree-walk hint over the whole-repo archive (a SkillMarketplace
// is discovery, not a narrow-at-fetch object — F1).
func skillMarketplaceSubPath(spec achv1alpha1.SkillMarketplaceSpec) string {
	switch {
	case spec.GitHub != nil:
		return spec.GitHub.Path
	case spec.GitLab != nil:
		return spec.GitLab.Path
	case spec.Bitbucket != nil:
		return spec.Bitbucket.Path
	default:
		return ""
	}
}

// skillFailure is the per-skill failure record aggregated into status.message.
type skillFailure struct {
	name   string
	reason string
}

// formatSkillStage2Message renders the one-line summary of per-skill failures
// (first 5 verbatim, then "+M more"). Empty string on zero failures.
func formatSkillStage2Message(failures []skillFailure) string {
	if len(failures) == 0 {
		return ""
	}
	const verbatim = 5
	var b strings.Builder
	fmt.Fprintf(&b, "stage-2: %d skill(s) failed: ", len(failures))
	n := len(failures)
	maxN := n
	if maxN > verbatim {
		maxN = verbatim
	}
	for i := 0; i < maxN; i++ {
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

// buildSkillMarketplaceRow assembles the skill_marketplaces projection row from
// the CR's terminal status. Pure — unit-tested without a DB.
func buildSkillMarketplaceRow(cr *achv1alpha1.SkillMarketplace, syncedStatus, syncedReason string) achdb.SkillMarketplaceRow {
	return achdb.SkillMarketplaceRow{
		Namespace:       cr.Namespace,
		Name:            cr.Name,
		SyncedStatus:    syncedStatus,
		SyncedReason:    syncedReason,
		SkillsCount:     cr.Status.SkillsCount,
		ResourceVersion: cr.ResourceVersion,
	}
}

// projectSkillMarketplace mirrors the CR's terminal Synced status into the
// skill_marketplaces projection table + NOTIFY, in one tx. No-op when DB nil.
func (r *SkillMarketplaceReconciler) projectSkillMarketplace(ctx context.Context, cr *achv1alpha1.SkillMarketplace, syncedStatus, syncedReason string) error {
	if r.DB == nil {
		return nil
	}
	row := buildSkillMarketplaceRow(cr, syncedStatus, syncedReason)
	return achdb.WithTxNotify(ctx, r.DB, skillMarketplacesChannel, cr.Name, func(tx pgx.Tx) error {
		return achdb.UpsertSkillMarketplaceTx(ctx, tx, row)
	})
}

// markSyncedTrue writes Synced=True with the supplied message + projects the
// object row. Mirrors PluginMarketplaceReconciler.markSyncedTrue.
func (r *SkillMarketplaceReconciler) markSyncedTrue(ctx context.Context, cr *achv1alpha1.SkillMarketplace, message string, requeue time.Duration) (ctrl.Result, error) {
	now := metav1.Now()
	desiredGen := cr.Generation
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh achv1alpha1.SkillMarketplace
		if err := r.Get(ctx, client.ObjectKeyFromObject(cr), &fresh); err != nil {
			return err
		}
		applyReconcileConditions(&fresh.Status.Conditions, ReasonSynced, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		fresh.Status.LastSuccessfulRefresh = &now
		fresh.Status.Skills = cr.Status.Skills
		fresh.Status.SkillsCount = cr.Status.SkillsCount
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
	if perr := r.projectSkillMarketplace(ctx, cr, "True", ""); perr != nil {
		return ctrl.Result{RequeueAfter: requeue}, perr
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// markSyncedFalse writes Synced=False + projects the object row. Mirrors
// PluginMarketplaceReconciler.markSyncedFalse: terminal reasons stop the
// hot-loop; transient reasons surface originalErr for workqueue backoff.
func (r *SkillMarketplaceReconciler) markSyncedFalse(ctx context.Context, cr *achv1alpha1.SkillMarketplace, reason, message string, requeue time.Duration, originalErr error) (ctrl.Result, error) {
	desiredGen := cr.Generation
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh achv1alpha1.SkillMarketplace
		if err := r.Get(ctx, client.ObjectKeyFromObject(cr), &fresh); err != nil {
			return err
		}
		applyReconcileConditions(&fresh.Status.Conditions, reason, message, desiredGen)
		fresh.Status.ObservedGeneration = desiredGen
		fresh.Status.Skills = cr.Status.Skills
		fresh.Status.SkillsCount = cr.Status.SkillsCount
		if u := r.Status().Update(ctx, &fresh); u != nil {
			return u
		}
		cr.Status = fresh.Status
		cr.ResourceVersion = fresh.ResourceVersion
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	if perr := r.projectSkillMarketplace(ctx, cr, "False", reason); perr != nil {
		return ctrl.Result{}, perr
	}
	switch reason {
	case ReasonInvalidConfig,
		ReasonUnauthorized,
		ReasonNotFound,
		ReasonUpstreamInvalid,
		ReasonPluginTooLarge:
		return ctrl.Result{RequeueAfter: requeue}, nil
	default:
		if originalErr != nil {
			return ctrl.Result{}, originalErr
		}
		return ctrl.Result{}, nil
	}
}

// SetupWithManager registers the reconciler with controller-runtime.
func (r *SkillMarketplaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.SkillMarketplace{}, builder.WithPredicates()).
		Named("ach-skillmarketplace")
	if r.ResyncSource != nil {
		b = b.WatchesRawSource(
			source.Channel(r.ResyncSource, &handler.EnqueueRequestForObject{}),
		)
	}
	return b.Complete(r)
}
