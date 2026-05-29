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

// jwtMiddleware returns middleware that validates the ACH JWT. Behavior:
//   - extracts "Authorization: Bearer <jwt>" (case-insensitive scheme),
//   - token present: calls verifier.Verify; on success drains+records the
//     body (jwt_present=true), restores it, attaches claims to ctx, calls
//     next; on failure writes 401 + WWW-Authenticate (a present-but-invalid
//     token is ALWAYS a hard failure, even when require=false),
//   - token absent:
//     require=true  → 401 "missing_or_malformed_bearer" (strict, the
//     production posture: the backend refuses on the
//     slightest mismatch),
//     require=false → records the no-JWT request (jwt_present=false) and
//     calls next. This is the BIP forwardIdentityJWT=false
//     path: the forwarder deliberately sends no JWT, and
//     the closed-loop e2e asserts the absence was observed.
//
// require is sourced from ACH_REQUIRE_JWT (default true). One deployment
// with require=false serves BOTH the jwt and nojwt demo routes: the jwt
// route always carries a token (validated), the nojwt route never does
// (recorded as absent).
func jwtMiddleware(verifier *echojwt.Verifier, sink *capture, require bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				if require {
					unauthorized(w, "missing_or_malformed_bearer")
					return
				}
				body, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				sink.record(r, body, echojwt.Verified{}, false)
				r.Body = io.NopCloser(bytes.NewReader(body))
				next.ServeHTTP(w, r)
				return
			}
			claims, err := verifier.Verify(r.Context(), tok)
			if err != nil {
				unauthorized(w, "invalid_token")
				return
			}
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			sink.record(r, body, claims, true)
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
