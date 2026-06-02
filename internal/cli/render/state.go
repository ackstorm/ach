// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// StateEntryView is the per-row shape rendered by FormatStateList +
// FormatStateListJSON for `ach-cli list` (LIFE-03 / D-31). It is a lean,
// plain-data local view — the cmd layer (cmd/ach-cli/cmd/list.go) builds
// these by walking the state.File buckets, deriving Kind from the owning
// bucket (state.FileEntry carries no kind field) and Environment from
// state.File.Environment, while Target comes straight from the entry.
//
// Defining the view here (rather than importing internal/cli/state) keeps
// the render package free of any state-package coupling beyond this plain
// struct — mirroring the EnvView lean-copy convention so render stays
// dependency-light (no k8s.io/* either). json tags are present so the
// --json renderer produces a stable wire shape.
type StateEntryView struct {
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Environment string `json:"environment"`
}

// FormatStateList renders the per-resource inventory table for
// `ach-cli list`. Empty input → stable "No resources installed\n" stub
// (mirrors FormatEnvList's empty-state convention). Otherwise a
// text/tabwriter table with the D-31 columns KIND / TARGET / ENVIRONMENT
// and one row per entry, in the caller-provided order.
func FormatStateList(entries []StateEntryView) string {
	if len(entries) == 0 {
		return "No resources installed\n"
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tTARGET\tENVIRONMENT")
	for _, e := range entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Kind, e.Target, e.Environment)
	}
	_ = tw.Flush()
	return sb.String()
}

// FormatStateListJSON emits the machine-readable inventory for
// `ach-cli list --json`. Output is deterministic: entries are sorted by
// (Kind, Target, Environment) before marshalling so two calls with the
// same logical set produce byte-identical output regardless of the
// caller's iteration order. Empty/nil input marshals to "[]\n" (a stable
// empty-state machine document, never "null"). The trailing newline keeps
// terminal output tidy and matches the table renderer's shape.
func FormatStateListJSON(entries []StateEntryView) (string, error) {
	// Copy so we never reorder the caller's slice; normalise nil → empty
	// so json.Marshal yields "[]" rather than "null".
	sorted := make([]StateEntryView, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		if sorted[i].Target != sorted[j].Target {
			return sorted[i].Target < sorted[j].Target
		}
		return sorted[i].Environment < sorted[j].Environment
	})
	b, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
