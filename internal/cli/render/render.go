// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/ackstorm/ach/internal/cli/config"
)

// EnvView is the per-row shape rendered by FormatEnvList +
// FormatEnvDescribe. Matches the per-row projection of the server's
// /platform/environments handler (internal/platformapi/store.EnvironmentView's
// caller-visible subset: name, namespace, status). We intentionally
// define a lean local copy so this package does NOT pull
// internal/platformapi (which transitively imports k8s.io/* + chi).
// json tags are present so callers can decode the wire payload
// directly into []EnvView when convenient.
type EnvView struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
}

// EkRowView is the per-row shape rendered by FormatEkList — used by
// 06-05 `ach env-keys list` AND 06-08 `ach admin keys list` (W7
// hoist; avoids inline duplication). Matches the wire shape of
// internal/platformapi/envkeys.EkRowView's subset that the CLI
// surfaces to the user (key_id, owner_email, environment, name,
// created_at). json tags match the server's snake_case for trivial
// json.Decoder round-trips.
type EkRowView struct {
	KeyID       string `json:"key_id"`
	OwnerEmail  string `json:"owner_email"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
	CreatedAt   string `json:"created_at"`
}

// HydrateView is the lean local copy of the server's HydrateResponse
// that render needs to format a describe block. Defined here (NOT
// imported from internal/platformapi/hydrate) so the CLI binary does
// not pull k8s.io/* via the API types. Field tags match the
// on-the-wire JSON keys verbatim so callers decode straight into
// HydrateView without a separate mapping layer.
type HydrateView struct {
	SchemaVersion string    `json:"schemaVersion"`
	Environment   string    `json:"environment"`
	Runtime       BlockView `json:"runtime"`
	Context       BlockView `json:"context"`
}

// BlockView unifies the runtime + context sub-blocks. The wire shape
// puts {models, mcpServers, a2aAgents} under "runtime" and
// {prompts, plugins, artifacts} under "context"; a single struct
// holds both axes because BlockView's zero-value omits the irrelevant
// slices via omitempty.
type BlockView struct {
	Models     []RuntimeItem `json:"models,omitempty"`
	MCPServers []RuntimeItem `json:"mcpServers,omitempty"`
	A2AAgents  []RuntimeItem `json:"a2aAgents,omitempty"`
	Prompts    []ContextItem `json:"prompts,omitempty"`
	Plugins    []ContextItem `json:"plugins,omitempty"`
	Artifacts  []ContextItem `json:"artifacts,omitempty"`
}

// RuntimeItem is one entry under runtime.{models,mcpServers,a2aAgents}.
// Endpoint surfaces the per-runtime `endpoint` JSON key emitted by the
// server's hydrate handler — the W3 contract anchors here: every
// rendered runtime row exposes this string verbatim so the user can
// inspect runtime wiring via `ach env describe`. The Name field is a
// local convenience (the server emits {id, endpoint} only — Name is
// populated by the caller from the same source as ID when present).
type RuntimeItem struct {
	Name     string `json:"name,omitempty"`
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
}

// ContextItem is one entry under context.{prompts,plugins,artifacts}.
// DownloadURL surfaces the per-context-entry `downloadUrl` JSON key
// emitted by the server's hydrate handler — the W3 contract anchors
// here: every rendered context row exposes this string verbatim so
// the user can inspect content routing via `ach env describe`.
type ContextItem struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	DownloadURL string `json:"downloadUrl"`
}

// FormatConfigList returns the deterministic multi-line table for
// `ach config list`. Rows are sorted alphabetically by deployment
// name; the default deployment is marked with " (default)" suffix.
// The PK column is "yes"/"no" (presence flag, never plaintext); the
// EK column is the count of entries in the ek map.
func FormatConfigList(f *config.File) string {
	if f == nil || len(f.Deployments) == 0 {
		return "No deployments configured\n"
	}
	names := make([]string, 0, len(f.Deployments))
	for n := range f.Deployments {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tURL\tPK\tEK")
	for _, name := range names {
		dep := f.Deployments[name]
		display := name
		if name == f.Default {
			display = name + " (default)"
		}
		pkPresent := "no"
		if dep != nil && dep.PK != "" {
			pkPresent = "yes"
		}
		ekCount := 0
		if dep != nil {
			ekCount = len(dep.EK)
		}
		url := ""
		if dep != nil {
			url = dep.URL
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", display, url, pkPresent, ekCount)
	}
	_ = tw.Flush()
	return sb.String()
}

// FormatConfigShow returns the block for `ach config show [deployment]`.
// When reveal=false (default), the PK and every EK value pass through
// config.Mask so only the "<prefix>_****<last-4>" tail is rendered;
// when reveal=true (D-05 opt-in unmask), the values flow through
// verbatim. The unmask is scoped to ONE named deployment per
// invocation (the caller picks; this function trusts its input).
func FormatConfigShow(name string, dep *config.Deployment, reveal bool) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Deployment: %s\n", name)
	if dep == nil {
		_, _ = fmt.Fprintln(&sb, "URL: (missing)")
		return sb.String()
	}
	_, _ = fmt.Fprintf(&sb, "URL: %s\n", dep.URL)
	pk := dep.PK
	if pk != "" {
		if !reveal {
			pk = config.Mask(pk)
		}
		_, _ = fmt.Fprintf(&sb, "PK: %s\n", pk)
	} else {
		_, _ = fmt.Fprintln(&sb, "PK: (none)")
	}
	if len(dep.EK) > 0 {
		_, _ = fmt.Fprintln(&sb, "EK:")
		// Deterministic label order.
		labels := make([]string, 0, len(dep.EK))
		for label := range dep.EK {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		for _, label := range labels {
			val := dep.EK[label]
			if !reveal {
				val = config.Mask(val)
			}
			_, _ = fmt.Fprintf(&sb, "  %s: %s\n", label, val)
		}
	} else {
		_, _ = fmt.Fprintln(&sb, "EK: (none)")
	}
	return sb.String()
}

// FormatEnvList renders the per-env table for `ach env list`. Empty
// input → stable "No environments visible" stub.
func FormatEnvList(envs []EnvView) string {
	if len(envs) == 0 {
		return "No environments visible\n"
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tNAMESPACE\tSTATUS")
	for _, e := range envs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.Namespace, e.Status)
	}
	_ = tw.Flush()
	return sb.String()
}

// FormatEnvDescribe returns the full describe block for `ach env
// describe <name>`. When hydrateAvailable=false (CLI-12 graceful
// admin-403 fallback), the body prints `Runtime: (unavailable)\n
// Context: (unavailable)\n` and omits the runtime/context sub-tables.
// When hydrateAvailable=true, both sub-tables render — each Runtime
// row surfaces the item's Endpoint (W3 contract) and each Context row
// surfaces the item's DownloadURL (W3 contract).
func FormatEnvDescribe(env EnvView, h *HydrateView, hydrateAvailable bool) string {
	var sb strings.Builder
	// Header — env metadata regardless of hydrate availability.
	_, _ = fmt.Fprintf(&sb, "Environment: %s\n", env.Name)
	if env.Namespace != "" {
		_, _ = fmt.Fprintf(&sb, "Namespace: %s\n", env.Namespace)
	}
	if env.Status != "" {
		_, _ = fmt.Fprintf(&sb, "Status: %s\n", env.Status)
	}

	if !hydrateAvailable || h == nil {
		_, _ = fmt.Fprintln(&sb, "Runtime: (unavailable)")
		_, _ = fmt.Fprintln(&sb, "Context: (unavailable)")
		return sb.String()
	}

	// Runtime block (3 axes — each row surfaces the per-runtime
	// `endpoint` per W3 phase-goal sentence).
	_, _ = fmt.Fprintln(&sb, "Runtime:")
	tw := tabwriter.NewWriter(&sb, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  KIND\tNAME\tID\tENDPOINT")
	for _, m := range h.Runtime.Models {
		_, _ = fmt.Fprintf(tw, "  model\t%s\t%s\t%s\n", m.Name, m.ID, m.Endpoint)
	}
	for _, s := range h.Runtime.MCPServers {
		_, _ = fmt.Fprintf(tw, "  mcpServer\t%s\t%s\t%s\n", s.Name, s.ID, s.Endpoint)
	}
	for _, a := range h.Runtime.A2AAgents {
		_, _ = fmt.Fprintf(tw, "  a2aAgent\t%s\t%s\t%s\n", a.Name, a.ID, a.Endpoint)
	}
	_ = tw.Flush()

	// Context block (3 axes — each row surfaces the per-context-entry
	// `downloadUrl` per W3 phase-goal sentence).
	_, _ = fmt.Fprintln(&sb, "Context:")
	tw = tabwriter.NewWriter(&sb, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  KIND\tNAME\tID\tDOWNLOADURL")
	for _, p := range h.Context.Prompts {
		_, _ = fmt.Fprintf(tw, "  prompt\t%s\t%s\t%s\n", p.Name, p.ID, p.DownloadURL)
	}
	for _, pl := range h.Context.Plugins {
		_, _ = fmt.Fprintf(tw, "  plugin\t%s\t%s\t%s\n", pl.Name, pl.ID, pl.DownloadURL)
	}
	for _, ar := range h.Context.Artifacts {
		_, _ = fmt.Fprintf(tw, "  artifact\t%s\t%s\t%s\n", ar.Name, ar.ID, ar.DownloadURL)
	}
	_ = tw.Flush()

	return sb.String()
}

// FormatEkList renders the env-keys table for `ach env-keys list`
// (06-05) AND `ach admin keys list` (06-08). Rows are sorted
// deterministically by KeyID ascending (W7 — both call sites consume
// the same output). Empty input → stable "No env-keys" stub.
func FormatEkList(rows []EkRowView) string {
	if len(rows) == 0 {
		return "No env-keys\n"
	}
	sorted := make([]EkRowView, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].KeyID < sorted[j].KeyID
	})
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY-ID\tOWNER\tENVIRONMENT\tNAME\tCREATED")
	for _, r := range sorted {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.KeyID, r.OwnerEmail, r.Environment, r.Name, r.CreatedAt)
	}
	_ = tw.Flush()
	return sb.String()
}

// FormatIdentity returns the three-line identity block for `ach
// whoami` (no-net default). Provided here as an optional refactor
// target for 06-03 follow-up — the whoami subcommand can lift its
// inline formatIdentityBlock to consume this function for a single
// source of truth. The key string is masked via config.Mask so this
// function NEVER emits plaintext on stdout (CLI-04).
func FormatIdentity(name, url, key string) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Deployment: %s\n", name)
	_, _ = fmt.Fprintf(&sb, "URL: %s\n", url)
	_, _ = fmt.Fprintf(&sb, "Key: %s\n", config.Mask(key))
	return sb.String()
}
