// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"bufio"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// LoadAllowlist reads the admin allowlist file at path and returns the
// in-memory lookup map every admin endpoint consults. Format (per Hub
// §18 + D-22):
//
//   - One email per line
//   - Blank lines (after whitespace trim) ignored
//   - Lines beginning with '#' (after whitespace trim) ignored
//   - Leading + trailing whitespace trimmed
//   - Case-sensitive verbatim comparison (mirrors §16 DB-05
//     owner_email storage discipline — no normalization)
//
// Behavior on absent / empty / malformed file (D-23):
//
//   - os.IsNotExist(err) → (empty-map, nil) + WARN log. Per Hub AC18 +
//     AC24 a missing ConfigMap is a deployer choice (zero admins) and
//     ACH MUST NOT refuse to start.
//   - Empty file (or all-comments/blanks) → (empty-map, nil) + WARN log.
//   - Other I/O errors (permission denied, etc.) → (nil, err) — caller
//     (cmd/platform-api/main.go) treats this as fatal.
//
// The file is NEVER re-read. ConfigMap edits require Platform API
// restart (D-22 / D-23).
//
// Behavior MUST match the SSO `email` claim format verbatim — case is
// preserved as-written; no normalization happens here or at the
// authn middleware site.
func LoadAllowlist(path string, logger *slog.Logger) (map[string]struct{}, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if logger != nil {
				logger.Warn("admin allowlist file missing; zero admins; all admin endpoints will return 403 not_admin", "path", path)
			}
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	result := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// strings.TrimSpace drops CR (from CRLF line endings) along with
		// LF/tab/space, so DOS-formatted ConfigMaps work without an
		// explicit CR-strip pass.
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result[line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 && logger != nil {
		logger.Warn("admin allowlist parsed to zero admins; all admin endpoints will return 403 not_admin", "path", path)
	}
	return result, nil
}

// AdminOnly is the middleware that gates every /platform/admin/* route.
// Per Hub §15.5 + §18 + API-12 it runs BEFORE any handler-specific
// validation so the rejection reason is uniform across endpoints.
//
// Two rejection paths (each emits one audit event before responding):
//
//   - keyCtx.KeyType != keys.PrefixPk → 401 invalid_key_type
//     Captures the ek_-on-admin contract: ek_ represents a workload,
//     not a person; admin endpoints are person-scoped tools.
//   - keyCtx.OwnerEmail not in allowlist → 403 not_admin
//     Empty allowlist (zero admins per D-23) rejects every caller here.
//
// On success the middleware passes the request through unchanged —
// the inner handler reads middleware.KeyContextFromCtx for the
// authenticated identity and emits its own per-action audit event.
//
// allowlist may be nil (treated as zero admins — every pk_ caller hits
// 403). auditLog may be nil (rejections still rendered; audit emission
// is best-effort visibility — never a correctness requirement).
//
// ns is the deployment namespace (downward API POD_NAMESPACE); used to
// compose the actor field per Hub §18.3 when the request-bound
// middleware.ActorFromCtx fallback ("unknown/<email>") would degrade
// log grep-ability.
func AdminOnly(allowlist map[string]struct{}, auditLog *slog.Logger, ns string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := middleware.RequestIDFromCtx(ctx)

			keyCtx, ok := middleware.KeyContextFromCtx(ctx)
			if !ok {
				// Defensive: the chi.Group that mounts admin routes always
				// runs Authn upstream, so this branch is unreachable in
				// production. The render shape mirrors the Authn 401 so a
				// misconfigured route still returns a well-formed envelope.
				render.Error(w, http.StatusUnauthorized, audit.OutcomeExpiredOrRevoked, "auth required", reqID)
				return
			}

			actor := composeActor(ns, keyCtx.OwnerEmail)

			if keyCtx.KeyType != keys.PrefixPk {
				if auditLog != nil {
					audit.EmitAudit(ctx, auditLog, audit.Event{
						Action:    audit.ActionAdminKeysRevoke,
						Outcome:   audit.OutcomeInvalidKeyType,
						Actor:     actor,
						RequestID: reqID,
					})
				}
				render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "admin endpoints require pk_", reqID)
				return
			}

			if _, isAdmin := allowlist[keyCtx.OwnerEmail]; !isAdmin {
				if auditLog != nil {
					audit.EmitAudit(ctx, auditLog, audit.Event{
						Action:    audit.ActionAdminKeysRevoke,
						Outcome:   audit.OutcomeNotAdmin,
						Actor:     actor,
						RequestID: reqID,
					})
				}
				render.Error(w, http.StatusForbidden, audit.OutcomeNotAdmin, "not in admin allowlist", reqID)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// composeActor returns the "<ns>/<email>" actor string per Hub §18.3.
// Falls back to "unknown" on empty ns and "-" on empty email so the
// emitted attribute is always grep-able.
func composeActor(ns, email string) string {
	if ns == "" {
		ns = "unknown"
	}
	if email == "" {
		email = "-"
	}
	return ns + "/" + email
}
