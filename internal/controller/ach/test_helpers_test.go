// SPDX-License-Identifier: Apache-2.0

// Shared envtest helpers for the per-kind admission and finalizer specs
// (Plan 01-11). The helpers live in their own file so the suite bootstrap
// (suite_test.go) stays focused on TestMain / setupAndRun lifecycle.

package ach

import (
	"context"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyFixture reads a YAML file from disk, decodes it into an Unstructured
// object, and Create-applies it to the envtest API server via k8sClient.
//
// The function is the CEL-admission-test workhorse: invalid fixtures
// return the apierrors.StatusError carrying the CEL message text, valid
// fixtures return nil. Callers compare err for nil-vs-non-nil and (when
// non-nil) substring-match against the expected CEL message fragment.
//
// Unstructured decoding (rather than typed Create) is deliberate: it lets
// one helper apply any of the six ACH kinds without per-kind switch
// statements. The API server enforces CEL admission identically regardless
// of how the request was constructed.
func ApplyFixture(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		return fmt.Errorf("read fixture %s: %w", path, err)
	}
	u := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(raw, u); err != nil {
		return fmt.Errorf("unmarshal fixture %s: %w", path, err)
	}
	return k8sClient.Create(ctx, u)
}

// DeleteByGVKName Get-then-Delete an Unstructured by its api version, kind,
// namespace, and name. Used to clean up successfully-applied valid fixtures
// between subtest iterations so subsequent applies see a clean slate.
//
// Returns nil when the object is already gone (NotFound is treated as
// success) and ignores polling errors during finalizer drain — callers
// can use WaitForGone if they need a hard "object disappeared" assertion.
func DeleteByGVKName(ctx context.Context, apiVersion, kind, namespace, name string) error {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetNamespace(namespace)
	u.SetName(name)
	if err := k8sClient.Delete(ctx, u); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// WaitForGone polls until the supplied object no longer exists, or until
// the deadline elapses. Returns true on disappearance, false on timeout.
// Used by finalizer tests to assert that the controller's finalizer drain
// runs to completion (the API server removes the object only after the
// finalizer list empties).
func WaitForGone(ctx context.Context, obj client.Object, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	key := client.ObjectKeyFromObject(obj)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(ctx, key, obj)
		if apierrors.IsNotFound(err) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Eventually polls cond at the supplied interval until it returns true or
// the timeout elapses. Returns true on success, false on timeout. Used in
// place of testify/require.Eventually so the suite stays stdlib-only.
func Eventually(cond func() bool, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(interval)
	}
	return cond()
}
