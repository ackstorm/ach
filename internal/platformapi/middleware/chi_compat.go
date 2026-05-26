// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// chiCompat is a compile-time validation that this package's
// middleware constructors return the exact signature chi.Router.Use
// accepts: func(http.Handler) http.Handler. If a future edit to any
// constructor drops or widens that signature, the var declarations
// below stop compiling and surface the regression before runtime.
//
// Per D-01 chi.Router (github.com/go-chi/chi/v5) is the canonical
// Platform API multiplexer; every middleware in this package is
// designed to be `r.Use(...)`-able on a chi.Mux. The dependency is
// declared in go.mod so Plan 03-06 (chi server constructor) inherits
// it directly.

// chiMiddleware is the signature chi.Router.Use accepts:
// `func(http.Handler) http.Handler`. The type alias makes the
// compile-time assertions below readable.
type chiMiddleware = func(http.Handler) http.Handler

var (
	_ chiMiddleware = RequestID
	_ chiMiddleware = ContentTypeJSON
)

// _chiRouterAnchor pins the chi import so go.mod retains the direct
// require entry. Plan 03-06 will replace this anchor with real chi.Mux
// usage in server.go.
var _ chi.Router = (chi.Router)(nil)
