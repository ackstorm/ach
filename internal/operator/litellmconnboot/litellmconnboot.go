// SPDX-License-Identifier: Apache-2.0

// Package litellmconnboot lets the operator bootstrap the canonical
// LiteLLMConnection/default CR on boot (issue #34 single-source-of-truth).
//
// Why the operator and not the Helm chart: the chart renders the ACH CRDs as
// ordinary templates (deploy/helm/ach/templates/crds.yaml) for managed-upgrade
// reasons, NOT via Helm's crds/ directory. Helm builds its REST mapper once per
// action and only refreshes it after installing the crds/ directory — never
// after a templates-rendered CRD. So on a FRESH `helm install` no manifest in
// the same release (a normal template OR a post-install hook) can be mapped to
// kind LiteLLMConnection: it fails with "no matches for kind ... in version
// ach.ackstorm.ai/v1alpha1". A prior hook-based attempt hit exactly this.
//
// The operator pod, by contrast, starts AFTER the CRD object exists and talks
// to the live API server with its own controller-runtime client (fresh
// discovery), so it can create the CR race-free. EnsureConnection mirrors
// jwtkeys.EnsureSigningKeys: run with a direct uncached client BEFORE
// mgr.Start. It is the only writer of the bootstrap spec; the helm
// litellmConnection values block is the single user-facing input and flows in
// via the operator's --litellm-* flags. Services (forwarder, platform-api,
// content-service) keep reading the resulting Postgres projection — they never
// see this bootstrap path.
//
// Idempotent + drift-correcting: if the CR is absent it is created; if present
// but its spec differs from the helm-desired spec (e.g. the operator was
// upgraded with a changed endpoint) the spec is updated so a `helm upgrade`
// value change propagates. Status is left untouched — that is the reconciler's.
package litellmconnboot

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// crdEstablishTimeout bounds how long EnsureConnection waits for the
// LiteLLMConnection CRD to become servable. On a fresh `helm install` the CRD
// object is created in the same release that schedules the operator Deployment;
// the operator pod usually starts well after the CRD is Established, but a
// fresh client's REST mapper can briefly lag — so a NoMatch (kind not yet in
// discovery) is retried rather than treated as fatal.
const (
	crdEstablishTimeout  = 60 * time.Second
	crdEstablishInterval = 2 * time.Second
)

// EnsureConnection get-or-creates LiteLLMConnection/<name> in namespace with
// the given endpoint + master-key Secret reference. A no-op-with-update on
// drift, it never touches status.
//
// endpoint == "" means the litellmConnection block is disabled in values; the
// function logs and returns nil (nothing to bootstrap).
//
// A concurrent creator (another operator replica racing between Get and Create)
// is handled: AlreadyExists on Create is treated as success.
func EnsureConnection(ctx context.Context, c client.Client, namespace, name, endpoint, secretName, secretKey string, log logr.Logger) error {
	if endpoint == "" {
		log.Info("litellmConnection bootstrap disabled (no endpoint) — skipping")
		return nil
	}

	key := client.ObjectKey{Namespace: namespace, Name: name}
	desired := achv1alpha1.LiteLLMConnectionSpec{
		Endpoint: endpoint,
		MasterKeySecretRef: achv1alpha1.SecretKeyRef{
			Name: secretName,
			Key:  secretKey,
		},
	}

	// Bounded retry: tolerate NoMatch (CRD not yet servable in this client's
	// discovery cache) until the CRD is established or the timeout elapses.
	return wait.PollUntilContextTimeout(ctx, crdEstablishInterval, crdEstablishTimeout, true,
		func(ctx context.Context) (bool, error) {
			var existing achv1alpha1.LiteLLMConnection
			err := c.Get(ctx, key, &existing)
			switch {
			case err == nil:
				if existing.Spec == desired {
					log.Info("LiteLLMConnection already present and in sync — leaving as-is",
						"namespace", namespace, "name", name, "endpoint", endpoint)
					return true, nil
				}
				existing.Spec = desired
				if uerr := c.Update(ctx, &existing); uerr != nil {
					if meta.IsNoMatchError(uerr) {
						return false, nil // CRD vanished mid-flight; retry
					}
					return false, fmt.Errorf("litellmconnboot: update %s/%s: %w", namespace, name, uerr)
				}
				log.Info("LiteLLMConnection spec drifted from values — updated to desired",
					"namespace", namespace, "name", name, "endpoint", endpoint)
				return true, nil

			case meta.IsNoMatchError(err):
				// CRD not yet established/served — wait and retry.
				log.Info("LiteLLMConnection CRD not yet servable — waiting", "namespace", namespace, "name", name)
				return false, nil

			case apierrors.IsNotFound(err):
				conn := &achv1alpha1.LiteLLMConnection{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      name,
						Labels: map[string]string{
							"app.kubernetes.io/name":       "ach",
							"app.kubernetes.io/component":  "litellm-connection",
							"app.kubernetes.io/managed-by": "ach-operator",
						},
					},
					Spec: desired,
				}
				if cerr := c.Create(ctx, conn); cerr != nil {
					if apierrors.IsAlreadyExists(cerr) {
						log.Info("LiteLLMConnection created concurrently — leaving as-is",
							"namespace", namespace, "name", name)
						return true, nil
					}
					if meta.IsNoMatchError(cerr) {
						return false, nil // CRD established between Get and Create raced away; retry
					}
					return false, fmt.Errorf("litellmconnboot: create %s/%s: %w", namespace, name, cerr)
				}
				log.Info("bootstrapped LiteLLMConnection", "namespace", namespace, "name", name, "endpoint", endpoint)
				return true, nil

			default:
				return false, fmt.Errorf("litellmconnboot: get %s/%s: %w", namespace, name, err)
			}
		})
}
