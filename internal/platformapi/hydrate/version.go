// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
	"github.com/ackstorm/ach/internal/platformapi/store"
	"github.com/ackstorm/ach/internal/refparse"
)

// VersionResponse is the body of POST /platform/hydrate/version: the items a
// hydrate writes, the revision each one serves, and a fingerprint of them all.
// A change to any item or revision changes Version.
type VersionResponse struct {
	Environment string        `json:"environment"`
	Version     string        `json:"version"`
	Items       []VersionItem `json:"items"`
}

// VersionItem is one item a hydrate writes. Rev is empty for runtime items,
// which carry no content.
type VersionItem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Rev  string `json:"rev,omitempty"`
}

// VersionHandler serves POST /platform/hydrate/version with the hydrate
// endpoint's caller rules.
func VersionHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromCtx(ctx)
		var req HydrateRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			render.Error(w, http.StatusBadRequest, "invalid_argument",
				"request body must be valid JSON with no unknown fields", reqID)
			return
		}
		env, envName, ok := resolveEnvironment(w, r, deps, req)
		if !ok {
			return
		}
		revs, err := deps.Store.ContentRevs(ctx)
		if err != nil {
			render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError,
				"failed to read content revisions", reqID)
			return
		}
		items := versionItems(env, revs)
		render.JSON(w, http.StatusOK, VersionResponse{Environment: envName, Version: versionOf(items), Items: items})
	}
}

// versionItems lists the Environment's items sorted by kind, then name.
func versionItems(row *db.EnvironmentRow, revs store.Revs) []VersionItem {
	var out []VersionItem
	add := func(kind string, names []string, rev func(string) string) {
		for _, n := range names {
			out = append(out, VersionItem{Kind: kind, Name: n, Rev: rev(n)})
		}
	}
	none := func(string) string { return "" }
	// ponytail: a marketplace item takes its marketplace's revision, so it reads
	// as updated whenever the marketplace moves; per-item revisions in
	// marketplace_plugins if that noise matters.
	scoped := func(own, mkts map[string]string) func(string) string {
		return func(ref string) string {
			if _, mkt, ok := refparse.Parse(ref); ok {
				return mkts[mkt]
			}
			return own[ref]
		}
	}
	add("prompt", row.ContextPrompts, func(n string) string { return revs.Prompts[n] })
	add("artifact", row.ContextArtifacts, func(n string) string { return revs.Artifacts[n] })
	add("plugin", row.ContextPlugins, scoped(revs.Plugins, revs.PluginMarketplaces))
	add("skill", row.ContextSkills, scoped(revs.Skills, revs.SkillMarketplaces))
	add("model", row.RuntimeModels, none)
	add("mcp", row.RuntimeMCPServers, none)
	add("a2a", row.RuntimeA2AAgents, none)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// versionOf fingerprints sorted items.
func versionOf(items []VersionItem) string {
	h := sha256.New()
	for _, it := range items {
		_, _ = io.WriteString(h, it.Kind+"\x00"+it.Name+"\x00"+it.Rev+"\n")
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
