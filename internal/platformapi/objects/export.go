// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"sigs.k8s.io/yaml"

	"github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// exportManifest is the CANONICAL GitOps manifest for YAML export: only
// apiVersion/kind/metadata/spec — NO status, conditions, uid, or
// resourceVersion. It is what `kubectl apply` round-trips back into the operator
// (the operator then takes the row over, origin='ui' → 'cr').
type exportManifest struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec v1alpha1.EnvironmentSpec `json:"spec"`
}

// ExportEnvironmentYAML renders a row as a canonical Environment manifest YAML.
// It is exported (and HTTP-independent) so it can be unit-tested directly and
// reused by any future GitOps tooling. The condition/status columns on the row
// are intentionally never read — the export carries the desired spec only.
func ExportEnvironmentYAML(row db.EnvironmentRow, namespace string) ([]byte, error) {
	em := exportManifest{APIVersion: apiVersion, Kind: kindName, Spec: rowToSpec(row)}
	em.Metadata.Name = row.Name
	em.Metadata.Namespace = namespace
	return yaml.Marshal(em)
}

// exportHandler returns GET /{kind}/{name}/yaml — the canonical manifest as
// application/yaml, 404 when the object is absent.
func exportHandler(deps Deps, s Store) http.HandlerFunc {
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

		out, err := ExportEnvironmentYAML(*row, deps.Namespace)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, "internal_error", "failed to render object YAML", reqID)
			return
		}

		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	}
}
