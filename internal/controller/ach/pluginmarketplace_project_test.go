// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestBuildMarketplaceRow_MapsStatusToRow asserts the pure row-builder copies
// the CR identity + the supplied Synced status/reason + pluginsCount + RV.
func TestBuildMarketplaceRow_MapsStatusToRow(t *testing.T) {
	cr := &achv1alpha1.PluginMarketplace{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ach",
			Name:            "ackstorm",
			ResourceVersion: "57772648",
		},
	}
	cr.Status.PluginsCount = 7

	rowTrue := buildMarketplaceRow(cr, "True", "")
	if rowTrue.Namespace != "ach" || rowTrue.Name != "ackstorm" {
		t.Errorf("identity = %s/%s, want ach/ackstorm", rowTrue.Namespace, rowTrue.Name)
	}
	if rowTrue.SyncedStatus != "True" || rowTrue.SyncedReason != "" {
		t.Errorf("synced = %q/%q, want True/empty", rowTrue.SyncedStatus, rowTrue.SyncedReason)
	}
	if rowTrue.PluginsCount != 7 {
		t.Errorf("PluginsCount = %d, want 7", rowTrue.PluginsCount)
	}
	if rowTrue.ResourceVersion != "57772648" {
		t.Errorf("ResourceVersion = %q, want 57772648", rowTrue.ResourceVersion)
	}

	rowFalse := buildMarketplaceRow(cr, "False", "UpstreamInvalid")
	if rowFalse.SyncedStatus != "False" || rowFalse.SyncedReason != "UpstreamInvalid" {
		t.Errorf("synced = %q/%q, want False/UpstreamInvalid", rowFalse.SyncedStatus, rowFalse.SyncedReason)
	}
}
