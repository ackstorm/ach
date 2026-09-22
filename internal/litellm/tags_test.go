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

// TestUpsertTagBudgetCreates — a brand-new tag takes one POST /tag/new.
func TestUpsertTagBudgetCreates(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(200)
		fmt.Fprint(w, `{"message":"Tag user:a@b created successfully"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 10, BudgetDuration: "30d"}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("want 1 request, got %d", len(captured))
	}
	if captured[0].Path != "/tag/new" {
		t.Errorf("path = %q, want /tag/new", captured[0].Path)
	}
	for _, want := range []string{`"name":"user:a@b"`, `"max_budget":10`, `"budget_duration":"30d"`} {
		if !strings.Contains(string(captured[0].Body), want) {
			t.Errorf("body %s missing %s", captured[0].Body, want)
		}
	}
}

// TestUpsertTagBudgetFallsBackToUpdate — /tag/new on an existing tag fails,
// so the upsert retries against /tag/update with the same body.
func TestUpsertTagBudgetFallsBackToUpdate(t *testing.T) {
	var captured []capturedRequest
	srv := httptest.NewServer(captureMock(t, &captured, func(i int, w http.ResponseWriter) {
		if i == 0 {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"Tag user:a@b already exists"}}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"message":"Tag user:a@b updated successfully"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.UpsertTagBudget(context.Background(), "user:a@b", TagBudget{MaxBudget: 5}); err != nil {
		t.Fatalf("UpsertTagBudget: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("want 2 requests (new then update), got %d", len(captured))
	}
	if captured[1].Path != "/tag/update" {
		t.Errorf("second path = %q, want /tag/update", captured[1].Path)
	}
	if strings.Contains(string(captured[1].Body), "budget_duration") {
		t.Errorf("empty duration must be omitted, got %s", captured[1].Body)
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
