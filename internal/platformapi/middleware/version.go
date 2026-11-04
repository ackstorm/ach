// SPDX-License-Identifier: Apache-2.0

package middleware

import "net/http"

// ServerVersionHeader carries the platform-api build on authenticated
// responses, so ach-cli can warn on a version mismatch without an extra
// request. It sits behind Authn: anonymous callers do not learn the version.
const ServerVersionHeader = "X-ACH-Server-Version"

// ServerVersion stamps ServerVersionHeader on every response.
func ServerVersion(version string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(ServerVersionHeader, version)
			next.ServeHTTP(w, r)
		})
	}
}
