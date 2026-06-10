// SPDX-License-Identifier: Apache-2.0

// Package litellmconn resolves the LiteLLMConnection/default projection +
// its master-key Secret into the (endpoint, masterKey) pair the forwarder
// needs at boot.
//
// Postgres is the source of truth for the CR projection (issue #34); the
// referenced Secret is read from the Kubernetes control plane via the
// caller's client.Reader. The forwarder retains a filtered Secret cache
// only for ach-jwt-signing-keys hot-reload; the LiteLLM master-key Secret
// is read once at boot via an uncached APIReader.
//
// Refuse-to-start contract: Resolve returns a wrapped sentinel when any
// of the projection row, the Secret, or the named key are
// missing/empty. The forwarder surfaces those errors verbatim so
// operators see exactly which piece of the chain is unwired. The
// resolveLiteLLMWithRetry caller treats ErrLiteLLMConnectionNotReady
// (projection row absent) and ErrSecretNotFound (Secret not yet
// reconciled by the operator) as retryable boot states.
package litellmconn

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
)

// CRName is the only admitted LiteLLMConnection name per namespace
// (enforced by the CRD's XValidation rule and by the projection's
// GetDefaultLiteLLMConnection helper).
const CRName = "default"

// Resolution carries the values resolved from the projection row +
// Secret pair.
type Resolution struct {
	// Endpoint is the LiteLLM upstream URL (CR Spec.Endpoint, e.g.
	// http://litellm.litellm-system.svc.cluster.local:4000).
	Endpoint string

	// MasterKey is the literal string read from the Secret data at the
	// key named by CR Spec.MasterKeySecretRef.Key. Used by the forwarder
	// as the litellm REST admin credential for the TeamsResolver precheck
	// (/user/info). TESTING-PHASE (reverts FIX01 §A.6 / D-13): it is NO
	// LONGER injected as the x-litellm-api-key header on proxied requests —
	// the Director forwards the caller's own LiteLLM virtual key instead.
	MasterKey string
}

// Sentinel errors callers can unwrap for refuse-to-start classification.
//
// ErrLiteLLMConnectionNotReady replaces the legacy ErrCRNotFound: the
// forwarder no longer reads the CR directly, so "not found" is now a
// "projection row absent" condition. The retry-wrapper treats this
// (alongside ErrSecretNotFound) as a transient cluster-hydration race
// that resolves once the operator runs.
var (
	ErrLiteLLMConnectionNotReady = errors.New("LiteLLMConnection projection row not yet reconciled")
	ErrEndpointEmpty             = errors.New("LiteLLMConnection.endpoint empty")
	ErrSecretNotFound            = errors.New("masterKey Secret not found")
	ErrSecretKeyMissing          = errors.New("masterKey Secret key empty/missing")
)

// Resolve reads the litellm_connections/default projection row from
// Postgres, then dereferences its MasterKeySecretRef into a Kubernetes
// Secret via the given uncached client.Reader (typically
// mgr.GetAPIReader() at bootstrap).
//
// The pool is the source of truth for the connection spec; the k8s
// reader exists solely because Secrets stay on the Kubernetes control
// plane (not projected to Postgres).
func Resolve(ctx context.Context, pool *pgxpool.Pool, reader client.Reader, namespace string) (*Resolution, error) {
	row, err := db.GetDefaultLiteLLMConnection(ctx, pool, namespace)
	if err != nil {
		return nil, fmt.Errorf("db.GetDefaultLiteLLMConnection(%s): %w", namespace, err)
	}
	if row == nil {
		return nil, fmt.Errorf("%w: %s/%s", ErrLiteLLMConnectionNotReady, namespace, CRName)
	}
	if row.Endpoint == "" {
		return nil, fmt.Errorf("%w: %s/%s", ErrEndpointEmpty, namespace, CRName)
	}

	var secret corev1.Secret
	secKey := types.NamespacedName{
		Namespace: row.MasterKeySecretNamespace,
		Name:      row.MasterKeySecretName,
	}
	if err := reader.Get(ctx, secKey, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrSecretNotFound,
				row.MasterKeySecretNamespace, row.MasterKeySecretName)
		}
		return nil, fmt.Errorf("get masterKey Secret %s/%s: %w",
			row.MasterKeySecretNamespace, row.MasterKeySecretName, err)
	}

	raw, ok := secret.Data[row.MasterKeySecretKey]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("%w: %s/%s data[%q]", ErrSecretKeyMissing,
			row.MasterKeySecretNamespace, row.MasterKeySecretName, row.MasterKeySecretKey)
	}

	return &Resolution{
		Endpoint:  row.Endpoint,
		MasterKey: string(raw),
	}, nil
}
