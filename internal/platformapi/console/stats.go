// SPDX-License-Identifier: Apache-2.0

package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/observability"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// maxLatencySpendLogPages bounds the /spend/logs/v2 fetch behind
// GET /platform/console/latency (spec §10.2/Task 4 brief).
const maxLatencySpendLogPages = 5

// maxRangeDays is the 366-day cap on a stats/latency window (leap-year
// inclusive year span), mirroring alitellm-auth's session.py
// _parse_stats_range.
const maxRangeDays = 366

// defaultRangeDays is the default window width (30 days, inclusive of
// both endpoints) when no start_date is given.
const defaultRangeDays = 30

// dateLayout is the YYYY-MM-DD wire format both range params and the
// LiteLLM analytics endpoints use.
const dateLayout = "2006-01-02"

// dataScopeUser is the D-13/AC-16 "data_scope" value every console
// analytics response carries — user-global, never Environment-scoped.
const dataScopeUser = "user"

// parseRange mirrors alitellm-auth's session.py _parse_stats_range: UTC,
// inclusive on both ends, default 30 days ending today, 366-day cap. ACH
// answers 400 invalid_argument on a malformed or oversized range (the
// caller renders it; this function never writes a response) — not
// FastAPI's 422.
func parseRange(q url.Values, now time.Time) (start, end time.Time, err error) {
	end = now.Truncate(24 * time.Hour)
	if v := q.Get("end_date"); v != "" {
		if end, err = time.Parse(dateLayout, v); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("end_date must be YYYY-MM-DD")
		}
	}
	start = end.AddDate(0, 0, -(defaultRangeDays - 1))
	if v := q.Get("start_date"); v != "" {
		if start, err = time.Parse(dateLayout, v); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("start_date must be YYYY-MM-DD")
		}
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start_date must not be after end_date")
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > maxRangeDays {
		return time.Time{}, time.Time{}, fmt.Errorf("range must not exceed %d days", maxRangeDays)
	}
	return start, end, nil
}

// stats serves GET /platform/console/stats — the user's own daily-activity
// window folded into the page-ready StatsContract, D-13 user-global
// (never Environment-scoped). Independent degradation (§10.3): a failed
// PRIOR window only drops capabilities.deltas; a failed budget read only
// degrades budget.source to "unknown"; only the CURRENT window's failure
// is a hard error (§10.1 — never a master-key retry).
func (d Deps) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	kc, u, ok := d.userReads(w, r)
	if !ok {
		return
	}
	start, end, err := parseRange(r.URL.Query(), time.Now().UTC())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	span := int(end.Sub(start).Hours()/24) + 1
	prevEnd := start.AddDate(0, 0, -1)
	prevStart := prevEnd.AddDate(0, 0, -(span - 1))
	day := func(t time.Time) string { return t.Format(dateLayout) }

	cur, err := u.DailyActivity(ctx, day(start), day(end))
	if err != nil {
		d.upstreamError(w, r, err)
		return
	}
	curAgg := observability.AggregateWindow(cur)
	caps := observability.Capabilities{TokenSplit: true, PerModelLastUsed: true, Deltas: true, PerKeySpend: true}
	var prevAgg *observability.WindowAggregate
	if prev, err := u.DailyActivity(ctx, day(prevStart), day(prevEnd)); err != nil {
		d.Logger.Warn("console.stats: prior window unavailable", "err", err)
		caps.Deltas = false
	} else {
		a := observability.AggregateWindow(prev)
		prevAgg = &a
	}

	// The caller's own ceiling lives on their LiteLLM tag, which is what
	// the forwarder enforces. Read with the MASTER client, not UserView:
	// /tag/info is an admin route, and the tag is ACH-owned metadata about
	// the caller rather than a LiteLLM user read. Degrades independently —
	// a failed read is budget.source "unknown", never an HTTP error.
	budget := d.userTagBudget(ctx, kc.OwnerEmail)

	rng := observability.Range{Start: day(start), End: day(end), Days: span}
	rng.Compare.Start, rng.Compare.End = day(prevStart), day(prevEnd)
	c := observability.BuildStatsContract(curAgg, prevAgg, observability.BudgetBlock(budget), observability.LastUsedFromWindow(cur), caps, rng)
	c.Keys = d.nameKeys(ctx, kc.OwnerEmail, c.Keys)
	render.JSON(w, http.StatusOK, withStatsScope(c))
}

// userTagBudget reads the caller's "user:<email>" tag and projects it into
// the console's budget input. nil means "no ceiling to report": the tag
// does not exist yet (nobody has spent through it and no default budget is
// configured), or the read failed — the panel says "unknown" either way,
// which is honest, where a zero would not be.
func (d Deps) userTagBudget(ctx context.Context, owner string) *observability.TagBudget {
	entry, err := d.LiteLLM.TagInfo(ctx, litellm.UserBudgetTag(owner))
	if err != nil {
		d.Logger.Warn("console.stats: user tag budget unavailable", "err", err)
		return nil
	}
	if entry == nil {
		return nil
	}
	out := &observability.TagBudget{Spend: entry.Spend}
	if entry.Budget != nil {
		maxBudget := entry.Budget.MaxBudget
		out.MaxBudget = &maxBudget
		if entry.Budget.BudgetDuration != "" {
			duration := entry.Budget.BudgetDuration
			out.BudgetDuration = &duration
		}
	}
	return out
}

// latency serves GET /platform/console/latency — the user's own
// /spend/logs/v2 rows folded into the page-ready LatencyContract, D-13
// user-global. A degraded fetch (401 role=unknown, or any other failure)
// is NOT an HTTP error: it renders 200 with available=false (§10.2 fact
// 1) so the console can show "latency unavailable" without a page-level
// failure. §10.2 fact 2: end_date is EXCLUSIVE on /spend/logs/v2, so the
// fetch sends end+1 day.
func (d Deps) latency(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := middleware.RequestIDFromCtx(ctx)
	_, u, ok := d.userReads(w, r)
	if !ok {
		return
	}
	start, end, err := parseRange(r.URL.Query(), time.Now().UTC())
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_argument", err.Error(), reqID)
		return
	}
	day := func(t time.Time) string { return t.Format(dateLayout) }
	window := observability.Window{Start: day(start), End: day(end), Days: int(end.Sub(start).Hours()/24) + 1}

	rows, truncated, err := u.SpendLogsV2(ctx, day(start), day(end.AddDate(0, 0, 1)), maxLatencySpendLogPages)
	if err != nil {
		reason := "fetch_failed"
		var a401 *litellm.Auth401Error
		if errors.As(err, &a401) {
			reason = "unavailable"
		}
		render.JSON(w, http.StatusOK, withLatencyScope(observability.LatencyUnavailable(reason)))
		return
	}
	c := observability.ComputeLatencyContract(rows, window, observability.DefaultRowCap, truncated)
	render.JSON(w, http.StatusOK, withLatencyScope(c))
}

// keyExternal labels the single folded row standing for every key in the
// window that ACH did not mint. The parenthetical names where such a key
// DOES come from, so the row reads as an explanation rather than an error.
const keyExternal = "external (LiteLLM)"

// nameKeys resolves each stats key row's key_alias — ACH stamps
// key_alias=ekid_…/pkid_… on the LiteLLM key at mint time
// (envkeys/handler.go, sso.go) — to the ACH-facing name: an ek_'s Name,
// or scopePersonal for a pk_.
//
// Every row that is NOT one of the owner's ACH-managed keys is folded into
// a single keyExternal row. LiteLLM aggregates a user's spend across every
// key tied to them, so this window legitimately contains keys ACH never
// issued — another product's, another ACH release's, or one made by hand
// in LiteLLM's UI. Left alone they render as raw key hashes (and a
// degenerate one as a bare "0"), which reads as a broken table rather than
// as what it is: spend outside ACH's control. Folding also keeps the row
// labels unique, which the UI relies on to key the table.
//
// Ownership is decided by the owner's OWN key rows, not by a metadata
// issuer check: each ACH release tracks its keys in its own database, so
// "absent from our rows" already means "not ours" — and it stays true for
// a sibling release, which an issuer comparison would have to special-case.
//
// Totals stay honest: the fold sums, never drops, so the keys block still
// accounts for the whole user-global window (D-13).
//
// A DB read failure degrades to "keep the raw aliases" rather than failing
// the whole response — without the key rows there is no basis to call any
// row foreign, and guessing would mislabel the user's own keys.
func (d Deps) nameKeys(ctx context.Context, owner string, rows []observability.KeyOut) []observability.KeyOut {
	if d.DB == nil {
		return rows
	}
	items, _, err := d.DB.ListKeys(ctx, db.KeyListFilter{OwnerEmail: &owner}, 500, "")
	if err != nil {
		d.Logger.Warn("console.stats: key names unavailable", "err", err)
		return rows
	}
	// ours is ownership; names is presentation. They are separate because an
	// ek_ row may carry a NULL name — still ours, just unnamed, and folding
	// it into the external row would be a lie.
	ours := make(map[string]bool, len(items))
	names := make(map[string]string, len(items))
	for _, it := range items {
		switch it.Type {
		case "pk":
			ours[it.KeyID] = true
			names[it.KeyID] = scopePersonal
		case "ek":
			ours[it.KeyID] = true
			if it.Name != nil {
				names[it.KeyID] = *it.Name
			}
		}
	}

	out := make([]observability.KeyOut, 0, len(rows)+1)
	external := observability.KeyOut{ID: "external", KeyAlias: nil}
	foundExternal := false
	for _, row := range rows {
		if row.KeyAlias != nil && ours[*row.KeyAlias] {
			row.ID = *row.KeyAlias
			if name, ok := names[*row.KeyAlias]; ok {
				row.KeyAlias = &name
			}
			out = append(out, row)
			continue
		}
		foundExternal = true
		external.Requests += row.Requests
		external.Spend += row.Spend
		if row.SpendPct != nil {
			pct := row.SpendPct
			if external.SpendPct == nil {
				zero := 0.0
				external.SpendPct = &zero
			}
			*external.SpendPct += *pct
		}
	}
	if foundExternal {
		label := keyExternal
		external.KeyAlias = &label
		out = append(out, external)
		// BuildStatsContract hands these over sorted by spend descending;
		// the folded row has to land in that order too.
		sort.SliceStable(out, func(i, j int) bool { return out[i].Spend > out[j].Spend })
	}
	return out
}

// withStatsScope embeds a StatsContract with data_scope:"user" (D-13,
// AC-16): every console analytics context shows the same user-global
// numbers, never an Environment-scoped slice. One marshal, no silent
// error path.
func withStatsScope(c observability.StatsContract) any {
	return struct {
		observability.StatsContract
		DataScope string `json:"data_scope"`
	}{c, dataScopeUser}
}

// withLatencyScope is withStatsScope's LatencyContract equivalent.
func withLatencyScope(c observability.LatencyContract) any {
	return struct {
		observability.LatencyContract
		DataScope string `json:"data_scope"`
	}{c, dataScopeUser}
}
