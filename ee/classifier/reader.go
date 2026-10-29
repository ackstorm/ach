// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Querier is evidence.ClickHouse's Query; an interface so tests can record SQL.
type Querier interface {
	Query(ctx context.Context, sql string, params map[string]string, maxRows int, out any) error
}

// Every query of this package starts here (plan §4). FINAL deduplicates the
// ReplacingMergeTree and also keeps length()/empty() on Array columns off the
// hidden .size0 subcolumn, which a column grant does not cover.
const base = `FROM default.events_full FINAL
WHERE project_id = {project:String} AND is_deleted = 0 AND type = 'GENERATION'`

const (
	maxCallsPerSession = 5000
	minHistoryChars    = 1000 // skips tiny calls (e.g. a client's title generator) without naming a client
	historyWindow      = 5    // the input is the largest among the last calls: the whole history
	// maxHistoryChars keeps the decoded input (body, string and JSON tree, several
	// copies) well inside the pod's 512Mi; a larger input would OOM-kill it on
	// every restart. The largest measured was 22.5 MB.
	maxHistoryChars = 16 << 20
)

// Reader reads sessions through the classifier's own ClickHouse user.
type Reader struct {
	CH      Querier
	Project string
}

type call struct {
	SpanID    string   `json:"span_id"`
	TraceID   string   `json:"trace_id"`
	Start     string   `json:"start_time"`
	End       string   `json:"end_time"`
	InputLen  int      `json:"input_length"`
	ToolNames []string `json:"tool_call_names"`
	ToolCalls []string `json:"tool_calls"`
	UserID    string   `json:"user_id"`
	Client    string   `json:"client"`
}

// Cursor is the (last generation, session id) of the newest session a job has
// finished with; IdleSessions only returns sessions after it.
type Cursor struct{ Last, ID string }

// Idle is one idle session and its last generation, in ClickHouse's own text
// form so it round-trips as a Cursor.
type Idle struct {
	ID   string `json:"session_id"`
	Last string `json:"last"`
}

// IdleSessions lists sessions whose last generation is older than idle, with
// at least one generation since `since`, after the cursor, oldest first. A
// non-empty user keeps only that user's generations. A session that already
// has this taxonomy's score is never listed, so each is labelled once: a
// restart or a resumed session costs no model call. A trivial label is the
// exception: it cost no model call, and a session that resumes may be real work. The scores read needs
// SELECT on default.scores; without it the query fails and nothing is spent.
func (r Reader) IdleSessions(ctx context.Context, taxonomy string, since time.Time, idle time.Duration, user string, after Cursor, limit int) ([]Idle, error) {
	afterLast := cmp.Or(after.Last, "1970-01-01 00:00:00.000000")
	var rows []Idle
	err := r.CH.Query(ctx, `SELECT session_id, max(start_time) AS last_at, toString(last_at) AS last `+base+`
  AND session_id != '' AND user_id != '' AND start_time >= {since:DateTime64(6)}
  AND ({user:String} = '' OR user_id = {user:String})
  AND session_id NOT IN (SELECT session_id FROM default.scores FINAL
    WHERE project_id = {project:String} AND is_deleted = 0 AND name = {score:String}
      AND metadata['taxonomy'] = {taxonomy:String} AND metadata['category'] != {trivial:String}
      AND session_id IS NOT NULL)
GROUP BY session_id HAVING last_at < {idle_before:DateTime64(6)} AND count() >= {min_calls:UInt32}
  AND (last_at, session_id) > ({after_last:DateTime64(6)}, {after_id:String})
ORDER BY last_at, session_id LIMIT {limit:UInt32} FORMAT JSON`, map[string]string{
		"project": r.Project, "since": ts(since), "idle_before": ts(time.Now().Add(-idle)), "limit": strconv.Itoa(limit),
		"min_calls": strconv.Itoa(minCalls), "user": user, "after_last": afterLast, "after_id": after.ID,
		"score": ScoreName, "taxonomy": taxonomy, "trivial": Trivial,
	}, limit, &rows)
	return rows, err
}

// Session reads one session: tool calls of every generation from the parsed
// columns, and the OTel messages of ONE call's input. The input is read by
// the full sorting key (project_id, start_time, trace_id, span_id): FINAL then
// reads one row; filtering by session alone loaded every call's input and
// exceeded the 2 GB limit (measured).
func (r Reader) Session(ctx context.Context, id string) (Session, error) {
	var calls []call
	if err := r.CH.Query(ctx, `SELECT span_id, trace_id, toString(start_time) AS start_time, toString(end_time) AS end_time,
  input_length, tool_call_names, tool_calls, user_id,
  arrayFirst(t -> startsWith(t, 'client:'), tags) AS client `+base+`
  AND session_id = {session:String} ORDER BY start_time DESC LIMIT {max_calls:UInt32} FORMAT JSON`,
		map[string]string{"project": r.Project, "session": id, "max_calls": strconv.Itoa(maxCallsPerSession)},
		maxCallsPerSession, &calls); err != nil {
		return Session{}, err
	}
	slices.Reverse(calls) // the latest maxCallsPerSession, oldest first
	if len(calls) == 0 {
		return Session{}, errors.New("classifier: session not found")
	}
	s := Session{ID: id, UserID: calls[0].UserID, Generations: len(calls)}
	for _, c := range calls {
		if s.Client == "" && c.Client != "" {
			s.Client = strings.TrimPrefix(c.Client, "client:")
		}
		s.ToolCalls = append(s.ToolCalls, ParsedToolCalls(c.ToolNames, c.ToolCalls)...)
	}
	pick := historyCall(calls)
	if pick == nil {
		return s, nil
	}
	var in []struct {
		Input string `json:"input"`
	}
	if err := r.CH.Query(ctx, `SELECT input `+base+`
  AND start_time = {start:DateTime64(6)} AND trace_id = {trace:String} AND span_id = {span:String} LIMIT 1 FORMAT JSON`,
		map[string]string{"project": r.Project, "start": pick.Start, "trace": pick.TraceID, "span": pick.SpanID}, 1, &in); err != nil {
		return Session{}, err
	}
	if len(in) == 1 {
		s.Messages = ToOTel([]byte(in[0].Input))
	}
	return s, nil
}

// historyCall is the call whose input carries the session's history: the
// largest among the last historyWindow calls between minHistoryChars and
// maxHistoryChars.
func historyCall(calls []call) *call {
	var big []call
	for _, c := range calls {
		if c.InputLen > minHistoryChars && c.InputLen <= maxHistoryChars {
			big = append(big, c)
		}
	}
	if len(big) == 0 {
		return nil
	}
	tail := big[max(0, len(big)-historyWindow):]
	best := &tail[0]
	for i := range tail {
		if tail[i].InputLen > best.InputLen {
			best = &tail[i]
		}
	}
	return best
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000000") }
