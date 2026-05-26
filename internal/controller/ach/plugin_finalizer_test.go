// SPDX-License-Identifier: Apache-2.0

// Plugin finalizer add/remove + OP-12 cached-file cleanup test —
// Plan 01-11 Task 4. Asserts CRD-06 (finalizer add) + §10.3 cached
// file removal before finalizer drops.

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

// TestPluginFinalizerAddRemove asserts:
//
//  1. Plugin CR Create → finalizer plugins.ach.ackstorm.ai/finalizer added.
//  2. A pre-created dummy file at <testCacheRoot>/plugin/<name>.tar.gz
//     exists at delete time.
//  3. Plugin CR Delete → CR removed AND the cached file is gone (OP-12
//     §10.3 external-ref finalizer cleanup).
//
// The reconciler is wired with CacheRoot=testCacheRoot in suite_test.go,
// so the deletion path os.Remove path resolves to this test's temp dir.
func TestPluginFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	name := "test-plugin-finalizer"
	cachedFile := filepath.Join(testCacheRoot, "plugin", name+".tar.gz")

	// Pre-create the dummy cached file BEFORE the CR exists — the
	// reconciler does not write this file in Phase 1 (Phase 2 owns the
	// refresh path), so we must seed it manually.
	if err := os.WriteFile(cachedFile, []byte("dummy-plugin-archive"), 0o644); err != nil {
		t.Fatalf("seed cached file: %v", err)
	}

	cr := &achv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.PluginSpec{
			Type: "github",
			GitHub: &achv1alpha1.GitHubSource{
				Repo: "ackstorm/example",
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
		t.Fatalf("create Plugin: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		_ = os.Remove(cachedFile)
	})

	// Step 1: poll for finalizer add.
	if !Eventually(func() bool {
		var got achv1alpha1.Plugin
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, pluginFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", pluginFinalizer)
	}

	// Sanity check: cached file still exists right before delete.
	if _, err := os.Stat(cachedFile); err != nil {
		t.Fatalf("seeded file vanished before delete: %v", err)
	}

	// Step 2: re-Get and Delete.
	var withFinalizer achv1alpha1.Plugin
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	// Step 3: poll for CR removal.
	probe := &achv1alpha1.Plugin{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("Plugin CR not removed within 15s of Delete (finalizer drain did not complete)")
	}

	// Step 4: assert cached file was removed before finalizer drop
	// (OP-12). os.Stat returns IsNotExist when the file is gone.
	if _, err := os.Stat(cachedFile); err == nil {
		t.Errorf("OP-12 FAIL: cached file %s still exists after CR deletion", cachedFile)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OP-12 FAIL: stat cached file: unexpected error %v", err)
	}
}
