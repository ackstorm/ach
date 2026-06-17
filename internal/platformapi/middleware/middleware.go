// SPDX-License-Identifier: Apache-2.0

package middleware

import (
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

// xAchKeyHeader is the canonical bearer-credential header per Hub §3.
// Authn reads it once, resolves it, and DISCARDS it from r.Header before
// the inner handler runs (D-19 / T-03-05-02).
const xAchKeyHeader = "x-ach-key"

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

// Authn is the load-bearing middleware. It reads x-ach-key from
// r.Header, resolves it via the Resolver, and either:
//
//   - rejects the request with 401 (missing/invalid/expired bearer) plus
//     a render.Error envelope; or
//   - injects a populated KeyContext into ctx and discards the plaintext
//     from r.Header before invoking next.ServeHTTP (D-19).
//
// allowlist is the admin-email map (D-22 / BLK-02). pk_ callers whose
// OwnerEmail appears in the map receive KeyContext.IsAdmin=true; ek_
// callers ALWAYS receive false (admin endpoints reject ek_ upstream
// regardless, but Authn enforces the contract uniformly).
//
// auditLog receives audit emissions on internal_error paths only (401
// rejections are operational signals, not audit-worthy events).
func Authn(resolver keystore.Resolver, allowlist map[string]struct{}, auditLog *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := RequestIDFromCtx(ctx)

			plaintext := r.Header.Get(xAchKeyHeader)
			if plaintext == "" {
				render.Error(w, http.StatusUnauthorized, "missing_key", "x-ach-key header required", reqID)
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
				// Revoked / expired / unknown — indistinguishable per
				// KEY-04 / KEY-06.
				render.Error(w, http.StatusUnauthorized, audit.OutcomeExpiredOrRevoked, "key expired or revoked", reqID)
				return
			}

			// D-19: discard plaintext from r.Header BEFORE invoking inner.
			// Literal header name kept inline (not via xAchKeyHeader
			// constant) so the static-analysis grep gate that proves the
			// plaintext-discard discipline catches this site verbatim.
			r.Header.Del("x-ach-key")

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
