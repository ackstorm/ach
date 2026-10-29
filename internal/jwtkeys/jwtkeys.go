// SPDX-License-Identifier: Apache-2.0

// Package jwtkeys lets the operator mint and persist the forwarder's
// Ed25519 JWT signing material (the ach-jwt-signing-keys Secret, FWD-09).
//
// The forwarder refuse-to-starts without that Secret; nothing previously
// created it (e2e seeded a fixture, real installs had to pre-create it).
// EnsureSigningKeys closes that gap: on operator boot it get-or-creates the
// Secret with a fresh random seed. It is deliberately MINT-ONCE — if the
// Secret already exists it is left untouched, so the key persists across
// operator restarts. Because the Secret is operator-owned (not part of any
// Helm release) it also survives `helm uninstall`, so a reinstall reuses the
// same key and backends see no JWKS rotation.
//
// Key separation is intentional: the seed is random, independent of the
// LiteLLM master key (whose compromise must NOT imply JWT forgery) and
// independently rotatable (delete the Secret to force a fresh mint).
package jwtkeys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ackstorm/ach/internal/forwarder/jwt"
)

// EnsureSigningKeys get-or-creates the ach-jwt-signing-keys Secret named by
// secretName in namespace. If the Secret already exists it is a no-op (the
// existing key is preserved — never overwritten). If absent, a fresh
// Ed25519 seed + a kid derived from the public key are generated and the
// Secret is created.
//
// A concurrent creator (another operator replica racing between Get and
// Create) is handled: an AlreadyExists on Create is treated as success.
func EnsureSigningKeys(ctx context.Context, c client.Client, namespace, secretName string, log logr.Logger) error {
	key := client.ObjectKey{Namespace: namespace, Name: secretName}

	var existing corev1.Secret
	err := c.Get(ctx, key, &existing)
	if err == nil {
		log.Info("jwt signing keys already present — leaving as-is (persist)",
			"namespace", namespace, "name", secretName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("jwtkeys: get %s/%s: %w", namespace, secretName, err)
	}

	kid, seed, err := generateSeed()
	if err != nil {
		return fmt.Errorf("jwtkeys: generate seed: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      secretName,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "ach",
				"app.kubernetes.io/component":  "forwarder",
				"app.kubernetes.io/managed-by": "ach-operator",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			jwt.DataKeyCurrentKid:  []byte(kid),
			jwt.DataKeyCurrentSeed: seed,
		},
	}

	if err := c.Create(ctx, secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Lost a race with another minter — the Secret now exists, which
			// is exactly the desired end state. Do NOT overwrite it.
			log.Info("jwt signing keys created concurrently — leaving as-is",
				"namespace", namespace, "name", secretName)
			return nil
		}
		return fmt.Errorf("jwtkeys: create %s/%s: %w", namespace, secretName, err)
	}

	log.Info("minted jwt signing keys", "namespace", namespace, "name", secretName, "kid", kid)
	return nil
}

// generateSeed returns a fresh 32-byte Ed25519 seed and a kid derived
// deterministically from the corresponding public key. Deriving the kid
// from the key (rather than a timestamp) guarantees a unique kid per
// distinct key, so a backend can never have a cached kid->pubkey entry
// collide with a different key.
func generateSeed() (kid string, seed []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, err
	}
	// ed25519.PrivateKey.Seed() returns the 32-byte seed the jwt loader
	// expects (it reconstructs the key via ed25519.NewKeyFromSeed).
	seed = priv.Seed()
	sum := sha256.Sum256(pub)
	kid = "ach-" + hex.EncodeToString(sum[:8])
	return kid, seed, nil
}
