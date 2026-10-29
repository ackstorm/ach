// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	core "github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func listRowJSON(project, trace string, total int, extra string) string {
	return fmt.Sprintf(`{"project_id":%q,"trace_id":%q,"started_at":"2026-10-02 10:00:00.000000","ended_at":"2026-10-02 10:00:03.000000","span_count":4,"status":"success","duration_ms":3000,"root_name":"POST /v1/chat/completions","request_id":"req-1","model":"claude-x","input_tokens":10,"output_tokens":20,"cost":0.5,"key_alias":"lk-1","total_count":%d%s}`, project, trace, total, extra)
}

func page(rows ...string) string { return `{"data":[` + strings.Join(rows, ",") + `]}` }

func TestListBindsProjectAndSubject(t *testing.T) {
	ch, calls := scriptedCH(t, page(listRowJSON("p1", "t1", 1, "")))
	r := NewReader(core.NewReader(ch, []string{"p1", "p2"}))
	if _, err := r.List(context.Background(), scope(t, "Ana@Corp.com"), validQuery()); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	for k, want := range map[string]string{"param_project0": "p1", "param_subject0": "ana@corp.com", "param_project1": "p2", "param_subject1": "ana@corp.com"} {
		if c.Q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, c.Q.Get(k), want)
		}
	}
	for _, frag := range []string{"user_id={subject0:String}", "project_id={project1:String}", "FINAL"} {
		if !strings.Contains(c.Body, frag) {
			t.Errorf("sql missing %q", frag)
		}
	}
	if strings.Contains(c.Body, "ana@corp.com") {
		t.Error("email must be a bound parameter, never SQL text")
	}
}

func TestListRejectsBadArguments(t *testing.T) {
	w := testWindow()
	for name, mut := range map[string]func(*TraceQuery){
		"limit 0":     func(q *TraceQuery) { q.Limit = 0 },
		"limit 51":    func(q *TraceQuery) { q.Limit = 51 },
		"neg cursor":  func(q *TraceQuery) { q.Cursor = -1 },
		"bad status":  func(q *TraceQuery) { q.Status = "weird" },
		"long search": func(q *TraceQuery) { q.Search = strings.Repeat("a", 201) },
		"bad sort":    func(q *TraceQuery) { q.Sort = "cost; DROP TABLE x" },
		"zero window": func(q *TraceQuery) { q.Window = core.Window{} },
		"end<=start":  func(q *TraceQuery) { q.Window.End = w.Start },
		"32 day span": func(q *TraceQuery) { q.Window.End = w.Start.AddDate(0, 0, 32) },
	} {
		t.Run(name, func(t *testing.T) {
			ch, calls := scriptedCH(t)
			q := validQuery()
			mut(&q)
			_, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), q)
			if !errors.Is(err, core.ErrInvalidArgument) || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%d", err, len(*calls))
			}
		})
	}
	// ports: test_trace_window_accepts_month_but_rejects_32_days — 31 days is the cap and passes.
	ch, _ := scriptedCH(t)
	q := validQuery()
	q.Window.End = w.Start.AddDate(0, 0, 31)
	if _, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), q); err != nil {
		t.Fatalf("31 days must pass: %v", err)
	}
}

func TestListOrdersByWhitelistedColumnWithStableTies(t *testing.T) {
	for _, c := range []struct {
		sort string
		asc  bool
		want string
	}{
		{"", false, "ORDER BY started_at DESC, project_id, trace_id "},
		{"started_at", true, "ORDER BY started_at ASC, project_id, trace_id "},
		{"tokens", false, "ORDER BY input_tokens + output_tokens DESC, project_id, trace_id "},
		{"key", true, "ORDER BY key_alias ASC, project_id, trace_id "},
	} {
		ch, calls := scriptedCH(t, page(listRowJSON("p1", "t1", 1, "")))
		q := validQuery()
		q.Sort, q.Asc = c.sort, c.asc
		if _, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), q); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains((*calls)[0].Body, c.want) {
			t.Errorf("sort %q asc=%v: sql missing %q", c.sort, c.asc, c.want)
		}
	}
}

func TestListPagesGroupBeforeLimitPastFiveHundred(t *testing.T) {
	// ports: test_trace_pages_group_complete_spans_and_reach_past_500 — a 1,200-span trace at offset 550.
	ch, calls := scriptedCH(t, page(listRowJSON("p1", "big", 700, "")))
	q := validQuery()
	q.Cursor = 550
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), q)
	if err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.Q.Get("param_offset") != "550" || c.Q.Get("param_limit") != "26" {
		t.Errorf("offset/limit = %q/%q", c.Q.Get("param_offset"), c.Q.Get("param_limit"))
	}
	if g, l := strings.Index(c.Body, "GROUP BY project_id, trace_id"), strings.Index(c.Body, "LIMIT {limit"); g < 0 || l < g {
		t.Error("grouping must happen before LIMIT/OFFSET")
	}
	if len(pg.Traces) != 1 || pg.TotalCount == nil || *pg.TotalCount != 700 {
		t.Errorf("page = %+v", pg)
	}
}

func TestListExhaustedOffsetIssuesCountQuery(t *testing.T) {
	// ports: test_trace_page_beyond_end_preserves_total_without_next_cursor
	ch, calls := scriptedCH(t, page(), page(`{"total_count":551}`))
	q := validQuery()
	q.Cursor, q.Status, q.Search = 550, "error", "abc"
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.HasPrefix((*calls)[1].Body, "SELECT count() AS total_count FROM (") {
		t.Fatalf("calls = %+v", *calls)
	}
	for _, k := range []string{"param_project0", "param_subject0", "param_start", "param_end", "param_search", "param_status"} {
		if (*calls)[0].Q.Get(k) != (*calls)[1].Q.Get(k) || (*calls)[1].Q.Get(k) == "" {
			t.Errorf("count query must share %s", k)
		}
	}
	if pg.TotalCount == nil || *pg.TotalCount != 551 || pg.NextCursor != nil || pg.Traces == nil || len(pg.Traces) != 0 {
		t.Errorf("page = %+v", pg)
	}
}

func TestListNextCursorWhenMoreRows(t *testing.T) {
	rows := make([]string, 26)
	for i := range rows {
		rows[i] = listRowJSON("p1", fmt.Sprintf("t%d", i), 90, "")
	}
	ch, _ := scriptedCH(t, page(rows...))
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery())
	if err != nil || len(pg.Traces) != 25 || pg.NextCursor == nil || *pg.NextCursor != 25 {
		t.Fatalf("err=%v page=%+v", err, pg)
	}
}

func TestListRejectsForeignResponseScope(t *testing.T) {
	// ports: test_trace_page_rejects_foreign_response_scope, test_foreign_project_or_user_response_is_not_retained
	ch, _ := scriptedCH(t, page(listRowJSON("p1", "ok", 2, ""), listRowJSON("other", "foreign", 2, "")))
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Traces) != 0 || pg.TotalCount != nil || pg.Provenance.Reason == nil || *pg.Provenance.Reason != "scope_mismatch" || pg.Provenance.State != "partial" {
		t.Errorf("page = %+v", pg)
	}
}

func TestListSourceFailureIsUnavailableNotError(t *testing.T) {
	ch, _ := scriptedCH(t, `{"data":null}`)
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery())
	if err != nil || pg.Provenance.Reason == nil || *pg.Provenance.Reason != "source_unavailable" || pg.Traces == nil {
		t.Fatalf("err=%v page=%+v", err, pg)
	}
}

func TestListUnknownProjectMakesNoQuery(t *testing.T) {
	// ports: test_unapproved_project_mapping_makes_no_clickhouse_query
	ch, calls := scriptedCH(t)
	_, err := NewReader(core.NewReader(ch, nil)).List(context.Background(), scope(t, "a@x"), validQuery())
	if !errors.Is(err, core.ErrProjectNotAllowed) || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(*calls))
	}
}

func TestListDecodesRealisticRowsAndKinds(t *testing.T) {
	nullEnd := `,"ended_at":null`
	_ = nullEnd
	mcp := strings.Replace(listRowJSON("p1", "m1", 2, ""), "POST /v1/chat/completions", "POST /mcp", 1)
	noChat := strings.Replace(strings.Replace(listRowJSON("p1", "n1", 2, ""), `"model":"claude-x"`, `"model":""`, 1), `"ended_at":"2026-10-02 10:00:03.000000"`, `"ended_at":null`, 1)
	ch, _ := scriptedCH(t, page(mcp, noChat))
	pg, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery())
	if err != nil || len(pg.Traces) != 2 {
		t.Fatalf("err=%v page=%+v", err, pg)
	}
	m, n := pg.Traces[0], pg.Traces[1]
	if m.Kind == nil || *m.Kind != "mcp" || m.SpanCount != 4 || m.DurationMS != 3000 || m.InputTokens == nil || *m.InputTokens != 10 || m.Cost == nil || *m.Cost != 0.5 {
		t.Errorf("mcp = %+v", m)
	}
	// no chat span: usage is null, never 0
	if n.Model != nil || n.InputTokens != nil || n.OutputTokens != nil || n.Cost != nil || n.EndedAt != nil {
		t.Errorf("noChat = %+v", n)
	}
}

func mustValidate(t *testing.T, schemaFile string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/contracts/" + schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://"+schemaFile, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem://" + schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	var inst any
	_ = json.Unmarshal(b, &inst)
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("%s: %v\n%s", schemaFile, err, b)
	}
}

func TestListResponseMatchesWireContract(t *testing.T) {
	ch, _ := scriptedCH(t, page(listRowJSON("p1", "t1", 1, "")))
	r := NewReader(core.NewReader(ch, []string{"p1"}))
	ok, _ := r.List(context.Background(), scope(t, "a@x"), validQuery())
	mustValidate(t, "traces.schema.json", ok)

	bad, _ := scriptedCH(t, `{"data":null}`)
	un, _ := NewReader(core.NewReader(bad, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery())
	mustValidate(t, "traces.schema.json", un)
}

// ---- detail ----

func spanJSON(project, user, trace, span, parent, typ, name, level, end string, cost float64, in, out int) string {
	e := "null"
	if end != "" {
		e = fmt.Sprintf("%q", end)
	}
	return fmt.Sprintf(`{"project_id":%q,"user_id":%q,"trace_id":%q,"span_id":%q,"parent_span_id":%q,"start_time":"2026-10-02 10:00:00.000000","end_time":%s,"type":%q,"name":%q,"level":%q,"provided_model_name":"claude-x","response_id":"req-9","cost":%v,"input_tokens":%d,"output_tokens":%d}`,
		project, user, trace, span, parent, e, typ, name, level, cost, in, out)
}

func detail(t *testing.T, projects []string, project string, rows ...string) (TraceDetail, error, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, page(rows...))
	d, err := NewReader(core.NewReader(ch, projects)).Detail(context.Background(), scope(t, "a@x"), "t1", project, testWindow())
	return d, err, calls
}

const done = "2026-10-02 10:00:03.000000"

func TestDetailTotalsFromChatSpan(t *testing.T) {
	d, err, calls := detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "POST /v1/chat/completions", "DEFAULT", done, 0, 0, 0),
		spanJSON("p1", "a@x", "t1", "c1", "root", "GENERATION", "chat claude-x", "DEFAULT", done, 0.25, 10, 20),
		spanJSON("p1", "a@x", "t1", "c2", "root", "GENERATION", "chat claude-x", "DEFAULT", done, 0.25, 5, 5))
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "success" || d.Model == nil || *d.Model != "claude-x" || *d.Cost != 0.5 || *d.InputTokens != 15 || *d.OutputTokens != 25 || *d.RequestID != "req-9" || len(d.Spans) != 3 {
		t.Errorf("detail = %+v", d)
	}
	if q := (*calls)[0].Q; q.Get("param_trace") != "t1" || q.Get("param_subject0") != "a@x" {
		t.Errorf("params = %v", q)
	}
	mustValidate(t, "trace-detail.schema.json", d)
}

func TestDetailUnfinishedRootHasNullUsageAndUnknownStatus(t *testing.T) {
	// ports: the no-chat detail test; its real precondition is an unfinished root (end_time null).
	d, err, _ := detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "POST /mcp", "DEFAULT", "", 0, 0, 0))
	if err != nil || d.Status != "unknown" || d.Model != nil || d.Cost != nil || d.InputTokens != nil || d.OutputTokens != nil {
		t.Fatalf("err=%v detail=%+v", err, d)
	}
	mustValidate(t, "trace-detail.schema.json", d)
	// sibling: finished root, still no chat span → success with null usage.
	d, err, _ = detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "POST /mcp", "DEFAULT", done, 0, 0, 0))
	if err != nil || d.Status != "success" || d.Cost != nil {
		t.Fatalf("err=%v detail=%+v", err, d)
	}
}

func TestDetailErrorLevelWins(t *testing.T) {
	d, err, _ := detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0),
		spanJSON("p1", "a@x", "t1", "c1", "root", "GENERATION", "chat x", "ERROR", done, 0, 0, 0))
	if err != nil || d.Status != "error" || d.Spans[1].Status != "error" || d.Spans[0].Status != "unknown" {
		t.Fatalf("err=%v detail=%+v", err, d)
	}
}

func TestDetailMixedOwnerRejectsWholeResult(t *testing.T) {
	// ports: test_trace_detail_reads_unowned_gateway_spans_but_never_another_users_span
	_, err, _ := detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0),
		spanJSON("p1", "b@x", "t1", "c1", "root", "GENERATION", "chat x", "DEFAULT", done, 1, 1, 1))
	if !errors.Is(err, core.ErrScopeMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetailForeignProjectOrTraceRejected(t *testing.T) {
	_, err, _ := detail(t, []string{"p1"}, "",
		spanJSON("other", "a@x", "t1", "s", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))
	if !errors.Is(err, core.ErrScopeMismatch) {
		t.Fatalf("foreign project err = %v", err)
	}
	_, err, _ = detail(t, []string{"p1"}, "",
		spanJSON("p1", "a@x", "other-trace", "s", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))
	if !errors.Is(err, core.ErrScopeMismatch) {
		t.Fatalf("foreign trace err = %v", err)
	}
}

func TestDetailSelectorValidatedBeforeAnyQuery(t *testing.T) {
	// ports: test_detail_scopes_selected_project_before_read_limit_and_rejects_foreign_without_query
	_, err, calls := detail(t, []string{"p1", "p2"}, "nope")
	if !errors.Is(err, core.ErrProjectNotAllowed) || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(*calls))
	}
	// A valid selector narrows the query to that project only.
	_, _, calls = detail(t, []string{"p1", "p2"}, "p2",
		spanJSON("p2", "a@x", "t1", "s", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))
	q := (*calls)[0].Q
	if q.Get("param_project0") != "p2" || q.Get("param_project1") != "" {
		t.Errorf("selector must narrow projects, got %v", q)
	}
}

func TestDetailSameTraceIDInTwoProjectsIsAmbiguous(t *testing.T) {
	// ports: same trace id in two projects stays separate; no selector → 409 project_required.
	rows := []string{
		spanJSON("p1", "a@x", "t1", "s1", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0),
		spanJSON("p2", "a@x", "t1", "s2", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0),
	}
	_, err, _ := detail(t, []string{"p1", "p2"}, "", rows...)
	if !errors.Is(err, core.ErrProjectRequired) {
		t.Fatalf("err = %v", err)
	}
	d, err, _ := detail(t, []string{"p1", "p2"}, "p1", rows[0])
	if err != nil || d.ProjectID != "p1" || len(d.Spans) != 1 {
		t.Fatalf("err=%v detail=%+v", err, d)
	}
}

func TestDetailUnknownTraceIsNotFoundAndSourceErrorIsUnavailable(t *testing.T) {
	_, err, _ := detail(t, []string{"p1"}, "")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	ch, _ := scriptedCH(t, `{"data":null}`)
	_, err = NewReader(core.NewReader(ch, []string{"p1"})).Detail(context.Background(), scope(t, "a@x"), "t1", "", testWindow())
	if !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// A trace holding another user's span must never be served: gateway spans have
// no owner, so a shared trace_id would otherwise hand them to the wrong caller.
func TestSQLExcludesTracesHoldingAnotherUsersSpan(t *testing.T) {
	// ports: none — review of ach#207, unowned spans leaked across a shared trace_id
	ch, calls := scriptedCH(t, page(listRowJSON("p1", "t1", 1, "")))
	if _, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*calls)[0].Body, "countIf(user_id NOT IN ({subject0:String}, '')) = 0") {
		t.Error("list must drop traces that hold another user's span")
	}
	_, _, dc := detail(t, []string{"p1"}, "", spanJSON("p1", "", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))
	b := (*dc)[0].Body
	if !strings.Contains(b, "NOT IN (") || !strings.Contains(b, "user_id NOT IN ({subject0:String}, '')") {
		t.Error("detail must refuse a trace that holds another user's span")
	}
}

// The newest-500 cut dropped the root of a long trace; keep the oldest instead.
func TestDetailKeepsOldestSpansWhenCapped(t *testing.T) {
	// ports: none — review of ach#207, newest-first cap dropped the root
	_, _, calls := detail(t, []string{"p1"}, "", spanJSON("p1", "", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))
	if !strings.Contains((*calls)[0].Body, "ORDER BY start_time ASC") {
		t.Error("detail must read oldest spans first")
	}
}

func TestTimestampsAreRFC3339UTC(t *testing.T) {
	// ports: none — review of ach#207, toString(DateTime64) carried no zone
	ch, calls := scriptedCH(t, page(listRowJSON("p1", "t1", 1, "")))
	if _, err := NewReader(core.NewReader(ch, []string{"p1"})).List(context.Background(), scope(t, "a@x"), validQuery()); err != nil {
		t.Fatal(err)
	}
	if b := (*calls)[0].Body; strings.Contains(b, "toString(") || !strings.Contains(b, "'UTC')") {
		t.Error("timestamps must be formatted as UTC RFC 3339")
	}
}

// request_id comes only from chat generations, as in the list.
func TestDetailRequestIDOnlyFromChatGeneration(t *testing.T) {
	// ports: none — review of ach#207, tool span supplied a different request_id
	d, err, _ := detail(t, []string{"p1"}, "",
		spanJSON("p1", "", "t1", "root", "", "SPAN", "tool x", "DEFAULT", done, 0, 0, 0),
		spanJSON("p1", "a@x", "t1", "c1", "root", "GENERATION", "chat x", "DEFAULT", done, 1, 1, 1))
	if err != nil || d.RequestID == nil || *d.RequestID != "req-9" {
		t.Fatalf("d.RequestID = %v err = %v", d.RequestID, err)
	}
	d, _, _ = detail(t, []string{"p1"}, "", spanJSON("p1", "", "t1", "root", "", "SPAN", "tool x", "DEFAULT", done, 0, 0, 0))
	if d.RequestID != nil {
		t.Errorf("a non-chat span must not set request_id, got %v", *d.RequestID)
	}
}

// ---- detail classification ----

// sessionSpan is a root span carrying a session id (spanJSON has none).
func sessionSpan(session string) string {
	return strings.Replace(spanJSON("p1", "a@x", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0),
		`"parent_span_id":""`, `"parent_span_id":"","session_id":`+fmt.Sprintf("%q", session), 1)
}

func classifiedDetail(t *testing.T, bodies ...string) (TraceDetail, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	d, err := NewReader(core.NewReader(ch, []string{"p1"})).Detail(context.Background(), scope(t, "a@x"), "t1", "", testWindow())
	if err != nil || len(d.Spans) != 1 {
		t.Fatalf("err=%v detail=%+v; a classification read must never fail the detail", err, d)
	}
	mustValidate(t, "trace-detail.schema.json", d)
	return d, calls
}

func TestDetailClassifiedSession(t *testing.T) {
	d, calls := classifiedDetail(t, page(sessionSpan("s1")),
		page(`{"category":"technology.genai_development","confidence":"0.82"}`))
	c := d.Classification
	if c.State != "classified" || *c.SessionID != "s1" || *c.Category != "technology.genai_development" ||
		*c.Domain != "technology" || c.Confidence == nil || *c.Confidence != 0.82 {
		t.Errorf("classification = %+v", c)
	}
	q, b := (*calls)[1].Q, (*calls)[1].Body
	if q.Get("param_project") != "p1" || q.Get("param_session") != "s1" ||
		q.Get("param_score") != "ach_classification_category" || q.Get("param_taxonomy") != "v3" {
		t.Errorf("params = %v", q)
	}
	for _, col := range []string{"comment", "string_value", "value"} {
		if strings.Contains(b, col) {
			t.Errorf("sql reads scores.%s", col)
		}
	}
}

func TestDetailClassificationStates(t *testing.T) {
	for name, tc := range map[string]struct {
		bodies []string
		state  string
		calls  int
	}{
		"not classified": {[]string{page(sessionSpan("s1")), page(`{"category":"","confidence":""}`)}, "not_classified", 2},
		"no session":     {[]string{page(spanJSON("p1", "a@x", "t1", "root", "", "SPAN", "r", "DEFAULT", done, 0, 0, 0))}, "no_session", 1},
		"query error":    {[]string{page(sessionSpan("s1")), `{"exception":"Code: 497. ACCESS_DENIED"}`}, "unavailable", 2},
		"unknown label":  {[]string{page(sessionSpan("s1")), page(`{"category":"made.up","confidence":"1"}`)}, "unavailable", 2},
	} {
		t.Run(name, func(t *testing.T) {
			d, calls := classifiedDetail(t, tc.bodies...)
			c := d.Classification
			if c.State != tc.state || c.Category != nil || c.Domain != nil || c.Confidence != nil || len(*calls) != tc.calls {
				t.Errorf("classification = %+v, calls = %d", c, len(*calls))
			}
			if (c.SessionID == nil) != (tc.state == "no_session") {
				t.Errorf("session_id = %v", c.SessionID)
			}
		})
	}
}
