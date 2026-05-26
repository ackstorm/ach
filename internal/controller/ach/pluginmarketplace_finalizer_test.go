// SPDX-License-Identifier: Apache-2.0

// PluginMarketplace finalizer add/remove + OP-12 cached-subtree cleanup test —
// Plan 01-11 Task 4. Cache path: marketplace/<name>/ (entire subtree per
// §10.3 — reconciler uses os.RemoveAll).

package ach

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestPluginMarketplaceFinalizerAddRemove pre-creates a subtree at
// <testCacheRoot>/marketplace/<name>/plugin/<plugin>.tar.gz with a
// dummy file, then asserts the reconciler's os.RemoveAll deletes the
// entire subtree (OP-12). The reconciler uses RemoveAll rather than
// Remove because §10.3 carves multiple plugin archives under a single
// marketplace directory.
func TestPluginMarketplaceFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	name := "test-marketplace-finalizer"
	subtreeDir := filepath.Join(testCacheRoot, "marketplace", name, "plugin")
	subtreeFile := filepath.Join(subtreeDir, "example.tar.gz")

	if err := os.MkdirAll(subtreeDir, 0o755); err != nil {
		t.Fatalf("seed marketplace subtree dir: %v", err)
	}
	if err := os.WriteFile(subtreeFile, []byte("dummy-marketplace-plugin"), 0o644); err != nil {
		t.Fatalf("seed marketplace subtree file: %v", err)
	}

	cr := &achv1alpha1.PluginMarketplace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.PluginMarketplaceSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "ackstorm/example-marketplace",
				Ref:  "main",
				AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
					Name: "github-readonly",
					Key:  "access-key",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				MaxStaleness: metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create PluginMarketplace: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		_ = os.RemoveAll(filepath.Join(testCacheRoot, "marketplace", name))
	})

	if !Eventually(func() bool {
		var got achv1alpha1.PluginMarketplace
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, pluginMarketplaceFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", pluginMarketplaceFinalizer)
	}

	if _, err := os.Stat(subtreeFile); err != nil {
		t.Fatalf("seeded subtree file vanished before delete: %v", err)
	}

	var withFinalizer achv1alpha1.PluginMarketplace
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	probe := &achv1alpha1.PluginMarketplace{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("PluginMarketplace CR not removed within 15s of Delete")
	}

	// Assert the entire marketplace/<name>/ subtree is gone (OP-12).
	subtreeRoot := filepath.Join(testCacheRoot, "marketplace", name)
	if _, err := os.Stat(subtreeRoot); err == nil {
		t.Errorf("OP-12 FAIL: marketplace subtree %s still exists after CR deletion", subtreeRoot)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OP-12 FAIL: stat marketplace subtree: unexpected error %v", err)
	}
}
