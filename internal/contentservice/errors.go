// SPDX-License-Identifier: Apache-2.0

package contentservice

import (
	"net/http"
	"os"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// errResp is the typed view a gate returns to the pipeline orchestrator
// on denial. It binds the §15.6 D-03 outcome table verbatim — every
// instance is built by one of the err* factory functions below; never
// constructed by hand at the gate site so the (status, code, message)
// triple stays grep-able and the closed enum stays closed.
type errResp struct {
	// HTTPStatus is the response status (per D-03 table).
	HTTPStatus int
	// Code is the §15.5 envelope `error.code` AND the audit `outcome`
	// string. Matches audit.Outcome* / D-03 entries verbatim.
	Code string
	// Message is the hard-coded `error.message` literal. T-03-02-02:
	// MUST NOT echo upstream errors, user input, or stack traces.
	Message string
}

// Shared error-response values for each D-03 outcome. Centralising the
// (status, code, message) triples here keeps the wire-format vocabulary
// closed and the gate-site code paths short. Message strings are
// deliberately terse and non-revealing (T-03-02-02 — no upstream-error
// echoing).
var (
	errMissingEnvironment  = &errResp{HTTPStatus: http.StatusBadRequest, Code: audit.OutcomeMissingEnvironment, Message: "x-ach-environment header required for pk_ requests"}
	errInvalidKeyFormat    = &errResp{HTTPStatus: http.StatusBadRequest, Code: audit.OutcomeInvalidKeyFormat, Message: "malformed bearer key"}
	errExpiredOrRevoked    = &errResp{HTTPStatus: http.StatusUnauthorized, Code: audit.OutcomeExpiredOrRevoked, Message: "key expired or revoked"}
	errUnauthorizedTeam    = &errResp{HTTPStatus: http.StatusForbidden, Code: audit.OutcomeUnauthorizedTeam, Message: "team membership does not authorize this environment"}
	errWrongEnvironment    = &errResp{HTTPStatus: http.StatusForbidden, Code: audit.OutcomeWrongEnvironment, Message: "ek_ bound environment does not match x-ach-environment header"}
	errUnauthorizedContent = &errResp{HTTPStatus: http.StatusForbidden, Code: audit.OutcomeUnauthorizedContent, Message: "content not in environment context allowlist"}
	errEnvironmentNotFound = &errResp{HTTPStatus: http.StatusNotFound, Code: audit.OutcomeEnvironmentNotFound, Message: "environment not found"}
	errContentNotFound     = &errResp{HTTPStatus: http.StatusNotFound, Code: audit.OutcomeContentNotFound, Message: "content not found"}
	errLitellmUnreachable  = &errResp{HTTPStatus: http.StatusServiceUnavailable, Code: audit.OutcomeLitellmUnreachable, Message: "team resolver unavailable"}
	errStaleCacheExpired   = &errResp{HTTPStatus: http.StatusServiceUnavailable, Code: audit.OutcomeStaleCacheExpired, Message: "cache file too stale to serve"}
	errInternal            = &errResp{HTTPStatus: http.StatusInternalServerError, Code: audit.OutcomeInternalError, Message: "internal error"}
)

// writeError is the centralised error-response writer for the Content
// Service. Three responsibilities, in this order:
//
//  1. Render the §15.5 error envelope via render.Error — reuses the
//     same {error:{code,message},request_id} shape that Phase 3
//     Platform API emits, so a single client-side parser handles
//     errors from both services.
//  2. Emit exactly one audit event via audit.EmitAudit with the
//     ActionContentGet action and the outcome string equal to the
//     response body `code` (so a log filter on `outcome=<code>` joins
//     the response and the audit line). Per the §18.5 / D-Discretion
//     contract, every Content Service request emits one audit record
//     (regardless of success or failure).
//  3. Increment the ach_content_service_requests_total{kind, outcome}
//     metric via Metrics.IncRequest — closes the §18.5 normative
//     metric contract (OBS-06) by tagging every denial with its
//     outcome code.
//
// The keystore.KeyInfo argument is optional — pass nil on gate-1 or
// pre-auth denials where no info has been resolved yet. nil info
// suppresses key.id from the audit attribute set (EmitAudit omits the
// attribute when KeyID == "").
func (d Deps) writeError(w http.ResponseWriter, r *http.Request, kind, name string, info *keystore.KeyInfo, e *errResp) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	render.Error(w, e.HTTPStatus, e.Code, e.Message, reqID)
	d.emitAudit(r.Context(), kind, name, e.Code, info)
	if d.Metrics != nil {
		d.Metrics.IncRequest(kind, e.Code)
	}
}

// actorFromInfo composes the "<namespace>/<owner-email>" actor per Hub
// §18.3. Falls back to "<namespace>/-" when info is nil (pre-auth gate
// denials). Mirrors middleware.ActorFromCtx but reads OwnerEmail
// directly from KeyInfo so the helper does not depend on the
// platformapi middleware context stack — Content Service does not
// run Phase 3's Authn middleware (auth happens inline in pipeline.go).
//
// Missing POD_NAMESPACE collapses to empty string (which still keeps
// the leading "/" so the actor field is parseable). Phase 3 collapsed
// to "unknown" via ActorFromCtx; this helper deliberately mirrors the
// raw env behavior to make test fixtures pure (no env mutation).
func actorFromInfo(info *keystore.KeyInfo) string {
	ns := os.Getenv("POD_NAMESPACE")
	if info == nil || info.OwnerEmail == "" {
		return ns + "/-"
	}
	return ns + "/" + info.OwnerEmail
}

// keyIDFromInfo returns info.KeyID when info != nil, else "". The
// caller passes the result as audit.Event.KeyID — EmitAudit omits the
// key.id attribute when the value is empty.
func keyIDFromInfo(info *keystore.KeyInfo) string {
	if info == nil {
		return ""
	}
	return info.KeyID
}
