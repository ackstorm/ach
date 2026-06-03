// SPDX-License-Identifier: Apache-2.0

package litellmconnboot_test

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/operator/litellmconnboot"
)

const (
	testNS   = "ach-system"
	testName = "default"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := achv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func getConn(t *testing.T, c client.Client) achv1alpha1.LiteLLMConnection {
	t.Helper()
	var got achv1alpha1.LiteLLMConnection
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &got); err != nil {
		t.Fatalf("get LiteLLMConnection: %v", err)
	}
	return got
}

func TestEnsureConnection_CreatesWhenAbsent(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	err := litellmconnboot.EnsureConnection(context.Background(), c, testNS, testName,
		"http://litellm.ach-system.svc:4000", "ach-litellm", "masterKey", logr.Discard())
	if err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}

	got := getConn(t, c)
	if got.Spec.Endpoint != "http://litellm.ach-system.svc:4000" {
		t.Errorf("endpoint = %q", got.Spec.Endpoint)
	}
	if got.Spec.MasterKeySecretRef.Name != "ach-litellm" || got.Spec.MasterKeySecretRef.Key != "masterKey" {
		t.Errorf("secretRef = %+v", got.Spec.MasterKeySecretRef)
	}
	if got.Labels["app.kubernetes.io/managed-by"] != "ach-operator" {
		t.Errorf("managed-by label = %q", got.Labels["app.kubernetes.io/managed-by"])
	}
}

func TestEnsureConnection_NoOpWhenInSync(t *testing.T) {
	pre := &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: testName, ResourceVersion: "7"},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint:           "http://litellm.ach-system.svc:4000",
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{Name: "ach-litellm", Key: "masterKey"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(pre).Build()

	err := litellmconnboot.EnsureConnection(context.Background(), c, testNS, testName,
		"http://litellm.ach-system.svc:4000", "ach-litellm", "masterKey", logr.Discard())
	if err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}

	got := getConn(t, c)
	if got.ResourceVersion != "7" {
		t.Errorf("ResourceVersion changed to %q — in-sync CR must not be rewritten", got.ResourceVersion)
	}
}

func TestEnsureConnection_UpdatesOnDrift(t *testing.T) {
	pre := &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: testName},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint:           "http://old.svc:4000",
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{Name: "ach-litellm", Key: "masterKey"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(pre).Build()

	err := litellmconnboot.EnsureConnection(context.Background(), c, testNS, testName,
		"http://new.svc:4000", "ach-litellm", "masterKey", logr.Discard())
	if err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}

	if got := getConn(t, c); got.Spec.Endpoint != "http://new.svc:4000" {
		t.Errorf("endpoint not updated on drift: got %q, want http://new.svc:4000", got.Spec.Endpoint)
	}
}

func TestEnsureConnection_DisabledWhenEmptyEndpoint(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()

	err := litellmconnboot.EnsureConnection(context.Background(), c, testNS, testName,
		"", "ach-litellm", "masterKey", logr.Discard())
	if err != nil {
		t.Fatalf("EnsureConnection (disabled): %v", err)
	}

	var got achv1alpha1.LiteLLMConnection
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &got); err == nil {
		t.Error("expected NO LiteLLMConnection created when endpoint is empty")
	}
}
