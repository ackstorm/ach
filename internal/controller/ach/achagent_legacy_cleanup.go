// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// agent-sandbox (kubernetes-sigs, v1.0.x) legacy kinds this cleanup retires — the only two GVKs
// a pre-workspace-v1 sandboxed ACHAgent ever owned. Only this file knows them; the workspace-v1
// control plane never creates, watches, or reads agent-sandbox objects again.
var (
	sandboxTemplateGVK = schema.GroupVersionKind{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Kind: "SandboxTemplate"}
	sandboxWarmPoolGVK = schema.GroupVersionKind{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Kind: "SandboxWarmPool"}
)

// +kubebuilder:rbac:groups=extensions.agents.x-k8s.io,resources=sandboxtemplates;sandboxwarmpools,verbs=get;delete

// pruneLegacySandbox deletes the pre-workspace-v1 agent-sandbox SandboxWarmPool then
// SandboxTemplate (pool first: a template deleted first can leave a pool referencing a
// vanished template transiently), both named achagent-<name> (agentResourceName — the
// existing legacy-name helper, same name pruneLegacyDeployment uses), if present AND
// controller-owned by this exact ACHAgent. A foreign owner, no owner, or a stale ACHAgent UID
// (same name reused by a different agent) is left untouched — same UID-precondition
// discipline as pruneLegacyDeployment (task-4-review.md finding 2). Idempotent: an absent
// object or a GVK the cluster does not serve (agent-sandbox CRDs never installed, or removed)
// is a no-op, never an error; any other discovery/Get/Delete failure propagates. Reads go
// through APIReader (uncached) — these GVKs have no informer (the controller no longer Owns()
// or Watches() them), so the cached Client would 404 forever. Never touches the old
// achagent-<name>-sandbox-key Secret, finalizers, or snapshots — this is deletion of the
// workload objects only.
func (r *ACHAgentReconciler) pruneLegacySandbox(ctx context.Context, a *achv1alpha1.ACHAgent) error {
	legacyName := agentResourceName(a.Name)
	for _, gvk := range []schema.GroupVersionKind{sandboxWarmPoolGVK, sandboxTemplateGVK} {
		if _, err := r.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			if apimeta.IsNoMatchError(err) {
				continue // agent-sandbox CRDs not installed (or removed) — nothing to prune
			}
			return fmt.Errorf("resolve %s mapping: %w", gvk.Kind, err)
		}

		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		key := types.NamespacedName{Namespace: a.Namespace, Name: legacyName}
		if err := r.APIReader.Get(ctx, key, u); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("get legacy %s: %w", gvk.Kind, err)
		}
		owner := metav1.GetControllerOf(u)
		if owner == nil || owner.APIVersion != achv1alpha1.GroupVersion.String() || owner.Kind != achAgentOwnerKind || owner.Name != a.Name || owner.UID != a.UID {
			continue
		}
		uid := u.GetUID()
		if err := r.Delete(ctx, u, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete legacy %s: %w", gvk.Kind, err)
		}
	}
	return nil
}
