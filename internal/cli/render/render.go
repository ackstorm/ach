// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/ackstorm/ach/internal/cli/config"
)

// ConditionView is a k8s-free mirror of one metav1.Condition as serialized by
// the platform-api environments handler (json keys match the wire shape). The
// CLI decodes conditions into this so `env describe` can explain WHY an
// environment is not ready, without importing k8s.io/apimachinery.
type ConditionView struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// EnvView is the per-row shape rendered by FormatEnvList +
// FormatEnvDescribe. Matches the per-row projection of the server's
// /platform/environments handler (internal/platformapi/store.EnvironmentView's
// caller-visible subset: name, namespace, status). We intentionally
// define a lean local copy so this package does NOT pull
// internal/platformapi (which transitively imports k8s.io/* + chi).
// json tags are present so callers can decode the wire payload
// directly into []EnvView when convenient.
type EnvView struct {
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace,omitempty"`
	Status      string          `json:"status,omitempty"`
	Description string          `json:"description,omitempty"`
	Conditions  []ConditionView `json:"conditions,omitempty"`
}

// KeyRowView + FormatKeyList live in ek.go — single source of truth
// for key row shape and table formatter (pk_ + ek_, with TYPE column).

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
	// Guardrails are plain names: LiteLLM applies them server-side, so unlike
	// the other runtime axes there is no endpoint to surface.
	Guardrails []string      `json:"guardrails,omitempty"`
	Prompts    []ContextItem `json:"prompts,omitempty"`
	Plugins    []ContextItem `json:"plugins,omitempty"`
	Artifacts  []ContextItem `json:"artifacts,omitempty"`
	Skills     []ContextItem `json:"skills,omitempty"`
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
// `ach config list`. Rows are sorted alphabetically by profile name.
// The leading CURRENT column carries "*" on the default (active-when-
// unspecified) profile — kubectl `config get-contexts` idiom — and is
// blank otherwise. The PK column is "yes"/"no" (presence flag, never
// plaintext); the EK column is the count of entries in the ek map.
func FormatConfigList(f *config.File) string {
	if f == nil || len(f.Profiles) == 0 {
		return "No profiles configured\n"
	}
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CURRENT\tNAME\tURL\tPK\tEK")
	for _, name := range names {
		dep := f.Profiles[name]
		current := ""
		if name == f.Default {
			current = "*"
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
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", current, name, url, pkPresent, ekCount)
	}
	_ = tw.Flush()
	return sb.String()
}

// FormatConfigShow returns the block for `ach config show [profile]`.
// When reveal=false (default), the PK and every EK value pass through
// config.Mask so only the "<prefix>-****<last-4>" tail is rendered;
// when reveal=true (D-05 opt-in unmask), the values flow through
// verbatim. The unmask is scoped to ONE named profile per
// invocation (the caller picks; this function trusts its input).
func FormatConfigShow(name string, dep *config.Profile, reveal bool) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Profile: %s\n", name)
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
	_, _ = fmt.Fprintln(tw, "NAME\tNAMESPACE\tSTATUS\tDESCRIPTION")
	for _, e := range envs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Namespace, e.Status, truncateField(e.Description, 40))
	}
	_ = tw.Flush()
	return sb.String()
}

// orEM returns s unchanged when non-empty, or emDash when empty — matches the
// keys list empty-cell convention so env describe looks consistent.
func orEM(s string) string {
	if s == "" {
		return emDash
	}
	return s
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
	// Surface WHY when not all conditions are satisfied (U4). Only non-True
	// conditions are listed — a healthy env prints nothing extra here.
	var problems []ConditionView
	for _, c := range env.Conditions {
		if c.Status != "True" {
			problems = append(problems, c)
		}
	}
	if len(problems) > 0 {
		_, _ = fmt.Fprintln(&sb, "Not ready:")
		tw := tabwriter.NewWriter(&sb, 2, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  CONDITION\tREASON\tMESSAGE")
		for _, c := range problems {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.Type, orEM(c.Reason), orEM(c.Message))
		}
		_ = tw.Flush()
	}
	if env.Description != "" {
		_, _ = fmt.Fprintf(&sb, "Description:\n  %s\n", strings.ReplaceAll(env.Description, "\n", "\n  "))
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
		_, _ = fmt.Fprintf(tw, "  model\t%s\t%s\t%s\n", orEM(m.Name), orEM(m.ID), orEM(m.Endpoint))
	}
	for _, s := range h.Runtime.MCPServers {
		_, _ = fmt.Fprintf(tw, "  mcpServer\t%s\t%s\t%s\n", orEM(s.Name), orEM(s.ID), orEM(s.Endpoint))
	}
	for _, a := range h.Runtime.A2AAgents {
		_, _ = fmt.Fprintf(tw, "  a2aAgent\t%s\t%s\t%s\n", orEM(a.Name), orEM(a.ID), orEM(a.Endpoint))
	}
	for _, g := range h.Runtime.Guardrails {
		// No endpoint: a guardrail is applied by LiteLLM, never called.
		_, _ = fmt.Fprintf(tw, "  guardrail\t%s\t%s\t%s\n", g, g, emDash)
	}
	_ = tw.Flush()

	// Context block (3 axes — each row surfaces the per-context-entry
	// `downloadUrl` per W3 phase-goal sentence).
	_, _ = fmt.Fprintln(&sb, "Context:")
	tw = tabwriter.NewWriter(&sb, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  KIND\tNAME\tDOWNLOADURL")
	for _, p := range h.Context.Prompts {
		_, _ = fmt.Fprintf(tw, "  prompt\t%s\t%s\n", p.Name, orEM(p.DownloadURL))
	}
	for _, pl := range h.Context.Plugins {
		_, _ = fmt.Fprintf(tw, "  plugin\t%s\t%s\n", pl.Name, orEM(pl.DownloadURL))
	}
	for _, ar := range h.Context.Artifacts {
		_, _ = fmt.Fprintf(tw, "  artifact\t%s\t%s\n", ar.Name, orEM(ar.DownloadURL))
	}
	for _, sk := range h.Context.Skills {
		_, _ = fmt.Fprintf(tw, "  skill\t%s\t%s\n", sk.Name, orEM(sk.DownloadURL))
	}
	_ = tw.Flush()

	return sb.String()
}

// truncateField collapses a field to its first non-empty line and caps it at
// max runes (appending "…" when truncated) so it fits one table cell.
func truncateField(s string, max int) string {
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
