// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/observability"
)

// UpsertTagBudget points the tag at a budget object whose budget_id IS the
// tag name, so /budget/list reads as user:<email> / environment:<env> /
// key:<id> instead of a wall of uuids.
//
// LiteLLM has no upsert on either object, and POST /tag/update cannot bind a
// budget_id at all (500, measured) — so an existing tag holding a different
// budget (LiteLLM auto-creates budgetless tag rows from x-litellm-tags
// traffic) is deleted and recreated. That restarts LiteLLM_TagTable.spend at
// 0; the per-day history in LiteLLM_DailyTagSpend is keyed by tag name and
// survives (references/litellm-permission-model.md §15).
//
// §9.1: only the tag name is logged by callers — never a budget amount tied
// to an identity beyond what the caller already holds.
//
// The tag is read FIRST: the operator re-asserts every Environment budget on
// each 5-minute resync, and a tag already bound to an identical budget needs
// no write at all (before, every pass paid a /budget/new 400 + /budget/update).
func (c *RESTClient) UpsertTagBudget(ctx context.Context, name string, b TagBudget) error {
	b.BudgetID = name
	info, err := c.TagInfo(ctx, name)
	if err != nil {
		return fmt.Errorf("litellm: read tag %s: %w", name, err)
	}
	bound := info != nil && info.Budget != nil && info.Budget.BudgetID == name
	if bound && info.Budget.MaxBudget == b.MaxBudget && info.Budget.BudgetDuration == b.BudgetDuration {
		return nil // already bound to this exact ceiling
	}
	if bound {
		if _, err := c.makeRequest(ctx, "POST", "/budget/update", b); err != nil {
			return fmt.Errorf("litellm: update budget %s: %w", name, err)
		}
		return nil
	}
	// New first: /budget/update on a missing id returns 200 null and does nothing.
	if _, err := c.makeRequest(ctx, "POST", "/budget/new", b); err != nil {
		if _, err := c.makeRequest(ctx, "POST", "/budget/update", b); err != nil {
			return fmt.Errorf("litellm: upsert budget %s: %w", name, err)
		}
	}
	if info != nil {
		if err := c.DeleteTagByName(ctx, name); err != nil {
			return fmt.Errorf("litellm: rebind tag %s: %w", name, err)
		}
	}
	bind := struct {
		Name     string `json:"name"`
		BudgetID string `json:"budget_id"`
	}{Name: name, BudgetID: name}
	if _, err := c.makeRequest(ctx, "POST", "/tag/new", bind); err != nil {
		return fmt.Errorf("litellm: bind tag %s: %w", name, err)
	}
	return nil
}

// DeleteBudget removes a budget object. A missing budget is success.
func (c *RESTClient) DeleteBudget(ctx context.Context, id string) error {
	if _, err := c.makeRequest(ctx, "POST", "/budget/delete", map[string]string{"id": id}); err != nil {
		if tagAbsent(err) {
			return nil
		}
		return fmt.Errorf("litellm: delete budget %s: %w", id, err)
	}
	return nil
}

// tagAbsent reports whether err is LiteLLM's "this tag does not exist"
// answer. Measured 2026-09-22 on the e2e cluster: /tag/info on an unknown
// tag answers **HTTP 500** with `{"detail":"404: Tags not found: ['<tag>']"}`
// — the handler raises a 404 that the outer error wrapper re-wraps as a 500,
// so neither the status code nor APIError.Error() (which excludes the body
// by §9.1) carries the 404. The body is the only signal, so this looks at
// APIError.Body. Absent is never an error for ACH: an unbudgeted tag is the
// normal state of the world.
func tagAbsent(err error) bool {
	if IsHTTPNotFound(err) {
		return true
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	body := strings.ToLower(string(apiErr.Body))
	return strings.Contains(body, "not found")
}

// TagInfo returns the tag's budget, or (nil, nil) when LiteLLM does
// not know the tag (no budget configured yet — not an error).
func (c *RESTClient) TagInfo(ctx context.Context, name string) (*TagInfoEntry, error) {
	raw, err := c.makeRequest(ctx, "POST", "/tag/info", map[string][]string{"names": {name}})
	if err != nil {
		if tagAbsent(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("litellm: POST /tag/info %s: %w", name, err)
	}
	var resp map[string]struct {
		CreatedAt string     `json:"created_at"`
		Budget    *TagBudget `json:"litellm_budget_table"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("litellm: decode /tag/info %s: %w", name, err)
	}
	entry, ok := resp[name]
	if !ok {
		return nil, nil
	}
	return &TagInfoEntry{Name: name, CreatedAt: entry.CreatedAt, Budget: entry.Budget}, nil
}

// budgetDuration matches LiteLLM's budget_duration grammar (duration_parser.py).
var budgetDuration = regexp.MustCompile(`^(\d+)(s|m|h|d|w|mo)$`)

// SpendWindowStart is when the tag's enforced spend last started from zero.
// The tag's spend starts at 0 when the tag is created (UpsertTagBudget's
// rebind recreates it, so a rebound tag restarts there too), and LiteLLM's
// reset job zeroes it again at budget_reset_at, scheduling the next reset
// on a calendar grid (duration_parser.get_next_standardized_reset_time):
// "30d" lands on the 1st of the month, "7d" on a Monday, any other count
// of days that many days on, "Nmo" N months on, h/m/s that many units on.
// The running cycle therefore began one such step before reset_at, or at
// the tag's creation if that is later. A tag with no resetting budget has
// only its creation. ok is false when nothing usable can be read.
func (e TagInfoEntry) SpendWindowStart() (time.Time, bool) {
	created, createdErr := ParseTime(e.CreatedAt)
	b := e.Budget
	if b == nil || b.BudgetResetAt == "" {
		return created, createdErr == nil
	}
	resetAt, err := ParseTime(b.BudgetResetAt)
	m := budgetDuration.FindStringSubmatch(b.BudgetDuration)
	if err != nil || m == nil {
		return time.Time{}, false
	}
	n, _ := strconv.Atoi(m[1])
	var start time.Time
	switch unit := m[2]; {
	case unit == "mo", unit == "d" && n == 30:
		if unit == "d" {
			n = 1
		}
		// Step back from the nearest UTC midnight (the 1st) and re-apply
		// the zone offset: LiteLLM resets at local midnight, which east of
		// UTC is the previous day in UTC, and AddDate from the 31st
		// normalises "Sep 31" to Oct 1.
		anchor := resetAt.UTC().Add(12 * time.Hour).Truncate(24 * time.Hour)
		start = anchor.AddDate(0, -n, 0).Add(resetAt.Sub(anchor))
	case unit == "w":
		start = resetAt.AddDate(0, 0, -7*n)
	case unit == "d":
		start = resetAt.AddDate(0, 0, -n)
	default:
		step := map[string]time.Duration{"h": time.Hour, "m": time.Minute, "s": time.Second}[unit]
		start = resetAt.Add(-time.Duration(n) * step)
	}
	if createdErr == nil && created.After(start) {
		start = created
	}
	return start, true
}

// ParseTime reads a LiteLLM timestamp: RFC 3339, or Python's naive
// isoformat (no zone), which LiteLLM means as UTC.
func ParseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04:05.999999999", s)
}

// TagSpend sums the tag's spend over [start, end] from LiteLLM's
// LiteLLM_DailyTagSpend via GET /tag/daily/activity (master key: the
// endpoint scopes a non-admin to its own keys). Buckets are whole UTC
// days, inclusive on both ends, so start rounds to the NEAREST bucket
// boundary: a cycle that begins at 22:00Z (midnight Europe/Madrid) counts
// from the next day, not the whole previous one — but never past end, or a
// tag born after 12:00Z today would ask for an empty, reversed range.
// ponytail: up to 12h of skew at the start, and a sub-day budget ("1h")
// still reads whole days; exact needs LiteLLM to expose
// LiteLLM_TagTable.spend (its own cycle counter) in /tag/info.
// The per-day rows of the same read come back too, oldest first, so a
// chart of the cycle sums to exactly the spend reported beside it.
func (c *RESTClient) TagSpend(ctx context.Context, name string, start, end time.Time) (float64, []observability.DaySpend, error) {
	endDay := end.UTC().Format(time.DateOnly)
	startDay := min(start.UTC().Add(12*time.Hour).Format(time.DateOnly), endDay)
	q := url.Values{}
	q.Set("tags", name)
	q.Set("start_date", startDay)
	q.Set("end_date", endDay)
	da, err := pageDailyActivity("/tag/daily/activity", q, func(pathAndQuery string) ([]byte, error) {
		return c.makeRequest(ctx, "GET", pathAndQuery, nil)
	})
	if err != nil {
		return 0, nil, fmt.Errorf("litellm: GET /tag/daily/activity %s: %w", name, err)
	}
	n, _ := da.Metadata["total_spend"].(json.Number)
	spend, _ := n.Float64()
	series := []observability.DaySpend{}
	for _, d := range observability.AggregateWindow(da).Series {
		series = append(series, observability.DaySpend{Date: d.Date, Spend: d.Spend})
	}
	return spend, series, nil
}

// DeleteTagByName removes the tag (and with it its budget link). Absent is
// success — callers use it on revoke paths that may run twice.
func (c *RESTClient) DeleteTagByName(ctx context.Context, name string) error {
	if _, err := c.makeRequest(ctx, "POST", "/tag/delete", map[string]string{"name": name}); err != nil {
		if tagAbsent(err) {
			return nil
		}
		return fmt.Errorf("litellm: POST /tag/delete %s: %w", name, err)
	}
	return nil
}

// DeleteKeyBudget reaps an ek_'s own "key:<id>" tag AND its budget object —
// a tag delete orphans the budget row, so the two always travel together.
// Both halves are attempted even if the first fails, and both are idempotent
// (absent = success). Every caller is a revoke path where the credential is
// already dead, so the error is for logging, never for failing the revoke.
func DeleteKeyBudget(ctx context.Context, c Client, keyID string) error {
	tag := KeyBudgetTag(keyID)
	return errors.Join(c.DeleteTagByName(ctx, tag), c.DeleteBudget(ctx, tag))
}

// UserBudgetTag / EnvironmentBudgetTag / KeyBudgetTag are the three tag
// namespaces ACH stamps (see references/litellm-permission-model.md).
// EnvironmentBudgetTag keeps FWD-06's existing "environment:<name>" name.
func UserBudgetTag(email string) string      { return "user:" + NormalizeEmail(email) }
func EnvironmentBudgetTag(env string) string { return "environment:" + env }
func KeyBudgetTag(keyID string) string       { return "key:" + keyID }
