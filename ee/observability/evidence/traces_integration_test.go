//go:build integration

// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

// Runs the trace SQL against a real ClickHouse, on a table with the column
// types and sort key of Langfuse's events_full (only the columns the reader
// touches). The unit tests only see SQL text; these see what it returns.
//
// Run with `make test-integration-pkg PKG=./internal/observability/evidence`.

package evidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const eventsFullDDL = `CREATE TABLE default.events_full (
  project_id String, trace_id String, span_id String, parent_span_id String,
  start_time DateTime64(6), end_time Nullable(DateTime64(6)),
  name String, type LowCardinality(String), user_id String, level LowCardinality(String),
  provided_model_name String,
  usage_details Map(LowCardinality(String), UInt64),
  cost_details Map(LowCardinality(String), Decimal(18, 12)),
  metadata_names Array(String), metadata_values Array(String),
  event_ts DateTime64(6), is_deleted UInt8
) ENGINE = ReplacingMergeTree(event_ts, is_deleted)
ORDER BY (project_id, toStartOfMinute(start_time), xxHash32(trace_id), span_id, start_time)`

func startClickHouse(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Docker; -short specified")
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "clickhouse/clickhouse-server:26.4.5.143",
			ExposedPorts: []string{"8123/tcp"},
			// Access management lets a test create the narrower read-only users.
			Env:        map[string]string{"CLICKHOUSE_USER": "t", "CLICKHOUSE_PASSWORD": "t", "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1"},
			WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("docker required: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	ep, err := c.PortEndpoint(ctx, "8123/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func exec(t *testing.T, url, sql string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(sql))
	req.SetBasicAuth("t", "t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: %s", sql[:40], b)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// row inserts one span; end "" stores a NULL end_time.
func row(project, trace, span, parent, user, typ, name, start, end string) string {
	e := "NULL"
	if end != "" {
		e = "'" + end + "'"
	}
	return fmt.Sprintf("('%s','%s','%s','%s','%s',%s,'%s','%s','%s','DEFAULT','claude-x',map('input',3,'output',4),map('total',0.5),['attributes.gen_ai.response.id'],['req-%s'],now64(6),0)",
		project, trace, span, parent, start, e, name, typ, user, trace)
}

func TestTraceSQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	const in, late, before = "2026-10-02 10:00:00", "2026-10-02 10:00:03", "2026-09-01 10:00:00"
	rows := []string{
		// mine: gateway root + my chat span
		row("p1", "mine", "r", "", "", "SPAN", "POST /v1/chat/completions", in, late),
		row("p1", "mine", "c", "r", "a@x", "GENERATION", "chat claude-x", in, late),
		// shared trace_id: my span and someone else's
		row("p1", "shared", "r", "", "", "SPAN", "POST", in, late),
		row("p1", "shared", "c1", "r", "a@x", "GENERATION", "chat claude-x", in, late),
		row("p1", "shared", "c2", "r", "b@x", "GENERATION", "chat claude-x", in, late),
		// someone else's span exists only outside the window: list and detail must agree
		row("p1", "straddle", "r", "", "", "SPAN", "POST", in, late),
		row("p1", "straddle", "c", "r", "a@x", "GENERATION", "chat claude-x", in, late),
		row("p1", "straddle", "old", "", "b@x", "GENERATION", "chat claude-x", before, before),
		// unfinished root: NULL end_time must stay null
		row("p1", "open", "r", "", "", "SPAN", "POST", in, ""),
		row("p1", "open", "c", "r", "a@x", "GENERATION", "chat claude-x", in, late),
		// not mine at all
		row("p1", "theirs", "c", "", "b@x", "GENERATION", "chat claude-x", in, late),
	}
	exec(t, url, "INSERT INTO default.events_full VALUES "+strings.Join(rows, ","))
	exec(t, url, "ALTER TABLE default.events_full ADD COLUMN session_id String") // detail reads it; '' = no session

	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "t", Password: "t"}, nil), []string{"p1"}))
	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	ctx := context.Background()

	pg, err := r.List(ctx, s, TraceQuery{Window: w, Limit: 50, Status: "all"})
	if err != nil || pg.Provenance.State != "available" {
		t.Fatalf("list err=%v provenance=%+v", err, pg.Provenance)
	}
	got := map[string]TraceSummary{}
	for _, tr := range pg.Traces {
		got[tr.TraceID] = tr
	}
	for _, id := range []string{"mine", "straddle", "open"} {
		if _, ok := got[id]; !ok {
			t.Errorf("list is missing %q", id)
		}
	}
	for _, id := range []string{"shared", "theirs"} {
		if _, ok := got[id]; ok {
			t.Errorf("list served %q, which holds another user's span", id)
		}
	}
	if m := got["mine"]; m.StartedAt == nil || *m.StartedAt != "2026-10-02T10:00:00.000000Z" || m.SpanCount != 2 {
		t.Errorf("mine = %+v", m)
	}
	if o := got["open"]; o.EndedAt == nil || !strings.HasSuffix(*o.EndedAt, "Z") || o.Status != "unknown" {
		t.Errorf("open = %+v (max(end_time) ignores the NULL root)", o)
	}

	for id, want := range map[string]error{"mine": nil, "straddle": nil, "open": nil, "shared": core.ErrNotFound, "theirs": core.ErrNotFound} {
		d, err := r.Detail(ctx, s, id, "", w)
		if !errors.Is(err, want) {
			t.Errorf("detail %q err = %v, want %v", id, err, want)
			continue
		}
		if err != nil {
			continue
		}
		for _, sp := range d.Spans {
			if sp.SpanID == "c2" || sp.SpanID == "old" {
				t.Errorf("detail %q served foreign span %q", id, sp.SpanID)
			}
		}
		if id == "open" && d.Spans[0].EndTime != nil && d.Spans[0].SpanID == "r" {
			t.Errorf("open root end_time = %q, want null", *d.Spans[0].EndTime)
		}
		if id == "mine" && (d.RequestID == nil || *d.RequestID != "req-mine" || d.Status != "success") {
			t.Errorf("mine detail = %+v", d)
		}
	}
}

// mapContains is what separates a missing measurement from an observed 0, and
// only a real ClickHouse shows it; the tools ARRAY JOIN runs here too.
func TestContextSQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	exec(t, url, "ALTER TABLE default.events_full ADD COLUMN tool_call_names Array(String), ADD COLUMN tool_calls Array(String)")
	cols := "(project_id, trace_id, span_id, user_id, start_time, type, name, usage_details, tool_call_names, tool_calls, event_ts, is_deleted)"
	gen := func(span, user, usage, tools string) string {
		calls := "[]"
		if span == "a" { // Claude Code sends arguments as a JSON string
			calls = `['{"id":"1","arguments":"{\\"skill\\":\\"git:commit\\"}"}','{}']`
		}
		if span == "b" { // an object works too; a Task call names its subagent
			tools, calls = "['Task']", `['{"arguments":{"subagent_type":"Explore"}}']`
		}
		return fmt.Sprintf("('p1','t','%s','%s','2026-10-02 10:00:00','GENERATION','chat x',%s,%s,%s,now64(6),0)", span, user, usage, tools, calls)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		// full usage: 1000 in (900 cached), 10 out (4 reasoning), cache creation observed as 0
		gen("a", "a@x", "map('input',1000,'output',10,'input_cached_tokens',900,'input_cache_creation',0,'completion_details.reasoning',4)", "['Skill','Bash']"),
		// no cache, no reasoning keys: must not count as observed zeros
		gen("b", "a@x", "map('input',3000,'output',30)", "['Bash']"),
		// another user's generation: never counted
		gen("c", "b@x", "map('input',99999,'output',1)", "['Bash','Bash']"),
	}, ","))

	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "t", Password: "t"}, nil), []string{"p1"}))
	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	rep, err := r.Context(context.Background(), s, w)
	if err != nil || rep.Provenance.State != "available" || rep.ToolProvenance.State != "available" {
		t.Fatalf("err=%v provenance=%+v tools=%+v", err, rep.Provenance, rep.ToolProvenance)
	}
	if rep.GenerationCount == nil || *rep.GenerationCount != 2 {
		t.Fatalf("generations = %v, want 2 (b@x excluded)", rep.GenerationCount)
	}
	if v, u := rep.TokenRatio.Value, rep.TokenRatio.UncachedValue; v == nil || *v != 100 || u == nil || *u != 77.5 || rep.TokenRatio.Coverage != 2 {
		t.Errorf("ratio = %+v, want 4000/40 and uncached 3100/40", rep.TokenRatio)
	}
	if v := rep.CacheReadShare.Value; v == nil || *v != 0.9 || rep.CacheReadShare.Coverage != 1 || rep.CacheCoverage != 1 {
		t.Errorf("cache share = %+v coverage=%d, want 900/1000 over the one row that carries it", rep.CacheReadShare, rep.CacheCoverage)
	}
	if rep.CacheCreationTokens == nil || *rep.CacheCreationTokens != 0 || rep.CacheCreationCoverage != 1 {
		t.Errorf("cache creation = %v/%d, want an observed 0 on one row", rep.CacheCreationTokens, rep.CacheCreationCoverage)
	}
	if v := rep.Reasoning.Share; v == nil || *v != 0.4 || rep.Reasoning.Coverage != 1 {
		t.Errorf("reasoning = %+v, want 4/10 over one row", rep.Reasoning)
	}
	if fmt.Sprint(rep.Skills, rep.Subagents) != "[{git:commit 1}] [{Explore 1}]" {
		t.Errorf("skills = %v subagents = %v", rep.Skills, rep.Subagents)
	}
	if rep.InvocationCount == nil || *rep.InvocationCount != 3 || rep.SkillInvocationCount == nil || *rep.SkillInvocationCount != 1 {
		t.Errorf("tools = %v skill = %v, want 3 / 1", rep.InvocationCount, rep.SkillInvocationCount)
	}
}

// The User-Agent lives on the unowned gateway root; the generation inherits it
// through the trace. Another user's trace never contributes.
func TestWorkloadsSQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	cols := "(project_id, trace_id, span_id, user_id, start_time, type, name, usage_details, cost_details, metadata_names, metadata_values, event_ts, is_deleted)"
	// Each of my generations used 10 in / 2 out at $0.25; g1 of t2 recorded none.
	span := func(trace, span, user, typ, ua string) string {
		names, vals := "[]", "[]"
		if ua != "" {
			names, vals = "['attributes.http.user_agent']", "['"+ua+"']"
		}
		usage, cost := "map('input',10,'output',2)", "map('total',0.25)"
		if span == "g2" { // a subscription generation: cached input, no cost
			usage, cost = "map('input',30,'output',2,'input_cached_tokens',24)", "map('total',0)"
		}
		if trace == "t2" {
			usage, cost = "map()", "map()"
		}
		return fmt.Sprintf("('p1','%s','%s','%s','2026-10-02 10:00:00','%s','x',%s,%s,%s,%s,now64(6),0)", trace, span, user, typ, usage, cost, names, vals)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		span("t1", "root", "", "GENERATION", "claude-cli/2.1.310 (external, cli)"),
		span("t1", "g1", "a@x", "GENERATION", ""),
		span("t1", "g2", "a@x", "GENERATION", ""),
		span("t2", "root", "", "SPAN", "opencode/latest/2.1.4/cli"),
		span("t2", "g1", "a@x", "GENERATION", ""),
		span("t3", "g1", "a@x", "GENERATION", ""), // no gateway root: unknown
		span("t4", "root", "", "SPAN", "codex"),
		span("t4", "g1", "b@x", "GENERATION", ""), // not mine
	}, ","))
	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "t", Password: "t"}, nil), []string{"p1"}))
	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	rep, err := r.Workloads(context.Background(), s, w)
	if err != nil || rep.Provenance.State != "available" {
		t.Fatalf("err=%v provenance=%+v", err, rep.Provenance)
	}
	f := func(v float64) *float64 { return &v }
	want := []RuntimeUsage{
		// medianExact of {10, 30} is the upper middle value, 30; cache only g2
		// reports it (24/30); cost only g1 is priced
		{"claude_code", 2, 40, 4, 0.25, f(30), f(0.8), f(0.25)},
		{"opencode", 1, 0, 0, 0, nil, nil, nil},
		{"unknown", 1, 10, 2, 0.25, f(10), nil, f(0.25)},
	}
	show := func(rs []RuntimeUsage) string {
		out := ""
		for _, r := range rs {
			out += fmt.Sprintf("%s %d %d %d %v %v %v %v; ", r.Name, r.Count, r.InputTokens, r.OutputTokens, r.Cost, deref(r.MedianInput), deref(r.CacheReadShare), deref(r.CostPerGeneration))
		}
		return out
	}
	if show(rep.Runtimes) != show(want) || rep.SourceCoverage.Runtime != 3 {
		t.Errorf("runtimes = %s coverage=%+v, want %s", show(rep.Runtimes), rep.SourceCoverage, show(want))
	}
}

// Zero-cost generations stay out of the denominators; the cost split comes
// from span metadata; the cache sums only priced generations.
func TestCostSQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	exec(t, url, "ALTER TABLE default.events_full ADD COLUMN session_id String")
	cols := "(project_id, trace_id, span_id, user_id, session_id, start_time, type, name, provided_model_name, usage_details, cost_details, metadata_names, metadata_values, event_ts, is_deleted)"
	gen := func(span, user, session, model string, cost, inputCost float64, input, cached int) string {
		return fmt.Sprintf("('p1','t','%s','%s','%s','2026-10-02 10:00:00','GENERATION','chat x','%s',map('input',%d,'output',1,'input_cached_tokens',%d),map('total',%v),['attributes.litellm.cost.input'],['%v'],now64(6),0)",
			span, user, session, model, input, cached, cost, inputCost)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		gen("a", "a@x", "s1", "smart", 1.0, 0.308, 1000000, 800000),
		gen("b", "a@x", "", "smart", 2.0, 0, 0, 0),
		gen("c", "a@x", "s2", "opus", 0, 0, 5000000, 4000000), // subscription: no cost
		gen("d", "b@x", "s9", "smart", 9.0, 9.0, 1, 0),        // not mine
	}, ","))
	prices := &fakePrices{p: map[string]ModelPrice{"smart": {Input: f64(1e-6), CacheRead: f64(1e-7)}, "opus": {Input: f64(1e-6), CacheRead: f64(1e-7)}}}
	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "t", Password: "t"}, nil), []string{"p1"})).WithPrices(prices)
	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	rep, err := r.Cost(context.Background(), s, w)
	if err != nil || rep.Provenance.State != "available" {
		t.Fatalf("err=%v provenance=%+v", err, rep.Provenance)
	}
	if rep.PricedGenerations != 2 || rep.ZeroCostGenerations != 1 || !near(rep.CostPerModelRequest, 1.5) {
		t.Errorf("priced=%d zero=%d per request=%v, want 2/1/1.5", rep.PricedGenerations, rep.ZeroCostGenerations, rep.CostPerModelRequest)
	}
	if rep.Sessions != 1 || !near(rep.CostPerSession, 1.0) || !near(rep.SessionSpendCoverage, 1.0/3) {
		t.Errorf("sessions=%d per session=%v coverage=%v, want s1 only (s2 is free)", rep.Sessions, rep.CostPerSession, rep.SessionSpendCoverage)
	}
	if c := rep.Cache; !near(c.WithCache, 0.308) || !near(c.WithoutCache, 1.1) || c.PricedModels != 1 {
		t.Errorf("cache = %+v, want smart only at the billed 1.1x", c)
	}
}

// Durations and tokens/s come from the span timestamps, TTFT from LiteLLM's
// time_to_first_chunk metadata (seconds); an unmeasured generation stays out
// of the percentiles; another user's rows never count.
func TestLatencySQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	cols := "(project_id, trace_id, span_id, user_id, start_time, end_time, type, name, level, provided_model_name, usage_details, metadata_names, metadata_values, event_ts, is_deleted)"
	gen := func(span, user, end, ttft, level string, out int) string {
		meta := "[],[]"
		if ttft != "" {
			meta = "['attributes.gen_ai.response.time_to_first_chunk'],['" + ttft + "']"
		}
		return fmt.Sprintf("('p1','t','%s','%s','2026-10-02 10:00:00',%s,'GENERATION','chat m','%s','m',map('output',%d),%s,now64(6),0)", span, user, end, level, out, meta)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		gen("g1", "a@x", "'2026-10-02 10:00:02'", "0.5", "DEFAULT", 100), // 2s, TTFT 500ms, 50 tok/s
		gen("g2", "a@x", "'2026-10-02 10:00:04'", "", "ERROR", 0),        // 4s, no TTFT, no tok/s
		gen("g3", "a@x", "NULL", "", "DEFAULT", 10),                      // unmeasured
		gen("g4", "b@x", "'2026-10-02 10:01:00'", "", "DEFAULT", 10),     // not mine
	}, ","))
	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "t", Password: "t"}, nil), []string{"p1"}))
	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	rep, err := r.Latency(context.Background(), s, w)
	if err != nil || !rep.Available {
		t.Fatalf("err=%v rep=%+v", err, rep)
	}
	c := rep.SourceCoverage
	if *rep.RowCount != 3 || c.Timing != 2 || c.TTFT != 1 || c.TokensPerSec != 1 {
		t.Errorf("coverage = %+v rows=%d", c, *rep.RowCount)
	}
	l := rep.Latency
	if l.P95 == nil || *l.P95 > 4000 || l.TTFTP50 == nil || *l.TTFTP50 != 500 || l.TokensPerSecP50 == nil || *l.TokensPerSecP50 != 50 {
		t.Errorf("latency = p95 %v ttft %v tok/s %v", l.P95, l.TTFTP50, l.TokensPerSecP50)
	}
	if rep.Outcomes[0].Count != 1 || rep.Histogram[4].Count != 2 {
		t.Errorf("outcomes=%+v histogram=%+v", rep.Outcomes, rep.Histogram)
	}
	if len(rep.ByModel) != 1 || rep.ByModel[0].Requests != 3 {
		t.Errorf("by_model = %+v", rep.ByModel)
	}
}

// Langfuse's scores table: its engine and sort key, the columns the
// classification read and grant touch, plus the free-text ones it must not.
const scoresDDL = `CREATE TABLE default.scores (
  id String, timestamp DateTime64(3), project_id String,
  trace_id Nullable(String), session_id Nullable(String), name String, value Float64,
  comment Nullable(String), string_value Nullable(String),
  metadata Map(LowCardinality(String), String),
  event_ts DateTime64(3), is_deleted UInt8
) ENGINE = ReplacingMergeTree(event_ts, is_deleted)
ORDER BY (project_id, toDate(timestamp), name, id)`

// Sessions take their label from default.scores through the caller's own
// generations; the read works with only the granted scores columns, and a
// user without that grant gets unavailable, not an error.
func TestClassificationSQLOnRealClickHouse(t *testing.T) {
	url := startClickHouse(t)
	exec(t, url, eventsFullDDL)
	exec(t, url, "ALTER TABLE default.events_full ADD COLUMN session_id String")
	exec(t, url, scoresDDL)
	cols := "(project_id, trace_id, span_id, user_id, session_id, start_time, type, name, usage_details, cost_details, event_ts, is_deleted)"
	gen := func(span, user, session string) string {
		return fmt.Sprintf("('p1','t','%s','%s','%s','2026-10-02 10:00:00','GENERATION','chat x',map('input',10,'output',2),map('total',0.25),now64(6),0)", span, user, session)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		gen("a", "a@x", "s1"), gen("b", "a@x", "s1"), // labelled genai, 2 generations
		gen("c", "a@x", "s2"), // labelled twice: the later score wins
		gen("d", "a@x", "s3"), // not labelled yet
		gen("e", "a@x", ""),   // no session: counted apart
		gen("f", "b@x", "s9"), // someone else's labelled session
	}, ","))
	// Trace-detail classification: one SPAN per trace (the classification read
	// counts generations only, so these leave its totals alone).
	span := func(trace, session string) string {
		return fmt.Sprintf("('p1','%s','root','a@x','%s','2026-10-02 10:00:00','SPAN','r',map(),map(),now64(6),0)", trace, session)
	}
	exec(t, url, "INSERT INTO default.events_full "+cols+" VALUES "+strings.Join([]string{
		span("t-s1", "s1"), span("t-s3", "s3"), span("t-none", ""),
	}, ","))
	score := func(id, session, category, taxonomy, ts string) string {
		return fmt.Sprintf("('%s','%s','p1',NULL,'%s','ach_classification_category',1,'secret comment','secret',map('category','%s','taxonomy','%s','confidence','0.9'),'%s',0)",
			id, ts, session, category, taxonomy, ts)
	}
	exec(t, url, "INSERT INTO default.scores VALUES "+strings.Join([]string{
		score("x1", "s1", "technology.genai_development", "v3", "2026-10-02 11:00:00"),
		score("x2", "s2", "other", "v3", "2026-10-02 11:00:00"),
		score("x2", "s2", "data.analytics", "v3", "2026-10-03 11:00:00"), // same id, new day: a new sort key
		score("x3", "s3", "other", "v2", "2026-10-02 11:00:00"),          // older taxonomy: not a v3 label
		score("x9", "s9", "sales.proposals", "v3", "2026-10-02 11:00:00"),
	}, ","))
	exec(t, url, "CREATE USER ro IDENTIFIED BY 'ro'")
	exec(t, url, "GRANT SELECT ON default.events_full TO ro")
	exec(t, url, "CREATE USER nogrant IDENTIFIED BY 'nogrant'")
	exec(t, url, "GRANT SELECT ON default.events_full TO nogrant")
	exec(t, url, "GRANT SELECT(project_id, timestamp, name, id, event_ts, is_deleted, session_id, metadata) ON default.scores TO ro")

	s, _ := core.NewUserScope("a@x")
	w := core.Window{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Timezone: "UTC"}
	r := NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "ro", Password: "ro"}, nil), []string{"p1"}))
	rep, err := r.Classification(context.Background(), s, w)
	if err != nil || rep.Provenance.State != "available" {
		t.Fatalf("err=%v provenance=%+v", err, rep.Provenance)
	}
	want := []CategoryUsage{ // tied on sessions: by category id
		{"data.analytics", "data", 1, 1, 10, 2, 0.25},
		{"technology.genai_development", "technology", 1, 2, 20, 4, 0.5},
	}
	if fmt.Sprint(rep.Categories) != fmt.Sprint(want) {
		t.Errorf("categories = %+v, want %+v", rep.Categories, want)
	}
	if c := rep.Coverage; c.Sessions != 3 || c.Labelled != 2 || c.SessionlessGenerations != 1 {
		t.Errorf("coverage = %+v, want 2 of 3 sessions, 1 sessionless generation", c)
	}

	for trace, want := range map[string]string{"t-s1": "classified", "t-s3": "not_classified", "t-none": "no_session"} {
		d, err := r.Detail(context.Background(), s, trace, "", w)
		if c := d.Classification; err != nil || c.State != want {
			t.Errorf("detail %s: err=%v classification=%+v, want %s", trace, err, c, want)
		}
	}
	if c := mustDetail(t, r, s, "t-s1", w).Classification; *c.Category != "technology.genai_development" || *c.Domain != "technology" || *c.Confidence != 0.9 {
		t.Errorf("t-s1 classification = %+v", c)
	}

	r = NewReader(core.NewReader(core.NewClickHouse(core.Config{URL: url, User: "nogrant", Password: "nogrant"}, nil), []string{"p1"}))
	if rep, err = r.Classification(context.Background(), s, w); err != nil || rep.Coverage != nil || rep.Provenance.Reason == nil {
		t.Errorf("without the scores grant: err=%v rep=%+v, want unavailable", err, rep)
	}
	if d := mustDetail(t, r, s, "t-s1", w); d.Classification.State != "unavailable" || len(d.Spans) != 1 {
		t.Errorf("without the scores grant: detail = %+v, want spans with an unavailable classification", d)
	}
}

func mustDetail(t *testing.T, r *Reader, s core.Scope, trace string, w core.Window) TraceDetail {
	t.Helper()
	d, err := r.Detail(context.Background(), s, trace, "", w)
	if err != nil {
		t.Fatalf("detail %s: %v", trace, err)
	}
	return d
}
