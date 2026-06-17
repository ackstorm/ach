// SPDX-License-Identifier: Apache-2.0

// BackendIdentityPolicy duplicate-target advisory test (G15). When ≥2 live
// BIPs name the same (target.kind, target.name) the alpha-FIRST metadata.name
// wins the forwarder tiebreak (bipcache.Resolve keys on rows[0]); the loser(s)
// get an advisory Synced=False/NameConflict condition. Runtime stays
// forwarder-resolved — the status is informational only.

package ach

import (
	"context"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestBIPDuplicateTarget_ShadowedGetsNameConflict applies two BIPs ("aaa" and
// "zzz") both targeting {kind:"MCPServer", name:"echo"} with forwardIdentityJWT=true.
// The alpha-FIRST name "aaa" wins; "zzz" must surface
// Synced=False/NameConflict referencing "BackendIdentityPolicy/aaa", and "aaa"
// must NOT carry a NameConflict.
func TestBIPDuplicateTarget_ShadowedGetsNameConflict(t *testing.T) {
	ctx := context.Background()

	mk := func(name string) *achv1alpha1.BackendIdentityPolicy {
		return &achv1alpha1.BackendIdentityPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: WatchNamespace},
			Spec: achv1alpha1.BackendIdentityPolicySpec{
				Target:             achv1alpha1.BackendTargetRef{Kind: "MCPServer", Name: "echo"},
				ForwardIdentityJWT: true,
			},
		}
	}
	aaa := mk("aaa")
	zzz := mk("zzz")
	for _, cr := range []*achv1alpha1.BackendIdentityPolicy{aaa, zzz} {
		if err := k8sClient.Create(ctx, cr); err != nil {
			t.Fatalf("create %s: %v", cr.Name, err)
		}
		c := cr
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), c) })
	}

	// zzz (loser) eventually carries Synced=False/NameConflict referencing aaa.
	if !Eventually(func() bool {
		var got achv1alpha1.BackendIdentityPolicy
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(zzz), &got); err != nil {
			return false
		}
		cond := apimeta.FindStatusCondition(got.Status.Conditions, "Synced")
		return cond != nil &&
			cond.Status == metav1.ConditionFalse &&
			cond.Reason == ReasonNameConflict &&
			strings.Contains(cond.Message, "BackendIdentityPolicy/aaa")
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("zzz never got Synced=False/NameConflict referencing aaa")
	}

	// aaa (winner) must NOT carry a NameConflict.
	var gotAAA achv1alpha1.BackendIdentityPolicy
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(aaa), &gotAAA); err != nil {
		t.Fatalf("get aaa: %v", err)
	}
	if cond := apimeta.FindStatusCondition(gotAAA.Status.Conditions, "Synced"); cond != nil && cond.Reason == ReasonNameConflict {
		t.Fatalf("aaa (alpha-FIRST winner) must not carry NameConflict, got %+v", cond)
	}
}
