// SPDX-License-Identifier: Apache-2.0

// Package render owns the CLI's text-output discipline: pure
// formatters returning strings, never writing to os.Stdout/os.Stderr
// directly. Callers (cobra RunE) own the io.Writer.
//
// This file (`ek.go`) lands the key lifecycle subset consumed by W2-P5
// `ach env-keys list` and W3-P2 `ach admin keys list` (per 06-04 W7 —
// single source of truth, no inline duplication). The broader render
// surface (FormatProfileList / FormatProfileShow / FormatEnvList /
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

// KeyRowView is the secret-free view of one key row (pk_ or ek_).
// Field names match the on-the-wire JSON keys so callers can decode
// straight into this type without a separate projection step.
//
// LastUsedAt / RevokedAt are pointers so empty-string vs absent is
// preserved (matches the omitempty contract in the server's wire
// shape).
type KeyRowView struct {
	KeyID       string  `json:"key_id"`
	Type        string  `json:"type"`        // "pk" | "ek"
	Environment string  `json:"environment"` // "" for pk
	Name        string  `json:"name"`        // "" for pk
	OwnerEmail  string  `json:"owner_email"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at,omitempty"`
	RevokedAt   *string `json:"revoked_at,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// emDash is the em dash placeholder for empty optional cells (U+2014).
const emDash = "—"

// FormatKeyList renders `keys list`: NAME, ENVIRONMENT, STATUS, EXPIRES
// ("never" when unset), ID — newest first. Empty input → "No keys found".
func FormatKeyList(rows []KeyRowView) string {
	if len(rows) == 0 {
		return "No keys found\n"
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tENVIRONMENT\tSTATUS\tEXPIRES\tID")
	for _, r := range rows {
		expires := "never"
		if r.ExpiresAt != nil && *r.ExpiresAt != "" {
			expires = *r.ExpiresAt
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", orDash(r.Name), orDash(r.Environment), r.Status, expires, r.KeyID)
	}
	_ = tw.Flush()
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return emDash
	}
	return s
}

// FormatAdminKeyList renders `admin keys list` rows as a deterministic tab-aligned table.
// Column order: KEY-ID, TYPE, OWNER, ENVIRONMENT, NAME, STATUS, CREATED.
// Rows are sorted by CreatedAt descending (newest first), pk and ek mixed —
// the TYPE column distinguishes them. CreatedAt values are RFC3339/ISO form;
// lexicographic descending sort equals chronological descending.
//
// Empty ENVIRONMENT or NAME cells are rendered as — (em dash U+2014) to
// avoid confusing blank cells for missing data. pk_ keys always have empty
// Environment and Name.
//
// Empty input returns a single line: "No keys found".
func FormatAdminKeyList(rows []KeyRowView) string {
	if len(rows) == 0 {
		return "No keys found\n"
	}
	// Sort newest-first by CreatedAt (RFC3339 strings sort correctly lexicographically).
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })

	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY-ID\tTYPE\tOWNER\tENVIRONMENT\tNAME\tSTATUS\tCREATED")
	for _, r := range rows {
		env := r.Environment
		if env == "" {
			env = emDash
		}
		name := r.Name
		if name == "" {
			name = emDash
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.KeyID, r.Type, r.OwnerEmail, env, name, r.Status, r.CreatedAt)
	}
	_ = tw.Flush()
	return b.String()
}
