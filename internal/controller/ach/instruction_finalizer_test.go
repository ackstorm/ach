// SPDX-License-Identifier: Apache-2.0

// Instruction finalizer add/remove + OP-12 cached-file cleanup test —
// Plan 01-11 Task 4. Cache path: instruction/<name>.tar.gz (uniform context
// format — single upstream file wrapped into a 1-entry gzip-tar at
// ingestion — §10.3).

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

// TestInstructionFinalizerAddRemove mirrors the Plugin pattern but with the
// §10.3 instruction cache path (instruction/<name>.tar.gz — uniform context format).
func TestInstructionFinalizerAddRemove(t *testing.T) {
	ctx := context.Background()
	name := "test-instruction-finalizer"
	cachedFile := filepath.Join(testCacheRoot, "instruction", name+".tar.gz")

	if err := os.WriteFile(cachedFile, []byte("dummy-instruction-body"), 0o644); err != nil {
		t.Fatalf("seed cached file: %v", err)
	}

	cr := &achv1alpha1.Instruction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: WatchNamespace,
		},
		Spec: achv1alpha1.InstructionSpec{
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
		t.Fatalf("create Instruction: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cr)
		_ = os.Remove(cachedFile)
	})

	if !Eventually(func() bool {
		var got achv1alpha1.Instruction
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(&got, instructionFinalizer)
	}, 30*time.Second, 250*time.Millisecond) {
		t.Fatalf("finalizer %q never added within 30s", instructionFinalizer)
	}

	if _, err := os.Stat(cachedFile); err != nil {
		t.Fatalf("seeded file vanished before delete: %v", err)
	}

	var withFinalizer achv1alpha1.Instruction
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &withFinalizer); err != nil {
		t.Fatalf("re-get CR before delete: %v", err)
	}
	if err := k8sClient.Delete(ctx, &withFinalizer); err != nil {
		t.Fatalf("delete CR: %v", err)
	}

	probe := &achv1alpha1.Instruction{ObjectMeta: metav1.ObjectMeta{
		Name: cr.Name, Namespace: cr.Namespace,
	}}
	if !WaitForGone(ctx, probe, 15*time.Second) {
		t.Fatalf("Instruction CR not removed within 15s of Delete")
	}

	if _, err := os.Stat(cachedFile); err == nil {
		t.Errorf("OP-12 FAIL: cached file %s still exists after CR deletion", cachedFile)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OP-12 FAIL: stat cached file: unexpected error %v", err)
	}
}
