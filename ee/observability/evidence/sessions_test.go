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

func sessionRowJSON(project, session string, total int) string {
	return fmt.Sprintf(`{"project_id":%q,"session_id":%q,"started_at":"2026-10-02T10:00:00.000000Z","last_activity_at":"2026-10-02T12:00:00.000000Z","duration_ms":7200000,"turns":253,"generations":253,"models":["claude-x"],"input_tokens":1000,"output_tokens":20,"cost":0.5,"measured":253,"status":"unknown","total_count":%d}`, project, session, total)
}

func turnRowJSON(project, session, trace, start string) string {
	return fmt.Sprintf(`{"project_id":%q,"session_id":%q,"trace_id":%q,"started_at":%q,"ended_at":%q,"duration_ms":3000,"generations":1,"model":"claude-x","tools":["Read","Bash"],"input_tokens":10,"output_tokens":2,"cost":0.1,"measured":1,"status":"unknown"}`, project, session, trace, start, start)
}

func readSessions(t *testing.T, q SessionQuery, bodies ...string) (SessionPage, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	p, err := NewReader(core.NewReader(ch, []string{"p1"})).Sessions(context.Background(), scope(t, "a@x"), q)
	if err != nil {
		t.Fatal(err)
	}
	return p, calls
}

func sessionQuery() SessionQuery { return SessionQuery{Window: testWindow(), Limit: 25} }

func TestSessionsAreTheCallersOwnGenerations(t *testing.T) {
	p, calls := readSessions(t, sessionQuery(), page(sessionRowJSON("p1", "s-1", 1)), page(`{"sessionless":86}`))
	if len(p.Sessions) != 1 || p.Sessions[0].Turns != 253 || *p.TotalCount != 1 || *p.SessionlessTraces != 86 || p.Provenance.State != "available" {
		t.Fatalf("page = %+v", p)
	}
	for i, c := range *calls {
		if !strings.Contains(c.Body, "(project_id={project0:String} AND user_id={subject0:String})") || strings.Contains(c.Body, "__") {
			t.Errorf("call %d is not scoped to the caller:\n%s", i, c.Body)
		}
		if c.Q.Get("param_subject0") != "a@x" {
			t.Errorf("call %d subject = %q", i, c.Q.Get("param_subject0"))
		}
	}
	if !strings.Contains((*calls)[0].Body, "type = 'GENERATION'") || !strings.Contains((*calls)[0].Body, "ifNull(session_id, '') != ''") {
		t.Error("sessions must group the caller's generations that carry a session")
	}
}

func TestSessionsSortIsAnAllowList(t *testing.T) {
	ch, calls := scriptedCH(t)
	r := NewReader(core.NewReader(ch, []string{"p1"}))
	for _, bad := range []string{"model", "1; DROP TABLE x", "status"} {
		q := sessionQuery()
		q.Sort = bad
		if _, err := r.Sessions(context.Background(), scope(t, "a@x"), q); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("sort %q: err = %v", bad, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a rejected sort issued %d queries", len(*calls))
	}
	q := sessionQuery()
	q.Sort, q.Asc = "tokens", true
	_, calls = readSessions(t, q, page())
	if !strings.Contains((*calls)[0].Body, "ORDER BY input_tokens + output_tokens ASC, project_id, session_id") {
		t.Errorf("order not applied:\n%s", (*calls)[0].Body)
	}
}

func TestSessionsFailedSourceIsUnavailableNotEmpty(t *testing.T) {
	p, _ := readSessions(t, sessionQuery(), `{"exception":"boom"}`)
	if p.TotalCount != nil || p.Provenance.State != core.StatePartial || p.Sessions == nil {
		t.Errorf("page = %+v", p)
	}
	// A failed sessionless count keeps the page: null, never 0.
	p, _ = readSessions(t, sessionQuery(), page(sessionRowJSON("p1", "s-1", 1)), `{"exception":"boom"}`)
	if len(p.Sessions) != 1 || p.SessionlessTraces != nil {
		t.Errorf("page = %+v", p)
	}
	// A row from a project outside the allow-list fails the page.
	p, _ = readSessions(t, sessionQuery(), page(sessionRowJSON("other", "s-1", 1)))
	if len(p.Sessions) != 0 || p.Provenance.Reason == nil || *p.Provenance.Reason != "scope_mismatch" {
		t.Errorf("page = %+v", p)
	}
}

func TestSessionDetailTurnsAndTotals(t *testing.T) {
	ch, calls := scriptedCH(t,
		page(turnRowJSON("p1", "s-1", "t1", "2026-10-02T10:00:00.000000Z"), turnRowJSON("p1", "s-1", "t2", "2026-10-02T10:05:00.000000Z")),
		page(`{"category":"technology.software_engineering","confidence":"0.95"}`))
	d, err := NewReader(core.NewReader(ch, []string{"p1"})).Session(context.Background(), scope(t, "a@x"), "s-1", "", testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if got := d.TurnsList[0].Tools; len(got) != 2 || got[0] != "Read" {
		t.Errorf("turn tools = %v", got)
	}
	if d.Turns != 2 || len(d.TurnsList) != 2 || *d.InputTokens != 20 || d.DurationMS != 300000 || d.Classification.State != classified {
		t.Errorf("detail = %+v", d)
	}
	if b := (*calls)[0].Body; !strings.Contains(b, "user_id={subject0:String}") || !strings.Contains(b, "= {session:String}") {
		t.Errorf("turns read is not scoped to the caller and session:\n%s", b)
	}
}

func TestSessionDetailRejectsForeignRowsAndUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want error
	}{
		"unknown":       {page(), core.ErrNotFound},
		"other project": {page(turnRowJSON("other", "s-1", "t1", "x")), core.ErrScopeMismatch},
		"other session": {page(turnRowJSON("p1", "s-2", "t1", "x")), core.ErrScopeMismatch},
		"source down":   {`{"exception":"boom"}`, core.ErrSourceUnavailable},
	} {
		ch, _ := scriptedCH(t, tc.body)
		if _, err := NewReader(core.NewReader(ch, []string{"p1"})).Session(context.Background(), scope(t, "a@x"), "s-1", "", testWindow()); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	ch, calls := scriptedCH(t)
	if _, err := NewReader(core.NewReader(ch, []string{"p1"})).Session(context.Background(), scope(t, "a@x"), "s-1", "p9", testWindow()); !errors.Is(err, core.ErrProjectNotAllowed) || len(*calls) != 0 {
		t.Errorf("foreign project selector: err = %v calls=%d", err, len(*calls))
	}
}

func TestSessionsMatchWireContract(t *testing.T) {
	ch, _ := scriptedCH(t, page(sessionRowJSON("p1", "s-1", 1)), page(`{"sessionless":3}`))
	p, _ := NewReader(core.NewReader(ch, []string{"p1"})).Sessions(context.Background(), scope(t, "a@x"), sessionQuery())
	ch, _ = scriptedCH(t, page(turnRowJSON("p1", "s-1", "t1", "2026-10-02T10:00:00.000000Z")), page(`{"category":"","confidence":""}`))
	d, _ := NewReader(core.NewReader(ch, []string{"p1"})).Session(context.Background(), scope(t, "a@x"), "s-1", "", testWindow())
	unavailable, _ := readSessions(t, sessionQuery(), `{"exception":"boom"}`)
	for name, tc := range map[string]struct {
		schema string
		v      any
	}{
		"sessions": {"sessions", p}, "sessions unavailable": {"sessions", unavailable}, "session": {"session-detail", d},
	} {
		b, _ := json.Marshal(tc.v)
		var v any
		_ = json.Unmarshal(b, &v)
		s, err := jsonschema.NewCompiler().Compile("testdata/contracts/" + tc.schema + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(v); err != nil {
			t.Errorf("%s does not match the contract: %v\n%s", name, err, b)
		}
	}
}
