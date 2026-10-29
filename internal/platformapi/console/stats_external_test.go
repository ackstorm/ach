// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/observability"
)

func ptr[T any](v T) *T { return &v }

func nameKeysDeps(items []db.KeyListItem, err error) Deps {
	return Deps{
		DB:     fakeDB{items: items, err: err},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func findRow(rows []observability.KeyOut, alias string) *observability.KeyOut {
	for i := range rows {
		if rows[i].KeyAlias != nil && *rows[i].KeyAlias == alias {
			return &rows[i]
		}
	}
	return nil
}

// TestNameKeys_FoldsForeignKeysIntoOneRow — LiteLLM reports spend for every
// key tied to the user, including keys ACH never minted (another product's
// lk-*, another ACH release's pk_, a hand-made one). They must collapse
// into a single labelled row instead of rendering as raw hashes, and the
// collapse must SUM so the block still accounts for the whole window.
func TestNameKeys_FoldsForeignKeysIntoOneRow(t *testing.T) {
	d := nameKeysDeps([]db.KeyListItem{
		{Type: "pk", KeyID: "pkid_ours"},
		{Type: "ek", KeyID: "ekid_ours", Name: ptr("build-bot")},
	}, nil)

	rows := []observability.KeyOut{
		{ID: "hash-a", KeyAlias: ptr("ekid_ours"), Requests: 10, Spend: 5, SpendPct: ptr(25.0)},
		{ID: "hash-b", KeyAlias: ptr("lk-9ef8eda7"), Requests: 4, Spend: 3, SpendPct: ptr(15.0)},
		{ID: "hash-c", KeyAlias: ptr("pkid_other_release"), Requests: 2, Spend: 2, SpendPct: ptr(10.0)},
		{ID: "hash-d", KeyAlias: ptr("pkid_ours"), Requests: 1, Spend: 1, SpendPct: ptr(5.0)},
	}

	got := d.nameKeys(context.Background(), "u@x.com", rows)

	if len(got) != 3 {
		t.Fatalf("want 3 rows (2 ours + 1 folded), got %d: %+v", len(got), got)
	}
	ext := findRow(got, keyExternal)
	if ext == nil {
		t.Fatalf("no %q row: %+v", keyExternal, got)
	}
	if ext.Requests != 6 || ext.Spend != 5 {
		t.Fatalf("fold did not sum: requests=%d spend=%v", ext.Requests, ext.Spend)
	}
	if ext.SpendPct == nil || *ext.SpendPct != 25.0 {
		t.Fatalf("fold did not sum spend_pct: %v", ext.SpendPct)
	}
	if findRow(got, "build-bot") == nil || findRow(got, scopePersonal) == nil {
		t.Fatalf("our own keys lost their names: %+v", got)
	}
	// BuildStatsContract hands rows over sorted by spend descending; the
	// folded row must not break that ordering.
	for i := 1; i < len(got); i++ {
		if got[i-1].Spend < got[i].Spend {
			t.Fatalf("rows not sorted by spend desc: %+v", got)
		}
	}
}

// TestNameKeys_UnnamedEkStaysOurs — an ek_ row may carry a NULL name. It is
// still the user's own key, so it must not be folded away as foreign.
func TestNameKeys_UnnamedEkStaysOurs(t *testing.T) {
	d := nameKeysDeps([]db.KeyListItem{{Type: "ek", KeyID: "ekid_unnamed"}}, nil)

	got := d.nameKeys(context.Background(), "u@x.com", []observability.KeyOut{
		{ID: "hash-a", KeyAlias: ptr("ekid_unnamed"), Requests: 1, Spend: 1},
	})

	if len(got) != 1 {
		t.Fatalf("want the row kept, got %+v", got)
	}
	if got[0].KeyAlias == nil || *got[0].KeyAlias != "ekid_unnamed" {
		t.Fatalf("unnamed ek_ was relabelled: %+v", got[0])
	}
	if findRow(got, keyExternal) != nil {
		t.Fatalf("unnamed ek_ was folded as foreign: %+v", got)
	}
}

// TestNameKeys_AliaslessRowIsForeign — ACH stamps key_alias on everything
// it mints, so a row without one cannot be ours. This is also the row that
// rendered as a bare "0" in the console.
func TestNameKeys_AliaslessRowIsForeign(t *testing.T) {
	d := nameKeysDeps([]db.KeyListItem{{Type: "pk", KeyID: "pkid_ours"}}, nil)

	got := d.nameKeys(context.Background(), "u@x.com", []observability.KeyOut{
		{ID: "0", KeyAlias: nil, Requests: 7, Spend: 9},
	})

	ext := findRow(got, keyExternal)
	if ext == nil || ext.Requests != 7 || ext.Spend != 9 {
		t.Fatalf("aliasless row not folded: %+v", got)
	}
}

// TestNameKeys_NoForeignKeysNoExtraRow — the fold must not invent a row on
// a clean account.
func TestNameKeys_NoForeignKeysNoExtraRow(t *testing.T) {
	d := nameKeysDeps([]db.KeyListItem{{Type: "pk", KeyID: "pkid_ours"}}, nil)

	got := d.nameKeys(context.Background(), "u@x.com", []observability.KeyOut{
		{ID: "hash-a", KeyAlias: ptr("pkid_ours"), Requests: 1, Spend: 1},
	})

	if len(got) != 1 || findRow(got, keyExternal) != nil {
		t.Fatalf("invented an external row: %+v", got)
	}
}

// TestNameKeys_DBFailureLeavesRowsAlone — without the owner's key rows
// there is no basis to call anything foreign; folding then would mislabel
// the user's OWN keys as external. Degrade to the raw aliases.
func TestNameKeys_DBFailureLeavesRowsAlone(t *testing.T) {
	d := nameKeysDeps(nil, errors.New("db down"))

	in := []observability.KeyOut{
		{ID: "hash-a", KeyAlias: ptr("ekid_ours"), Requests: 1, Spend: 1},
		{ID: "hash-b", KeyAlias: ptr("lk-foreign"), Requests: 2, Spend: 2},
	}
	got := d.nameKeys(context.Background(), "u@x.com", in)

	if len(got) != 2 || findRow(got, keyExternal) != nil {
		t.Fatalf("degraded path folded anyway: %+v", got)
	}
}
