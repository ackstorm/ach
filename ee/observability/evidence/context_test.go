// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

func usageRow(fields string) string { return page(`{` + fields + `}`) }

const fullUsage = `"generation_count":1000,"input_coverage":1000,"output_coverage":1000,"cache_coverage":500,
"input_mean":10,"input_median":10,"input_max":40,
"pair_coverage":1000,"pair_input":10000,"pair_output":400,"pair_uncached_input":2000,
"cache_pair_coverage":500,"cache_pair_input":5000,"cache_pair_read":4500,
"cache_read_tokens":4500,"cache_creation_coverage":400,"cache_creation_tokens":0,
"reasoning_coverage":300,"reasoning_tokens":100,"reasoning_output":200,
"band0":990,"band1":10,"band2":0,"band3":0,"band4":0,
"first":"2026-10-01T00:00:00.000000Z","last":"2026-10-30T00:00:00.000000Z"`

func readContext(t *testing.T, bodies ...string) (ContextReport, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).Context(context.Background(), scope(t, "Ana@Corp.com"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	return rep, calls
}

func TestContextScopesAndBoundsEveryQuery(t *testing.T) {
	// ports: test_full_window_context_preserves_missing_and_zero_and_scopes — scope, FINAL, no LIMIT on the aggregate
	rep, calls := readContext(t, usageRow(fullUsage), page(`{"name":"Skill","count":7,"total_invocations":7,"skill_invocations":7,"subagent_invocations":0}`),
		page(`{"kind":"skill","name":"git:commit","count":5}`, `{"kind":"subagent","name":"Explore","count":2}`))
	if len(*calls) != 3 {
		t.Fatalf("calls = %d, want usage + tools + named", len(*calls))
	}
	if fmt.Sprint(rep.Skills, rep.Subagents) != "[{git:commit 5}] [{Explore 2}]" {
		t.Errorf("skills = %v subagents = %v", rep.Skills, rep.Subagents)
	}
	for i, c := range *calls {
		if c.Q.Get("param_subject0") != "ana@corp.com" || c.Q.Get("param_project0") != "p1" || c.Q.Get("readonly") != "2" {
			t.Errorf("call %d params = %v", i, c.Q)
		}
		if !strings.Contains(c.Body, " FINAL") || !strings.Contains(c.Body, "user_id={subject0:String}") || strings.Contains(c.Body, "ana@corp.com") {
			t.Errorf("call %d is not scoped by bound parameters", i)
		}
	}
	if strings.Contains((*calls)[0].Body, "LIMIT") {
		t.Error("the usage aggregate must cover the whole window")
	}
}

func TestContextRatiosUseMatchedPopulations(t *testing.T) {
	// ports: test_full_window_context_preserves_missing_and_zero_and_scopes — each ratio has its own matched population
	rep, _ := readContext(t, usageRow(fullUsage), page())
	if rep.TokenRatio.Value == nil || *rep.TokenRatio.Value != 25 || rep.TokenRatio.UncachedValue == nil || *rep.TokenRatio.UncachedValue != 5 || rep.TokenRatio.Coverage != 1000 {
		t.Errorf("token ratio = %+v", rep.TokenRatio)
	}
	if rep.CacheReadShare.Value == nil || *rep.CacheReadShare.Value != 0.9 || rep.CacheReadShare.Coverage != 500 {
		t.Errorf("cache share = %+v", rep.CacheReadShare)
	}
	if rep.CacheCreationTokens == nil || *rep.CacheCreationTokens != 0 || rep.CacheCreationCoverage != 400 {
		t.Errorf("cache creation: an observed 0 must stay 0, got %v / %d", rep.CacheCreationTokens, rep.CacheCreationCoverage)
	}
	if rep.Reasoning.Share == nil || *rep.Reasoning.Share != 0.5 || rep.Reasoning.Coverage != 300 {
		t.Errorf("reasoning = %+v (share of the output of the same generations)", rep.Reasoning)
	}
	if rep.GenerationCount == nil || *rep.GenerationCount != 1000 || rep.Provenance.State != "available" || rep.Provenance.Sampled {
		t.Errorf("rep = %+v", rep)
	}
}

func TestContextZeroOutputMakesRatioUndefinedNotZero(t *testing.T) {
	// ports: test_full_window_context_preserves_missing_and_zero_and_scopes — zero output, missing cache
	rep, _ := readContext(t, usageRow(`"generation_count":1000,"input_coverage":1000,"pair_coverage":1000,"pair_input":10000,"pair_output":0,"cache_coverage":0,"cache_read_tokens":null,"cache_pair_input":null,"cache_pair_read":null`), page())
	if rep.TokenRatio.Value != nil || rep.TokenRatio.OutputTokens == nil || *rep.TokenRatio.OutputTokens != 0 {
		t.Errorf("token ratio = %+v", rep.TokenRatio)
	}
	if rep.CacheReadTokens != nil || rep.CacheReadShare.Value != nil {
		t.Errorf("missing cache must be null, got %v / %v", rep.CacheReadTokens, rep.CacheReadShare.Value)
	}
}

func TestContextReasoningAboveOutputIsCapped(t *testing.T) {
	// ports: none — live data has a few rows with reasoning > output; a share above 100% is impossible
	rep, _ := readContext(t, usageRow(`"generation_count":2,"reasoning_coverage":2,"reasoning_tokens":30,"reasoning_output":20`), page())
	if rep.Reasoning.Share == nil || *rep.Reasoning.Share != 1 {
		t.Errorf("reasoning share = %v, want capped at 1", rep.Reasoning.Share)
	}
}

func TestContextEmptySourceHasZeroCountsAndNullMeasurements(t *testing.T) {
	// ports: test_observed_empty_source_has_zero_counts_and_null_measurements
	rep, _ := readContext(t, usageRow(`"generation_count":0,"input_coverage":0,"cache_coverage":0,"input_mean":null,"pair_input":null,"pair_output":null`), page())
	if rep.GenerationCount == nil || *rep.GenerationCount != 0 || rep.InputSummary.Mean != nil || rep.TokenRatio.InputTokens != nil {
		t.Errorf("rep = %+v", rep)
	}
	if rep.Provenance.Coverage.Numerator != 0 || rep.Provenance.Coverage.Denominator == nil || *rep.Provenance.Coverage.Denominator != 0 {
		t.Errorf("coverage = %+v", rep.Provenance.Coverage)
	}
	if rep.Invocations == nil || rep.ContextBands == nil {
		t.Error("arrays must be [], never null")
	}
}

func TestContextFailedSourceIsNotObservedEmpty(t *testing.T) {
	// ports: test_failed_source_is_not_observed_empty
	ch, calls := scriptedCH(t, `{"exception":"boom"}`)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).Context(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if rep.GenerationCount != nil || rep.Provenance.Reason == nil || *rep.Provenance.Reason != core.SourceUnavailable {
		t.Errorf("rep = %+v", rep)
	}
	if rep.ToolProvenance.Reason == nil || len(*calls) != 1 {
		t.Errorf("a failed usage read must not issue the tools read; calls=%d", len(*calls))
	}
}

func TestContextToolGroupCapDoesNotCapTotals(t *testing.T) {
	// ports: test_tool_group_limit_does_not_cap_invocation_total, test_exact_skill_totals_survive_truncated_names_and_cache_creation_keeps_coverage
	rows := make([]string, 101)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"name":"tool-%d","count":10,"total_invocations":2000,"skill_invocations":71,"subagent_invocations":24}`, i)
	}
	rep, _ := readContext(t, usageRow(fullUsage), page(rows...))
	if len(rep.Invocations) != 100 || !rep.InvocationsTruncated || rep.InvocationCount == nil || *rep.InvocationCount != 2000 {
		t.Errorf("invocations = %d truncated=%v count=%v", len(rep.Invocations), rep.InvocationsTruncated, rep.InvocationCount)
	}
	if rep.SkillInvocationCount == nil || *rep.SkillInvocationCount != 71 || rep.SubagentInvocationCount == nil || *rep.SubagentInvocationCount != 24 {
		t.Errorf("skill/subagent = %v / %v", rep.SkillInvocationCount, rep.SubagentInvocationCount)
	}
	if rep.ToolProvenance.Reason == nil || *rep.ToolProvenance.Reason != "group_cap" || rep.ToolProvenance.Coverage.Denominator == nil || *rep.ToolProvenance.Coverage.Denominator != 2000 {
		t.Errorf("tool provenance = %+v", rep.ToolProvenance)
	}
}

func TestContextRejectsBadScopeAndWindowWithoutQuery(t *testing.T) {
	// ports: test_unverified_scope_never_queries, test_month_window_rejected_before_source_access
	ch, calls := scriptedCH(t)
	r := NewReader(core.NewReader(ch, []string{"p1"}))
	if _, err := r.Context(context.Background(), core.Scope{}, testWindow()); !errors.Is(err, core.ErrNoScope) {
		t.Errorf("zero scope err = %v", err)
	}
	w := testWindow()
	w.End = w.Start.AddDate(0, 0, 32)
	if _, err := r.Context(context.Background(), scope(t, "a@x"), w); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("32-day window err = %v", err)
	}
	if _, err := NewReader(core.NewReader(ch, nil)).Context(context.Background(), scope(t, "a@x"), testWindow()); !errors.Is(err, core.ErrProjectNotAllowed) {
		t.Errorf("no projects err = %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("rejected reads issued %d queries", len(*calls))
	}
}

func TestContextMatchesWireContract(t *testing.T) {
	rep, _ := readContext(t, usageRow(fullUsage), page(`{"name":"Skill","count":7,"total_invocations":7,"skill_invocations":7,"subagent_invocations":0}`))
	b, _ := json.Marshal(rep)
	var v any
	_ = json.Unmarshal(b, &v)
	s, err := jsonschema.NewCompiler().Compile("testdata/contracts/context.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(v); err != nil {
		t.Errorf("context does not match the contract: %v\n%s", err, b)
	}
}
