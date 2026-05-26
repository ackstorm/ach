// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/connection"
)

func TestLiteLLMConnectionProbeReady(t *testing.T) {
	ctx := context.Background()
	cleanupLiteLLMConnection(t, ctx)
	connCache.Rebuild(connection.Snapshot{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "litellm-master-key", Namespace: WatchNamespace},
		Data:       map[string][]byte{"masterKey": []byte("sk-test-master-key")},
	}
	if err := k8sClient.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create secret: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), secret) })

	cr := &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: WatchNamespace},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint: srv.URL,
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{
				Name: "litellm-master-key",
				Key:  "masterKey",
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create LiteLLMConnection/default: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		return connCache.Snapshot().Reason == "Synced"
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatalf("connection cache reason=%q, want Synced", connCache.Snapshot().Reason)
	}

	var got achv1alpha1.LiteLLMConnection
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatalf("get LiteLLMConnection/default: %v", err)
	}
	if !apimeta.IsStatusConditionTrue(got.Status.Conditions, "Ready") {
		t.Fatalf("Ready condition not true: %+v", got.Status.Conditions)
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf("observedGeneration=%d, want %d", got.Status.ObservedGeneration, got.Generation)
	}
}

func TestLiteLLMConnectionSecretMissing(t *testing.T) {
	ctx := context.Background()
	cleanupLiteLLMConnection(t, ctx)
	connCache.Rebuild(connection.Snapshot{})

	cr := &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: WatchNamespace},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint: "http://127.0.0.1:1",
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{
				Name: "missing",
				Key:  "masterKey",
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create LiteLLMConnection/default: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

	if !Eventually(func() bool {
		return connCache.Snapshot().Reason == "SecretNotFound"
	}, 10*time.Second, 100*time.Millisecond) {
		t.Fatalf("connection cache reason=%q, want SecretNotFound", connCache.Snapshot().Reason)
	}
}

func cleanupLiteLLMConnection(t *testing.T, ctx context.Context) {
	t.Helper()
	var existing achv1alpha1.LiteLLMConnection
	key := client.ObjectKey{Name: "default", Namespace: WatchNamespace}
	if err := k8sClient.Get(ctx, key, &existing); err == nil {
		_ = k8sClient.Delete(ctx, &existing)
	}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, key, &existing)
		return apierrors.IsNotFound(err)
	}, 5*time.Second, 100*time.Millisecond)
}
