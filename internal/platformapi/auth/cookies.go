// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

// cookieNameSecure is the production cookie name. The __Host- prefix
// is browser-enforced to require Path=/, Secure, and no Domain attribute —
// closing the CSRF + script-exfiltration attack surface on the SSO bridge.
const cookieNameSecure = "__Host-ach_sso"

// cookieNameInsecure is the dev-mode cookie name used when
// ACH_SSO_INSECURE_COOKIE=1. The __Host- prefix is dropped because it
// is only valid with Secure=true (browsers reject __Host- cookies that
// lack Secure). The dev-mode flag exists for local HTTP-only fixtures
// where the entire SSO chain rides plain http://localhost — production
// MUST run with the Secure variant.
const cookieNameInsecure = "ach_sso"

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
// no Cookie header named cookieNameSecure or cookieNameInsecure. The
// handler renders 400 invalid_argument.
var ErrCookieMissing = errors.New("auth: SSO state cookie missing")

// ErrCookieMalformed is returned by readSSOCookie when the cookie value
// fails base64 decode or does not split into exactly (state, verifier).
// The handler renders 400 invalid_argument.
var ErrCookieMalformed = errors.New("auth: SSO state cookie malformed")

// cookieName returns the cookie name in use for the given mode.
// insecure=false → "__Host-ach_sso" (production); insecure=true → "ach_sso"
// (dev-mode HTTP-only fixture).
func cookieName(insecure bool) string {
	if insecure {
		return cookieNameInsecure
	}
	return cookieNameSecure
}

// setSSOCookie writes the (state, verifier) pair as a Set-Cookie header.
// The cookie value is base64url(state + "|" + verifier).
//
// Default (insecure=false): name=__Host-ach_sso, Secure=true, Path=/,
// HttpOnly, SameSite=Strict, no Domain — all __Host- invariants enforced
// at the http.Cookie literal level.
//
// Dev-mode (insecure=true): name=ach_sso, Secure=false — for local HTTP
// fixtures where the entire flow rides plain http://. Production MUST
// run with insecure=false.
//
// Max-Age is the 10-minute Hub §17 / D-04 ceiling. base64url encoding
// uses URLEncoding (not RawURLEncoding) so the padding is preserved
// across the wire — this matches the readSSOCookie decoder.
func setSSOCookie(w http.ResponseWriter, state, verifier string, insecure bool) {
	payload := state + cookieSeparator + verifier
	encoded := base64.URLEncoding.EncodeToString([]byte(payload))
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(insecure),
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(cookieTTL.Seconds()),
	})
}

// readSSOCookie returns the (state, verifier) pair previously stored by
// setSSOCookie, decoding the base64url payload and splitting on the "|"
// separator. Any failure returns (zero, zero, ErrCookieMissing or
// ErrCookieMalformed) — the handler maps both to 400 invalid_argument.
//
// The cookie name is selected by the same insecure flag setSSOCookie used.
func readSSOCookie(r *http.Request, insecure bool) (state, verifier string, err error) {
	c, cerr := r.Cookie(cookieName(insecure))
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

// clearSSOCookie writes a Set-Cookie header that deletes the SSO state
// cookie. Max-Age is set to -1 (which Go's http.SetCookie emits as
// Max-Age=0, the canonical delete signal). All other attributes match
// setSSOCookie so the browser accepts the deletion.
func clearSSOCookie(w http.ResponseWriter, insecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(insecure),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
