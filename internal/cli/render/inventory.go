// SPDX-License-Identifier: Apache-2.0

// inventory.go renders `ach admin list` output. Per Pattern S5 the formatter
// returns a string; the cobra RunE owns the io.Writer. The DTO mirrors the
// server's internal/platformapi/admin/inventory.AdminObjectView wire shape so
// the CLI decodes responses straight into it (no separate projection step).
package render

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// AdminObjectView is the uniform per-object row `ach admin list` decodes and
// renders. JSON tags match the server emitter verbatim.
type AdminObjectView struct {
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`
	Sync       string            `json:"sync"`
	SyncReason string            `json:"syncReason,omitempty"`
	UpdatedAt  string            `json:"updatedAt,omitempty"` // RFC3339
	Origin     string            `json:"origin,omitempty"`
	Locked     bool              `json:"locked"`
	Extra      map[string]string `json:"extra,omitempty"`
}

// syncFalseGreen is the marker the server attaches to a fresh prompt/artifact
// (content-presence not actually gated). Its presence in any row triggers the
// table footnote.
const syncFalseGreen = "fresh*"

// FormatAdminInventory renders the kind→rows map as kind-sectioned, tab-aligned
// tables. Groups print in stable (alphabetical) kind order; rows within a group
// sort by (namespace, name) for reproducible output regardless of server
// pagination ordering. A footnote is appended when any row carries the
// prompts/artifacts false-green marker.
func FormatAdminInventory(grouped map[string][]AdminObjectView) string {
	if len(grouped) == 0 {
		return "No objects found\n"
	}
	kinds := make([]string, 0, len(grouped))
	for k := range grouped {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	var sb strings.Builder
	falseGreen := false
	now := time.Now()
	for i, kind := range kinds {
		rows := grouped[kind]
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "%s (%d)\n", strings.ToUpper(kind), len(rows))
		if len(rows) == 0 {
			sb.WriteString("  (none)\n")
			continue
		}
		sorted := make([]AdminObjectView, len(rows))
		copy(sorted, rows)
		sort.Slice(sorted, func(a, b int) bool {
			if sorted[a].Namespace != sorted[b].Namespace {
				return sorted[a].Namespace < sorted[b].Namespace
			}
			return sorted[a].Name < sorted[b].Name
		})
		tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "NAME\tNAMESPACE\tVERSION\tSYNC\tAGE\tORIGIN")
		for _, r := range sorted {
			if r.Sync == syncFalseGreen {
				falseGreen = true
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				dash(r.Name), dash(r.Namespace), dash(r.Version),
				syncCell(r), ageOf(r.UpdatedAt, now), dash(r.Origin))
		}
		_ = tw.Flush()
	}
	if falseGreen {
		sb.WriteString("\n* prompts/artifacts: name-resolved only; content presence is not gated\n")
	}
	return sb.String()
}

// dash renders an empty cell as "-" so columns stay visually aligned.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// syncCell renders the SYNC column, appending the reason in parentheses when
// present (e.g. "STALE(2h over)", "Degraded(AccessGroupSyncedFalse)").
func syncCell(r AdminObjectView) string {
	if r.SyncReason == "" {
		return r.Sync
	}
	return r.Sync + "(" + r.SyncReason + ")"
}

// ageOf converts an RFC3339 updatedAt into a coarse age relative to now.
// Empty / unparseable timestamps render as "-".
func ageOf(updatedAt string, now time.Time) string {
	if updatedAt == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
