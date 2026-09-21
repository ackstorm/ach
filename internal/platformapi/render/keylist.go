// SPDX-License-Identifier: Apache-2.0

package render

import (
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// KeyListRow is the secret-free wire projection of one key shared by
// GET /platform/keys and GET /platform/admin/keys. credential_hash and
// litellm_* columns are excluded by construction.
//
// ExpiresAt is ek_-only (D-24, §7.3) and always present in the JSON —
// null means perpetual. A pk_'s window slides on every use, so no expiry
// date is ever projected for it (Status carries the liveness instead).
// Reasons is the (possibly empty, then omitted) set of secondary facts
// EffectiveState derived alongside Status — e.g. "suspended", "no_access".
type KeyListRow struct {
	KeyID       string   `json:"key_id"`
	Type        string   `json:"type"`
	OwnerEmail  string   `json:"owner_email"`
	Environment string   `json:"environment,omitempty"`
	Name        string   `json:"name,omitempty"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
	LastUsedAt  *string  `json:"last_used_at,omitempty"`
	RevokedAt   *string  `json:"revoked_at,omitempty"`
	ExpiresAt   *string  `json:"expires_at"`
	Reasons     []string `json:"reasons,omitempty"`
}

// KeyRow builds one KeyListRow from a db.KeyListItem plus a caller-supplied
// status/reasons pair. GET /platform/keys passes envkeys.EffectiveState's
// derived output; the admin inventory passes the raw SQL-level it.Status
// with nil reasons (D-20 — admins get no Invalid derivation).
func KeyRow(it db.KeyListItem, status string, reasons []string) KeyListRow {
	row := KeyListRow{
		KeyID:      it.KeyID,
		Type:       it.Type,
		OwnerEmail: it.OwnerEmail,
		Status:     status,
		CreatedAt:  it.CreatedAt.UTC().Format(time.RFC3339),
		Reasons:    reasons,
	}
	if it.Environment != nil {
		row.Environment = *it.Environment
	}
	if it.Name != nil {
		row.Name = *it.Name
	}
	if it.LastUsedAt != nil {
		s := it.LastUsedAt.UTC().Format(time.RFC3339)
		row.LastUsedAt = &s
	}
	if it.RevokedAt != nil {
		s := it.RevokedAt.UTC().Format(time.RFC3339)
		row.RevokedAt = &s
	}
	if it.ExpiresAt != nil {
		s := it.ExpiresAt.UTC().Format(time.RFC3339)
		row.ExpiresAt = &s
	}
	return row
}

// KeyList writes the paginated {items,next_cursor} key-list envelope.
func KeyList(w http.ResponseWriter, rows []KeyListRow, next string) {
	if rows == nil {
		rows = []KeyListRow{}
	}
	JSON(w, http.StatusOK, map[string]any{"items": rows, "next_cursor": next})
}
