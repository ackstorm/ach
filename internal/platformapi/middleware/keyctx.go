// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"os"
	"time"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
)

// KeyContext is the read-only view downstream handlers receive after a
// successful Authn pass. It mirrors keystore.KeyInfo plus the IsAdmin
// flag computed at Authn time against the admin allowlist (BLK-02).
//
// Handlers retrieve it via KeyContextFromCtx(r.Context()); they NEVER
// see the raw bearer plaintext (Authn discards it before next.ServeHTTP).
type KeyContext struct {
	KeyID        string
	KeyType      keys.BearerPrefix
	OwnerEmail   string
	ExpiresAt    *time.Time
	Environment  string
	LiteLLMToken *string
	// TESTING-PHASE (reverts FIX01 §A.6): caller's own LiteLLM virtual-key
	// plaintext (sk-…), forwarded by the proxy as x-litellm-api-key.
	LiteLLMKeyMaterial *string
	LiteLLMUserID      *string
	IsAdmin            bool
}

// ctxKey is the private type used for the two context.Value keys this
// package writes. Keys MUST be private so external packages cannot
// inject or read the values (Go convention for context-key safety).
type ctxKey int

const (
	keyContextKey ctxKey = iota
	requestIDKey
)

// WithKeyContext attaches a populated KeyContext to ctx, derived from
// the given keystore.KeyInfo plus the isAdmin flag the Authn middleware
// computed against the allowlist (BLK-02). Handlers retrieve the value
// via KeyContextFromCtx.
//
// If info is nil the function returns ctx unchanged — defensive, since
// Authn only calls this with a non-nil KeyInfo.
func WithKeyContext(ctx context.Context, info *keystore.KeyInfo, isAdmin bool) context.Context {
	if info == nil {
		return ctx
	}
	kc := KeyContext{
		KeyID:        info.KeyID,
		KeyType:      info.KeyType,
		OwnerEmail:   info.OwnerEmail,
		ExpiresAt:    info.ExpiresAt,
		Environment:  info.Environment,
		LiteLLMToken: info.LiteLLMToken,
		// TESTING-PHASE (reverts FIX01 §A.6)
		LiteLLMKeyMaterial: info.LiteLLMKeyMaterial,
		LiteLLMUserID:      info.LiteLLMUserID,
		IsAdmin:            isAdmin,
	}
	return context.WithValue(ctx, keyContextKey, kc)
}

// KeyContextFromCtx returns the KeyContext previously stored by Authn
// via WithKeyContext. The second return value reports whether a
// KeyContext was present — false (and a zero-value KeyContext) on any
// context that did not pass through Authn (e.g. unauthenticated routes
// like /healthz).
func KeyContextFromCtx(ctx context.Context) (KeyContext, bool) {
	v, ok := ctx.Value(keyContextKey).(KeyContext)
	if !ok {
		return KeyContext{}, false
	}
	return v, true
}

// WithRequestID attaches a request-id string (typically "req_<ulid>") to
// ctx. The RequestID middleware calls this once per request after
// generating the id; handlers retrieve via RequestIDFromCtx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromCtx returns the request-id stored in ctx by the
// RequestID middleware. Returns "" on contexts that did not pass
// through the middleware (the RequestID middleware is the OUTERMOST
// layer per D-02, so this should only happen on direct handler tests).
func RequestIDFromCtx(ctx context.Context) string {
	v, ok := ctx.Value(requestIDKey).(string)
	if !ok {
		return ""
	}
	return v
}

// ActorFromCtx composes the "<namespace>/<sso-email>" actor string per
// Hub §18.3. The namespace comes from the POD_NAMESPACE env var
// (downward API, set at deployment time); the email comes from the
// resolved KeyContext.OwnerEmail.
//
// Handlers compose this once per emission inline; the helper centralizes
// the format so the colon-vs-slash + missing-namespace fallback is
// consistent across every audit event.
//
// Missing POD_NAMESPACE collapses to "unknown" rather than the empty
// string so the actor field is always grep-able. Missing OwnerEmail
// collapses to "-" (typical when Authn has not yet run, e.g. on the SSO
// login pre-auth path).
func ActorFromCtx(ctx context.Context) string {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		ns = "unknown"
	}
	email := "-"
	if kc, ok := KeyContextFromCtx(ctx); ok && kc.OwnerEmail != "" {
		email = kc.OwnerEmail
	}
	return ns + "/" + email
}
