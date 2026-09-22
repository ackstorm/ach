// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if got := strings.Join(paths(captured), ","); got != "/budget/new,/tag/info,/tag/new" {
		t.Fatalf("request sequence = %s", got)
	}
	for _, want := range []string{`"budget_id":"user:a@b"`, `"max_budget":10`, `"budget_duration":"30d"`} {
		if !strings.Contains(string(captured[0].Body), want) {
			t.Errorf("/budget/new body %s missing %s", captured[0].Body, want)
		}
	}
	if body := string(captured[2].Body); !strings.Contains(body, `"name":"user:a@b"`) ||
		!strings.Contains(body, `"budget_id":"user:a@b"`) {
		t.Errorf("/tag/new body = %s", body)
	}
}

// TestUpsertTagBudgetUpdatesExistingBudgetAndKeepsBoundTag — /budget/new
// 400s on an existing id, so the ceiling is written with /budget/update;
// a tag already pointing at that budget is left alone (no delete/recreate).
func TestUpsertTagBudgetUpdatesExistingBudgetAndKeepsBoundTag(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		switch captured[i].Path {
		case "/budget/new":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"detail":{"error":"Budget with id 'user:a@b' already exists."}}`)
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
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 5}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if got := strings.Join(paths(captured), ","); got != "/budget/new,/budget/update,/tag/info" {
		t.Fatalf("request sequence = %s", got)
	}
	if strings.Contains(string(captured[1].Body), "budget_duration") {
		t.Errorf("empty duration must be omitted, got %s", captured[1].Body)
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
	if got := strings.Join(paths(captured), ","); got != "/budget/new,/tag/info,/tag/delete,/tag/new" {
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
		t.Fatal("want an error when both /tag/new and /tag/update fail")
	}
}

// TestTagInfoReadsNestedBudget — /tag/info returns a map keyed by tag name
// with the budget object nested under litellm_budget_table.
func TestTagInfoReadsNestedBudget(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"user:a@b":{"name":"user:a@b","spend":1.25,`+
			`"litellm_budget_table":{"max_budget":10,"budget_duration":"30d"}}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.TagInfo(context.Background(), "user:a@b")
	if err != nil {
		t.Fatalf("TagInfo: %v", err)
	}
	if got == nil || got.Spend != 1.25 || got.Budget == nil || got.Budget.MaxBudget != 10 ||
		got.Budget.BudgetDuration != "30d" {
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
