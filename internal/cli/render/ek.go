// SPDX-License-Identifier: Apache-2.0

// Package render owns the CLI's text-output discipline: pure
// formatters returning strings, never writing to os.Stdout/os.Stderr
// directly. Callers (cobra RunE) own the io.Writer.
//
// This file (`ek.go`) lands the ek_ lifecycle subset consumed by W2-P5
// `ach env-keys list` and W3-P2 `ach admin keys list` (per 06-04 W7 —
// single source of truth, no inline duplication). The broader render
// surface (FormatConfigList / FormatConfigShow / FormatEnvList /
// FormatEnvDescribe) lands via 06-04 in `render.go`
// alongside this file — both files contribute to the same `render`
// package without a merge conflict because they own disjoint symbols.
//
// Pattern S5: NO `log`, NO `slog`, NO `fmt.Print*`. Formatters return
// strings; callers write to their own stdout/stderr.
package render

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// EkRowView is the lean per-row DTO for `ach env-keys list` and
// `ach admin keys list`. Field names match the on-the-wire JSON keys
// of `internal/platformapi/envkeys.EkRowView` so callers can decode
// straight into this type without a separate projection step.
//
// LastUsedAt / RevokedAt are pointers so empty-string vs absent is
// preserved (matches the omitempty contract in the server's wire
// shape).
type EkRowView struct {
	KeyID       string  `json:"key_id"`
	Environment string  `json:"environment"`
	Name        string  `json:"name"`
	OwnerEmail  string  `json:"owner_email"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
	RevokedAt   *string `json:"revoked_at,omitempty"`
}

// FormatEkList renders rows as a deterministic tab-aligned table.
// Column order: KEY-ID, OWNER, ENVIRONMENT, NAME, STATUS, CREATED.
// Rows are sorted by KEY-ID ascending so output is reproducible
// regardless of server-side pagination ordering.
//
// Empty input returns a single line: "No env-keys found".
//
// Consumed by:
//   - cmd/ach/cmd/env_keys.go (envKeysListCmd) — W2-P5 / 06-05.
//   - cmd/ach/cmd/admin.go (adminKeysListCmd) — W3-P2 / 06-08.
//
// Per 06-04 Task 1 W7 spec: this formatter is the single source of
// truth — neither caller embeds an inline tabwriter.
func FormatEkList(rows []EkRowView) string {
	if len(rows) == 0 {
		return "No env-keys found\n"
	}
	// Deterministic order by KEY-ID ascending. Copy so we do not
	// mutate caller's slice ordering.
	sorted := make([]EkRowView, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].KeyID < sorted[j].KeyID
	})

	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY-ID\tOWNER\tENVIRONMENT\tNAME\tSTATUS\tCREATED")
	for _, r := range sorted {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.KeyID, r.OwnerEmail, r.Environment, r.Name, r.Status, r.CreatedAt)
	}
	_ = tw.Flush()
	return sb.String()
}
