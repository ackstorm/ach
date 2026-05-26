// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/ackstorm/ach/internal/platformapi/store"
	achteams "github.com/ackstorm/ach/internal/platformapi/teams"
)

// SchemaVersion is the wire-format version emitted in the hydrate response
// (Hub §15.2 / API-04 / D-17). Phase 6 + 7 CLI binds to this literal.
const SchemaVersion = "v1alpha1"

// Deps is the dependency bag the chi server (Plan 03-11) constructs and
// hands to HydrateHandler via Mount.
//
//   - Store      — informer-backed Environment reader (Plan 03-06).
//   - LiteLLM    — REST client; BLK-03 contract. Used by teams.LookupCallerTeams
//     for the §8.2 step-4 team-intersection check on pk_ callers.
//     ek_ callers do NOT trigger a team lookup (binding decides).
//   - BaseURL    — ACH_BASE_URL; used to construct runtime endpoint and
//     context downloadUrl values. MUST be the public ACH base
//     URL (Phase 3 deployment-level config).
//   - Allowlist  — admin allowlist (retained for parity with other Deps;
//     this handler does NOT consult it, by design — hydrate
//     is not an admin endpoint).
//   - Audit      — slog logger (audit.NewLogger result).
//   - Namespace  — deployment watch namespace (retained for symmetry; Store
//     is already namespace-scoped at construction).
type Deps struct {
	Store     *store.Store
	LiteLLM   litellm.Client
	BaseURL   string
	Allowlist map[string]struct{}
	Audit     *slog.Logger
	Namespace string
}

// HydrateRequest is the strict JSON shape POST /platform/hydrate accepts.
// json.Decoder.DisallowUnknownFields() rejects any extra fields with 400
// invalid_argument (D-16).
type HydrateRequest struct {
	Environment string `json:"environment"`
}

// RuntimeItem is one entry in the runtime block (Hub §15.2 / D-17).
// `id` is the resource name (DNS-1123 — stable across reconciles).
// `endpoint` points at the ACH Forwarder ingress for the kind (Phase 4
// extends; Phase 3 freezes the §15.2 contract).
type RuntimeItem struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
}

// ContextItem is one entry in the context block (Hub §15.2 / D-17).
// `name` is the CRD metadata.name (the canonical reference name).
// `id` is the resource name (NOT CRD UID — names are stable across
// reconciles; UIDs change on delete+recreate). Phase 6 CLI binds on the
// name field for diff/sync; the id field exists for forward-compat with
// a future Phase that may carry an opaque object identifier here.
// `downloadUrl` is the §15.6 Content Service URL the CLI fetches from.
type ContextItem struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	DownloadURL string `json:"downloadUrl"`
}

// RuntimeBlock is the {models, mcpServers, a2aAgents} envelope; every slice
// is ALWAYS present and serialized as `[]` when empty per API-04.
type RuntimeBlock struct {
	Models     []RuntimeItem `json:"models"`
	MCPServers []RuntimeItem `json:"mcpServers"`
	A2AAgents  []RuntimeItem `json:"a2aAgents"`
}

// ContextBlock is the {prompts, plugins, artifacts} envelope; every slice
// is ALWAYS present and serialized as `[]` when empty per API-04.
type ContextBlock struct {
	Prompts   []ContextItem `json:"prompts"`
	Plugins   []ContextItem `json:"plugins"`
	Artifacts []ContextItem `json:"artifacts"`
}

// HydrateResponse is the §15.2 manifest shape Phase 6 + 7 CLI binds to.
// Field tags are intentionally explicit so the JSON wire matches the spec
// byte-for-byte (no struct-tag drift through future refactors).
type HydrateResponse struct {
	SchemaVersion string       `json:"schemaVersion"`
	Environment   string       `json:"environment"`
	Runtime       RuntimeBlock `json:"runtime"`
	Context       ContextBlock `json:"context"`
}

// emptyRuntime returns a RuntimeBlock with all three slices initialized to
// zero-length non-nil. Used to enforce the API-04 invariant that the
// response carries `[]` (not `null`) when the underlying Environment has
// no entries in the corresponding sub-block.
func emptyRuntime() RuntimeBlock {
	return RuntimeBlock{
		Models:     []RuntimeItem{},
		MCPServers: []RuntimeItem{},
		A2AAgents:  []RuntimeItem{},
	}
}

// emptyContext is the context-block counterpart of emptyRuntime — same
// invariant, same shape.
func emptyContext() ContextBlock {
	return ContextBlock{
		Prompts:   []ContextItem{},
		Plugins:   []ContextItem{},
		Artifacts: []ContextItem{},
	}
}

// HydrateHandler returns POST /platform/hydrate.
//
// Error matrix (Hub §15.1):
//
//   - 400 invalid_argument          — body has unknown fields / non-JSON
//   - 400 missing_environment       — pk_ caller, body.environment is empty
//   - 401 invalid_key_type          — caller is neither pk_ nor ek_ (defensive)
//   - 403 wrong_environment         — ek_ caller, body.environment != keyCtx.Environment
//   - 403 unauthorized_team         — pk_ caller, no intersection with env.AuthorizedTeams
//   - 404 environment_not_found     — env not in informer cache
//   - 503 litellm_unreachable       — teams.LookupCallerTeams transport error
//   - 500 internal_error            — Store read failure
//   - 200 OK                         — HydrateResponse
func HydrateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		actor := middleware.ActorFromCtx(ctx)

		keyCtx, ok := middleware.KeyContextFromCtx(ctx)
		if !ok {
			// Authn middleware must have run.
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "auth context missing", reqID)
			return
		}

		// Strict JSON decode (D-16). Empty body is io.EOF — treat as zero-
		// value HydrateRequest{}.
		var req HydrateRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"request body must be valid JSON with no unknown fields", reqID)
			return
		}

		// Resolve target env name based on caller type (Hub §15.1 / D-16).
		var envName string
		switch keyCtx.KeyType {
		case keys.PrefixPk:
			if req.Environment == "" {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    audit.ActionHydrate,
						Outcome:   audit.OutcomeMissingEnvironment,
						Actor:     actor,
						RequestID: reqID,
						KeyID:     keyCtx.KeyID,
					})
				}
				render.Error(w, http.StatusBadRequest, audit.OutcomeMissingEnvironment,
					"environment is required for pk_ callers", reqID)
				return
			}
			envName = req.Environment
		case keys.PrefixEk:
			if req.Environment != "" && req.Environment != keyCtx.Environment {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    audit.ActionHydrate,
						Outcome:   audit.OutcomeWrongEnvironment,
						Actor:     actor,
						RequestID: reqID,
						KeyID:     keyCtx.KeyID,
						Target:    &audit.Target{Kind: "environment", Name: req.Environment},
					})
				}
				render.Error(w, http.StatusForbidden, audit.OutcomeWrongEnvironment,
					"ek_ caller may only hydrate its bound Environment", reqID)
				return
			}
			envName = keyCtx.Environment
		default:
			render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType,
				"hydrate requires pk_ or ek_", reqID)
			return
		}

		env, err := deps.Store.GetEnvironment(ctx, envName)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read environment", reqID)
			return
		}
		if env == nil {
			render.Error(w, http.StatusNotFound, audit.OutcomeEnvironmentNotFound,
				"environment not found", reqID)
			return
		}

		// pk_ team-intersection check (Hub §15.1 step 4). ek_ skips — the
		// binding already restricted the env (no live re-auth per §8.1).
		// Admin pk_ callers ALSO skip — admins see every Environment.
		if keyCtx.KeyType == keys.PrefixPk && !keyCtx.IsAdmin {
			teams, err := achteams.LookupCallerTeams(ctx, deps.LiteLLM, keyCtx.OwnerEmail)
			if err != nil {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    audit.ActionHydrate,
						Outcome:   audit.OutcomeLitellmUnreachable,
						Actor:     actor,
						RequestID: reqID,
						KeyID:     keyCtx.KeyID,
						Target:    &audit.Target{Kind: "environment", Name: envName},
					})
				}
				render.Error(w, http.StatusServiceUnavailable, audit.OutcomeLitellmUnreachable,
					"upstream LiteLLM unreachable", reqID)
				return
			}
			if !hasIntersect(env.Spec.AuthorizedTeams, teams) {
				if deps.Audit != nil {
					audit.EmitAudit(ctx, deps.Audit, audit.Event{
						Action:    audit.ActionHydrate,
						Outcome:   audit.OutcomeUnauthorizedTeam,
						Actor:     actor,
						RequestID: reqID,
						KeyID:     keyCtx.KeyID,
						Target:    &audit.Target{Kind: "environment", Name: envName},
					})
				}
				render.Error(w, http.StatusForbidden, audit.OutcomeUnauthorizedTeam,
					"caller is not a member of any authorized team", reqID)
				return
			}
		}

		// Build response. The endpoint shapes referenced verbatim by
		// WARN-02 (Phase 3 frozen) are:
		//
		//   deps.BaseURL + "/v1"            for models (all share one endpoint)
		//   deps.BaseURL + "/mcp/"  + name  for mcpServers
		//   deps.BaseURL + "/a2a/"  + name  for a2aAgents
		//   deps.BaseURL + "/content/" + kind + "/" + name  for context items
		//
		// These literals also appear in toRuntimeBlock / toContextBlock
		// below; the comment serves as the locator for the WARN-02
		// commit.
		resp := HydrateResponse{
			SchemaVersion: SchemaVersion,
			Environment:   envName,
			Runtime:       toRuntimeBlock(env.Spec.Runtime, deps.BaseURL),
			Context:       toContextBlock(env.Spec.Context, deps.BaseURL),
		}

		// Audit success — ActionHydrate + OutcomeCreated (created is the
		// success outcome shared with other write paths; hydrate is read-
		// only but emits a single audit per call so the request_id +
		// target.name pair is grep-able).
		if deps.Audit != nil {
			audit.EmitAudit(ctx, deps.Audit, audit.Event{
				Action:    audit.ActionHydrate,
				Outcome:   audit.OutcomeCreated,
				Actor:     actor,
				RequestID: reqID,
				KeyID:     keyCtx.KeyID,
				Target:    &audit.Target{Kind: "environment", Name: envName},
			})
		}

		render.JSON(w, http.StatusOK, resp)
	}
}

// toRuntimeBlock maps the CRD's RuntimeBlock (a slice-of-names per kind)
// to the §15.2 response shape (slice of {id, endpoint} per kind).
// Endpoint shapes are the WARN-02 commit (Phase 3 frozen):
//
//   - models     -> ${BaseURL}/v1               (all models share one endpoint)
//   - mcpServers -> ${BaseURL}/mcp/<name>
//   - a2aAgents  -> ${BaseURL}/a2a/<name>
//
// Phase 4 Forwarder may extend the prefix scheme; Phase 3 freezes these.
func toRuntimeBlock(in achv1alpha1.RuntimeBlock, baseURL string) RuntimeBlock {
	out := emptyRuntime()
	for _, name := range in.Models {
		out.Models = append(out.Models, RuntimeItem{
			ID:       name,
			Endpoint: baseURL + "/v1",
		})
	}
	for _, name := range in.MCPServers {
		out.MCPServers = append(out.MCPServers, RuntimeItem{
			ID:       name,
			Endpoint: baseURL + "/mcp/" + name,
		})
	}
	for _, name := range in.A2AAgents {
		out.A2AAgents = append(out.A2AAgents, RuntimeItem{
			ID:       name,
			Endpoint: baseURL + "/a2a/" + name,
		})
	}
	return out
}

// toContextBlock maps the CRD's ContextBlock (a slice-of-names per kind)
// to the §15.2 response shape (slice of {name, id, downloadUrl} per kind).
// The downloadUrl is the §15.6 Content Service URL; kind in the path is
// the singular form (prompt|plugin|artifact) — Phase 5 binds against this.
func toContextBlock(in achv1alpha1.ContextBlock, baseURL string) ContextBlock {
	out := emptyContext()
	// Strict literal "/content/" + kind + "/" + name keeps the Phase 5
	// Content Service URL construction grep-able from a single place and
	// makes the §15.6 contract obvious to future readers.
	const contentPrefix = "/content/"
	for _, name := range in.Prompts {
		out.Prompts = append(out.Prompts, ContextItem{
			Name:        name,
			ID:          name,
			DownloadURL: baseURL + contentPrefix + "prompt/" + name,
		})
	}
	for _, name := range in.Plugins {
		out.Plugins = append(out.Plugins, ContextItem{
			Name:        name,
			ID:          name,
			DownloadURL: baseURL + contentPrefix + "plugin/" + name,
		})
	}
	for _, name := range in.Artifacts {
		out.Artifacts = append(out.Artifacts, ContextItem{
			Name:        name,
			ID:          name,
			DownloadURL: baseURL + contentPrefix + "artifact/" + name,
		})
	}
	return out
}

// Mount returns a chi.Router subtree wiring the hydrate endpoint. The
// caller mounts the subtree at "/platform/hydrate" inside the authenticated
// chi.Group (Plan 03-11's server.go composes this).
func Mount(deps Deps) func(chi.Router) {
	return func(r chi.Router) {
		r.Post("/", HydrateHandler(deps))
	}
}

// hasIntersect reports whether the two slices share at least one element.
// Mirrors the helper in environments.handler.go; duplicated rather than
// shared to keep package boundaries clean (each handler package is
// self-contained beyond the explicit Deps + render + audit imports).
func hasIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}
