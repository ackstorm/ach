// SPDX-License-Identifier: Apache-2.0

// Package obsapi holds the request helpers the Enterprise observability routes
// under /platform/observability/* share: the key guard, the window parser, the
// report handler and the error mapping. The core edition mounts no route here.
// Identity comes only from the Authn key context, never from the request, and
// a future manager view is another handler with a wider evidence.Scope.
package obsapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

const (
	dateLayout       = "2006-01-02"
	defaultRangeDays = 30
	maxRangeDays     = 31
)

// ScopeFrom applies the guards in order: key context present, pk only, then
// configuration. It writes the rejection itself and reports whether to go on.
func ScopeFrom(w http.ResponseWriter, r *http.Request, configured bool) (evidence.Scope, bool) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	// Authn can pass foreign credentials through without a key context.
	kc, ok := middleware.KeyContextFromCtx(r.Context())
	if !ok || kc.KeyType != keys.PrefixPk {
		render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "observability requires a personal identity", reqID)
		return evidence.Scope{}, false
	}
	s, err := evidence.NewUserScope(kc.OwnerEmail)
	if err != nil {
		render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "observability requires a personal identity", reqID)
		return evidence.Scope{}, false
	}
	if !configured {
		render.Error(w, http.StatusServiceUnavailable, audit.OutcomeNotReady, "observability is not configured", reqID)
		return evidence.Scope{}, false
	}
	return s, true
}

// ParseWindow reads start/end (YYYY-MM-DD, UTC, inclusive end; default the last 30
// days) and returns the half-open [start, end+1d) the evidence layer expects.
func ParseWindow(r *http.Request, now time.Time) (evidence.Window, error) {
	today := now.UTC().Truncate(24 * time.Hour)
	end := today
	if v := r.URL.Query().Get("end"); v != "" {
		var err error
		if end, err = time.Parse(dateLayout, v); err != nil {
			return evidence.Window{}, errors.New("end must be YYYY-MM-DD")
		}
	}
	start := end.AddDate(0, 0, -(defaultRangeDays - 1))
	if v := r.URL.Query().Get("start"); v != "" {
		var err error
		if start, err = time.Parse(dateLayout, v); err != nil {
			return evidence.Window{}, errors.New("start must be YYYY-MM-DD")
		}
	}
	if start.After(end) || start.After(today) {
		return evidence.Window{}, errors.New("start must not be after end or today")
	}
	if int(end.Sub(start).Hours()/24)+1 > maxRangeDays {
		return evidence.Window{}, fmt.Errorf("range must not exceed %d days", maxRangeDays)
	}
	return evidence.Window{Start: start, End: end.AddDate(0, 0, 1), Timezone: "UTC"}, nil
}

// Report serves a whole-window summary (context, workloads, ...). A source
// failure is a 200 with unavailable provenance, like the trace list:
// unavailable is not empty.
func Report[T any](get func(context.Context, evidence.Scope, evidence.Window) (T, error), configured bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := middleware.RequestIDFromCtx(r.Context())
		s, ok := ScopeFrom(w, r, configured)
		if !ok {
			return
		}
		win, err := ParseWindow(r, time.Now())
		if err != nil {
			render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
			return
		}
		out, err := get(r.Context(), s, win)
		if err != nil {
			WriteErr(w, err, reqID)
			return
		}
		render.JSON(w, http.StatusOK, out)
	}
}

// WriteErr maps evidence errors to responses. Upstream text is never returned:
// a foreign project selector and an unknown trace look identical (no oracle).
func WriteErr(w http.ResponseWriter, err error, reqID string) {
	switch {
	case errors.Is(err, evidence.ErrInvalidArgument):
		render.Error(w, http.StatusBadRequest, "invalid_argument", "invalid argument", reqID)
	case errors.Is(err, evidence.ErrProjectRequired):
		render.Error(w, http.StatusConflict, "project_required", "trace exists in several projects; pass project", reqID)
	case errors.Is(err, evidence.ErrNotFound), errors.Is(err, evidence.ErrProjectNotAllowed):
		render.Error(w, http.StatusNotFound, "trace_not_found", "trace not found", reqID)
	default: // source error, scope mismatch, anything unexpected
		render.Error(w, http.StatusServiceUnavailable, audit.OutcomeNotReady, "observability source unavailable", reqID)
	}
}
