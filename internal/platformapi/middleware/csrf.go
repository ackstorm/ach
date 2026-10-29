// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"errors"
	"net/http"
	"net/url"
)

// ErrTemporarilyUnavailable: the session could not be verified because a
// dependency (the IdP) did not answer — 503, never a logout.
var ErrTemporarilyUnavailable = errors.New("temporarily unavailable")

// SameSite is the D-28 fetch-metadata check for cookie-authenticated
// mutations: Sec-Fetch-Site must say same-origin; only when a client sends
// no fetch metadata at all does Origin (exactly our scheme://host) stand
// in. No token, no double-submit — SameSite=Lax on the cookie plus this
// check is the whole CSRF story.
func SameSite(r *http.Request, issuer string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		o, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || o.Scheme == "" || o.Host == "" || o.Path != "" {
			return false
		}
		i, err := url.Parse(issuer)
		return err == nil && o.Scheme == i.Scheme && o.Host == i.Host
	default:
		return false
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
