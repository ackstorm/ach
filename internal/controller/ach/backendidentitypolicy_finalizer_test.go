// SPDX-License-Identifier: Apache-2.0

// BackendIdentityPolicy finalizer add/remove test — Plan 01-11 Task 4.
// BackendIdentityPolicy has NO PVC-cached form (no Source*, no upstream
// content — Plan 01-05 design), so this test only asserts the CRD-06
// finalizer lifecycle.

package ach

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestBackendIdentityPolicyFinalizerAddRemove exercises the CRD-06
// finalizer lifecycle for BackendIdentityPolicy. No file-cleanup
// assertion — the BIP reconciler has no CacheRoot field by design.
func TestBackendIdentityPolicyFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	cr := &achv1alpha1.BackendIdentityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-bip-finalizer",
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.BackendIdentityPolicySpec{
			Target: achv1alpha1.BackendTargetRef{
				Kind: "MCPServer",
				Name: "example-mcp",
			},
			ForwardIdentityJWT: false,
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create BackendIdentityPolicy: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
	})

	if !Eventually(func() bool {
		var got achv1alpha1.BackendIdentityPolicy
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, backendIdentityPolicyFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", backendIdentityPolicyFinalizer)
	}

	var withFinalizer achv1alpha1.BackendIdentityPolicy
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	probe := &achv1alpha1.BackendIdentityPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("BackendIdentityPolicy CR not removed within 15s of Delete")
	}
}
