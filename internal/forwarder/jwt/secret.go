// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
)

// SecretName is the default coordinate of the K8s Secret that holds
// the Forwarder's signing material. Hub §9.2 + CONTEXT D-10 fix the
// name; deployments override the namespace via the cobra forwarder
// flag set but RARELY override the name (the per-tenant isolation
// already comes from the namespace).
const SecretName = "ach-jwt-signing-keys" //nolint:gosec // G101 false positive: name of a k8s Secret, not a credential value

// Data-key layout per CONTEXT D-10. The K8s wire format base64-decodes
// values into raw bytes; what we receive in secret.Data is already the
// raw seed (32 bytes) and the raw kid (UTF-8 string).
const (
	// DataKeyCurrentKid is the kid for the slot Sign currently uses.
	DataKeyCurrentKid = "current.kid"
	// DataKeyCurrentSeed is the 32-byte Ed25519 seed for the current slot.
	DataKeyCurrentSeed = "current.seed"
	// DataKeyNextKid is the optional kid for the next-slot, published in
	// JWKS during a rotation overlap window but never used to sign.
	DataKeyNextKid = "next.kid"
	// DataKeyNextSeed is the optional 32-byte seed for the next slot.
	DataKeyNextSeed = "next.seed"
)

// SecretLoader bridges the K8s Secret containing the signing keys
// (D-10 data-key layout) and the in-memory Ed25519Signer slots. The
// loader is split into TWO entry points by lifecycle phase:
//
//   - LoadOnce — the startup path. Surfaces any error to the cobra
//     RunE so the forwarder REFUSES TO START when current.kid is empty
//     or current.seed is not exactly 32 bytes. This is the FWD-09
//     refuse-to-load contract (Hub §9.1 + D-10).
//
//   - Reload — the informer event path. On a malformed current slot
//     (empty kid or non-32-byte seed in an updated Secret) it LOGS the
//     error and KEEPS the prior valid slot — refuse-to-update, not
//     refuse-to-die. Production traffic continues flowing while
//     operators rectify the Secret. The error is returned so the
//     caller (the informer event handler) can record a metric, but
//     the in-memory slot is NEVER cleared on Reload failure.
//
// Single-writer discipline: the controller-runtime informer guarantees
// one event handler invocation at a time per object, so Reload is the
// only writer to s.signer.{current,next} during steady state. The
// atomic.Pointer publication in Ed25519Signer protects against the
// concurrent in-flight Sign() Loads.
type SecretLoader struct {
	signer    *Ed25519Signer
	namespace string
	name      string
	log       logr.Logger
}

// NewSecretLoader constructs a SecretLoader wired to a signer + the
// K8s Secret coordinates. namespace is typically POD_NAMESPACE
// (downward API) and name is the package constant SecretName. The
// logger is scoped — caller passes ctrl.Log.WithName("jwt-loader") or
// equivalent. Panics if signer is nil (programmer error, not runtime).
func NewSecretLoader(signer *Ed25519Signer, namespace, name string, log logr.Logger) *SecretLoader {
	if signer == nil {
		panic("jwt: NewSecretLoader requires a non-nil signer")
	}
	return &SecretLoader{
		signer:    signer,
		namespace: namespace,
		name:      name,
		log:       log,
	}
}

// applyNextSlot resolves the optional next signing slot from the Secret
// and publishes it (or clears it when absent/malformed). phase is "load"
// or "reload" — it selects the malformed-next log wording (refuse-to-start
// vs refuse-to-update semantics) without duplicating the branch. Always
// publishes a next slot (possibly nil); never returns an error (a
// malformed next is non-fatal — current stays valid).
func (l *SecretLoader) applyNextSlot(secret *corev1.Secret, phase string) {
	nxtKid := string(secret.Data[DataKeyNextKid])
	if nxtKid == "" {
		l.signer.loadNext(nil)
		return
	}
	nxtSlot, nxtErr := newSignerSlot(nxtKid, secret.Data[DataKeyNextSeed])
	if nxtErr != nil {
		msg := "next slot malformed; continuing with current only"
		if phase == "reload" {
			msg = "jwt secret reload next malformed; clearing next slot"
		}
		l.log.Error(nxtErr, msg,
			"namespace", l.namespace, "name", l.name, "next.kid", nxtKid)
		l.signer.loadNext(nil)
		return
	}
	l.signer.loadNext(nxtSlot)
}

// LoadOnce is the startup-path loader. Returns an error wrapping
// ErrEmptyKid or ErrEmptySeed when current.kid is empty or
// current.seed is not 32 bytes; the caller MUST surface this error
// to the cobra RunE so the forwarder refuses to start. The signer's
// current slot is NEVER mutated on error.
//
// On success: current is populated; if next.kid is non-empty AND
// next.seed is valid, next is also populated. If next.kid is non-empty
// but next.seed is malformed, the next slot is CLEARED (loadNext(nil))
// and the error is logged at Error level — the forwarder still starts
// (current is valid) but the rotation overlap window is unavailable
// until the operator fixes the Secret.
//
// If next.kid is empty (no rotation pending), next is cleared and
// LoadOnce returns nil without comment.
func (l *SecretLoader) LoadOnce(secret *corev1.Secret) error {
	if secret == nil {
		return errors.New("jwt: LoadOnce called with nil Secret")
	}
	curKid := string(secret.Data[DataKeyCurrentKid])
	curSeed := secret.Data[DataKeyCurrentSeed]
	curSlot, err := newSignerSlot(curKid, curSeed)
	if err != nil {
		return fmt.Errorf("jwt secret %s/%s current: %w", l.namespace, l.name, err)
	}
	l.signer.loadCurrent(curSlot)
	l.applyNextSlot(secret, "load")

	l.log.Info("jwt signing keys loaded",
		"namespace", l.namespace,
		"name", l.name,
		"current.kid", curKid,
		"next.present", l.signer.next.Load() != nil,
	)
	return nil
}

// Reload is the informer-event-path loader. On a malformed current
// slot in the updated Secret, Reload LOGS the error and KEEPS the
// prior valid slot — refuse-to-update discipline. Returns the
// underlying newSignerSlot error so the caller (the informer
// FilteringResourceEventHandler) can emit a metric, but signer state
// is left untouched on failure.
//
// On success, behavior mirrors LoadOnce: current is published via
// atomic swap; next is published if present-and-valid, cleared
// otherwise, with malformed-next logged at Error level.
//
// Reload MUST be invoked only from the informer event goroutine
// (single-writer) — the atomic.Pointer publication in the signer
// handles the in-flight Sign() races.
func (l *SecretLoader) Reload(secret *corev1.Secret) error {
	if secret == nil {
		err := errors.New("jwt: Reload called with nil Secret")
		l.log.Error(err, "jwt secret reload: nil Secret; keeping prior slot",
			"namespace", l.namespace, "name", l.name)
		return err
	}
	curKid := string(secret.Data[DataKeyCurrentKid])
	curSeed := secret.Data[DataKeyCurrentSeed]
	curSlot, err := newSignerSlot(curKid, curSeed)
	if err != nil {
		// Refuse-to-update: prior current slot stays in place. Caller
		// metric path records the event; in-flight Sign() calls
		// continue to use the prior valid key.
		l.log.Error(err, "jwt secret reload current malformed; keeping prior slot",
			"namespace", l.namespace, "name", l.name)
		return fmt.Errorf("jwt secret %s/%s reload current: %w", l.namespace, l.name, err)
	}
	l.signer.loadCurrent(curSlot)
	l.applyNextSlot(secret, "reload")

	l.log.Info("jwt signing keys reloaded",
		"namespace", l.namespace,
		"name", l.name,
		"current.kid", curKid,
		"next.present", l.signer.next.Load() != nil,
	)
	return nil
}
