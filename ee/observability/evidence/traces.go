// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ackstorm/ach/ee/classifier"
	core "github.com/ackstorm/ach/internal/observability/evidence"
)

const (
	statusError   = "error"
	statusSuccess = "success"
	stateDerived  = "derived"
	maxSearch     = 200
	maxLimit      = 50
	detailRowCap  = 500
	classified    = "classified"
)

// TraceQuery selects one page of the caller's traces. End is exclusive.
type TraceQuery struct {
	Window core.Window
	Limit  int
	Cursor int64
	Status string // "" == all
	Search string
	Sort   string // a TraceSorts key; "" == started_at
	Asc    bool   // false == descending (newest, largest first)
}

// TraceSorts maps each sortable list column to its SQL expression over the
// grouped rows. Only these literals ever reach the ORDER BY.
var TraceSorts = map[string]string{
	"started_at": "started_at", "kind": "root_name", "spans": "span_count", "status": "status",
	"request_id": "request_id", "model": "model", "duration": "duration_ms",
	"tokens": "input_tokens + output_tokens", "cost": "cost", "key": "key_alias",
}

// TraceSummary is one grouped (project, trace) row.
type TraceSummary struct {
	TraceID      string   `json:"trace_id"`
	ProjectID    string   `json:"project_id"`
	StartedAt    *string  `json:"started_at"`
	EndedAt      *string  `json:"ended_at"`
	SpanCount    int      `json:"span_count"`
	Status       string   `json:"status"`
	Kind         *string  `json:"kind"`
	RequestID    *string  `json:"request_id"`
	Model        *string  `json:"model"`
	DurationMS   int      `json:"duration_ms"`
	InputTokens  *int     `json:"input_tokens"`
	OutputTokens *int     `json:"output_tokens"`
	Cost         *float64 `json:"cost"`
	KeyAlias     *string  `json:"key_alias"`
	SessionID    *string  `json:"session_id"`
}

// TracePage is the list response; its JSON shape is docs traces.schema.json.
type TracePage struct {
	Window     core.Window     `json:"window"`
	Traces     []TraceSummary  `json:"traces"`
	NextCursor *int64          `json:"next_cursor"`
	TotalCount *int            `json:"total_count"`
	Provenance core.Provenance `json:"provenance"`
}

// Span is one row of a trace hierarchy; content-free.
type Span struct {
	SpanID       string  `json:"span_id"`
	ParentSpanID *string `json:"parent_span_id"`
	Type         string  `json:"type"`
	Name         string  `json:"name"`
	StartTime    *string `json:"start_time"`
	EndTime      *string `json:"end_time"`
	Status       string  `json:"status"`
	Model        *string `json:"model"`
	RequestID    *string `json:"request_id"`
}

// TraceDetail is the detail response; its JSON shape is trace-detail.schema.json.
type TraceDetail struct {
	TraceID      string          `json:"trace_id"`
	ProjectID    string          `json:"project_id"`
	Status       string          `json:"status"`
	Model        *string         `json:"model"`
	Cost         *float64        `json:"cost"`
	InputTokens  *int            `json:"input_tokens"`
	OutputTokens *int            `json:"output_tokens"`
	RequestID    *string         `json:"request_id"`
	Spans        []Span          `json:"spans"`
	Provenance   core.Provenance `json:"provenance"`
	// Classification is the work category of the trace's session.
	Classification TraceClassification `json:"classification"`
}

// TraceClassification is the classifier's label for the trace's session.
// State is classified, not_classified (no label yet), no_session (the trace
// has no session id, so it is never classified) or unavailable (the scores
// read failed); only classified carries a category.
type TraceClassification struct {
	SessionID  *string  `json:"session_id"`
	Category   *string  `json:"category"`
	Domain     *string  `json:"domain"`
	Confidence *float64 `json:"confidence"`
	State      string   `json:"state"`
}

// Reader adds the Enterprise reports to the core reader.
type Reader struct {
	*core.Reader
	prices  Prices  // list prices for the cache comparison; nil = unavailable
	metrics *Thanos // gateway metrics for Performance; nil = not_instrumented
	tax     classifier.Taxonomy
}

func NewReader(c *core.Reader) *Reader {
	return &Reader{Reader: c, tax: classifier.Builtin}
}

// WithTaxonomy sets the taxonomy the classifier labels with; classification
// reads only that version's scores. Unset is the built-in one.
func (r *Reader) WithTaxonomy(t classifier.Taxonomy) *Reader {
	r.tax = t
	return r
}

// Gateway spans (auth, POST, guardrails) carry no user_id: only chat/tool spans do.
// A trace is the subject's when a user-bearing span in the window is theirs and no
// span in the window belongs to anyone else; a trace holding another user's span is
// dropped whole (list HAVING, detail NOT IN), never filtered down to the rest.
const traceGroupSQL = `
SELECT project_id, trace_id,
       formatDateTime(min(source.start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS started_at,
       formatDateTime(max(source.end_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS ended_at,
       count() AS span_count,
       multiIf(countIf(level = 'ERROR') > 0, 'error',
               countIf(parent_span_id = '' AND isNotNull(end_time)) > 0, 'success', 'unknown') AS status,
       dateDiff('millisecond', min(source.start_time), max(source.end_time)) AS duration_ms,
       anyIf(name, parent_span_id = '') AS root_name,
       anyIf(metadata_values[indexOf(metadata_names, 'attributes.gen_ai.response.id')], type = 'GENERATION' AND startsWith(name, 'chat ')) AS request_id,
       anyIf(provided_model_name, type = 'GENERATION' AND startsWith(name, 'chat ')) AS model,
       sumIf(usage_details['input'], type = 'GENERATION' AND startsWith(name, 'chat ')) AS input_tokens,
       sumIf(usage_details['output'], type = 'GENERATION' AND startsWith(name, 'chat ')) AS output_tokens,
       sumIf(cost_details['total'], type = 'GENERATION' AND startsWith(name, 'chat ')) AS cost,
       anyIf(metadata_values[indexOf(metadata_names, 'attributes.litellm.metadata.user_api_key_alias')], type = 'GENERATION' AND startsWith(name, 'chat ')) AS key_alias,
       max(ifNull(session_id, '')) AS session_id
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__UNOWNED_SCOPE__)
  AND (project_id, trace_id) IN (
    SELECT project_id, trace_id FROM default.events_full AS own FINAL
    WHERE is_deleted = 0 AND (__SCOPE__)
      AND own.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
      AND own.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
      AND type IN ('SPAN', 'GENERATION', 'TOOL') AND trace_id != ''
  )
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type IN ('SPAN', 'GENERATION', 'TOOL') AND trace_id != ''
  AND ({search:String} = '' OR positionCaseInsensitive(trace_id, {search:String}) > 0)
GROUP BY project_id, trace_id
HAVING countIf(user_id NOT IN ({subject0:String}, '')) = 0
  AND ({status:String} = 'all' OR status = {status:String})
`

const traceSpanSQL = `
SELECT project_id, user_id, trace_id, span_id, parent_span_id, ifNull(session_id, '') AS session_id,
       formatDateTime(start_time, '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS start_time, formatDateTime(end_time, '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS end_time,
       type, name, level, provided_model_name,
       metadata_values[indexOf(metadata_names, 'attributes.gen_ai.response.id')] AS response_id,
       cost_details['total'] AS cost, usage_details['input'] AS input_tokens, usage_details['output'] AS output_tokens
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__UNOWNED_SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type IN ('SPAN', 'GENERATION', 'TOOL') AND trace_id = {trace:String}
  AND (project_id, trace_id) IN (
    SELECT project_id, trace_id FROM default.events_full AS own FINAL
    WHERE is_deleted = 0 AND (__SCOPE__)
      AND own.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
      AND own.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
      AND trace_id = {trace:String}
  )
  AND (project_id, trace_id) NOT IN (
    SELECT project_id, trace_id FROM default.events_full AS other FINAL
    WHERE is_deleted = 0 AND (__UNOWNED_SCOPE__)
      AND other.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
      AND other.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
      AND type IN ('SPAN', 'GENERATION', 'TOOL') AND trace_id = {trace:String}
      AND user_id NOT IN ({subject0:String}, '')
  )
ORDER BY start_time ASC, project_id, trace_id, span_id
LIMIT {limit:UInt32}
FORMAT JSON
`

// sessionLabelSQL reads the latest taxonomy label of one session, with the
// granted scores columns only (see classificationSQL). No score is one row of
// empty strings, not zero rows.
const sessionLabelSQL = `
SELECT argMax(metadata['category'], event_ts) AS category, argMax(metadata['confidence'], event_ts) AS confidence
FROM default.scores FINAL
WHERE is_deleted = 0 AND project_id = {project:String} AND session_id = {session:String}
  AND name = {score:String} AND metadata['taxonomy'] = {taxonomy:String}
FORMAT JSON`

// classify labels a trace by its session. The trace is already proven the
// caller's, and the session id is its own, so this reads no one else's label.
// A failed read is unavailable, never an error: it must not fail the detail.
func (r *Reader) classify(ctx context.Context, project, session string) TraceClassification {
	if session == "" {
		return TraceClassification{State: "no_session"}
	}
	c := TraceClassification{SessionID: &session, State: "unavailable"}
	params := map[string]string{"project": project, "session": session, "score": classifier.ScoreName, "taxonomy": r.tax.Version}
	var rows []struct {
		Category   string `json:"category"`
		Confidence string `json:"confidence"`
	}
	if err := r.CH().Query(ctx, sessionLabelSQL, params, 1, &rows); err != nil || len(rows) != 1 {
		return c
	}
	switch label := rows[0].Category; {
	case label == "":
		c.State = "not_classified"
	case r.tax.Index(label) >= 0:
		c.State, c.Category, c.Domain = classified, &label, core.Ptr(classifier.Domain(label))
		if f, err := strconv.ParseFloat(rows[0].Confidence, 64); err == nil {
			c.Confidence = &f
		}
	}
	return c
}

func direction(asc bool) string {
	if asc {
		return " ASC"
	}
	return " DESC"
}

type listRow struct {
	ProjectID    string  `json:"project_id"`
	TraceID      string  `json:"trace_id"`
	StartedAt    *string `json:"started_at"`
	EndedAt      *string `json:"ended_at"`
	SpanCount    float64 `json:"span_count"`
	Status       string  `json:"status"`
	DurationMS   float64 `json:"duration_ms"`
	RootName     string  `json:"root_name"`
	RequestID    string  `json:"request_id"`
	Model        string  `json:"model"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
	Cost         float64 `json:"cost"`
	KeyAlias     string  `json:"key_alias"`
	SessionID    string  `json:"session_id"`
	TotalCount   float64 `json:"total_count"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func summary(r listRow) TraceSummary {
	t := TraceSummary{
		TraceID: r.TraceID, ProjectID: r.ProjectID, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		SpanCount: int(r.SpanCount), Status: r.Status, DurationMS: int(r.DurationMS),
		RequestID: nilIfEmpty(r.RequestID), Model: nilIfEmpty(r.Model), KeyAlias: nilIfEmpty(r.KeyAlias), SessionID: nilIfEmpty(r.SessionID),
	}
	switch {
	case strings.Contains(r.RootName, "/mcp"):
		k := "mcp"
		t.Kind = &k
	case r.RootName != "":
		k := "llm"
		t.Kind = &k
	}
	// Usage is null, not 0, when the trace has no chat span.
	if t.Model != nil {
		in, out, cost := int(r.InputTokens), int(r.OutputTokens), r.Cost
		t.InputTokens, t.OutputTokens, t.Cost = &in, &out, &cost
	}
	return t
}

// List pages the caller's complete trace groups over the whole window.
// The cursor is an offset in the requested sort order (default started_at),
// ties broken by project_id, trace_id so pages stay stable.
func (r *Reader) List(ctx context.Context, s core.Scope, q TraceQuery) (TracePage, error) {
	if !s.Valid() {
		return TracePage{}, core.ErrNoScope
	}
	status := q.Status
	if status == "" {
		status = "all"
	}
	sortBy := q.Sort
	if sortBy == "" {
		sortBy = "started_at"
	}
	order, sortable := TraceSorts[sortBy]
	if q.Limit < 1 || q.Limit > maxLimit || q.Cursor < 0 || len(q.Search) > maxSearch || !sortable ||
		!slices.Contains([]string{"all", statusError, statusSuccess, core.StatusUnknown}, status) || !q.Window.Valid() {
		return TracePage{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return TracePage{}, core.ErrProjectNotAllowed
	}
	unavailable := func(reason string) TracePage {
		return TracePage{Window: q.Window, Traces: []TraceSummary{}, Provenance: core.UnavailableProvenance(q.Window, "trace", reason)}
	}

	params := map[string]string{
		"limit": fmt.Sprint(q.Limit + 1), "offset": fmt.Sprint(q.Cursor),
		"search": q.Search, "status": status,
	}
	core.WindowParams(q.Window, params)
	group := core.Scoped(traceGroupSQL, r.Projects(), s, params)
	sql := "SELECT project_id, trace_id, started_at, ended_at, span_count, status, " +
		"duration_ms, root_name, request_id, model, input_tokens, output_tokens, cost, key_alias, session_id, " +
		"count() OVER () AS total_count FROM (" + group + ") " +
		"ORDER BY " + order + direction(q.Asc) + ", project_id, trace_id " +
		"LIMIT {limit:UInt32} OFFSET {offset:UInt64} FORMAT JSON"

	var data []listRow
	if err := r.CH().Query(ctx, sql, params, maxLimit+1, &data); err != nil || len(data) > q.Limit+1 {
		return unavailable(core.SourceUnavailable), nil
	}
	for _, row := range data {
		if !slices.Contains(r.Projects(), row.ProjectID) {
			return unavailable("scope_mismatch"), nil
		}
	}
	total := 0
	if len(data) > 0 {
		total = int(data[0].TotalCount)
	} else if q.Cursor > 0 { // an exhausted offset has no row to carry the window count
		var c []struct {
			Total float64 `json:"total_count"`
		}
		if err := r.CH().Query(ctx, "SELECT count() AS total_count FROM ("+group+") FORMAT JSON", params, 1, &c); err != nil || len(c) != 1 {
			return unavailable(core.SourceUnavailable), nil
		}
		total = int(c[0].Total)
	}
	n := min(len(data), q.Limit)
	traces := make([]TraceSummary, n)
	for i := range n {
		traces[i] = summary(data[i])
	}
	if total < int(q.Cursor)+n && n > 0 {
		return unavailable(core.SourceUnavailable), nil
	}
	p := core.NewProvenance(q.Window, "trace", "all scoped traces matching filters in the requested window", "full-window-final-trace-page")
	p.Coverage = core.Coverage{Numerator: n, Denominator: &total}
	page := TracePage{Window: q.Window, Traces: traces, TotalCount: &total, Provenance: p}
	if len(data) > q.Limit {
		next := q.Cursor + int64(q.Limit)
		page.NextCursor = &next
	}
	return page, nil
}

type spanRow struct {
	ProjectID    string  `json:"project_id"`
	UserID       string  `json:"user_id"`
	TraceID      string  `json:"trace_id"`
	SpanID       string  `json:"span_id"`
	ParentSpanID string  `json:"parent_span_id"`
	SessionID    string  `json:"session_id"`
	StartTime    *string `json:"start_time"`
	EndTime      *string `json:"end_time"`
	Type         string  `json:"type"`
	Name         string  `json:"name"`
	Level        string  `json:"level"`
	Model        string  `json:"provided_model_name"`
	ResponseID   string  `json:"response_id"`
	Cost         float64 `json:"cost"`
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
}

// checkRows rejects any row outside the caller's scope before a byte of it is
// used, then an empty result and a trace that spans several projects.
func checkRows(rows []spanRow, projects []string, s core.Scope, traceID string) error {
	for _, row := range rows {
		if !slices.Contains(projects, row.ProjectID) || row.TraceID != traceID || (row.UserID != s.Email() && row.UserID != "") {
			return core.ErrScopeMismatch
		}
	}
	if len(rows) == 0 {
		return core.ErrNotFound
	}
	for _, row := range rows {
		if row.ProjectID != rows[0].ProjectID {
			return core.ErrProjectRequired
		}
	}
	return nil
}

// Detail returns the caller's hierarchy for one trace. project narrows to one
// allow-listed project; it is validated before any query. Any row outside the
// scope fails the whole read (ErrScopeMismatch) rather than returning a partial trace.
func (r *Reader) Detail(ctx context.Context, s core.Scope, traceID, project string, w core.Window) (TraceDetail, error) {
	if !s.Valid() {
		return TraceDetail{}, core.ErrNoScope
	}
	if traceID == "" || len(traceID) > 200 || !w.Valid() {
		return TraceDetail{}, core.ErrInvalidArgument
	}
	projects := r.Projects()
	if project != "" {
		if !slices.Contains(r.Projects(), project) {
			return TraceDetail{}, core.ErrProjectNotAllowed
		}
		projects = []string{project}
	}
	if len(projects) == 0 {
		return TraceDetail{}, core.ErrProjectNotAllowed
	}
	params := map[string]string{"trace": traceID, "limit": fmt.Sprint(detailRowCap + 1)}
	core.WindowParams(w, params)
	sql := core.Scoped(traceSpanSQL, projects, s, params)

	var rows []spanRow
	if err := r.CH().Query(ctx, sql, params, detailRowCap+1, &rows); err != nil {
		return TraceDetail{}, core.ErrSourceUnavailable
	}
	sampled := len(rows) > detailRowCap
	if sampled {
		rows = rows[:detailRowCap]
	}
	if err := checkRows(rows, projects, s, traceID); err != nil {
		return TraceDetail{}, err
	}

	d := TraceDetail{TraceID: traceID, ProjectID: rows[0].ProjectID, Status: core.StatusUnknown, Spans: make([]Span, len(rows))}
	var cost float64
	var in, out int
	chat, rootDone, failed := false, false, false
	session := ""
	for i, row := range rows {
		if session == "" {
			session = row.SessionID
		}
		isErr := row.Level == "ERROR"
		sp := Span{
			SpanID: row.SpanID, ParentSpanID: nilIfEmpty(row.ParentSpanID), Type: row.Type, Name: row.Name,
			StartTime: row.StartTime, EndTime: row.EndTime, Status: core.StatusUnknown,
			Model: nilIfEmpty(row.Model), RequestID: nilIfEmpty(row.ResponseID),
		}
		if sp.Name == "" {
			sp.Name = "Unnamed span"
		}
		if isErr {
			sp.Status, failed = statusError, true
		}
		d.Spans[i] = sp
		if row.ParentSpanID == "" && row.EndTime != nil {
			rootDone = true
		}
		if row.Type == "GENERATION" && strings.HasPrefix(row.Name, "chat ") {
			chat = true
			cost += row.Cost
			in += int(row.InputTokens)
			out += int(row.OutputTokens)
			if d.Model == nil {
				d.Model = sp.Model
			}
			if d.RequestID == nil {
				d.RequestID = sp.RequestID
			}
		}
	}
	switch {
	case failed:
		d.Status = statusError
	case rootDone:
		d.Status = statusSuccess
	}
	if chat {
		d.Cost, d.InputTokens, d.OutputTokens = &cost, &in, &out
	}
	p := core.NewProvenance(w, "trace_span", "scoped source rows", "bounded-explicit-projection")
	p.Coverage = core.Coverage{Numerator: len(rows)}
	if sampled {
		p.State, p.Sampled = core.StatePartial, true
		reason := "row_cap"
		p.Reason = &reason
	} else {
		n := len(rows)
		p.Coverage.Denominator = &n
	}
	d.Provenance = p
	d.Classification = r.classify(ctx, d.ProjectID, session)
	return d, nil
}
