// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

// cookieName is the load-bearing __Host- prefixed cookie carrying the
// (state, code_verifier) pair between LoginHandler and CallbackHandler.
//
// __Host- prefix invariants enforced by every browser:
//   - Path MUST be "/"
//   - Secure MUST be set
//   - Domain MUST NOT be set
//
// Combined with HttpOnly and SameSite=Strict, the cookie is single-origin,
// JavaScript-inaccessible, and never sent on cross-site navigation —
// closing the CSRF + script-exfiltration attack surface on the SSO bridge.
const cookieName = "__Host-ach_sso"

// cookieTTL is the maximum lifetime of the SSO state cookie. 10 minutes is
// the Hub §17 / D-04 ceiling for the round-trip from /platform/auth/login
// to /platform/auth/sso/callback. Any flow that takes longer than this
// expires the cookie and forces re-initiation.
const cookieTTL = 10 * time.Minute

// cookieSeparator joins the state and verifier inside the base64 payload.
// Both values are base64url-encoded random bytes that NEVER contain "|",
// so the separator is unambiguous.
const cookieSeparator = "|"

// ErrCookieMissing is returned by readSSOCookie when the request carries
// no Cookie header named cookieName. The handler renders 400 invalid_argument.
var ErrCookieMissing = errors.New("auth: SSO state cookie missing")

// ErrCookieMalformed is returned by readSSOCookie when the cookie value
// fails base64 decode or does not split into exactly (state, verifier).
// The handler renders 400 invalid_argument.
var ErrCookieMalformed = errors.New("auth: SSO state cookie malformed")

// setSSOCookie writes the (state, verifier) pair as a __Host-ach_sso
// Set-Cookie header. The cookie value is base64url(state + "|" + verifier).
//
// All __Host- invariants (Path=/, Secure, HttpOnly, SameSite=Strict, no
// Domain attribute) are enforced at the http.Cookie literal level; the
// Max-Age is the 10-minute Hub §17 / D-04 ceiling.
//
// The base64 encoding uses URLEncoding (not RawURLEncoding) so the
// padding is preserved across the wire — this matches the readSSOCookie
// decoder.
func setSSOCookie(w http.ResponseWriter, state, verifier string) {
	payload := state + cookieSeparator + verifier
	encoded := base64.URLEncoding.EncodeToString([]byte(payload))
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(cookieTTL.Seconds()),
	})
}

// readSSOCookie returns the (state, verifier) pair previously stored by
// setSSOCookie, decoding the base64url payload and splitting on the "|"
// separator. Any failure returns (zero, zero, ErrCookieMissing or
// ErrCookieMalformed) — the handler maps both to 400 invalid_argument.
func readSSOCookie(r *http.Request) (state, verifier string, err error) {
	c, cerr := r.Cookie(cookieName)
	if cerr != nil {
		return "", "", ErrCookieMissing
	}
	raw, decErr := base64.URLEncoding.DecodeString(c.Value)
	if decErr != nil {
		return "", "", ErrCookieMalformed
	}
	parts := strings.Split(string(raw), cookieSeparator)
	if len(parts) != 2 {
		return "", "", ErrCookieMalformed
	}
	return parts[0], parts[1], nil
}

// clearSSOCookie writes a Set-Cookie header that deletes the __Host-ach_sso
// cookie. Max-Age is set to -1 (which Go's http.SetCookie emits as Max-Age=0,
// the canonical delete signal). All other security attributes match
// setSSOCookie so the browser accepts the deletion under the __Host- prefix.
func clearSSOCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
