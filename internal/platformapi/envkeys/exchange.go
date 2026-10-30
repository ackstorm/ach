// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	authnv1 "k8s.io/api/authentication/v1"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keygrant"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// TokenReviewFunc asks the Kubernetes API server to review a ServiceAccount
// token for one audience (authentication.k8s.io/v1 TokenReview).
type TokenReviewFunc func(ctx context.Context, token, audience string) (authnv1.TokenReviewStatus, error)

// exchangeDB is the exchange handler's Postgres seam beyond dbOps.
type exchangeDB interface {
	ListKeyGrantsForIdentity(ctx context.Context, ns, identity string) ([]db.KeyGrantRow, error)
	FindExchangeKey(ctx context.Context, owner, environment, requester string) (*db.EkKeyInfo, error)
}

// ExchangeDeps is Deps plus what POST /platform/keys/exchange alone needs.
type ExchangeDeps struct {
	Deps
	ExchangeDB  exchangeDB
	TokenReview TokenReviewFunc
	Signer      jwt.Signer
	// TTL is the exchanged JWT's lifetime (ACH_KEY_EXCHANGE_TOKEN_TTL).
	TTL time.Duration
}

// ExchangeRequest is the POST /platform/keys/exchange body.
type ExchangeRequest struct {
	Identity    string `json:"identity"`
	Environment string `json:"environment"`
}

// ExchangeResponse carries the JWT. Clients schedule the next exchange from
// ExpiresIn, never from the token's wall-clock exp.
type ExchangeResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
	KeyID     string `json:"key_id"`
}

const (
	saUserPrefix = "system:serviceaccount:"
	extraPodName = "authentication.kubernetes.io/pod-name"
	extraPodUID  = "authentication.kubernetes.io/pod-uid"

	// The limits guard the API server, not ACH: each admitted request costs
	// one TokenReview. A healthy agent exchanges every ~12 minutes; the
	// per-client burst covers a node's worth of agents restarting at once
	// behind one egress address.
	exchangePerClientRate  = 1
	exchangePerClientBurst = 10
	exchangeGlobalRate     = 10
	exchangeGlobalBurst    = 50
	exchangeMaxClients     = 10000

	// The route is anonymous until TokenReview: bound what it reads and what
	// reaches the audit log.
	exchangeMaxBody  = 4096
	exchangeMaxField = 256
)

// ExchangeHandler serves POST /platform/keys/exchange. A pod-bound
// ServiceAccount token plus a KeyGrant naming its ServiceAccount, the identity
// and the Environment together buys a short-lived ACH JWT whose subject is
// the tuple's one stable ek_, minted on first use by the create path's mint.
// The raw ek_ never leaves ACH.
//
// Two guards never reach the API server: the bearer must be a compact JWS
// whose unverified aud names this ACH (401 invalid_token), then the
// per-client and global rate limit (429) — after the first guard, so junk
// cannot drain the budget that protects TokenReview. Then, first failure
// answers: TokenReview (401 invalid_token), one grant allows the tuple (403
// not_granted, one code for every miss; the audit names which), the
// Environment exists and is synced (404 / 503), the identity's teams
// intersect authorizedTeams (403 unauthorized_team). Exactly one
// platform.ek.exchange audit event per request, never carrying a token.
func ExchangeHandler(d ExchangeDeps) http.HandlerFunc {
	aud := strings.TrimRight(d.Issuer, "/")
	lim := newExchangeLimiter()
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		ev := audit.Event{Action: audit.ActionEkExchange, RequestID: reqID, Extra: map[string]string{}}
		deny := func(status int, code, msg, reason string) {
			ev.Outcome = code
			if reason != "" {
				ev.Extra["exchange.reason"] = reason
			}
			audit.EmitAudit(ctx, d.Audit, ev)
			render.Error(w, status, code, msg, reqID)
		}

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !keys.LooksLikeJWS(token) || !slices.Contains(keys.PeekAudience(token), aud) {
			deny(http.StatusUnauthorized, audit.OutcomeInvalidToken, "a ServiceAccount token issued for this ACH is required", "bearer")
			return
		}
		if !lim.allow(middleware.ClientIP(r)) {
			w.Header().Set("Retry-After", "1")
			deny(http.StatusTooManyRequests, audit.OutcomeRateLimited, "too many exchange requests", "")
			return
		}
		var req ExchangeRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, exchangeMaxBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || dec.More() || req.Identity == "" || req.Environment == "" ||
			len(req.Identity) > exchangeMaxField || len(req.Environment) > exchangeMaxField {
			deny(http.StatusBadRequest, codeInvalidArgument, "identity and environment required", "")
			return
		}
		identity := litellm.NormalizeEmail(req.Identity)
		ev.Extra["exchange.identity"] = identity
		ev.Target = &audit.Target{Kind: "environment", Name: req.Environment}

		st, err := d.TokenReview(ctx, token, aud)
		if err != nil {
			d.Logger.Error("envkeys.exchange: TokenReview failed", "err", err)
			deny(http.StatusServiceUnavailable, audit.OutcomeNotReady, "token review unavailable", "tokenreview_error")
			return
		}
		requester, why := reviewedRequester(st, aud)
		if why != "" {
			deny(http.StatusUnauthorized, audit.OutcomeInvalidToken, "invalid ServiceAccount token", why)
			return
		}
		ev.Actor = st.User.Username
		ev.Extra["exchange.requester"] = requester
		ev.Extra["exchange.pod_name"] = firstExtra(st.User.Extra[extraPodName])
		ev.Extra["exchange.pod_uid"] = firstExtra(st.User.Extra[extraPodUID])

		grants, err := d.ExchangeDB.ListKeyGrantsForIdentity(ctx, d.Namespace, identity)
		if err != nil {
			d.Logger.Error("envkeys.exchange: grant read failed", "err", err)
			deny(http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", "")
			return
		}
		covering := keygrant.Covering(grants, identity, req.Environment, requester)
		if len(covering) == 0 {
			deny(http.StatusForbidden, audit.OutcomeNotGranted, "no KeyGrant allows this exchange", keygrant.Miss(grants, req.Environment))
			return
		}

		// Environment, team and mint are the create path's own steps.
		cr := &createReq{
			deps: d.Deps, w: w, ctx: ctx, action: audit.ActionEkExchange, extra: ev.Extra,
			actor: ev.Actor, reqID: reqID, target: ev.Target, requester: requester, xdb: d.ExchangeDB,
			keyCtx: middleware.KeyContext{OwnerEmail: identity},
			req:    CreateRequest{Environment: req.Environment, Name: "exchange:" + requester},
		}
		if b := keygrant.Budget(covering); b != nil {
			cr.req.Budget = &BudgetRequest{MaxBudget: &b.MaxBudget, BudgetDuration: b.BudgetDuration}
		}
		env, handled := cr.validateAndLoadEnv()
		if handled {
			return
		}
		if cr.validateTeamMembership(env) {
			return
		}
		keyID, ok := cr.stableKey(env)
		if !ok {
			return
		}
		signed, err := d.Signer.Sign(ctx, jwt.Claims{
			Iss: aud, Sub: keyID, Aud: keystore.ExchangeAudience, TTL: d.TTL, JTI: rand.Text(),
		})
		if err != nil {
			cr.emitInternalError("envkeys.exchange: sign failed", err)
			return
		}
		ev.Outcome, ev.KeyID = audit.OutcomeIssued, keyID
		audit.EmitAudit(ctx, d.Audit, ev)
		render.JSON(w, http.StatusOK, ExchangeResponse{Token: signed, ExpiresIn: int(d.TTL / time.Second), KeyID: keyID})
	}
}

// stableKey returns the tuple's live exchange key, minting it on first use.
// The key's budget (lowest covering grant) is written by mint at creation
// and re-written on every later exchange; the operator's KeyGrant sweep
// keeps it current in between.
func (cr *createReq) stableKey(env *db.EnvironmentRow) (string, bool) {
	row, err := cr.xdb.FindExchangeKey(cr.ctx, cr.keyCtx.OwnerEmail, env.Name, cr.requester)
	if err != nil {
		cr.emitInternalError("envkeys.exchange: FindExchangeKey failed", err)
		return "", false
	}
	if row != nil {
		if row.Status != statusActive {
			cr.refuseSuspended(row.KeyID)
			return "", false
		}
		return row.KeyID, cr.applyBudget(row.KeyID)
	}
	userID, handled := cr.provisionUser()
	if handled {
		return "", false
	}
	keyID, plaintext, ok := cr.mint(env, userID)
	if ok && plaintext == "" {
		// Race loser: the winner's budget write may have failed.
		ok = cr.applyBudget(keyID)
	}
	return keyID, ok
}

// refuseSuspended answers 409: a JWT for a suspended key could never
// authenticate.
func (cr *createReq) refuseSuspended(keyID string) {
	audit.EmitAudit(cr.ctx, cr.deps.Audit, audit.Event{
		Action: cr.action, Outcome: audit.OutcomeKeySuspended, Actor: cr.actor,
		RequestID: cr.reqID, KeyID: keyID, Target: cr.target, Extra: cr.extra,
	})
	render.Error(cr.w, http.StatusConflict, audit.OutcomeKeySuspended, "the exchange key for this grant is suspended", cr.reqID)
}

// applyBudget re-writes the covering grants' ceiling onto an existing key's
// tag, so a write refused at mint is never skipped by a later exchange: no JWT
// is issued for a key that may be uncapped. It answers the request itself on
// failure.
func (cr *createReq) applyBudget(keyID string) bool {
	if cr.req.Budget == nil {
		return true
	}
	if err := cr.deps.LiteLLM.UpsertTagBudget(cr.ctx, litellm.KeyBudgetTag(keyID), cr.req.Budget.TagBudget()); err != nil {
		cr.emitLitellmError(err, "envkeys.exchange: key budget tag")
		return false
	}
	return true
}

// reviewedRequester turns a TokenReview answer into "<namespace>/<name>", or
// names why the token is refused. Only a pod-bound token carries the pod
// extras.
func reviewedRequester(st authnv1.TokenReviewStatus, aud string) (requester, why string) {
	switch {
	case !st.Authenticated:
		return "", "unauthenticated"
	case !slices.Contains(st.Audiences, aud):
		return "", "audience"
	}
	rest, isSA := strings.CutPrefix(st.User.Username, saUserPrefix)
	ns, name, ok := strings.Cut(rest, ":")
	if !isSA || !ok || ns == "" || name == "" {
		return "", "not_service_account"
	}
	if firstExtra(st.User.Extra[extraPodName]) == "" || firstExtra(st.User.Extra[extraPodUID]) == "" {
		return "", "not_pod_bound"
	}
	return ns + "/" + name, ""
}

func firstExtra(v authnv1.ExtraValue) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// exchangeLimiter is a per-client token bucket in front of a global one.
// ponytail: the client map is cleared when it reaches exchangeMaxClients and
// is keyed on the first X-Forwarded-For hop, which a caller can forge — the
// global bucket is the hard bound on TokenReview calls. An LRU keyed on a
// trusted proxy hop is the upgrade if per-client fairness ever matters.
type exchangeLimiter struct {
	mu      sync.Mutex
	global  *rate.Limiter
	clients map[string]*rate.Limiter
}

func newExchangeLimiter() *exchangeLimiter {
	return &exchangeLimiter{
		global:  rate.NewLimiter(exchangeGlobalRate, exchangeGlobalBurst),
		clients: map[string]*rate.Limiter{},
	}
}

func (l *exchangeLimiter) allow(addr string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host // RemoteAddr carries a per-connection port
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.clients[addr]
	if !ok {
		if len(l.clients) >= exchangeMaxClients {
			clear(l.clients)
		}
		c = rate.NewLimiter(exchangePerClientRate, exchangePerClientBurst)
		l.clients[addr] = c
	}
	return c.Allow() && l.global.Allow()
}
