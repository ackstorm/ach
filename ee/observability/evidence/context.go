// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"strings"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Context summarises the caller's own generations over the whole window: input
// size, input/output ratio, cache-read share, reasoning share and tool use.
// Every ratio has its own matched population (rows that carry both sides), and
// a measurement no row carries stays null, never an observed zero.
//
// usage_details semantics, measured on Langfuse events_full: input_cached_tokens
// is part of input, and completion_details.reasoning is part of output (total is
// always input + output), so both are shares and never added to a total.

// ponytail: aggregate per request, no cache; add one when the console polls it.

var contextBands = []string{"<50K", "50–100K", "100–200K", "200–300K", ">=300K"}

// mapContains is what tells a missing measurement from an observed 0: reading
// an absent key returns 0.
const contextUsageSQL = `
WITH if(mapContains(usage_details, 'input'), toNullable(usage_details['input']), NULL) AS input_tokens,
     if(mapContains(usage_details, 'output'), toNullable(usage_details['output']), NULL) AS output_tokens,
     if(mapContains(usage_details, 'input_cached_tokens'), toNullable(usage_details['input_cached_tokens']), NULL) AS cache_tokens,
     if(mapContains(usage_details, 'input_cache_creation'), toNullable(usage_details['input_cache_creation']), NULL) AS cache_creation,
     if(mapContains(usage_details, 'completion_details.reasoning'), toNullable(usage_details['completion_details.reasoning']), NULL) AS reasoning
SELECT count() AS generation_count,
       count(input_tokens) AS input_coverage, count(output_tokens) AS output_coverage, count(cache_tokens) AS cache_coverage,
       avgOrNull(input_tokens) AS input_mean, medianExactOrNull(input_tokens) AS input_median, maxOrNull(input_tokens) AS input_max,
       countIf(input_tokens IS NOT NULL AND output_tokens IS NOT NULL) AS pair_coverage,
       sumOrNullIf(input_tokens, output_tokens IS NOT NULL) AS pair_input,
       sumOrNullIf(output_tokens, input_tokens IS NOT NULL) AS pair_output,
       sumOrNullIf(input_tokens - ifNull(cache_tokens, 0), output_tokens IS NOT NULL) AS pair_uncached_input,
       countIf(input_tokens IS NOT NULL AND cache_tokens IS NOT NULL) AS cache_pair_coverage,
       sumOrNullIf(input_tokens, cache_tokens IS NOT NULL) AS cache_pair_input,
       sumOrNullIf(cache_tokens, input_tokens IS NOT NULL) AS cache_pair_read,
       sumOrNull(cache_tokens) AS cache_read_tokens,
       count(cache_creation) AS cache_creation_coverage, sumOrNull(cache_creation) AS cache_creation_tokens,
       countIf(reasoning IS NOT NULL AND output_tokens IS NOT NULL) AS reasoning_coverage,
       sumOrNullIf(reasoning, output_tokens IS NOT NULL) AS reasoning_tokens,
       sumOrNullIf(output_tokens, reasoning IS NOT NULL) AS reasoning_output,
       countIf(input_tokens < 50000) AS band0,
       countIf(input_tokens >= 50000 AND input_tokens < 100000) AS band1,
       countIf(input_tokens >= 100000 AND input_tokens < 200000) AS band2,
       countIf(input_tokens >= 200000 AND input_tokens < 300000) AS band3,
       countIf(input_tokens >= 300000) AS band4,
       formatDateTime(minOrNull(start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS first,
       formatDateTime(maxOrNull(start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS last
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION'
FORMAT JSON`

const contextToolsSQL = `
SELECT tool_name AS name, count() AS count, sum(count()) OVER () AS total_invocations,
       sum(if(match(tool_name, '(?i)(^|:)skill$'), count(), 0)) OVER () AS skill_invocations,
       sum(if(match(tool_name, '(?i)(^|:)(agent|task)$'), count(), 0)) OVER () AS subagent_invocations
FROM (
  SELECT tool_call_names FROM default.events_full AS source FINAL
  WHERE is_deleted = 0 AND (__SCOPE__)
    AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
    AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
)
ARRAY JOIN tool_call_names AS tool_name
GROUP BY tool_name ORDER BY count DESC, name LIMIT 101
FORMAT JSON`

// contextNamedSQL names what the Skill and Agent/Task tools were asked for,
// from each call's arguments: Claude Code sends {"skill": ...} (OpenCode
// {"name": ...}) and {"subagent_type": ...}. Names and calls pair by index; a
// call without a name is not counted here (it still counts as an invocation).
const contextNamedSQL = `
SELECT kind, name, count() AS count FROM (
  SELECT tool_call_names[i] AS tool, tool_calls[i] AS call,
         if(match(tool, '(?i)(^|:)skill$'), 'skill', 'subagent') AS kind,
         if(JSONType(call, 'arguments') = 'String', JSONExtractString(call, 'arguments'), JSONExtractRaw(call, 'arguments')) AS args,
         if(kind = 'skill',
            coalesce(nullIf(JSONExtractString(args, 'skill'), ''), nullIf(JSONExtractString(args, 'name'), ''), nullIf(JSONExtractString(args, 'command'), '')),
            nullIf(JSONExtractString(args, 'subagent_type'), '')) AS name
  FROM (
    SELECT tool_call_names, tool_calls FROM default.events_full AS source FINAL
    WHERE is_deleted = 0 AND (__SCOPE__)
      AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
      AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  )
  ARRAY JOIN arrayEnumerate(tool_call_names) AS i
  WHERE match(tool, '(?i)(^|:)(skill|agent|task)$')
)
WHERE name IS NOT NULL
GROUP BY kind, name ORDER BY count DESC, name LIMIT 20 BY kind
FORMAT JSON`

type ContextReport struct {
	Window                  core.Window     `json:"window"`
	Provenance              core.Provenance `json:"provenance"`
	ToolProvenance          core.Provenance `json:"tool_provenance"`
	GenerationCount         *int            `json:"generation_count"`
	Eligibility             string          `json:"eligibility"`
	InputSummary            InputSummary    `json:"input_summary"`
	TokenRatio              TokenRatio      `json:"token_ratio"`
	CacheReadShare          Share           `json:"cache_read_share"`
	Reasoning               Reasoning       `json:"reasoning"`
	ContextBands            []NameCount     `json:"context_bands"`
	InputCoverage           int             `json:"input_coverage"`
	CacheReadTokens         *float64        `json:"cache_read_tokens"`
	CacheCoverage           int             `json:"cache_coverage"`
	CacheCreationTokens     *float64        `json:"cache_creation_tokens"`
	CacheCreationCoverage   int             `json:"cache_creation_coverage"`
	Invocations             []NameCount     `json:"invocations"`
	InvocationCount         *int            `json:"invocation_count"`
	InvocationsTruncated    bool            `json:"invocations_truncated"`
	SkillInvocationCount    *int            `json:"skill_invocation_count"`
	SubagentInvocationCount *int            `json:"subagent_invocation_count"`
	// Skills and Subagents name the top 20 of each, from the call arguments;
	// null when that read failed.
	Skills           []NameCount     `json:"skills"`
	Subagents        []NameCount     `json:"subagents"`
	SourceCoverage   *SourceCoverage `json:"source_coverage,omitempty"`
	ExecutionSuccess *bool           `json:"execution_success"` // always null: tool success is not observed
}

type InputSummary struct {
	Mean   *float64 `json:"mean"`
	Median *float64 `json:"median"`
	Max    *float64 `json:"max"`
}

// TokenRatio is input over output on generations carrying both. Uncached
// excludes cache-read tokens (input includes them): the part of the input
// the provider processed at full price.
type TokenRatio struct {
	InputTokens         *float64 `json:"input_tokens"`
	OutputTokens        *float64 `json:"output_tokens"`
	Value               *float64 `json:"value"`
	UncachedInputTokens *float64 `json:"uncached_input_tokens"`
	UncachedValue       *float64 `json:"uncached_value"`
	Coverage            int      `json:"coverage"`
}

type Share struct {
	Value    *float64 `json:"value"`
	Coverage int      `json:"coverage"`
}

// Reasoning is the reasoning part of output, over generations carrying both.
type Reasoning struct {
	Tokens   *float64 `json:"tokens"`
	Share    *float64 `json:"share"`
	Coverage int      `json:"coverage"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type SourceCoverage struct {
	GenerationCount int `json:"generation_count"`
	Input           int `json:"input"`
	Output          int `json:"output"`
	Cache           int `json:"cache"`
}

type usageAgg struct {
	GenerationCount       float64  `json:"generation_count"`
	InputCoverage         float64  `json:"input_coverage"`
	OutputCoverage        float64  `json:"output_coverage"`
	CacheCoverage         float64  `json:"cache_coverage"`
	InputMean             *float64 `json:"input_mean"`
	InputMedian           *float64 `json:"input_median"`
	InputMax              *float64 `json:"input_max"`
	PairCoverage          float64  `json:"pair_coverage"`
	PairInput             *float64 `json:"pair_input"`
	PairOutput            *float64 `json:"pair_output"`
	PairUncachedInput     *float64 `json:"pair_uncached_input"`
	CachePairCoverage     float64  `json:"cache_pair_coverage"`
	CachePairInput        *float64 `json:"cache_pair_input"`
	CachePairRead         *float64 `json:"cache_pair_read"`
	CacheReadTokens       *float64 `json:"cache_read_tokens"`
	CacheCreationCoverage float64  `json:"cache_creation_coverage"`
	CacheCreationTokens   *float64 `json:"cache_creation_tokens"`
	ReasoningCoverage     float64  `json:"reasoning_coverage"`
	ReasoningTokens       *float64 `json:"reasoning_tokens"`
	ReasoningOutput       *float64 `json:"reasoning_output"`
	Band0                 float64  `json:"band0"`
	Band1                 float64  `json:"band1"`
	Band2                 float64  `json:"band2"`
	Band3                 float64  `json:"band3"`
	Band4                 float64  `json:"band4"`
}

type toolRow struct {
	Name                string   `json:"name"`
	Count               float64  `json:"count"`
	TotalInvocations    float64  `json:"total_invocations"`
	SkillInvocations    *float64 `json:"skill_invocations"`
	SubagentInvocations *float64 `json:"subagent_invocations"`
}

func intPtr(f float64) *int { n := int(f); return &n }

func (r *Reader) Context(ctx context.Context, s core.Scope, w core.Window) (ContextReport, error) {
	if !s.Valid() {
		return ContextReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return ContextReport{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return ContextReport{}, core.ErrProjectNotAllowed
	}
	rep := ContextReport{
		Window: w, Eligibility: "observed_generations_not_logical_requests",
		ContextBands: []NameCount{}, Invocations: []NameCount{},
	}
	params := map[string]string{}
	core.WindowParams(w, params)

	var usage []usageAgg
	if err := r.CH().Query(ctx, core.Scoped(contextUsageSQL, r.Projects(), s, params), params, 1, &usage); err != nil || len(usage) != 1 {
		rep.Provenance = core.UnavailableProvenance(w, "observed_generation", core.SourceUnavailable)
		rep.ToolProvenance = core.UnavailableProvenance(w, "tool_invocation", core.SourceUnavailable)
		return rep, nil
	}
	u := usage[0]
	n := int(u.GenerationCount)
	rep.GenerationCount = &n
	rep.InputSummary = InputSummary{Mean: u.InputMean, Median: u.InputMedian, Max: u.InputMax}
	rep.TokenRatio = TokenRatio{InputTokens: u.PairInput, OutputTokens: u.PairOutput, Value: core.Ratio(u.PairInput, u.PairOutput), UncachedInputTokens: u.PairUncachedInput, UncachedValue: core.Ratio(u.PairUncachedInput, u.PairOutput), Coverage: int(u.PairCoverage)}
	rep.CacheReadShare = Share{Value: core.Ratio(u.CachePairRead, u.CachePairInput), Coverage: int(u.CachePairCoverage)}
	rep.Reasoning = Reasoning{Tokens: u.ReasoningTokens, Share: core.Ratio(u.ReasoningTokens, u.ReasoningOutput), Coverage: int(u.ReasoningCoverage)}
	if rep.Reasoning.Share != nil && *rep.Reasoning.Share > 1 { // a few upstream rows report reasoning > output
		one := 1.0
		rep.Reasoning.Share = &one
	}
	for i, c := range []float64{u.Band0, u.Band1, u.Band2, u.Band3, u.Band4} {
		rep.ContextBands = append(rep.ContextBands, NameCount{Name: contextBands[i], Count: int(c)})
	}
	rep.InputCoverage, rep.CacheCoverage = int(u.InputCoverage), int(u.CacheCoverage)
	rep.CacheReadTokens = u.CacheReadTokens
	rep.CacheCreationTokens, rep.CacheCreationCoverage = u.CacheCreationTokens, int(u.CacheCreationCoverage)
	rep.SourceCoverage = &SourceCoverage{GenerationCount: n, Input: rep.InputCoverage, Output: int(u.OutputCoverage), Cache: rep.CacheCoverage}
	rep.Provenance = core.NewProvenance(w, "observed_generation", "all scoped deduplicated source observations in requested window", "full-window-final-aggregate")
	rep.Provenance.Coverage = core.Coverage{Numerator: n, Denominator: &n}

	params = map[string]string{}
	core.WindowParams(w, params)
	var tools []toolRow
	if err := r.CH().Query(ctx, core.Scoped(contextToolsSQL, r.Projects(), s, params), params, core.GroupCap+1, &tools); err != nil {
		rep.ToolProvenance = core.UnavailableProvenance(w, "tool_invocation", core.SourceUnavailable)
		return rep, nil
	}
	shown := 0
	for i, t := range tools {
		if i == core.GroupCap {
			break
		}
		name := t.Name
		if len(name) > 200 {
			name = name[:200]
		}
		rep.Invocations = append(rep.Invocations, NameCount{Name: strings.TrimSpace(name), Count: int(t.Count)})
		shown += int(t.Count)
	}
	total := 0
	zero := 0
	rep.SkillInvocationCount, rep.SubagentInvocationCount = &zero, &zero
	if len(tools) > 0 {
		total = int(tools[0].TotalInvocations)
		if t := tools[0].SkillInvocations; t != nil {
			rep.SkillInvocationCount = intPtr(*t)
		}
		if t := tools[0].SubagentInvocations; t != nil {
			rep.SubagentInvocationCount = intPtr(*t)
		}
	}
	params = map[string]string{}
	core.WindowParams(w, params)
	var named []struct {
		Kind  string  `json:"kind"`
		Name  string  `json:"name"`
		Count float64 `json:"count"`
	}
	if err := r.CH().Query(ctx, core.Scoped(contextNamedSQL, r.Projects(), s, params), params, 40, &named); err == nil {
		rep.Skills, rep.Subagents = []NameCount{}, []NameCount{}
		for _, n := range named {
			nc := NameCount{Name: strings.TrimSpace(n.Name), Count: int(n.Count)}
			if len(nc.Name) > 200 {
				nc.Name = nc.Name[:200]
			}
			if n.Kind == "skill" {
				rep.Skills = append(rep.Skills, nc)
			} else {
				rep.Subagents = append(rep.Subagents, nc)
			}
		}
	}
	rep.InvocationCount = &total
	rep.InvocationsTruncated = len(tools) > core.GroupCap
	rep.ToolProvenance = core.NewProvenance(w, "tool_invocation", "all scoped deduplicated source observations in requested window", "full-window-final-aggregate")
	rep.ToolProvenance.Coverage = core.Coverage{Numerator: shown, Denominator: &shown}
	if rep.InvocationsTruncated {
		reason := "group_cap"
		rep.ToolProvenance.State, rep.ToolProvenance.Reason = core.StatePartial, &reason
		rep.ToolProvenance.Coverage = core.Coverage{Numerator: shown, Denominator: &total}
	}
	return rep, nil
}
