// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"net/http"

	"github.com/ackstorm/ach/internal/forwarder/bip"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/metrics"
	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/go-chi/chi/v5"
)

// HandlerDeps extends proxy.Deps with the per-route dependencies
// (signer, BIP resolver path via PrecheckDeps.K8sClient, precheck deps,
// base URL, namespace).
type HandlerDeps struct {
	// Deps wires the shared *httputil.ReverseProxy.
	Deps Deps
	// Signer mints per-request ACH JWTs for /mcp + /a2a routes.
	Signer jwt.Signer
	// PrecheckDeps wires precheck.CheckMCP / CheckA2A.
	PrecheckDeps precheck.Deps
	// BaseURL is the JWT "iss" claim (ACH_BASE_URL).
	BaseURL string
	// Namespace is the POD_NAMESPACE; used as the JWT "sub" prefix and as
	// the bip.ResolveWinner namespace argument.
	Namespace string
}

// HandlerV1 returns the /v1/* proxy handler. No precheck, no JWT — LiteLLM
// handles model-level auth via the shared key + key_id headers. FWD-06:
// inject Environment attribution tag for ek_ traffic only.
func HandlerV1(deps HandlerDeps) http.HandlerFunc {
	rp := New(deps.Deps)
	return func(w http.ResponseWriter, r *http.Request) {
		maybeInjectEnvironmentTag(r)
		metrics.IncRequests("/v1", keyTypeFor(r.Context()), "forwarded")
		rp.ServeHTTP(w, r)
	}
}

// HandlerGemini mirrors HandlerV1 for /gemini/*.
func HandlerGemini(deps HandlerDeps) http.HandlerFunc {
	rp := New(deps.Deps)
	return func(w http.ResponseWriter, r *http.Request) {
		maybeInjectEnvironmentTag(r)
		metrics.IncRequests("/gemini", keyTypeFor(r.Context()), "forwarded")
		rp.ServeHTTP(w, r)
	}
}

// maybeInjectEnvironmentTag is the FWD-06 ek_ guard shared by /v1 + /gemini.
// pk_ traffic and bodyless requests pass through unmodified.
func maybeInjectEnvironmentTag(r *http.Request) {
	kc, ok := middleware.KeyContextFromCtx(r.Context())
	if !ok || kc.KeyType != keys.PrefixEk || kc.Environment == "" {
		return
	}
	_ = InjectEnvironmentTag(r, kc.Environment) // fail-open per FWD-06
}

// HandlerMCP returns the /mcp/{name}/* handler — runs precheck.CheckMCP,
// optional BIP lookup + JWT attach, then proxies. See FWD-03, FWD-05,
// FWD-07. v1alpha1: no body tag injection (deferred to v1beta1 per
// CONTEXT.md <deferred>).
func HandlerMCP(deps HandlerDeps) http.HandlerFunc {
	return handlerNamed(deps, "MCPServer", precheck.CheckMCP, "mcp:", "/mcp")
}

// HandlerA2A returns the /a2a/{name}/* handler. Same shape as HandlerMCP
// but consults Environment.spec.runtime.a2aAgents and emits "a2a:<name>"
// as the JWT audience.
func HandlerA2A(deps HandlerDeps) http.HandlerFunc {
	return handlerNamed(deps, "A2AAgent", precheck.CheckA2A, "a2a:", "/a2a")
}

type precheckFunc func(ctx context.Context, kc middleware.KeyContext, name string, deps precheck.Deps) error

func handlerNamed(deps HandlerDeps, kind string, check precheckFunc, audPrefix, routeLabel string) http.HandlerFunc {
	rp := New(deps.Deps)
	return func(w http.ResponseWriter, r *http.Request) {
		kc, _ := middleware.KeyContextFromCtx(r.Context())
		name := chi.URLParam(r, "name")
		reqID := middleware.RequestIDFromCtx(r.Context())
		keyTypeLabel := keyTypeFor(r.Context())

		// 1. Precheck — §5.1 step-4.
		if err := check(r.Context(), kc, name, deps.PrecheckDeps); err != nil {
			outcome, status, code := classifyPrecheckErr(err)
			metrics.IncRequests(routeLabel, keyTypeLabel, outcome)
			if errors.Is(err, precheck.ErrLiteLLMUnreachable) {
				metrics.IncLiteLLMUnreachable()
			}
			render.Error(w, status, code, codeMessage(code), reqID)
			return
		}

		// 2. BIP resolve. ResolveWinner returns nil for no-policy AND for
		//    explicit opt-out (winner.Spec.ForwardIdentityJWT == false);
		//    both collapse to "no_policy" at the metrics layer per
		//    CONTEXT D-Discretion.
		winner := bip.ResolveWinner(r.Context(), deps.PrecheckDeps.K8sClient, kind, name, deps.Namespace)
		if winner == nil {
			metrics.IncJWTSuppressed(kind, "no_policy")
			metrics.IncRequests(routeLabel, keyTypeLabel, "forwarded")
			rp.ServeHTTP(w, r)
			return
		}

		// 3. Sign + stash for Director.
		token, err := deps.Signer.Sign(r.Context(), jwt.Claims{
			Iss: deps.BaseURL,
			Sub: deps.Namespace + "/" + kc.OwnerEmail,
			Aud: audPrefix + name,
		})
		if err != nil {
			metrics.IncJWTSuppressed(kind, "signing_failure")
			metrics.IncRequests(routeLabel, keyTypeLabel, "internal_error")
			render.Error(w, http.StatusInternalServerError, "internal_error", "jwt sign failed", reqID)
			return
		}
		metrics.IncJWTSigned(kind)
		metrics.IncRequests(routeLabel, keyTypeLabel, "forwarded")

		// 4. Hand off — Director reads jwtCtxKey and writes Authorization
		//    AFTER headers.StripAndRewrite (jwt-LAST ordering, D-05).
		r = r.WithContext(WithJWT(r.Context(), token))
		rp.ServeHTTP(w, r)
	}
}

// Precheck outcome / envelope-code constants per Hub §15.5.
const (
	outcomeUnauthorizedResource = "unauthorized_resource"
	outcomeUnauthorizedTeam     = "unauthorized_team"
	outcomeLitellmUnreachable   = "litellm_unreachable"
	outcomeInvalidKeyType       = "invalid_key_type"
	outcomeEnvironmentNotFound  = "environment_not_found"
	outcomeInternalError        = "internal_error"
)

// classifyPrecheckErr maps typed sentinels to outcome + HTTP status +
// envelope code per Hub §15.5.
func classifyPrecheckErr(err error) (outcome string, status int, code string) {
	switch {
	case errors.Is(err, precheck.ErrUnauthorizedResource):
		return outcomeUnauthorizedResource, http.StatusForbidden, outcomeUnauthorizedResource
	case errors.Is(err, precheck.ErrUnauthorizedTeam):
		return outcomeUnauthorizedTeam, http.StatusForbidden, outcomeUnauthorizedTeam
	case errors.Is(err, precheck.ErrLiteLLMUnreachable):
		return outcomeLitellmUnreachable, http.StatusServiceUnavailable, outcomeLitellmUnreachable
	case errors.Is(err, precheck.ErrInvalidKeyType):
		return outcomeInvalidKeyType, http.StatusUnauthorized, outcomeInvalidKeyType
	case errors.Is(err, precheck.ErrEnvironmentNotFound):
		return outcomeEnvironmentNotFound, http.StatusNotFound, outcomeEnvironmentNotFound
	}
	return outcomeInternalError, http.StatusInternalServerError, outcomeInternalError
}

// codeMessage returns the stable human-readable message per Hub §15.5
// outcome. v1alpha1 ships English literals; localization is v1beta1.
func codeMessage(code string) string {
	switch code {
	case "unauthorized_resource":
		return "name not authorized for bound environment"
	case "unauthorized_team":
		return "caller's teams do not grant access to this resource"
	case "litellm_unreachable":
		return "litellm reachability failure during teams resolve"
	case "invalid_key_type":
		return "invalid or missing key type for this route"
	case "environment_not_found":
		return "environment not found"
	case "internal_error":
		return "internal error"
	}
	return ""
}
