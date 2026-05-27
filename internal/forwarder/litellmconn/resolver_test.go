// SPDX-License-Identifier: Apache-2.0

package litellmconn

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("clientgoscheme.AddToScheme: %v", err)
	}
	if err := achv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("achv1alpha1.AddToScheme: %v", err)
	}
	return scheme
}

func conn(ns, secretName, secretKey, endpoint string) *achv1alpha1.LiteLLMConnection {
	return &achv1alpha1.LiteLLMConnection{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: CRName},
		Spec: achv1alpha1.LiteLLMConnectionSpec{
			Endpoint: endpoint,
			MasterKeySecretRef: achv1alpha1.SecretKeyRef{
				Name: secretName,
				Key:  secretKey,
			},
		},
	}
}

func secret(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       data,
	}
}

func TestResolve_HappyPath(t *testing.T) {
	const ns = "ach-system"
	scheme := newScheme(t)
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		conn(ns, "litellm-master-key", "masterKey", "http://litellm.example:4000"),
		secret(ns, "litellm-master-key", map[string][]byte{"masterKey": []byte("sk-test-master-key")}),
	).Build()

	got, err := Resolve(context.Background(), cli, ns)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Endpoint != "http://litellm.example:4000" {
		t.Errorf("endpoint = %q, want http://litellm.example:4000", got.Endpoint)
	}
	if got.MasterKey != "sk-test-master-key" {
		t.Errorf("masterKey = %q, want sk-test-master-key", got.MasterKey)
	}
}

func TestResolve_CRMissing(t *testing.T) {
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	_, err := Resolve(context.Background(), cli, "ach-system")
	if !errors.Is(err, ErrCRNotFound) {
		t.Fatalf("want ErrCRNotFound, got %v", err)
	}
}

func TestResolve_EndpointEmpty(t *testing.T) {
	const ns = "ach-system"
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		conn(ns, "litellm-master-key", "masterKey", ""),
		secret(ns, "litellm-master-key", map[string][]byte{"masterKey": []byte("k")}),
	).Build()
	_, err := Resolve(context.Background(), cli, ns)
	if !errors.Is(err, ErrEndpointEmpty) {
		t.Fatalf("want ErrEndpointEmpty, got %v", err)
	}
}

func TestResolve_SecretMissing(t *testing.T) {
	const ns = "ach-system"
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		conn(ns, "litellm-master-key", "masterKey", "http://litellm.example:4000"),
	).Build()
	_, err := Resolve(context.Background(), cli, ns)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("want ErrSecretNotFound, got %v", err)
	}
}

func TestResolve_SecretKeyMissing(t *testing.T) {
	const ns = "ach-system"
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		conn(ns, "litellm-master-key", "masterKey", "http://litellm.example:4000"),
		secret(ns, "litellm-master-key", map[string][]byte{"otherKey": []byte("k")}),
	).Build()
	_, err := Resolve(context.Background(), cli, ns)
	if !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("want ErrSecretKeyMissing, got %v", err)
	}
}

func TestResolve_SecretKeyEmptyValue(t *testing.T) {
	const ns = "ach-system"
	cli := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		conn(ns, "litellm-master-key", "masterKey", "http://litellm.example:4000"),
		secret(ns, "litellm-master-key", map[string][]byte{"masterKey": {}}),
	).Build()
	_, err := Resolve(context.Background(), cli, ns)
	if !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("want ErrSecretKeyMissing for empty value, got %v", err)
	}
}

