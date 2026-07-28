// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

const (
	apiVersion = "ach.ackstorm.ai/v1alpha1"
	kindName   = "Environment"
)

// manifest is the GET/POST/PATCH body shape — a trimmed Kubernetes-style
// manifest carrying only apiVersion/kind/metadata/spec (no status). It is what
// the UI authors and what the read endpoints return.
type manifest struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace,omitempty"`
	} `json:"metadata"`
	Spec v1alpha1.EnvironmentSpec `json:"spec"`
}

// specToRow maps an EnvironmentSpec to the db.EnvironmentRow the UI write
// helpers consume. Conditions/ResourceVersion are left zero — the DB layer pins
// those (origin='ui', conditions NULL) for a draft row.
func specToRow(ns, name string, spec v1alpha1.EnvironmentSpec) db.EnvironmentRow {
	return db.EnvironmentRow{
		Namespace:         ns,
		Name:              name,
		AuthorizedTeams:   spec.AuthorizedTeams,
		ContextPrompts:    spec.Context.Prompts,
		ContextPlugins:    spec.Context.Plugins,
		ContextArtifacts:  spec.Context.Artifacts,
		ContextSkills:     spec.Context.Skills,
		RuntimeModels:     spec.Runtime.Models,
		RuntimeMCPServers: spec.Runtime.MCPServers,
		RuntimeA2AAgents:  spec.Runtime.A2AAgents,
		RuntimeGuardrails: spec.Runtime.Guardrails,
		Notice:            spec.Notice,
		Description:       spec.Description,
	}
}

// rowToSpec is the inverse of specToRow: it projects the spec-bearing columns of
// a row back into an EnvironmentSpec (status/condition columns are dropped).
func rowToSpec(row db.EnvironmentRow) v1alpha1.EnvironmentSpec {
	return v1alpha1.EnvironmentSpec{
		Runtime: v1alpha1.RuntimeBlock{
			Models:     row.RuntimeModels,
			MCPServers: row.RuntimeMCPServers,
			A2AAgents:  row.RuntimeA2AAgents,
			Guardrails: row.RuntimeGuardrails,
		},
		Context: v1alpha1.ContextBlock{
			Prompts:   row.ContextPrompts,
			Plugins:   row.ContextPlugins,
			Artifacts: row.ContextArtifacts,
			Skills:    row.ContextSkills,
		},
		AuthorizedTeams: row.AuthorizedTeams,
		Notice:          row.Notice,
		Description:     row.Description,
	}
}

// rowToManifest builds the read-side manifest envelope for a row.
func rowToManifest(ns string, row db.EnvironmentRow) manifest {
	m := manifest{APIVersion: apiVersion, Kind: kindName, Spec: rowToSpec(row)}
	m.Metadata.Name = row.Name
	m.Metadata.Namespace = ns
	return m
}

// kindGuard validates the {kind} path segment, writing a 404 and returning false
// when it is not a UI-writable kind.
func kindGuard(w http.ResponseWriter, r *http.Request, reqID string) bool {
	if !validKind(chi.URLParam(r, "kind")) {
		render.Error(w, http.StatusNotFound, "not_found", "unknown or non-UI-writable kind", reqID)
		return false
	}
	return true
}

// writesDisabled emits 403 ui_writes_disabled when the deployment has UI writes
// turned off; the write handlers call it first.
func writesDisabled(w http.ResponseWriter, deps Deps, reqID string) bool {
	if deps.DisableUIWrites {
		render.Error(w, http.StatusForbidden, "ui_writes_disabled", "UI writes are disabled on this deployment", reqID)
		return true
	}
	return false
}

// listHandler returns GET /{kind} — every UI-readable row as a manifest,
// paginated via ?limit + ?cursor (opaque base64-of-decimal offset).
func listHandler(deps Deps, s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if !kindGuard(w, r, reqID) {
			return
		}

		limit, offset, ok := render.PageParams(w, r, reqID)
		if !ok {
			return
		}

		rows, err := s.List(ctx)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, "internal_error", "failed to list objects", reqID)
			return
		}

		page, nextCursor := render.PageWindow(rows, offset, limit)
		items := make([]manifest, 0, len(page))
		for _, row := range page {
			items = append(items, rowToManifest(deps.Namespace, row))
		}

		render.JSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": nextCursor,
		})
	}
}

// getHandler returns GET /{kind}/{name} — a single object as a manifest, 404
// when absent.
func getHandler(deps Deps, s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if !kindGuard(w, r, reqID) {
			return
		}
		name := chi.URLParam(r, "name")

		row, err := s.Get(ctx, name)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, "internal_error", "failed to read object", reqID)
			return
		}
		if row == nil {
			render.Error(w, http.StatusNotFound, "not_found", "object not found", reqID)
			return
		}
		render.JSON(w, http.StatusOK, rowToManifest(deps.Namespace, *row))
	}
}

// createHandler handles POST /{kind} — decode the manifest, insert a UI-owned
// draft row, return 201 with the created manifest.
func createHandler(deps Deps, s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if !kindGuard(w, r, reqID) {
			return
		}
		if writesDisabled(w, deps, reqID) {
			return
		}

		var m manifest
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			render.Error(w, http.StatusBadRequest, "invalid_argument", "request body is not valid JSON", reqID)
			return
		}
		if m.Metadata.Name == "" {
			render.Error(w, http.StatusBadRequest, "invalid_argument", "metadata.name is required", reqID)
			return
		}

		row := specToRow(deps.Namespace, m.Metadata.Name, m.Spec)
		if err := s.Insert(ctx, row); err != nil {
			switch {
			case errors.Is(err, db.ErrConflictWithCR):
				render.Error(w, http.StatusConflict, "conflict_with_kubernetes_object",
					"a Kubernetes-owned object with that name already exists", reqID)
			case errors.Is(err, db.ErrUIAlreadyExists):
				render.Error(w, http.StatusConflict, "already_exists",
					"a UI-managed object with that name already exists; use PATCH", reqID)
			default:
				render.Error(w, http.StatusInternalServerError, "internal_error", "failed to create object", reqID)
			}
			return
		}
		render.JSON(w, http.StatusCreated, rowToManifest(deps.Namespace, row))
	}
}

// patchBody is the PATCH wrapper used to detect whether the caller sent a full
// manifest (with a top-level "spec") or a bare spec patch. When Spec is present
// it is the merge patch; otherwise the whole body is treated as the spec patch.
type patchBody struct {
	Spec json.RawMessage `json:"spec"`
}

// patchHandler handles PATCH /{kind}/{name} — an RFC7386-style JSON merge of the
// supplied spec fields onto the existing spec. The merge is achieved by
// unmarshalling the patch bytes ON TOP OF the existing spec struct: fields
// present in the patch overwrite, fields absent are preserved (Go's json package
// leaves untouched struct fields as-is). The body may be either a full manifest
// ({"spec": {...}}) or a bare spec object ({...}); patchBody disambiguates.
func patchHandler(deps Deps, s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if !kindGuard(w, r, reqID) {
			return
		}
		if writesDisabled(w, deps, reqID) {
			return
		}
		name := chi.URLParam(r, "name")

		row, err := s.Get(ctx, name)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, "internal_error", "failed to read object", reqID)
			return
		}
		if row == nil {
			render.Error(w, http.StatusNotFound, "not_found", "object not found", reqID)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			render.Error(w, http.StatusBadRequest, "invalid_argument", "failed to read request body", reqID)
			return
		}

		// Pick the merge patch: a top-level "spec" if present, else the whole body.
		patch := body
		var pb patchBody
		if json.Unmarshal(body, &pb) == nil && len(pb.Spec) > 0 {
			patch = pb.Spec
		}

		// RFC7386-ish merge: overlay the patch onto the existing spec.
		merged := rowToSpec(*row)
		if err := json.Unmarshal(patch, &merged); err != nil {
			render.Error(w, http.StatusBadRequest, "invalid_argument", "patch body is not valid JSON", reqID)
			return
		}

		updated := specToRow(deps.Namespace, name, merged)
		if err := s.Update(ctx, updated); err != nil {
			switch {
			case errors.Is(err, db.ErrImmutableViaUI):
				render.Error(w, http.StatusForbidden, "immutable_via_ui",
					"object is operator-owned and cannot be modified via the UI", reqID)
			case errors.Is(err, db.ErrUINotFound):
				render.Error(w, http.StatusNotFound, "not_found", "object not found", reqID)
			default:
				render.Error(w, http.StatusInternalServerError, "internal_error", "failed to update object", reqID)
			}
			return
		}
		render.JSON(w, http.StatusOK, rowToManifest(deps.Namespace, updated))
	}
}

// deleteHandler handles DELETE /{kind}/{name} — removes a UI-owned row, 204 on
// success.
func deleteHandler(deps Deps, s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		if !kindGuard(w, r, reqID) {
			return
		}
		if writesDisabled(w, deps, reqID) {
			return
		}
		name := chi.URLParam(r, "name")

		if err := s.Delete(ctx, name); err != nil {
			switch {
			case errors.Is(err, db.ErrImmutableViaUI):
				render.Error(w, http.StatusForbidden, "immutable_via_ui",
					"object is operator-owned and cannot be deleted via the UI", reqID)
			case errors.Is(err, db.ErrUINotFound):
				render.Error(w, http.StatusNotFound, "not_found", "object not found", reqID)
			default:
				render.Error(w, http.StatusInternalServerError, "internal_error", "failed to delete object", reqID)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
