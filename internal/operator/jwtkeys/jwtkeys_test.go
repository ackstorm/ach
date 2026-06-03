// SPDX-License-Identifier: Apache-2.0

package jwtkeys_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/operator/jwtkeys"
)

const (
	testNS   = "ach-system"
	testName = "ach-jwt-signing-keys"
)

func TestEnsureSigningKeys_CreatesWhenAbsent(t *testing.T) {
	c := fake.NewClientBuilder().Build()

	if err := jwtkeys.EnsureSigningKeys(context.Background(), c, testNS, testName, logr.Discard()); err != nil {
		t.Fatalf("EnsureSigningKeys: %v", err)
	}

	var s corev1.Secret
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &s); err != nil {
		t.Fatalf("expected Secret created, get: %v", err)
	}

	kid := string(s.Data[jwt.DataKeyCurrentKid])
	if kid == "" {
		t.Error("current.kid is empty")
	}
	seed := s.Data[jwt.DataKeyCurrentSeed]
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("current.seed length = %d, want %d", len(seed), ed25519.SeedSize)
	}
	// Seed must reconstruct a usable Ed25519 key (the jwt loader does this).
	if got := ed25519.NewKeyFromSeed(seed); len(got) != ed25519.PrivateKeySize {
		t.Errorf("seed did not yield a valid Ed25519 private key (len %d)", len(got))
	}
}

func TestEnsureSigningKeys_NoOpWhenPresent(t *testing.T) {
	existingSeed := bytes.Repeat([]byte{0x07}, ed25519.SeedSize)
	pre := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: testName},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			jwt.DataKeyCurrentKid:  []byte("preexisting-kid"),
			jwt.DataKeyCurrentSeed: existingSeed,
		},
	}
	c := fake.NewClientBuilder().WithObjects(pre).Build()

	if err := jwtkeys.EnsureSigningKeys(context.Background(), c, testNS, testName, logr.Discard()); err != nil {
		t.Fatalf("EnsureSigningKeys: %v", err)
	}

	var s corev1.Secret
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &s); err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(s.Data[jwt.DataKeyCurrentKid]) != "preexisting-kid" {
		t.Errorf("kid overwritten: got %q, want preexisting-kid", string(s.Data[jwt.DataKeyCurrentKid]))
	}
	if !bytes.Equal(s.Data[jwt.DataKeyCurrentSeed], existingSeed) {
		t.Error("seed overwritten — mint-once contract violated (must persist existing key)")
	}
}

func TestEnsureSigningKeys_Idempotent(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	ctx := context.Background()

	if err := jwtkeys.EnsureSigningKeys(ctx, c, testNS, testName, logr.Discard()); err != nil {
		t.Fatalf("first EnsureSigningKeys: %v", err)
	}
	var first corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: testName}, &first); err != nil {
		t.Fatalf("get after first: %v", err)
	}

	if err := jwtkeys.EnsureSigningKeys(ctx, c, testNS, testName, logr.Discard()); err != nil {
		t.Fatalf("second EnsureSigningKeys: %v", err)
	}
	var second corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: testName}, &second); err != nil {
		t.Fatalf("get after second: %v", err)
	}

	if !bytes.Equal(first.Data[jwt.DataKeyCurrentSeed], second.Data[jwt.DataKeyCurrentSeed]) {
		t.Error("seed changed across calls — not idempotent")
	}
}
