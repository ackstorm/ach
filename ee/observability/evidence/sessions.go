// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"fmt"
	"slices"
	"time"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// A trace is one gateway request; the unit of work is the client's session,
// carried on the GENERATION rows. Sessions group the caller's own generations
// by (project, session_id); a generation without a session stays a call only.

// SessionSorts maps each sortable session column to its SQL expression over
// the grouped rows. Only these literals ever reach the ORDER BY.
var SessionSorts = map[string]string{
	"started_at": "started_at", "last_activity_at": "last_activity_at", "duration": "duration_ms",
	"turns": "turns", "tokens": "input_tokens + output_tokens", "cost": "cost",
}

// SessionQuery selects one page of the caller's sessions. End is exclusive.
type SessionQuery struct {
	Window core.Window
	Limit  int
	Cursor int64
	Sort   string // a SessionSorts key; "" == started_at
	Asc    bool
}

// SessionSummary is one (project, session) row.
type SessionSummary struct {
	SessionID      string   `json:"session_id"`
	ProjectID      string   `json:"project_id"`
	StartedAt      *string  `json:"started_at"`
	LastActivityAt *string  `json:"last_activity_at"`
	DurationMS     int      `json:"duration_ms"`
	Turns          int      `json:"turns"`
	Generations    int      `json:"generations"`
	InputTokens    *int     `json:"input_tokens"`
	OutputTokens   *int     `json:"output_tokens"`
	Cost           *float64 `json:"cost"`
	Models         []string `json:"models"`
	Status         string   `json:"status"`
}

// SessionPage is the list response; its JSON shape is sessions.schema.json.
type SessionPage struct {
	Window            core.Window      `json:"window"`
	Sessions          []SessionSummary `json:"sessions"`
	NextCursor        *int64           `json:"next_cursor"`
	TotalCount        *int             `json:"total_count"`
	SessionlessTraces *int             `json:"sessionless_traces"`
	Provenance        core.Provenance  `json:"provenance"`
}

// SessionTurn is one trace (one gateway request) of a session.
type SessionTurn struct {
	TraceID      string   `json:"trace_id"`
	StartedAt    *string  `json:"started_at"`
	EndedAt      *string  `json:"ended_at"`
	Model        *string  `json:"model"`
	InputTokens  *int     `json:"input_tokens"`
	OutputTokens *int     `json:"output_tokens"`
	Cost         *float64 `json:"cost"`
	DurationMS   int      `json:"duration_ms"`
	Status       string   `json:"status"`
	// Tools the model asked for in this turn, in order (first 20); the client
	// runs them before the next turn.
	Tools []string `json:"tools"`
}

// SessionDetail is the detail response; its JSON shape is session-detail.schema.json.
type SessionDetail struct {
	SessionID      string              `json:"session_id"`
	ProjectID      string              `json:"project_id"`
	StartedAt      *string             `json:"started_at"`
	LastActivityAt *string             `json:"last_activity_at"`
	DurationMS     int                 `json:"duration_ms"`
	Turns          int                 `json:"turns"`
	Generations    int                 `json:"generations"`
	InputTokens    *int                `json:"input_tokens"`
	OutputTokens   *int                `json:"output_tokens"`
	Cost           *float64            `json:"cost"`
	Status         string              `json:"status"`
	Classification TraceClassification `json:"classification"`
	TurnsList      []SessionTurn       `json:"turns_list"`
	TurnsTruncated bool                `json:"turns_truncated"`
	Provenance     core.Provenance     `json:"provenance"`
}

// genSource is the caller's own generations in the window; the gateway does
// not observe task success, so a session without an ERROR is unknown.
const genSource = `
FROM default.events_full AS source FINAL
WHERE is_deleted = 0 AND (__SCOPE__)
  AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
  AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
  AND type = 'GENERATION'`

const genUsage = `
       sumIf(usage_details['input'], mapContains(usage_details, 'input')) AS input_tokens,
       sumIf(usage_details['output'], mapContains(usage_details, 'output')) AS output_tokens,
       sumIf(toFloat64(cost_details['total']), mapContains(cost_details, 'total')) AS cost,
       countIf(mapContains(usage_details, 'input') OR mapContains(usage_details, 'output')) AS measured,
       if(countIf(level = 'ERROR') > 0, 'error', 'unknown') AS status`

const sessionGroupSQL = `
SELECT project_id, ifNull(session_id, '') AS session_id,
       formatDateTime(min(source.start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS started_at,
       formatDateTime(max(ifNull(source.end_time, source.start_time)), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS last_activity_at,
       dateDiff('millisecond', min(source.start_time), max(ifNull(source.end_time, source.start_time))) AS duration_ms,
       uniqExact(trace_id) AS turns, count() AS generations,
       topKIf(3)(provided_model_name, provided_model_name != '') AS models,` + genUsage + genSource + `
  AND ifNull(session_id, '') != ''
GROUP BY project_id, session_id
`

// sessionlessSQL counts the caller's traces that carry no session at all.
const sessionlessSQL = `
SELECT countIf(sid = '') AS sessionless FROM (
  SELECT project_id, trace_id, max(ifNull(session_id, '')) AS sid
  FROM default.events_full AS source FINAL
  WHERE is_deleted = 0 AND (__SCOPE__)
    AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
    AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
    AND type IN ('SPAN', 'GENERATION', 'TOOL') AND trace_id != ''
  GROUP BY project_id, trace_id
)
FORMAT JSON`

const sessionTurnsSQL = `
SELECT project_id, ifNull(session_id, '') AS session_id, trace_id,
       formatDateTime(min(source.start_time), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS started_at,
       formatDateTime(max(ifNull(source.end_time, source.start_time)), '%Y-%m-%dT%H:%i:%S.%fZ', 'UTC') AS ended_at,
       dateDiff('millisecond', min(source.start_time), max(ifNull(source.end_time, source.start_time))) AS duration_ms,
       count() AS generations,
       anyIf(provided_model_name, provided_model_name != '') AS model,
       arraySlice(groupArrayArray(tool_call_names), 1, 20) AS tools,` + genUsage + genSource + `
  AND ifNull(session_id, '') = {session:String}
GROUP BY project_id, session_id, trace_id
ORDER BY started_at ASC, trace_id
LIMIT {limit:UInt32}
FORMAT JSON`

type sessionRow struct {
	ProjectID      string   `json:"project_id"`
	SessionID      string   `json:"session_id"`
	StartedAt      *string  `json:"started_at"`
	LastActivityAt *string  `json:"last_activity_at"`
	DurationMS     float64  `json:"duration_ms"`
	Turns          float64  `json:"turns"`
	Generations    float64  `json:"generations"`
	Models         []string `json:"models"`
	InputTokens    float64  `json:"input_tokens"`
	OutputTokens   float64  `json:"output_tokens"`
	Cost           float64  `json:"cost"`
	Measured       float64  `json:"measured"`
	Status         string   `json:"status"`
	TotalCount     float64  `json:"total_count"`
}

type turnRow struct {
	ProjectID    string   `json:"project_id"`
	SessionID    string   `json:"session_id"`
	TraceID      string   `json:"trace_id"`
	StartedAt    *string  `json:"started_at"`
	EndedAt      *string  `json:"ended_at"`
	DurationMS   float64  `json:"duration_ms"`
	Generations  float64  `json:"generations"`
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
	InputTokens  float64  `json:"input_tokens"`
	OutputTokens float64  `json:"output_tokens"`
	Cost         float64  `json:"cost"`
	Measured     float64  `json:"measured"`
	Status       string   `json:"status"`
}

// usage is null, not 0, when no generation reported usage.
func usage(measured, in, out, cost float64) (*int, *int, *float64) {
	if measured == 0 {
		return nil, nil, nil
	}
	i, o := int(in), int(out)
	return &i, &o, &cost
}

// Sessions pages the caller's sessions over the whole window, like List.
func (r *Reader) Sessions(ctx context.Context, s core.Scope, q SessionQuery) (SessionPage, error) {
	if !s.Valid() {
		return SessionPage{}, core.ErrNoScope
	}
	sortBy := q.Sort
	if sortBy == "" {
		sortBy = "started_at"
	}
	order, sortable := SessionSorts[sortBy]
	if q.Limit < 1 || q.Limit > maxLimit || q.Cursor < 0 || !sortable || !q.Window.Valid() {
		return SessionPage{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return SessionPage{}, core.ErrProjectNotAllowed
	}
	unavailable := SessionPage{Window: q.Window, Sessions: []SessionSummary{}, Provenance: core.UnavailableProvenance(q.Window, "session", core.SourceUnavailable)}

	params := map[string]string{"limit": fmt.Sprint(q.Limit + 1), "offset": fmt.Sprint(q.Cursor)}
	core.WindowParams(q.Window, params)
	group := core.Scoped(sessionGroupSQL, r.Projects(), s, params)
	sql := "SELECT *, count() OVER () AS total_count FROM (" + group + ") " +
		"ORDER BY " + order + direction(q.Asc) + ", project_id, session_id " +
		"LIMIT {limit:UInt32} OFFSET {offset:UInt64} FORMAT JSON"
	var data []sessionRow
	if err := r.CH().Query(ctx, sql, params, maxLimit+1, &data); err != nil || len(data) > q.Limit+1 {
		return unavailable, nil
	}
	total := 0
	if len(data) > 0 {
		total = int(data[0].TotalCount)
	} else if q.Cursor > 0 { // an exhausted offset has no row to carry the window count
		var c []struct {
			Total float64 `json:"total_count"`
		}
		if err := r.CH().Query(ctx, "SELECT count() AS total_count FROM ("+group+") FORMAT JSON", params, 1, &c); err != nil || len(c) != 1 {
			return unavailable, nil
		}
		total = int(c[0].Total)
	}
	n := min(len(data), q.Limit)
	sessions := make([]SessionSummary, n)
	for i, row := range data[:n] {
		if !slices.Contains(r.Projects(), row.ProjectID) || row.SessionID == "" {
			return SessionPage{Window: q.Window, Sessions: []SessionSummary{}, Provenance: core.UnavailableProvenance(q.Window, "session", "scope_mismatch")}, nil
		}
		in, out, cost := usage(row.Measured, row.InputTokens, row.OutputTokens, row.Cost)
		sessions[i] = SessionSummary{
			SessionID: row.SessionID, ProjectID: row.ProjectID, StartedAt: row.StartedAt, LastActivityAt: row.LastActivityAt,
			DurationMS: int(row.DurationMS), Turns: int(row.Turns), Generations: int(row.Generations),
			InputTokens: in, OutputTokens: out, Cost: cost, Models: append([]string{}, row.Models...), Status: row.Status,
		}
	}
	p := core.NewProvenance(q.Window, "session", "all scoped sessions in the requested window", "full-window-final-session-page")
	p.Coverage = core.Coverage{Numerator: n, Denominator: &total}
	page := SessionPage{Window: q.Window, Sessions: sessions, TotalCount: &total, Provenance: p}
	if len(data) > q.Limit {
		next := q.Cursor + int64(q.Limit)
		page.NextCursor = &next
	}

	// A failed count leaves the page usable: null is "unknown", never 0.
	params = map[string]string{}
	core.WindowParams(q.Window, params)
	var sl []struct {
		Sessionless float64 `json:"sessionless"`
	}
	if err := r.CH().Query(ctx, core.Scoped(sessionlessSQL, r.Projects(), s, params), params, 1, &sl); err == nil && len(sl) == 1 {
		v := int(sl[0].Sessionless)
		page.SessionlessTraces = &v
	}
	return page, nil
}

// Session returns one of the caller's sessions, one row per turn (trace).
// project narrows to one allow-listed project, validated before any query.
func (r *Reader) Session(ctx context.Context, s core.Scope, sessionID, project string, w core.Window) (SessionDetail, error) {
	if !s.Valid() {
		return SessionDetail{}, core.ErrNoScope
	}
	if sessionID == "" || len(sessionID) > 200 || !w.Valid() {
		return SessionDetail{}, core.ErrInvalidArgument
	}
	projects := r.Projects()
	if project != "" {
		if !slices.Contains(r.Projects(), project) {
			return SessionDetail{}, core.ErrProjectNotAllowed
		}
		projects = []string{project}
	}
	if len(projects) == 0 {
		return SessionDetail{}, core.ErrProjectNotAllowed
	}
	params := map[string]string{"session": sessionID, "limit": fmt.Sprint(detailRowCap + 1)}
	core.WindowParams(w, params)
	var rows []turnRow
	if err := r.CH().Query(ctx, core.Scoped(sessionTurnsSQL, projects, s, params), params, detailRowCap+1, &rows); err != nil {
		return SessionDetail{}, core.ErrSourceUnavailable
	}
	for _, row := range rows {
		if !slices.Contains(projects, row.ProjectID) || row.SessionID != sessionID {
			return SessionDetail{}, core.ErrScopeMismatch
		}
	}
	if len(rows) == 0 {
		return SessionDetail{}, core.ErrNotFound
	}
	for _, row := range rows {
		if row.ProjectID != rows[0].ProjectID {
			return SessionDetail{}, core.ErrProjectRequired
		}
	}
	truncated := len(rows) > detailRowCap
	if truncated {
		rows = rows[:detailRowCap]
	}

	d := SessionDetail{SessionID: sessionID, ProjectID: rows[0].ProjectID, Status: core.StatusUnknown, Turns: len(rows), TurnsList: make([]SessionTurn, len(rows)), TurnsTruncated: truncated}
	var measured, in, out, cost float64
	for i, row := range rows {
		tin, tout, tcost := usage(row.Measured, row.InputTokens, row.OutputTokens, row.Cost)
		d.TurnsList[i] = SessionTurn{
			TraceID: row.TraceID, StartedAt: row.StartedAt, EndedAt: row.EndedAt, Model: nilIfEmpty(row.Model),
			InputTokens: tin, OutputTokens: tout, Cost: tcost, DurationMS: int(row.DurationMS), Status: row.Status,
			Tools: append([]string{}, row.Tools...),
		}
		d.Generations += int(row.Generations)
		measured, in, out, cost = measured+row.Measured, in+row.InputTokens, out+row.OutputTokens, cost+row.Cost
		if row.Status == statusError {
			d.Status = statusError
		}
		if row.EndedAt != nil && (d.LastActivityAt == nil || *row.EndedAt > *d.LastActivityAt) {
			d.LastActivityAt = row.EndedAt
		}
	}
	d.StartedAt = rows[0].StartedAt
	d.InputTokens, d.OutputTokens, d.Cost = usage(measured, in, out, cost)
	if d.StartedAt != nil && d.LastActivityAt != nil {
		t0, err0 := time.Parse(time.RFC3339Nano, *d.StartedAt)
		t1, err1 := time.Parse(time.RFC3339Nano, *d.LastActivityAt)
		if err0 == nil && err1 == nil {
			d.DurationMS = int(t1.Sub(t0).Milliseconds())
		}
	}
	p := core.NewProvenance(w, "session_turn", "scoped source rows", "bounded-explicit-projection")
	p.Coverage = core.Coverage{Numerator: len(rows)}
	if truncated {
		p.State, p.Sampled = core.StatePartial, true
		reason := "row_cap"
		p.Reason = &reason
	} else {
		n := len(rows)
		p.Coverage.Denominator = &n
	}
	d.Provenance = p
	d.Classification = r.classify(ctx, d.ProjectID, sessionID)
	return d, nil
}
