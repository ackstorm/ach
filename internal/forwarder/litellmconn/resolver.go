// SPDX-License-Identifier: Apache-2.0

// Package litellmconn resolves the LiteLLMConnection/default CR + its
// master-key Secret into the (endpoint, masterKey) pair the forwarder
// needs at boot. Mirrors the operator's LiteLLMConnection reconciler
// probe path so the forwarder and operator agree on what "ready" means,
// without duplicating reconcile logic.
//
// Refuse-to-start contract: Resolve returns a wrapped error if any of
// the CR, the Secret, or the named key are missing/empty. The forwarder
// surfaces those errors verbatim so operators see exactly which piece
// of the chain is unwired.
//
// Hot-reload of the master key is intentionally out of scope here —
// operators rotate the Secret by editing the LiteLLMConnection CR or
// the Secret, and the forwarder Deployment is restarted to pick the new
// value up. The JWT signing-keys Secret has a stricter rotation SLO and
// uses a controller-runtime informer; the LiteLLM master key does not.
package litellmconn

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// CRName is the only admitted LiteLLMConnection name per namespace
// (enforced by the CRD's XValidation rule).
const CRName = "default"

// Resolution carries the values resolved from the CR + Secret pair.
type Resolution struct {
	// Endpoint is the LiteLLM upstream URL (CR Spec.Endpoint, e.g.
	// http://litellm.litellm-system.svc.cluster.local:4000).
	Endpoint string

	// MasterKey is the literal string read from the Secret data at the
	// key named by CR Spec.MasterKeySecretRef.Key. Used by the forwarder
	// both as the litellm REST admin credential AND as the
	// x-litellm-api-key header value StripAndRewrite injects on every
	// proxied request (proxy-trust assertion — LiteLLM upstream rejects
	// requests without this header with 401).
	MasterKey string
}

// Sentinel errors callers can unwrap for refuse-to-start classification.
var (
	ErrCRNotFound       = errors.New("LiteLLMConnection CR not found")
	ErrEndpointEmpty    = errors.New("LiteLLMConnection.spec.endpoint empty")
	ErrSecretNotFound   = errors.New("masterKey Secret not found")
	ErrSecretKeyMissing = errors.New("masterKey Secret key empty/missing")
)

// Resolve reads LiteLLMConnection/default + the referenced master-key
// Secret in `namespace` via an uncached client.Reader (typically
// mgr.GetAPIReader() during bootstrap, before the controller-runtime
// cache has synced).
func Resolve(ctx context.Context, reader client.Reader, namespace string) (*Resolution, error) {
	var conn achv1alpha1.LiteLLMConnection
	connKey := types.NamespacedName{Namespace: namespace, Name: CRName}
	if err := reader.Get(ctx, connKey, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrCRNotFound, namespace, CRName)
		}
		return nil, fmt.Errorf("get LiteLLMConnection %s/%s: %w", namespace, CRName, err)
	}
	if conn.Spec.Endpoint == "" {
		return nil, fmt.Errorf("%w: %s/%s", ErrEndpointEmpty, namespace, CRName)
	}

	var secret corev1.Secret
	secKey := types.NamespacedName{
		Namespace: namespace,
		Name:      conn.Spec.MasterKeySecretRef.Name,
	}
	if err := reader.Get(ctx, secKey, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrSecretNotFound,
				namespace, conn.Spec.MasterKeySecretRef.Name)
		}
		return nil, fmt.Errorf("get masterKey Secret %s/%s: %w",
			namespace, conn.Spec.MasterKeySecretRef.Name, err)
	}

	raw, ok := secret.Data[conn.Spec.MasterKeySecretRef.Key]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("%w: %s/%s data[%q]", ErrSecretKeyMissing,
			namespace, conn.Spec.MasterKeySecretRef.Name,
			conn.Spec.MasterKeySecretRef.Key)
	}

	return &Resolution{
		Endpoint:  conn.Spec.Endpoint,
		MasterKey: string(raw),
	}, nil
}
