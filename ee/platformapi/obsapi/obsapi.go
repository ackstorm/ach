// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

// Package obsapi serves the Enterprise observability routes under
// /platform/observability/*: traces, sessions, context, workloads, cost,
// latency, performance and classification. It registers itself with the platform-api through the
// edition seam; the guard, window and error mapping are the core ones.
package obsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/ee/classifier"
	eeevidence "github.com/ackstorm/ach/ee/observability/evidence"
	"github.com/ackstorm/ach/internal/edition"
	"github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/obsapi"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

const defaultLimit = 25

// Paths every Enterprise observability route is served on.
const (
	pathTraces         = "/platform/observability/traces"
	pathTrace          = "/platform/observability/traces/{trace_id}"
	pathSessions       = "/platform/observability/sessions"
	pathSession        = "/platform/observability/sessions/{session_id}"
	pathContext        = "/platform/observability/context"
	pathWorkloads      = "/platform/observability/workloads"
	pathCost           = "/platform/observability/cost"
	pathLatency        = "/platform/observability/latency"
	pathPerformance    = "/platform/observability/performance"
	pathClassification = "/platform/observability/classification"
)

var paths = []string{pathTraces, pathTrace, pathSessions, pathSession, pathContext, pathWorkloads, pathCost, pathLatency, pathPerformance, pathClassification}

// Reader is the Enterprise evidence surface the handlers need (*eeevidence.Reader).
type Reader interface {
	List(ctx context.Context, s evidence.Scope, q eeevidence.TraceQuery) (eeevidence.TracePage, error)
	Detail(ctx context.Context, s evidence.Scope, traceID, project string, w evidence.Window) (eeevidence.TraceDetail, error)
	Sessions(ctx context.Context, s evidence.Scope, q eeevidence.SessionQuery) (eeevidence.SessionPage, error)
	Session(ctx context.Context, s evidence.Scope, sessionID, project string, w evidence.Window) (eeevidence.SessionDetail, error)
	Context(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.ContextReport, error)
	Workloads(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.WorkloadReport, error)
	Cost(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.CostReport, error)
	Latency(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.LatencyReport, error)
	Performance(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.PerformanceReport, error)
	Classification(ctx context.Context, s evidence.Scope, w evidence.Window) (eeevidence.ClassificationReport, error)
}

// deps wires the handlers. A nil Reader means ClickHouse is not configured.
type deps struct{ Reader Reader }

// Hook is the Enterprise platform-api hook: it reads the Enterprise-only
// configuration and returns the routes.
func Hook(env edition.PlatformAPIEnv) (func(chi.Router), error) {
	if !env.Enterprise {
		return func(r chi.Router) {
			for _, p := range paths {
				r.Get(p, edition.EnterpriseOnly)
			}
		}, nil
	}
	tax, err := classifier.FromEnv()
	if err != nil {
		return nil, err
	}
	thanos, err := thanosURL()
	if err != nil {
		return nil, err
	}
	var rd Reader
	if env.ClickHouse != nil {
		r := eeevidence.NewReader(evidence.NewReader(env.ClickHouse, env.Projects)).
			WithPrices(eeevidence.LiteLLMPrices{Catalog: env.LiteLLM}).WithTaxonomy(tax)
		if thanos != "" {
			r.WithMetrics(eeevidence.NewThanos(thanos, nil))
		}
		rd = r
	}
	return func(r chi.Router) { mount(r, rd) }, nil
}

func init() { edition.RegisterPlatformAPI(Hook) }

// thanosURL reads ACH_OBSERVABILITY_THANOS_URL: empty (Performance metrics
// answer not_instrumented) or an absolute http(s) URL.
func thanosURL() (string, error) {
	raw := os.Getenv("ACH_OBSERVABILITY_THANOS_URL")
	if raw == "" {
		return "", nil
	}
	if u, err := url.Parse(raw); err != nil || !slices.Contains([]string{"http", "https"}, u.Scheme) || u.Host == "" {
		return "", fmt.Errorf("ACH_OBSERVABILITY_THANOS_URL: must be an absolute http(s) URL")
	}
	return strings.TrimRight(raw, "/"), nil
}

// mount registers the routes on r, which must already sit behind Authn.
func mount(r chi.Router, rd Reader) {
	d := deps{Reader: rd}
	r.Get(pathTraces, d.list)
	r.Get(pathTrace, d.detail)
	r.Get(pathSessions, d.sessions)
	r.Get(pathSession, d.session)
	var ctxReport, workloads, cost, latency, performance, classification http.HandlerFunc
	if rd != nil {
		ctxReport, workloads = obsapi.Report(rd.Context, true), obsapi.Report(rd.Workloads, true)
		cost, latency = obsapi.Report(rd.Cost, true), obsapi.Report(rd.Latency, true)
		performance, classification = obsapi.Report(rd.Performance, true), obsapi.Report(rd.Classification, true)
	} else {
		ctxReport = obsapi.Report[eeevidence.ContextReport](nil, false)
		workloads = obsapi.Report[eeevidence.WorkloadReport](nil, false)
		cost = obsapi.Report[eeevidence.CostReport](nil, false)
		latency = obsapi.Report[eeevidence.LatencyReport](nil, false)
		performance = obsapi.Report[eeevidence.PerformanceReport](nil, false)
		classification = obsapi.Report[eeevidence.ClassificationReport](nil, false)
	}
	r.Get(pathContext, ctxReport)
	r.Get(pathWorkloads, workloads)
	r.Get(pathCost, cost)
	r.Get(pathLatency, latency)
	r.Get(pathPerformance, performance)
	r.Get(pathClassification, classification)
}

func (d deps) list(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	s, ok := obsapi.ScopeFrom(w, r, d.Reader != nil)
	if !ok {
		return
	}
	win, err := obsapi.ParseWindow(r, time.Now())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	pg, msg := paging(r)
	if msg != "" {
		render.Error(w, http.StatusBadRequest, "invalid_argument", msg, reqID)
		return
	}
	q := eeevidence.TraceQuery{Window: win, Limit: pg.limit, Cursor: pg.cursor, Sort: pg.sort, Asc: pg.asc, Status: r.URL.Query().Get("status"), Search: r.URL.Query().Get("search")}
	page, err := d.Reader.List(r.Context(), s, q)
	if err != nil {
		obsapi.WriteErr(w, err, reqID)
		return
	}
	render.JSON(w, http.StatusOK, page)
}

type pageParams struct {
	limit  int
	cursor int64
	sort   string
	asc    bool
}

// paging reads limit, cursor, sort=<column> and dir=asc|desc (default newest
// first). The column itself is checked against the reader's allow-list.
func paging(r *http.Request) (pageParams, string) {
	p := pageParams{limit: defaultLimit, sort: r.URL.Query().Get("sort")}
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		if p.limit, err = strconv.Atoi(v); err != nil {
			return p, "limit must be an integer"
		}
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		if p.cursor, err = strconv.ParseInt(v, 10, 64); err != nil || p.cursor < 0 {
			return p, "cursor must be a non-negative integer"
		}
	}
	switch r.URL.Query().Get("dir") {
	case "", "desc":
	case "asc":
		p.asc = true
	default:
		return p, "dir must be asc or desc"
	}
	return p, ""
}

func (d deps) sessions(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	s, ok := obsapi.ScopeFrom(w, r, d.Reader != nil)
	if !ok {
		return
	}
	win, err := obsapi.ParseWindow(r, time.Now())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	pg, msg := paging(r)
	if msg != "" {
		render.Error(w, http.StatusBadRequest, "invalid_argument", msg, reqID)
		return
	}
	page, err := d.Reader.Sessions(r.Context(), s, eeevidence.SessionQuery{Window: win, Limit: pg.limit, Cursor: pg.cursor, Sort: pg.sort, Asc: pg.asc})
	if err != nil {
		obsapi.WriteErr(w, err, reqID)
		return
	}
	render.JSON(w, http.StatusOK, page)
}

func (d deps) session(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	s, ok := obsapi.ScopeFrom(w, r, d.Reader != nil)
	if !ok {
		return
	}
	win, err := obsapi.ParseWindow(r, time.Now())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	out, err := d.Reader.Session(r.Context(), s, chi.URLParam(r, "session_id"), r.URL.Query().Get("project"), win)
	if err != nil {
		obsapi.WriteErr(w, err, reqID)
		return
	}
	render.JSON(w, http.StatusOK, out)
}

func (d deps) detail(w http.ResponseWriter, r *http.Request) {
	reqID := middleware.RequestIDFromCtx(r.Context())
	s, ok := obsapi.ScopeFrom(w, r, d.Reader != nil)
	if !ok {
		return
	}
	win, err := obsapi.ParseWindow(r, time.Now())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	out, err := d.Reader.Detail(r.Context(), s, chi.URLParam(r, "trace_id"), r.URL.Query().Get("project"), win)
	if err != nil {
		obsapi.WriteErr(w, err, reqID)
		return
	}
	render.JSON(w, http.StatusOK, out)
}
