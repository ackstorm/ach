// SPDX-License-Identifier: Apache-2.0

// Artifact finalizer add/remove + OP-12 cached-file cleanup test —
// Plan 01-11 Task 4. Cache paths: object AND directory both now publish
// artifact/<name>.tar.gz (uniform context format); artifact/<name> is
// the legacy pre-uniform bare object path the finalizer still sweeps for
// cleanup. The reconciler attempts both and tolerates IsNotExist on
// either (Plan 01-05 decision). This test seeds BOTH paths to prove both
// attempt sites run.

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

// TestArtifactFinalizerAddRemove seeds both potential cache paths to
// prove the reconciler removes BOTH (its deletion path tries both
// because spec.scope is not consulted post-DeletionTimestamp — Plan
// 01-05 decision).
func TestArtifactFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	name := "test-artifact-finalizer"
	legacyBarePath := filepath.Join(testCacheRoot, "artifact", name)
	tarGzPath := filepath.Join(testCacheRoot, "artifact", name+".tar.gz")

	if err := os.WriteFile(legacyBarePath, []byte("dummy-artifact-legacy-bare"), 0o644); err != nil {
		t.Fatalf("seed legacy bare cached file: %v", err)
	}
	if err := os.WriteFile(tarGzPath, []byte("dummy-artifact-archive"), 0o644); err != nil {
		t.Fatalf("seed .tar.gz cached file: %v", err)
	}

	cr := &achv1alpha1.Artifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.ArtifactSpec{
			Type:  "s3",
			Scope: "directory",
			S3: &achv1alpha1.S3Source{
				Bucket: "ach-example-bucket",
				Key:    "artifacts/customer-kb/",
				Region: "us-east-1",
				AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
					Name:               "ach-example-s3",
					AccessKeyIDKey:     "accessKeyId",
					SecretAccessKeyKey: "secretAccessKey",
				},
			},
			Refresh: achv1alpha1.RefreshBlock{
				MaxStaleness: metav1.Duration{Duration: 12 * time.Hour},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create Artifact: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		_ = os.Remove(legacyBarePath)
		_ = os.Remove(tarGzPath)
	})

	if !Eventually(func() bool {
		var got achv1alpha1.Artifact
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, artifactFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", artifactFinalizer)
	}

	var withFinalizer achv1alpha1.Artifact
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	probe := &achv1alpha1.Artifact{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("Artifact CR not removed within 15s of Delete")
	}

	// Both seeded paths MUST be gone (the reconciler attempts both with
	// IsNotExist tolerance).
	for _, p := range []string{legacyBarePath, tarGzPath} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("OP-12 FAIL: cached file %s still exists after CR deletion", p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("OP-12 FAIL: stat %s: unexpected error %v", p, err)
		}
	}
}
