// SPDX-License-Identifier: Apache-2.0

// handler.go ships the Content Service entrypoint per Plan 05-05 D-16:
// the Deps struct, RegisterRoutes, and the per-kind serve()
// orchestrator. The §15.6 7-gate pipeline lives in pipeline.go; the
// kind-specific gate functions live in authz.go; the §15.5 envelope
// writer + audit emitter live in errors.go; the sendfile-backed body
// writer lives in stream.go. This file glues them together.
//
// Routing topology (chi):
//
//	GET /healthz
//	GET /content/prompt/{name}
//	GET /content/plugin/{name}
//	GET /content/artifact/{name}
//	GET /content/skill/{name}
//
// /metrics is NOT registered here — Plan 05-06 wires the
// promhttp.Handler at the cmd-level server setup so the metrics surface
// stays symmetric with Phase 3's platform-api (which also mounts
// metrics at the server level, not inside the application router).
//
// Routes are explicit per kind (not via {kind} URL param) so chi can
// return 404 for /content/marketplace/<name> without the handler body
// ever running. The kind-allowlist stays at the routing layer.

package contentservice

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/featuregate"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// Deps bundles the handler's runtime collaborators per Plan 05-05 D-16.
//
// Required fields (request-path collaborators):
//   - CacheRoot          — absolute path of the shared PVC where the
//     Operator rename(2)s cache files.
//   - Namespace          — the k8s namespace the Content Service serves
//     (typically "ach-system"). Read on every
//     envcache + db.* call.
//   - Pool               — pgxpool.Pool for projection-row reads.
//   - EnvCache           — in-memory envcache.Cache snapshot, refreshed via
//     LISTEN/NOTIFY (replaces the prior Redis cache).
//   - Resolver           — keystore.Resolver (Phase 3 D-08 reused).
//   - Teams              — keystore.TeamsResolver (Phase 4 D-17 reused).
//   - Metrics            — *metrics.ContentServiceCollectors (Plan 05-01).
//   - LiteLLMUnreachable — shared ach_litellm_unreachable_total CounterVec
//     (Plan 05-01 / OBS-05). Incremented with
//     caller="content_service" on transport
//     failures inside enforceTeams.
//   - AuditLog           — *slog.Logger built by audit.NewLogger
//     (audit=true predicate already attached).
//
// Optional fields (defaulted in RegisterRoutes):
//   - Logger             — operational logger. Defaults to slog.Default().
//
// RegisterRoutes does NOT nil-guard the required fields — a request
// that lands on a nil-Pool / nil-Resolver Deps panics at request time,
// surfacing the wiring bug loudly rather than silently 500-ing every
// request. The stub-patched cmd/ach/cmd/content_service.go currently
// passes a partial Deps (CacheRoot + Namespace + Logger only) so the
// build stays green between Plan 05-05 and Plan 05-06; that
// configuration is NOT runtime-safe for content requests.
type Deps struct {
	CacheRoot          string
	Namespace          string
	Pool               *pgxpool.Pool
	EnvCache           *envcache.Cache
	Resolver           keystore.Resolver
	Teams              keystore.TeamsResolver
	Metrics            *metrics.ContentServiceCollectors
	LiteLLMUnreachable *prometheus.CounterVec
	AuditLog           *slog.Logger
	Logger             *slog.Logger
}

// RegisterRoutes wires Content Service routes onto r. The router MUST
// NOT have a Compress middleware registered — the body is identity-
// transfer per CS-06 / D-01 (no chunked encoding, no compression).
// Plan 05-06 wires the chi.NewRouter() instance and is the canonical
// site for that invariant.
func RegisterRoutes(r chi.Router, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/content/prompt/{name}", d.serve(kindPrompt))
	if featuregate.PluginsEnabled {
		r.Get("/content/plugin/{name}", d.serve(kindPlugin))
	}
	r.Get("/content/artifact/{name}", d.serve(kindArtifact))
	r.Get("/content/skill/{name}", d.serve(kindSkill))
}

// serve returns the http.HandlerFunc for one kind. It orchestrates the
// §15.6 7-gate pipeline (pipeline.go) and the body-write step.
//
// On a denial (any gate returns *errResp), serve calls writeError
// (errors.go) which renders the §15.5 envelope, emits one audit event,
// and increments the request-counter. The duration histogram is
// observed AFTER writeError so denials are still measured.
//
// On success, serve calls stream (stream.go) to write the body via
// io.Copy → sendfile(2). After io.Copy returns, serve emits the
// success-path audit event with Outcome=forwarded, increments the
// success metric, adds bytes-served, and records the duration.
func (d Deps) serve(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		name := chi.URLParam(r, "name")

		row, errR := pipeline(ctx, d, kind, r)
		if errR != nil {
			// pipeline.go MAY return a row even on error if file open
			// succeeded but a later check failed — but the current
			// pipeline returns (nil, errR) on every denial. Defensive
			// close anyway.
			if row != nil && row.File != nil {
				_ = row.File.Close()
			}
			d.writeError(w, r, kind, name, errR.keyInfo, errR.errResp)
			if d.Metrics != nil {
				d.Metrics.ObserveRequestDuration(kind, time.Since(start).Seconds())
			}
			return
		}
		// Success: stream the body, close the file, emit success audit.
		defer func() { _ = row.File.Close() }()
		n, copyErr := stream(w, r, row.File, row.ContentType, row.Size)
		if copyErr != nil {
			// Body already flushed (WriteHeader 200 ran inside stream).
			// We CANNOT switch to an error envelope at this point —
			// just log + emit the audit event with the partial-write
			// indication. The wire response is whatever bytes made it.
			d.Logger.Warn("content stream interrupted",
				"kind", kind, "name", name, "bytes_written", n, "err", copyErr)
		}
		d.emitAudit(ctx, kind, name, audit.OutcomeForwarded, row.KeyInfo)
		if d.Metrics != nil {
			d.Metrics.IncRequest(kind, audit.OutcomeForwarded)
			d.Metrics.AddBytesServed(kind, n)
			d.Metrics.ObserveRequestDuration(kind, time.Since(start).Seconds())
		}
	}
}

// emitAudit emits a content.get audit event with the shared field set.
// Both the forwarded-success path (serve) and the denial path
// (writeError) call it so an audit-schema change is single-site. info
// may be nil on gate-1 denials (actorFromInfo/keyIDFromInfo nil-tolerant).
func (d Deps) emitAudit(ctx context.Context, kind, name, outcome string, info *keystore.KeyInfo) {
	if d.AuditLog == nil {
		return
	}
	env := ""
	if info != nil {
		env = info.Environment
	}
	audit.EmitAudit(ctx, d.AuditLog, audit.Event{
		Action:      audit.ActionContentGet,
		Outcome:     outcome,
		Actor:       actorFromInfo(info),
		RequestID:   middleware.RequestIDFromCtx(ctx),
		KeyID:       keyIDFromInfo(info),
		Target:      &audit.Target{Kind: kind, Name: name},
		Environment: env, // G20: the key-bound Environment (governance dim)
	})
}
