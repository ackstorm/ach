// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// Credential slots, in precedence order (alitellm-auth authz/decide.go, the
// rules ACH mirrors): x-ach-key is ACH's own header (Hub §3); x-api-key is
// where the Anthropic SDK puts a key (Claude Code with ANTHROPIC_API_KEY or
// an apiKeyHelper); Authorization is consulted LAST and only consumed when
// it carries a LiteLLM key or our JWT — otherwise it is the upstream
// provider's credential and passes through untouched. Authn reads the slot
// once, resolves it, and DISCARDS it from r.Header before the inner handler
// runs (D-19 / T-03-05-02).
const (
	ModeResolve     = "resolve"
	ModePassthrough = "passthrough"

	authzHeader = "Authorization"
)

// CredentialHeader is one declared credential slot (chart forwarder.headers /
// platformApi.headers → ACH_CREDENTIAL_HEADERS): the header ACH reads and
// what it does with the value.
type CredentialHeader struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// ParseCredentialHeaders decodes the JSON list. Names are lower-cased.
// "authorization" is never a slot: it is ACH's own OAuth bearer, handled by
// protocol, not by config.
func ParseCredentialHeaders(raw string) ([]CredentialHeader, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []CredentialHeader
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("credential headers: %w", err)
	}
	seen := map[string]bool{}
	for i := range out {
		out[i].Name = strings.ToLower(strings.TrimSpace(out[i].Name))
		switch {
		case out[i].Name == "":
			return nil, errors.New("credential headers: empty name")
		case out[i].Name == "authorization":
			return nil, errors.New("credential headers: authorization is not configurable")
		case out[i].Mode != ModeResolve && out[i].Mode != ModePassthrough:
			return nil, fmt.Errorf("credential headers: %s: mode must be resolve or passthrough", out[i].Name)
		case seen[out[i].Name]:
			return nil, fmt.Errorf("credential headers: %s listed twice", out[i].Name)
		}
		seen[out[i].Name] = true
	}
	return out, nil
}

// AuthnOptions is per-service policy.
type AuthnOptions struct {
	// Challenge composes the WWW-Authenticate value for a 401 (the RFC 9728
	// resource_metadata pointer). nil → no header.
	Challenge func(r *http.Request) string
	// Headers are the declared credential slots, consulted in order; the
	// first one present decides.
	Headers []CredentialHeader
	// Optional lets a request with NO credential through with no identity
	// (the forwarder's catch-all: LiteLLM decides). A present-but-invalid
	// credential is still a 401.
	Optional bool
}

// credential returns the first declared header present (value, header,
// mode). With none present, Authorization: Bearer is a resolve candidate
// ONLY when it is JWS-shaped — ACH's own OAuth token; the resolver's
// signature check decides. Anything else in Authorization is not ours:
// no candidate, and foreign=true so it is never forwarded as anonymous.
func credential(r *http.Request, headers []CredentialHeader) (value, from, mode string, foreign bool) {
	for _, h := range headers {
		if v := strings.TrimSpace(r.Header.Get(h.Name)); v != "" {
			return v, h.Name, h.Mode, false
		}
	}
	raw := strings.TrimSpace(r.Header.Get(authzHeader))
	if raw == "" {
		return "", "", "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	if strings.HasPrefix(raw, "Bearer ") && keys.LooksLikeJWS(tok) {
		return tok, authzHeader, ModeResolve, false
	}
	return "", "", "", true
}

// requestIDPrefix is the namespace for server-generated request IDs.
// "req_" mirrors the bearer prefix grammar so log filters can grep
// uniformly across pkid_/ekid_/req_ identifiers.
const requestIDPrefix = "req_"

// newRequestID generates a fresh "req_<ulid-lowercase>" id. The ULID
// generator is monotonic within a millisecond, so concurrent requests
// receive strictly increasing IDs — useful for log correlation.
//
// Per T-03-05-06 RequestID NEVER preserves a caller-supplied
// X-Request-Id. ALWAYS server-generated.
func newRequestID() string {
	return requestIDPrefix + strings.ToLower(ulid.Make().String())
}

// RequestID is the outermost middleware (D-02 step 1). It generates a
// server-side "req_<ulid>" id, stores it in ctx (so RequestIDFromCtx
// can read it), and sets the X-Request-Id response header.
//
// Caller-supplied X-Request-Id values are IGNORED — defeats request-id
// injection attacks on log correlation (T-03-05-06).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := WithRequestID(r.Context(), id)
		// G20: capture forensics metadata once, at the outermost layer, so
		// every downstream EmitAudit attaches source.ip + source.user_agent.
		ctx = audit.WithRequestMeta(ctx, audit.RequestMeta{
			SourceIP:  clientIP(r),
			UserAgent: r.Header.Get("User-Agent"),
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP returns the request's originating client address: the first
// X-Forwarded-For hop when present (the gateway/Ingress prepends it), else
// r.RemoteAddr. Used only for the audit source.ip attribute (G20).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// RecoverPanic wraps inner handlers so a panic becomes a 500
// internal_error response envelope + an audit emission. The panic value
// is sent to the operational logger; it NEVER appears in the response
// body (T-03-05-07).
//
// auditLog must be the *slog.Logger constructed by audit.NewLogger
// (audit=true predicate already attached).
func RecoverPanic(opLogger, auditLog *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler is the stdlib sentinel
					// httputil.ReverseProxy panics with to abort a streamed
					// (SSE/MCP) response — client disconnect or upstream close.
					// Re-panic so net/http closes the conn silently: it is NOT
					// an internal error, and rendering a 500 over an
					// already-started stream just yields a superfluous
					// WriteHeader. (net/http suppresses its stack trace.)
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					reqID := RequestIDFromCtx(r.Context())
					if opLogger != nil {
						opLogger.Error("platform-api: recovered from panic",
							"panic", rec,
							"method", r.Method,
							"path", r.URL.Path,
							"request_id", reqID,
						)
					}
					if auditLog != nil {
						audit.EmitAudit(r.Context(), auditLog, audit.Event{
							Action:    "platform.recover",
							Outcome:   audit.OutcomeInternalError,
							Actor:     ActorFromCtx(r.Context()),
							RequestID: reqID,
						})
					}
					render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusCapturingWriter wraps http.ResponseWriter so AccessLog can read
// the response status code after the inner handler returns. The wrapper
// intentionally exposes NO other state — body bytes, headers, etc. are
// neither captured nor logged (FWD-11 analog).
type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusCapturingWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapturingWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// AccessLog logs {method, path, status, latency_ms, request_id} for
// every request. NEVER reads or logs the x-ach-key header, request body,
// or response body (T-03-05-01 / FWD-11 invariant).
//
// opLogger is the operational logger (NOT the audit logger). Pass nil
// to disable access logging — useful for test fixtures that capture
// output via a custom logger.
func AccessLog(opLogger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if opLogger == nil {
				return
			}
			opLogger.Info("platform-api: access",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromCtx(r.Context()),
			)
		})
	}
}

// contentTypeJSONWriter sets the application/json Content-Type the
// first time WriteHeader runs, if and only if the inner handler has
// not already set Content-Type. This preserves SSO 302 redirects (which
// set Location and Content-Type: text/html) and Content-Type-aware
// success handlers.
type contentTypeJSONWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (c *contentTypeJSONWriter) WriteHeader(code int) {
	if !c.wroteHeader {
		if c.Header().Get("Content-Type") == "" {
			c.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		c.wroteHeader = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *contentTypeJSONWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		if c.Header().Get("Content-Type") == "" {
			c.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		c.wroteHeader = true
	}
	return c.ResponseWriter.Write(b)
}

// ContentTypeJSON ensures every response carries
// Content-Type: application/json; charset=utf-8 unless the inner
// handler has already set Content-Type explicitly. Idempotent — does
// NOT overwrite caller-set Content-Type values.
func ContentTypeJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&contentTypeJSONWriter{ResponseWriter: w}, r)
	})
}

// Authn is the load-bearing middleware. It reads the credential from the
// first declared slot present (opts.Headers, in order) and either:
//
//   - mode resolve: resolves it via the Resolver (pk_/ek_ or an OAuth JWS),
//     injects a populated KeyContext into ctx and discards that header
//     before invoking next.ServeHTTP (D-19); 401 when missing/invalid/expired,
//     plus the opts.Challenge WWW-Authenticate pointer when set; or
//   - mode passthrough: the backend's own key — no ACH identity
//     (RawLiteLLMKeyFromCtx), header kept; or
//   - no declared slot present: Authorization: Bearer is accepted only as
//     ACH's own OAuth token (JWS that verifies); anything else there is 401.
//
// allowlist is the admin-email map (D-22 / BLK-02). pk_ callers whose
// OwnerEmail appears in the map receive KeyContext.IsAdmin=true; ek_
// callers ALWAYS receive false (admin endpoints reject ek_ upstream
// regardless, but Authn enforces the contract uniformly).
//
// auditLog receives audit emissions on internal_error paths only (401
// rejections are operational signals, not audit-worthy events).
func Authn(resolver keystore.Resolver, allowlist map[string]struct{}, auditLog *slog.Logger, opts AuthnOptions) func(http.Handler) http.Handler {
	unauthorized := func(w http.ResponseWriter, r *http.Request, code, msg, reqID string) {
		if opts.Challenge != nil {
			w.Header().Set("WWW-Authenticate", opts.Challenge(r))
		}
		render.Error(w, http.StatusUnauthorized, code, msg, reqID)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := RequestIDFromCtx(ctx)

			plaintext, from, mode, foreign := credential(r, opts.Headers)
			if plaintext == "" {
				if opts.Optional && !foreign {
					next.ServeHTTP(w, r) // no identity: the upstream decides
					return
				}
				unauthorized(w, r, "missing_key", "present an API key or a bearer token", reqID)
				return
			}

			// passthrough: the backend's own key, no ACH identity. The header
			// stays (the backend may read it by that name); the proxy mirrors
			// it to x-litellm-api-key.
			if mode == ModePassthrough {
				next.ServeHTTP(w, r.WithContext(WithRawLiteLLMKey(ctx, plaintext)))
				return
			}

			info, err := resolver.Resolve(ctx, plaintext)
			if err != nil {
				if auditLog != nil {
					audit.EmitAudit(ctx, auditLog, audit.Event{
						Action:    "platform.authn",
						Outcome:   audit.OutcomeInternalError,
						Actor:     ActorFromCtx(ctx),
						RequestID: reqID,
					})
				}
				render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "internal error", reqID)
				return
			}
			if info == nil {
				// Revoked / expired / unknown / bad JWT — indistinguishable
				// per KEY-04 / KEY-06.
				unauthorized(w, r, audit.OutcomeExpiredOrRevoked, "key expired or revoked", reqID)
				return
			}

			// D-19: discard the presented credential BEFORE invoking inner.
			// Only the slot that carried it: any other header — including an
			// Authorization that is the upstream provider's own credential —
			// passes as it came.
			r.Header.Del(from)

			// BLK-02: admin status is the allowlist lookup on pk_ only.
			isAdmin := false
			if info.KeyType == keys.PrefixPk && allowlist != nil {
				_, isAdmin = allowlist[info.OwnerEmail]
			}

			ctx = WithKeyContext(ctx, info, isAdmin)
			// G20: the key-bound Environment is the governance dimension for
			// audit; handlers that operate on a different env (hydrate's
			// requested env) override the typed Event field explicitly.
			if info.Environment != "" {
				ctx = audit.WithRequestMeta(ctx, audit.RequestMeta{Environment: info.Environment})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
