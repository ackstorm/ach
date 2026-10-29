// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"errors"
	"net/http"

	"github.com/ackstorm/ach/internal/forwarder/metrics"
	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// EkOwnerGate applies precheck.CheckEkOwner to every authenticated family
// (D-30 / AC-09): access loss denies /v1 and /gemini model traffic as well
// as the already-prechecked /mcp and /a2a. pk_ and no-identity requests
// pass untouched — their rules live in the per-route handlers.
func EkOwnerGate(deps precheck.Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kc, ok := middleware.KeyContextFromCtx(r.Context())
			if ok && kc.KeyType == keys.PrefixEk {
				if err := precheck.CheckEkOwner(r.Context(), kc, deps); err != nil {
					outcome, status, code := classifyPrecheckErr(err)
					metrics.IncRequests("ek_owner", keyTypeFor(r.Context()), outcome)
					if errors.Is(err, precheck.ErrLiteLLMUnreachable) {
						metrics.IncLiteLLMUnreachable()
					}
					render.Error(w, status, code, codeMessage(code), middleware.RequestIDFromCtx(r.Context()))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
