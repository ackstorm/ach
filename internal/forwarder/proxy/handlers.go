// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/forwarder/metrics"
	"github.com/ackstorm/ach/internal/forwarder/precheck"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/go-chi/chi/v5"
)

// BIPResolver is the narrow contract handlers consume from the
// Postgres-backed BIP cache (internal/forwarder/bipcache). Returning
// nil collapses both "no policy" and "explicit opt-out" into the
// no-JWT path, matching bipcache.Cache.Resolve and the legacy
// bip.ResolveWinner contract.
type BIPResolver interface {
	Resolve(targetKind, targetName string) *db.BIPRow
}

// HandlerDeps extends proxy.Deps with the per-route dependencies
// (signer, BIP resolver, precheck deps, base URL).
type HandlerDeps struct {
	// Deps wires the shared *httputil.ReverseProxy.
	Deps Deps
	// Signer mints per-request ACH JWTs for /mcp + /a2a routes.
	Signer jwt.Signer
	// BIPResolver is the Postgres-backed BIP cache (C4). Resolve returns
	// nil for "no policy" AND for explicit opt-out (alpha-FIRST winner
	// has ForwardIdentityJWT=false).
	BIPResolver BIPResolver
	// PrecheckDeps wires precheck.CheckMCP / CheckA2A.
	PrecheckDeps precheck.Deps
	// BaseURL is the JWT "iss" claim (ACH_BASE_URL).
	BaseURL string
}

// taggedPassthrough builds the no-precheck passthrough handler shared by
// /v1, /v2, and /gemini: inject the Environment attribution tag (FWD-06, ek_
// traffic only) then forward. routeLabel is the metrics route dimension.
func taggedPassthrough(deps HandlerDeps, routeLabel string) http.HandlerFunc {
	rp := New(deps.Deps)
	inner := func(w http.ResponseWriter, r *http.Request) {
		maybeInjectEnvironmentTag(r)
		metrics.IncRequests(routeLabel, keyTypeFor(r.Context()), "forwarded")
		rp.ServeHTTP(w, r)
	}
	return observeDuration(routeLabel, inner)
}

// HandlerV1 returns the /v1/* proxy handler. No precheck, no JWT — LiteLLM
// handles model-level auth via the shared key + key_id headers.
func HandlerV1(deps HandlerDeps) http.HandlerFunc { return taggedPassthrough(deps, "/v1") }

// HandlerV2 mirrors HandlerV1 for /v2/*. Same taggedPassthrough constructor,
// same auth translation, no precheck and no JWT — LiteLLM stays the
// authorization boundary for every /v2 endpoint, exactly as for /v1 (B.3.2).
//
// Security note to preserve verbatim in the HandlerV2 doc comment or the PR
// body (B.3.9, flagged not blocked — user-approved design): a blanket `/v2/*`
// pass-through exposes every LiteLLM `/v2` endpoint (e.g. `/v2/team/list`,
// `/v2/key/info`) to any authenticated `pk_`/`ek_`, precisely as `/v1/*`
// exposes every `/v1` endpoint today. LiteLLM stays the authorization boundary.
// Narrowing would be a change to **both** families, never a `/v2` special case.
// Its measured extent is recorded by P0-v2 item (6) (C.3).
func HandlerV2(deps HandlerDeps) http.HandlerFunc { return taggedPassthrough(deps, "/v2") }

// HandlerGemini mirrors HandlerV1 for /gemini/*.
func HandlerGemini(deps HandlerDeps) http.HandlerFunc { return taggedPassthrough(deps, "/gemini") }

// maybeInjectEnvironmentTag is the FWD-06 ek_ guard shared by /v1 + /v2 + /gemini.
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

type precheckFunc func(ctx context.Context, kc middleware.KeyContext, name string, deps precheck.Deps) ([]string, error)

func handlerNamed(deps HandlerDeps, kind string, check precheckFunc, audPrefix, routeLabel string) http.HandlerFunc {
	rp := New(deps.Deps)
	inner := func(w http.ResponseWriter, r *http.Request) {
		kc, _ := middleware.KeyContextFromCtx(r.Context())
		name := chi.URLParam(r, "name")
		reqID := middleware.RequestIDFromCtx(r.Context())
		keyTypeLabel := keyTypeFor(r.Context())

		// 1. Precheck — §5.1 step-4.
		if _, err := check(r.Context(), kc, name, deps.PrecheckDeps); err != nil {
			outcome, status, code := classifyPrecheckErr(err)
			metrics.IncRequests(routeLabel, keyTypeLabel, outcome)
			if errors.Is(err, precheck.ErrLiteLLMUnreachable) {
				metrics.IncLiteLLMUnreachable()
			}
			render.Error(w, status, code, codeMessage(code), reqID)
			return
		}

		// 2. BIP resolve. BIPResolver.Resolve returns nil for no-policy
		//    AND for explicit opt-out (winner.ForwardIdentityJWT == false);
		//    both collapse to "no_policy" at the metrics layer per
		//    CONTEXT D-Discretion. The cache itself reads from Postgres
		//    (internal/forwarder/bipcache, issue #34 C1).
		winner := deps.BIPResolver.Resolve(kind, name)
		if winner == nil {
			metrics.IncJWTSuppressed(kind, "no_policy")
			metrics.IncRequests(routeLabel, keyTypeLabel, "forwarded")
			if deps.Deps.Logger != nil {
				deps.Deps.Logger.Debug("forwarder: no backend identity policy; forwarding without JWT",
					"kind", kind,
					"target", name,
					"request_id", reqID,
				)
			}
			rp.ServeHTTP(w, r)
			return
		}

		// 3. Sign + stash for Director.
		token, err := deps.Signer.Sign(r.Context(), jwt.Claims{
			Iss:   deps.BaseURL,
			Sub:   kc.OwnerEmail,
			Aud:   audPrefix + name,
			Email: kc.OwnerEmail,
		})
		if err != nil {
			metrics.IncJWTSuppressed(kind, "signing_failure")
			metrics.IncRequests(routeLabel, keyTypeLabel, "internal_error")
			render.Error(w, http.StatusInternalServerError, "internal_error", "jwt sign failed", reqID)
			return
		}
		metrics.IncJWTSigned(kind)
		metrics.IncRequests(routeLabel, keyTypeLabel, "forwarded")
		if deps.Deps.Logger != nil {
			// BFI event: the forwarder minted + is attaching the ACH identity
			// JWT for this backend. Info-level (a meaningful trust-path event,
			// same verbosity as the per-request AccessLog) so operators can
			// confirm identity forwarding without raising the log level.
			deps.Deps.Logger.Info("forwarder: backend identity forwarded (JWT minted)",
				"kind", kind,
				"target", name,
				"aud", audPrefix+name,
				"owner", kc.OwnerEmail,
				"request_id", reqID,
			)
		}

		// 4. Hand off — Director reads jwtCtxKey and writes Authorization
		//    AFTER headers.StripAndRewrite (jwt-LAST ordering, D-05).
		r = r.WithContext(WithJWT(r.Context(), token))
		rp.ServeHTTP(w, r)
	}
	return observeDuration(routeLabel, inner)
}

// Precheck outcome / envelope-code constants per Hub §15.5.
const (
	outcomeUnauthorizedResource = "unauthorized_resource"
	outcomeUnauthorizedTeam     = "unauthorized_team"
	outcomeLitellmUnreachable   = "litellm_unreachable"
	outcomeInvalidKeyType       = "invalid_key_type"
	outcomeInternalError        = "internal_error"
)

// precheckOutcomes binds each precheck outcome to its HTTP status + stable
// message (Hub §15.5). Keyed by the outcome* constants so the taxonomy
// lives in ONE place — classifyPrecheckErr and codeMessage both read it.
var precheckOutcomes = map[string]struct {
	status int
	msg    string
}{
	outcomeUnauthorizedResource: {http.StatusForbidden, "name not authorized for bound environment"},
	outcomeUnauthorizedTeam:     {http.StatusForbidden, "caller's teams do not grant access to this resource"},
	outcomeLitellmUnreachable:   {http.StatusServiceUnavailable, "litellm reachability failure during teams resolve"},
	outcomeInvalidKeyType:       {http.StatusUnauthorized, "invalid or missing key type for this route"},
	outcomeInternalError:        {http.StatusInternalServerError, "internal error"},
}

// classifyPrecheckErr maps typed sentinels to outcome + HTTP status +
// envelope code per Hub §15.5. code is identical to outcome (the
// outcome constant doubles as the envelope code).
func classifyPrecheckErr(err error) (outcome string, status int, code string) {
	oc := outcomeInternalError
	switch {
	case errors.Is(err, precheck.ErrUnauthorizedResource):
		oc = outcomeUnauthorizedResource
	case errors.Is(err, precheck.ErrUnauthorizedTeam):
		oc = outcomeUnauthorizedTeam
	case errors.Is(err, precheck.ErrLiteLLMUnreachable):
		oc = outcomeLitellmUnreachable
	case errors.Is(err, precheck.ErrInvalidKeyType):
		oc = outcomeInvalidKeyType
	}
	return oc, precheckOutcomes[oc].status, oc
}

// codeMessage returns the stable human-readable message for an outcome
// code (Hub §15.5). Unknown codes return "". v1alpha1 ships English
// literals; localization is v1beta1.
func codeMessage(code string) string {
	return precheckOutcomes[code].msg
}

// statusRecorder captures the first WriteHeader for the duration metric.
// Unwrap keeps http.ResponseController (and ReverseProxy's flush path)
// working through the wrapper — same pattern as the platformapi
// statusCapturingWriter.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// statusClass buckets a status code into the §18.5 status_class enum.
// An implicit 200 (handler never called WriteHeader) counts as 2xx.
func statusClass(code int) string {
	if code == 0 {
		code = http.StatusOK
	}
	return strconv.Itoa(code/100) + "xx"
}

// observeDuration wraps h, emitting
// ach_forwarder_request_duration_seconds{route, key_type, status_class}
// per Hub §18.5 once the response completes.
func observeDuration(routeLabel string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		metrics.ObserveRequestDuration(routeLabel, keyTypeFor(r.Context()),
			statusClass(rec.status), time.Since(start).Seconds())
	}
}
