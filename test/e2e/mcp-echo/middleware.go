// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	echojwt "github.com/ackstorm/ach/test/e2e/mcp-echo/jwt"
)

type ctxKey int

const claimsCtxKey ctxKey = 1

// claimsFromContext returns the verified claims attached by requireJWT
// or (zero, false) if no claims are present.
func claimsFromContext(ctx context.Context) (echojwt.Verified, bool) {
	v, ok := ctx.Value(claimsCtxKey).(echojwt.Verified)
	return v, ok
}

// requireJWT returns middleware that:
//   - extracts "Authorization: Bearer <jwt>" (case-insensitive scheme),
//   - calls verifier.Verify,
//   - on success: drains the body, records into capture, restores the
//     body for the inner handler, attaches claims to ctx, calls next,
//   - on failure: writes 401 + WWW-Authenticate, does NOT call next.
func requireJWT(verifier *echojwt.Verifier, sink *capture) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				unauthorized(w, "missing_or_malformed_bearer")
				return
			}
			claims, err := verifier.Verify(r.Context(), tok)
			if err != nil {
				unauthorized(w, "invalid_token")
				return
			}
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			sink.record(r, body, claims)
			r.Body = io.NopCloser(bytes.NewReader(body))
			ctx := context.WithValue(r.Context(), claimsCtxKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(h string) (string, bool) {
	const prefix = "bearer "
	if len(h) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	t := strings.TrimSpace(h[len(prefix):])
	if t == "" {
		return "", false
	}
	return t, true
}

func unauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Bearer error="`+reason+`"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","reason":"` + reason + `"}`))
}
