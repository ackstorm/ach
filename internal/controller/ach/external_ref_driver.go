// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	achdb "github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/sources"
)

// externalRefCR is the constraint for the generic content-reconciler
// driver: any CR whose pointer is a client.Object exposing the shared
// ExternalRefStatus. Satisfied by *Plugin, *Prompt, *Artifact.
type externalRefCR[T any] interface {
	*T
	client.Object
	GetExternalRefStatus() *achv1alpha1.ExternalRefStatus
}

// externalRefSpecView is the per-kind projection of cr.Spec the driver
// needs. The per-kind reconciler builds it from its typed spec (the only
// place that knows the concrete *Spec shape).
type externalRefSpecView struct {
	SourceSpec    sources.SourceSpec
	AuthSecretRef *achv1alpha1.SourceAuthSecretRef
	Refresh       achv1alpha1.RefreshBlock
	Scope         string // "" for plugin/prompt; cr.Spec.Scope for artifact
	SizeCapBytes  int64  // plugin: PluginMaxSizeMiB<<20; prompt/artifact: 0
}

// externalRefDriverConfig wires a per-kind reconciler into the generic
// driver. The driver owns the shared state machine (Get → deletion →
// finalizer → §10.3 gate → materialize → failure switch → success status
// → projection → status update → annotation clear); the config supplies
// the per-kind knobs.
type externalRefDriverConfig[T any, PT externalRefCR[T]] struct {
	client    client.Client
	db        *pgxpool.Pool
	cacheRoot string
	namespace string
	fetchers  FetcherFactory

	kind      string // "plugin" / "prompt" / "artifact"
	finalizer string // pluginFinalizer / promptFinalizer / artifactFinalizer

	// specView projects cr.Spec → the driver's needed fields.
	specView func(PT) externalRefSpecView
	// handleDeletion dispatches to the per-kind deletion path.
	handleDeletion func(context.Context, PT, logr.Logger) (ctrl.Result, error)
	// writeProjection dual-writes the per-kind projection row + NOTIFY,
	// returning achdb.ErrOriginConflict raw on a UI-row conflict.
	writeProjection func(context.Context, PT, time.Time, time.Duration) error
}

// reconcileExternalRefCR is the shared content-reconciler state machine.
// See plugin/prompt/artifact_controller.go Reconcile wrappers for usage.
func reconcileExternalRefCR[T any, PT externalRefCR[T]](
	ctx context.Context,
	req ctrl.Request,
	cfg externalRefDriverConfig[T, PT],
) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(cfg.kind, req.NamespacedName)

	var obj T
	cr := PT(&obj)
	if err := cfg.client.Get(ctx, req.NamespacedName, cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// ─── Deletion path. ───
	if !cr.GetDeletionTimestamp().IsZero() {
		return cfg.handleDeletion(ctx, cr, logger)
	}

	// ─── Finalizer-add path. ───
	if !controllerutil.ContainsFinalizer(cr, cfg.finalizer) {
		controllerutil.AddFinalizer(cr, cfg.finalizer)
		if err := cfg.client.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	view := cfg.specView(cr)
	st := cr.GetExternalRefStatus()

	// ─── Phase 2 steady state: §10.3 refresh. ───
	var priorRev string
	var lastRefresh time.Time
	var forceRefreshRequestedAt time.Time
	if cfg.db != nil {
		priorRow, err := achdb.GetExternalRef(ctx, cfg.db, cfg.kind, cr.GetName())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("db get external_ref: %w", err)
		}
		if priorRow != nil {
			priorRev = priorRow.UpstreamRev
			lastRefresh = priorRow.LastSuccessfulRefresh
			forceRefreshRequestedAt = priorRow.ForceRefreshRequestedAt
		}
	}

	// F1 review: a spec change bumps .metadata.generation. spec.path in
	// particular changes WHAT content is fetched without moving the upstream
	// commit SHA — so sending the prior SHA as PriorRev would let the fetcher
	// short-circuit NotModified and keep serving content narrowed to the OLD
	// path. Clear PriorRev on a generation change to force a full re-fetch +
	// re-narrow (harmless for ref/repo changes, which already resolve a new SHA).
	if st.ObservedGeneration != cr.GetGeneration() {
		priorRev = ""
	}

	// §10.3 within-interval gate (reads status.LastSuccessfulRefresh — the
	// reliable source across the NotModified/304 path; see the original
	// per-kind comment block for the full rationale).
	var gateLastRefresh time.Time
	if st.LastSuccessfulRefresh != nil {
		gateLastRefresh = st.LastSuccessfulRefresh.Time
	}
	if shouldSkipFetch(view.Refresh, gateLastRefresh, st.ObservedGeneration, cr.GetGeneration(), cr.GetAnnotations(), forceRefreshRequestedAt, time.Now()) {
		remaining := time.Until(gateLastRefresh.Add(requeueDurationFromRefresh(view.Refresh)))
		if remaining < time.Second {
			remaining = time.Second
		}
		logger.V(1).Info("§10.3 within-interval gate: skipping fetch",
			"lastRefresh", gateLastRefresh, "requeueAfter", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	finalPath := computeFinalPath(cfg.cacheRoot, cfg.kind, cr.GetName(), view.Scope)

	deps := ExternalRefRefreshDeps{
		Client:        cfg.client,
		Namespace:     cfg.namespace,
		DB:            cfg.db,
		CacheRoot:     cfg.cacheRoot,
		Kind:          cfg.kind,
		Name:          cr.GetName(),
		SourceSpec:    view.SourceSpec,
		AuthSecretRef: view.AuthSecretRef,
		Refresh:       view.Refresh,
		PriorRev:      priorRev,
		SizeCapBytes:  view.SizeCapBytes,
		FinalPath:     finalPath,
		Fetchers:      cfg.fetchers,
		Log:           logger,
	}
	result := materializeExternalRef(ctx, deps)

	requeue := requeueDurationFromRefresh(view.Refresh)

	// ─── Failure path. ───
	if result.Err != nil {
		reason, message := classifyFetchError(result.Err, view.Refresh, lastRefresh)
		applyReconcileConditions(&st.Conditions, reason, message, cr.GetGeneration())
		st.ObservedGeneration = cr.GetGeneration()
		desired := *st
		if statusErr := retryStatusUpdate(ctx, cfg.client, cr, func(fresh PT) {
			*fresh.GetExternalRefStatus() = desired
		}); statusErr != nil {
			logger.Error(statusErr, "status update failed", "reason", reason)
		}
		switch reason {
		case ReasonPluginTooLarge, ReasonUnauthorized, ReasonNotFound, ReasonUpstreamInvalid:
			return ctrl.Result{RequeueAfter: requeue}, nil
		default:
			return ctrl.Result{}, result.Err
		}
	}

	// ─── Success / NotModified. ───
	applyReconcileConditions(&st.Conditions, ReasonSynced, sourceReachableMessage(view.SourceSpec), cr.GetGeneration())
	st.UpstreamRev = result.UpstreamRev
	if result.NotModified {
		if st.StorageLocation == "" {
			st.StorageLocation = finalPath
		}
	} else {
		st.StorageLocation = finalPath
	}
	now := metav1.Now()
	st.LastSuccessfulRefresh = &now
	st.ObservedGeneration = cr.GetGeneration()

	// Dual-write the projection row BEFORE the best-effort status update.
	if err := cfg.writeProjection(ctx, cr, now.Time, view.Refresh.MaxStaleness.Duration); err != nil {
		if errors.Is(err, achdb.ErrOriginConflict) {
			return writeExternalRefConflictStatus(ctx, cfg.client, cr, logger)
		}
		return ctrl.Result{}, err
	}

	desired := *st
	if err := retryStatusUpdate(ctx, cfg.client, cr, func(fresh PT) {
		*fresh.GetExternalRefStatus() = desired
	}); err != nil {
		logger.Error(err, "status update failed; skipping annotation-clear")
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	// Clear force-refresh annotation if present.
	if ann := cr.GetAnnotations(); ann != nil {
		if _, ok := ann["ach.ackstorm.ai/force-refresh"]; ok {
			delete(ann, "ach.ackstorm.ai/force-refresh")
			cr.SetAnnotations(ann)
			if err := cfg.client.Update(ctx, cr); err != nil {
				logger.Error(err, "force-refresh annotation removal failed")
			}
		}
	}

	return ctrl.Result{RequeueAfter: requeue}, nil
}

// writeExternalRefConflictStatus flips Synced=False/ConflictWithUIRow for
// any content CR sharing ExternalRefStatus. Subsumes the three byte-
// identical writeXConflictStatus methods (review finding D6).
func writeExternalRefConflictStatus[T any, PT externalRefCR[T]](
	ctx context.Context,
	c client.Client,
	cr PT,
	logger logr.Logger,
) (ctrl.Result, error) {
	st := cr.GetExternalRefStatus()
	setConflictWithUIRowCondition(&st.Conditions, ConditionSynced, cr.GetGeneration())
	st.ObservedGeneration = cr.GetGeneration()
	desired := *st
	if err := retryStatusUpdate(ctx, c, cr, func(fresh PT) {
		*fresh.GetExternalRefStatus() = desired
	}); err != nil {
		logger.Error(err, "status update failed", "reason", ReasonConflictWithUIRow)
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}
