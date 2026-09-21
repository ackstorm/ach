// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	achdb "github.com/ackstorm/ach/internal/db"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
)

// FetchedObjectReconciler is the field set shared by the four fetched-object
// reconcilers (Plugin, Skill, Prompt, Artifact). CacheRoot is the PVC mount
// root from ACH_CACHE_ROOT (default /var/cache/ach).
type FetchedObjectReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
	Log       logr.Logger
	CacheRoot string

	// DB is the Postgres pool used for external_refs UPSERT/GET/DELETE and
	// the projection row. Nil in envtest (finalizer test); the steady-state
	// branch skips DB reads/writes when nil so the existing test stays green.
	DB *pgxpool.Pool

	// Fetchers is the FetcherFactory; nil → defaults to registry.For.
	// Tests inject a fake fetcher to exercise the §10.3 staging /
	// rename(2) / UPSERT branches without live HTTPS traffic.
	Fetchers FetcherFactory

	// ResyncSource (issue #34 A10/A11) is the external source.Channel feed
	// used by the resync runnable (periodic full re-list) and the
	// refreshsignal listener (NOTIFY ach_refresh).
	ResyncSource chan event.GenericEvent

	// Metrics is the operator collector set (G7). Nil-tolerant (envtest
	// leaves it unset); wired from cmd/ach/cmd/operator.go.
	Metrics *achmetrics.OperatorCollectors
}

// projectRow runs upsert + NOTIFY(channel, "ns/name") in one transaction.
// Nil pool is the envtest path (no projection). achdb.ErrOriginConflict is
// returned raw so the caller can flip to Synced=False/ConflictWithUIRow;
// every other error is wrapped with the non-secret label.
func projectRow(ctx context.Context, pool *pgxpool.Pool, channel, ns, name, label string, upsert func(pgx.Tx) error) error {
	if pool == nil {
		return nil
	}
	err := achdb.WithTxNotify(ctx, pool, channel, ns+"/"+name, upsert)
	if err == nil || errors.Is(err, achdb.ErrOriginConflict) {
		return err
	}
	return fmt.Errorf("db upsert %s projection: %w", label, err)
}
