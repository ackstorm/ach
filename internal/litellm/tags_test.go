// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/observability"
)

// paths flattens the captured request paths for sequence assertions.
func paths(captured []capturedRequest) []string {
	out := make([]string, 0, len(captured))
	for _, c := range captured {
		out = append(out, c.Path)
	}
	return out
}

// TestUpsertTagBudgetCreatesAndBinds — a tag LiteLLM has never seen takes
// POST /budget/new (friendly budget_id = the tag name) then POST /tag/new
// binding the two. No /tag/delete: there is nothing to rebind.
func TestUpsertTagBudgetCreatesAndBinds(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		if captured[i].Path == "/tag/info" {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprint(w, `{"message":"ok"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 10, BudgetDuration: "30d"}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/tag/info,/budget/new,/tag/new" {
		t.Fatalf("request sequence = %s", got)
	}
	for _, want := range []string{`"budget_id":"user:a@b"`, `"max_budget":10`, `"budget_duration":"30d"`} {
		if !strings.Contains(string(captured[1].Body), want) {
			t.Errorf("/budget/new body %s missing %s", captured[1].Body, want)
		}
	}
	// budget_reset_at is LiteLLM-computed and read-only: a write never sends it.
	if strings.Contains(string(captured[1].Body), "budget_reset_at") {
		t.Errorf("/budget/new body %s carries budget_reset_at", captured[1].Body)
	}
	if body := string(captured[2].Body); !strings.Contains(body, `"name":"user:a@b"`) ||
		!strings.Contains(body, `"budget_id":"user:a@b"`) {
		t.Errorf("/tag/new body = %s", body)
	}
}

// TestUpsertTagBudgetUpdatesExistingBudgetAndKeepsBoundTag — a tag already
// bound to its own budget with a different ceiling takes ONE /budget/update;
// no /budget/new (it would 400 on the existing id) and no delete/recreate.
func TestUpsertTagBudgetUpdatesExistingBudgetAndKeepsBoundTag(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		switch captured[i].Path {
		case "/tag/info":
			w.WriteHeader(200)
			fmt.Fprint(w, `{"user:a@b":{"spend":1,"litellm_budget_table":{"budget_id":"user:a@b","max_budget":5}}}`)
		default:
			w.WriteHeader(200)
			fmt.Fprint(w, `{"message":"ok"}`)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 7}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/tag/info,/budget/update" {
		t.Fatalf("request sequence = %s", got)
	}
	if body := string(captured[1].Body); strings.Contains(body, "budget_duration") || !strings.Contains(body, `"max_budget":7`) {
		t.Errorf("/budget/update body = %s (want max_budget 7, no empty duration)", body)
	}
}

// TestUpsertTagBudgetUnchangedIsReadOnly — the operator re-asserts every
// Environment budget on each resync; an identical, already-bound ceiling
// must cost exactly one /tag/info and no write.
func TestUpsertTagBudgetUnchangedIsReadOnly(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"environment:demo":{"spend":1,"litellm_budget_table":`+
			`{"budget_id":"environment:demo","max_budget":50,"budget_duration":"30d"}}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "environment:demo",
		TagBudget{MaxBudget: 50, BudgetDuration: "30d"}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/tag/info" {
		t.Fatalf("request sequence = %s, want only /tag/info", got)
	}
}

// TestUpsertTagBudgetRebindsForeignTag — LiteLLM auto-creates budgetless
// tag rows from x-litellm-tags traffic, and /tag/update can never bind a
// budget_id (500, measured), so such a tag is deleted and recreated. Spend
// survives: it lives in LiteLLM_DailyTagSpend, keyed by tag name.
func TestUpsertTagBudgetRebindsForeignTag(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		switch captured[i].Path {
		case "/tag/info":
			fmt.Fprint(w, `{"environment:demo":{"spend":2,"litellm_budget_table":{"budget_id":"6b1f-uuid","max_budget":3}}}`)
		default:
			fmt.Fprint(w, `{"message":"ok"}`)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "environment:demo", TagBudget{MaxBudget: 3}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/tag/info,/budget/new,/tag/delete,/tag/new" {
		t.Fatalf("request sequence = %s", got)
	}
}

// TestUpsertTagBudgetUpdateErrorSurfaces — both calls failing is an error.
func TestUpsertTagBudgetUpdateErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 5}); err == nil {
		t.Fatal("want an error when LiteLLM fails")
	}
}

// TestTagInfoReadsNestedBudget — /tag/info returns a map keyed by tag name
// with the budget object nested under litellm_budget_table.
func TestTagInfoReadsNestedBudget(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"user:a@b":{"name":"user:a@b",`+
			`"litellm_budget_table":{"max_budget":10,"budget_duration":"30d","budget_reset_at":"2026-10-31T23:00:00Z"}}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.TagInfo(context.Background(), "user:a@b")
	if err != nil {
		t.Fatalf("TagInfo: %v", err)
	}
	if got == nil || got.Budget == nil || got.Budget.MaxBudget != 10 ||
		got.Budget.BudgetDuration != "30d" || got.Budget.BudgetResetAt != "2026-10-31T23:00:00Z" {
		t.Fatalf("got %+v", got)
	}
	if captured[0].Path != "/tag/info" || !strings.Contains(string(captured[0].Body), `"names":["user:a@b"]`) {
		t.Errorf("request = %s %s", captured[0].Path, captured[0].Body)
	}
}

// TestTagInfoAbsentTagIsNilNotError — an unknown tag is "no budget yet".
func TestTagInfoAbsentTagIsNilNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.TagInfo(context.Background(), "user:missing@b")
	if err != nil || got != nil {
		t.Fatalf("got (%+v, %v), want (nil, nil)", got, err)
	}
}

// TestTagInfoUnknownTagIs500WithA404Body — the shape LiteLLM ACTUALLY
// answers for an unknown tag (measured on the e2e cluster 2026-09-22):
// HTTP 500 whose body carries a 404. Neither the status nor the error
// string says 404, so a naive check treats "no budget yet" as an outage —
// which broke UpsertTagBudget for every tag LiteLLM had never seen.
func TestTagInfoUnknownTagIs500WithA404Body(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"detail":"404: Tags not found: ['key:ekid_01m34qxs9e79bea01j0v26jq8g']"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.TagInfo(context.Background(), "key:ekid_01m34qxs9e79bea01j0v26jq8g")
	if err != nil || got != nil {
		t.Fatalf("got (%+v, %v), want (nil, nil)", got, err)
	}
}

// TestTagInfoRealErrorStillSurfaces — an outage must NOT be read as "no
// tag": only a body that says so counts as absent.
func TestTagInfoRealErrorStillSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"detail":"database connection failed"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if _, err := c.TagInfo(context.Background(), "key:ek_1"); err == nil {
		t.Fatal("want an error when LiteLLM genuinely fails")
	}
}

// TestUpsertTagBudgetCreatesTagLiteLLMNeverSaw — the end-to-end of the two
// tests above: a brand-new tag (500/404 from /tag/info) must still get its
// budget created and bound, not fail the whole upsert.
func TestUpsertTagBudgetCreatesTagLiteLLMNeverSaw(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		if captured[i].Path == "/tag/info" {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"detail":"404: Tags not found: ['key:ek_new']"}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"message":"ok"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "key:ek_new", TagBudget{MaxBudget: 0}); err != nil {
		t.Fatalf("UpsertTagBudget on a never-seen tag: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/tag/info,/budget/new,/tag/new" {
		t.Fatalf("request sequence = %s", got)
	}
}

// TestDeleteTagByNameIsIdempotent — a 404 from LiteLLM is success.
func TestDeleteTagByNameIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"message":"Tag not found"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.DeleteTagByName(context.Background(), "key:ek_gone"); err != nil {
		t.Fatalf("delete of an absent tag must be nil, got %v", err)
	}
}

// TestDeleteBudgetIsIdempotent — a missing budget object is success.
func TestDeleteBudgetIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"message":"Budget not found"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.DeleteBudget(context.Background(), "key:ek_gone"); err != nil {
		t.Fatalf("delete of an absent budget must be nil, got %v", err)
	}
}

// TestTagSpendPagesAndSums — /tag/info carries no spend (LiteLLM v1.99's
// info_tag never emits it), so the tag's spend comes from
// /tag/daily/activity, whose metadata.total_spend covers ONE page only:
// TagSpend follows has_more and sums every page.
func TestTagSpendPagesAndSums(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		if i == 0 {
			fmt.Fprint(w, `{"results":[],"metadata":{"total_spend":40.5,"has_more":true}}`)
			return
		}
		fmt.Fprint(w, `{"results":[],"metadata":{"total_spend":2.25,"has_more":false}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	// A Europe/Madrid cycle starts at 22:00Z; buckets are whole UTC days,
	// so the start rounds to the NEAREST bucket boundary — 2026-10-01.
	start := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	got, _, err := c.TagSpend(context.Background(), "user:a@b", start, end)
	if err != nil {
		t.Fatalf("TagSpend: %v", err)
	}
	if got != 42.75 {
		t.Fatalf("spend = %v, want 42.75 (both pages)", got)
	}
	if len(captured) != 2 {
		t.Fatalf("requests = %d, want 2", len(captured))
	}
	u, _ := url.Parse(captured[1].Path)
	q := u.Query()
	if u.Path != "/tag/daily/activity" || q.Get("tags") != "user:a@b" || q.Get("start_date") != "2026-10-01" ||
		q.Get("end_date") != "2026-10-27" || q.Get("page") != "2" {
		t.Fatalf("request = %s", captured[1].Path)
	}
}

// TestTagSpendReturnsTheDailySeries — the allowance chart plots the same
// read as the cycle spend beside it, so its days must sum to that figure.
// A day LiteLLM splits across two pages is one point, not two.
func TestTagSpendReturnsTheDailySeries(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		w.WriteHeader(200)
		if i == 0 {
			fmt.Fprint(w, `{"results":[{"date":"2026-10-02","metrics":{"spend":1.5}},{"date":"2026-10-01","metrics":{"spend":2}}],"metadata":{"total_spend":3.5,"has_more":true}}`)
			return
		}
		fmt.Fprint(w, `{"results":[{"date":"2026-10-02","metrics":{"spend":0.25}},{"date":"2026-10-03","metrics":{"spend":4}}],"metadata":{"total_spend":4.25,"has_more":false}}`)
	}))
	defer srv.Close()

	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	spend, series, err := newTestClient(t, srv.URL).TagSpend(context.Background(), "user:a@b", start, start.AddDate(0, 0, 2))
	if err != nil {
		t.Fatalf("TagSpend: %v", err)
	}
	want := []observability.DaySpend{{Date: "2026-10-01", Spend: 2}, {Date: "2026-10-02", Spend: 1.75}, {Date: "2026-10-03", Spend: 4}}
	if !reflect.DeepEqual(series, want) {
		t.Fatalf("series = %+v, want %+v", series, want)
	}
	sum := 0.0
	for _, d := range series {
		sum += d.Spend
	}
	if sum != spend {
		t.Fatalf("series sums to %v, spend = %v", sum, spend)
	}
}

// TestTagInfoEntrySpendWindowStart — LiteLLM zeroes a tag's spend at
// budget_reset_at and schedules the next reset on a CALENDAR grid
// (duration_parser.get_next_standardized_reset_time): "30d" is the 1st of
// the month, "7d"/"1w" a Monday, other day counts N days on. The running
// cycle began one such step before reset_at — but never before the tag
// existed, since the tag's spend starts at 0 when it is created. A ceiling
// with no duration never resets: its spend runs from the tag's creation.
func TestTagInfoEntrySpendWindowStart(t *testing.T) {
	cases := []struct {
		name   string
		entry  TagInfoEntry
		want   string
		wantOK bool
	}{
		// Measured 2026-10-27 (kind, v1.99.1): a "30d" budget made that day resets 2026-11-01.
		{"30d is a calendar month", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "30d", BudgetResetAt: "2026-11-01T00:00:00Z"}}, "2026-10-01T00:00:00Z", true},
		// Production (2026-10-27, LiteLLM timezone Europe/Madrid): midnight on
		// the 1st is 23:00Z the day before. A naive AddDate lands on "Sep 31"
		// = Oct 1 and loses the whole first day of the cycle.
		{"30d in a zone east of UTC", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "30d", BudgetResetAt: "2026-10-31T23:00:00Z"}}, "2026-09-30T23:00:00Z", true},
		// A timestamp without a zone (Python's naive isoformat) is UTC, not unreadable.
		{"naive timestamps are UTC", TagInfoEntry{CreatedAt: "2026-09-01T00:00:00.123456", Budget: &TagBudget{BudgetDuration: "30d", BudgetResetAt: "2026-11-01T00:00:00"}}, "2026-10-01T00:00:00Z", true},
		{"other day counts", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "10d", BudgetResetAt: "2026-11-01T00:00:00Z"}}, "2026-10-22T00:00:00Z", true},
		{"tag younger than the cycle", TagInfoEntry{CreatedAt: "2026-10-27T10:44:18.671000+00:00",
			Budget: &TagBudget{BudgetDuration: "30d", BudgetResetAt: "2026-11-01T00:00:00Z"}}, "2026-10-27T10:44:18Z", true},
		{"offset form", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "12h", BudgetResetAt: "2026-10-31T23:00:00+00:00"}}, "2026-10-31T11:00:00Z", true},
		{"weeks", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "2w", BudgetResetAt: "2026-10-31T00:00:00Z"}}, "2026-10-17T00:00:00Z", true},
		{"months", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "1mo", BudgetResetAt: "2026-11-01T00:00:00Z"}}, "2026-10-01T00:00:00Z", true},
		{"lifetime ceiling", TagInfoEntry{CreatedAt: "2026-09-22T10:00:00Z", Budget: &TagBudget{MaxBudget: 5}}, "2026-09-22T10:00:00Z", true},
		{"no budget", TagInfoEntry{CreatedAt: "2026-09-22T10:00:00Z"}, "2026-09-22T10:00:00Z", true},
		{"unknown unit", TagInfoEntry{Budget: &TagBudget{BudgetDuration: "30x", BudgetResetAt: "2026-10-31T23:00:00Z"}}, "", false},
		{"nothing to go on", TagInfoEntry{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.entry.SpendWindowStart()
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatalf("start = %s, want %s", got.UTC().Format(time.RFC3339), tc.want)
			}
		})
	}
}

// TestTagInfoReadsCreatedAt — created_at anchors a lifetime ceiling's window.
func TestTagInfoReadsCreatedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"user:a@b":{"name":"user:a@b","created_at":"2026-09-22T10:00:00+00:00"}}`)
	}))
	defer srv.Close()
	got, err := newTestClient(t, srv.URL).TagInfo(context.Background(), "user:a@b")
	if err != nil || got == nil || got.CreatedAt != "2026-09-22T10:00:00+00:00" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// TestTagSpendNeverAsksForAReversedRange — a tag created after 12:00Z today
// rounds its start to TOMORROW; asking LiteLLM for start > end would answer
// nothing (or 400) and hide today's real spend. The start clamps to end.
func TestTagSpendNeverAsksForAReversedRange(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"results":[],"metadata":{"total_spend":0.5,"has_more":false}}`)
	}))
	defer srv.Close()

	start := time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	if _, _, err := newTestClient(t, srv.URL).TagSpend(context.Background(), "user:a@b", start, end); err != nil {
		t.Fatalf("TagSpend: %v", err)
	}
	u, _ := url.Parse(captured[0].Path)
	if q := u.Query(); q.Get("start_date") != "2026-10-27" || q.Get("end_date") != "2026-10-27" {
		t.Fatalf("range = %s..%s, want 2026-10-27..2026-10-27", q.Get("start_date"), q.Get("end_date"))
	}
}
