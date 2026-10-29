// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeCH records every query and answers by the first matching fragment. An
// answer may be a func of the query's parameters; an error answer fails the query.
type fakeCH struct {
	sqls    []string
	params  []map[string]string
	answers map[string]any
}

func (f *fakeCH) Query(_ context.Context, sql string, params map[string]string, _ int, out any) error {
	f.sqls = append(f.sqls, sql)
	f.params = append(f.params, params)
	for frag, v := range f.answers {
		if strings.Contains(sql, frag) {
			if fn, ok := v.(func(map[string]string) any); ok {
				v = fn(params)
			}
			if err, ok := v.(error); ok {
				return err
			}
			raw, _ := json.Marshal(v)
			return json.Unmarshal(raw, out)
		}
	}
	return json.Unmarshal([]byte("[]"), out)
}

// Every query must keep the plan §4 rules: FINAL, project, not deleted, generations.
func TestEveryQueryKeepsTheReadRules(t *testing.T) {
	ch := &fakeCH{answers: map[string]any{
		"GROUP BY session_id": []map[string]any{{"session_id": "s1"}},
		"tool_call_names": []map[string]any{{"span_id": "sp", "trace_id": "tr", "start_time": "2026-10-27 10:00:00.000000",
			"input_length": 5000, "tool_call_names": []string{}, "tool_calls": []string{}, "user_id": "u@x", "client": ""}},
		"SELECT input": []map[string]any{{"input": `[{"role":"user","content":"hello"}]`}},
	}}
	r := Reader{CH: ch, Project: "default"}
	if _, err := r.IdleSessions(context.Background(), Builtin.Version, time.Now().Add(-time.Hour), time.Minute, "", Cursor{}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if len(ch.sqls) != 3 {
		t.Fatalf("queries = %d, want 3", len(ch.sqls))
	}
	for _, q := range ch.sqls {
		for _, must := range []string{"FROM default.events_full FINAL", "project_id = {project:String}", "is_deleted = 0", "type = 'GENERATION'"} {
			if !strings.Contains(q, must) {
				t.Errorf("query lacks %q:\n%s", must, q)
			}
		}
		if regexp.MustCompile(`length\(input\)`).MatchString(q) {
			t.Errorf("length(input) materialises the column (2 GB limit); use input_length:\n%s", q)
		}
	}
	if !strings.Contains(ch.sqls[2], "span_id = {span:String}") || !strings.Contains(ch.sqls[2], "start_time = {start:DateTime64(6)}") {
		t.Error("input must be read by the full sorting key, one row")
	}
}

func TestHistoryCallSkipsTinyCallsAndTakesTheLargestRecent(t *testing.T) {
	calls := []call{{SpanID: "a", InputLen: 5000}, {SpanID: "b", InputLen: 900000}, {SpanID: "c", InputLen: 38}}
	if got := historyCall(calls); got == nil || got.SpanID != "b" {
		t.Errorf("got %v, want b (c is a 38-char title call)", got)
	}
	if historyCall([]call{{InputLen: 38}}) != nil {
		t.Error("no call above the floor = no history")
	}
	// An input too large to decode in the pod's memory would OOM-kill it on every
	// restart (the same session is read first again): take the largest that fits.
	big := []call{{SpanID: "a", InputLen: 5000}, {SpanID: "b", InputLen: maxHistoryChars + 1}}
	if got := historyCall(big); got == nil || got.SpanID != "a" {
		t.Errorf("got %v, want a (b is over maxHistoryChars)", got)
	}
}

// A session past maxCallsPerSession must still be read (its latest calls), not
// throw on every attempt; the rows come newest first and are put back in order.
func TestSessionReadsTheLatestCallsInOrder(t *testing.T) {
	ch := &fakeCH{answers: map[string]any{"tool_call_names": []map[string]any{
		{"span_id": "2", "start_time": "2026-10-27 10:00:02.000000", "tool_call_names": []string{"late"}, "tool_calls": []string{"{}"}, "user_id": "u"},
		{"span_id": "1", "start_time": "2026-10-27 10:00:01.000000", "tool_call_names": []string{"early"}, "tool_calls": []string{"{}"}, "user_id": "u"},
	}}}
	s, err := Reader{CH: ch, Project: "default"}.Session(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ch.sqls[0], "ORDER BY start_time DESC LIMIT {max_calls:UInt32}") {
		t.Errorf("session read must take the latest calls:\n%s", ch.sqls[0])
	}
	if len(s.ToolCalls) != 2 || s.ToolCalls[0].Name != "early" {
		t.Errorf("tool calls out of order: %+v", s.ToolCalls)
	}
}

func TestTrivialAndBuildText(t *testing.T) {
	hi := Session{Generations: 3, Messages: []Message{{Role: "user", Parts: []Part{{Type: "text", Content: "Hi"}}}}}
	if !IsTrivial(hi) {
		t.Error("Hi is trivial")
	}
	long := Session{Generations: 1, Messages: []Message{{Role: "user", Parts: []Part{{Type: "text", Content: strings.Repeat("x", 5000)}}}}}
	if !IsTrivial(long) {
		t.Error("a single call is trivial whatever its size")
	}
	hi.ToolCalls = []Part{{Type: "tool_call", Name: "shell", Arguments: map[string]any{"command": "ls"}}}
	if IsTrivial(hi) {
		t.Error("a short ask followed by work is not trivial")
	}
	if txt := BuildText(hi); !strings.Contains(txt, "user: Hi") || !strings.Contains(txt, `[tool shell: {"command":"ls"}]`) {
		t.Errorf("text = %q", txt)
	}
	many := Session{}
	for range 2000 {
		many.ToolCalls = append(many.ToolCalls, Part{Type: "tool_call", Name: "shell", Arguments: strings.Repeat("x", 500)})
	}
	if n := len(BuildText(many)); n > textBudget+200 {
		t.Errorf("text %d chars, budget %d", n, textBudget)
	}
}

// The built-in taxonomy is what production scores were labelled with: its
// prompt must not change without a new version.
func TestBuiltinPromptIsV3(t *testing.T) {
	want, err := os.ReadFile("testdata/prompt-v3.txt")
	if err != nil {
		t.Fatal(err)
	}
	if Builtin.Prompt() != string(want) {
		t.Error("built-in prompt differs from testdata/prompt-v3.txt")
	}
}

func TestPromptListsEveryCategoryButTrivial(t *testing.T) {
	for _, c := range Builtin.Categories {
		if in := strings.Contains(Builtin.Prompt(), "- "+c.ID+":"); in == (c.ID == Trivial) {
			t.Errorf("%s offered to the model = %v", c.ID, in)
		}
	}
	n := len(Builtin.Categories)
	if Builtin.Version != "v3" || Builtin.Index(Other) != n-2 || Builtin.Index(Trivial) != n-1 || Builtin.Index("nope") != -1 {
		t.Errorf("version %q, other %d, trivial %d of %d", Builtin.Version, Builtin.Index(Other), Builtin.Index(Trivial), n)
	}
}

func modelServer(t *testing.T, answers ...string) (Model, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "pasted_message") {
			w.WriteHeader(400)
			return
		}
		a := answers[min(calls, len(answers)-1)]
		calls++
		w.Header().Set("x-litellm-response-cost", "0.004")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": a}}}})
	}))
	t.Cleanup(srv.Close)
	return Model{URL: srv.URL + "/v1", Key: "k", Name: "m", HC: srv.Client()}, &calls
}

func TestClassifyRetriesOnceThenFails(t *testing.T) {
	m, calls := modelServer(t, "not json", `{"category":"technology.infrastructure_ops","confidence":0.9}`)
	lb, err := m.Classify(context.Background(), Builtin, "x")
	if err != nil || lb.Category != "technology.infrastructure_ops" || lb.Cost != 0.004 || *calls != 2 {
		t.Fatalf("lb=%+v err=%v calls=%d", lb, err, *calls)
	}
	m, _ = modelServer(t, `{"category":"trivial"}`, `{"category":"made.up"}`)
	if _, err := m.Classify(context.Background(), Builtin, "x"); err == nil {
		t.Error("trivial and unknown ids are not answers the model may give")
	}
}

func TestScoreIsNumericWithMetadataAndDeterministicID(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "pk" || p != "sk" || r.URL.Path != "/api/public/scores" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	l := Langfuse{URL: srv.URL, PublicKey: "pk", SecretKey: "sk", HC: srv.Client()}
	if err := l.Write(context.Background(), Builtin, "s1", Label{Category: "sales.proposals", Confidence: 0.8}, "m"); err != nil {
		t.Fatal(err)
	}
	md, _ := got["metadata"].(map[string]any)
	if got["dataType"] != "NUMERIC" || got["value"] != float64(Builtin.Index("sales.proposals")) || got["sessionId"] != "s1" ||
		got["id"] != ScoreID(Builtin.Version, "s1") || md["category"] != "sales.proposals" || md["domain"] != "sales" || md["taxonomy"] != Builtin.Version {
		t.Errorf("score = %v", got)
	}
	if ScoreID(Builtin.Version, "s1") == ScoreID(Builtin.Version, "s2") {
		t.Error("ids must differ per session")
	}
	if ScoreID("v3", "s1") == ScoreID("custom-x", "s1") {
		t.Error("ids must differ per taxonomy")
	}
}

type memWriter struct{ got map[string]string }

func (w *memWriter) Write(_ context.Context, _ Taxonomy, id string, lb Label, _ string) error {
	w.got[id] = lb.Category
	return nil
}

func TestPassLabelsOnceAndHonoursOnlyUser(t *testing.T) {
	ch := &fakeCH{answers: map[string]any{
		"GROUP BY session_id": []map[string]any{{"session_id": "s1"}},
		"tool_call_names": []map[string]any{{"span_id": "sp", "trace_id": "tr", "start_time": "2026-10-27 10:00:00.000000",
			"input_length": 50, "tool_call_names": []string{}, "tool_calls": []string{}, "user_id": "me@x", "client": "client:x"}},
	}}
	m, calls := modelServer(t, `{"category":"other"}`)
	w := &memWriter{got: map[string]string{}}
	j := &Job{Tax: Builtin, Reader: Reader{CH: ch, Project: "default"}, Model: m, Writer: w, Batch: 5, OnlyUser: "me@x",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	j.Pass(context.Background())
	j.Pass(context.Background())
	if w.got["s1"] != Trivial || *calls != 0 {
		t.Errorf("trivial session: label %q, model calls %d (want trivial, 0)", w.got["s1"], *calls)
	}
	// OnlyUser filters in SQL, so other users' sessions are never read.
	ch.sqls, ch.params = nil, nil
	j2 := &Job{Tax: Builtin, Reader: Reader{CH: ch, Project: "default"}, Model: m, Writer: &memWriter{got: map[string]string{}}, Batch: 5,
		OnlyUser: "someone@else", Log: j.Log}
	j2.Pass(context.Background())
	if !strings.Contains(ch.sqls[0], "user_id = {user:String}") || ch.params[0]["user"] != "someone@else" {
		t.Errorf("OnlyUser must be a SQL filter, got params %v:\n%s", ch.params[0], ch.sqls[0])
	}
}

// A session already scored for this taxonomy is never listed again: restarts
// and resumed sessions must not pay for a second model call.
func TestIdleSessionsSkipsSessionsAlreadyScored(t *testing.T) {
	ch := &fakeCH{}
	if _, err := (Reader{CH: ch, Project: "default"}).IdleSessions(context.Background(), "custom-abc",
		time.Now().Add(-time.Hour), time.Minute, "", Cursor{}, 10); err != nil {
		t.Fatal(err)
	}
	q, p := ch.sqls[0], ch.params[0]
	for _, must := range []string{"session_id NOT IN (SELECT session_id FROM default.scores FINAL",
		"name = {score:String}", "metadata['taxonomy'] = {taxonomy:String}",
		// trivial costs no model call and a resumed session may become real work
		"metadata['category'] != {trivial:String}"} {
		if !strings.Contains(q, must) {
			t.Errorf("query lacks %q:\n%s", must, q)
		}
	}
	if p["score"] != ScoreName || p["taxonomy"] != "custom-abc" || p["trivial"] != Trivial {
		t.Errorf("params = %v", p)
	}
}

// idleAfter answers IdleSessions like ClickHouse: sessions after the cursor,
// oldest first, up to the limit. Session i is idle since minute i.
func idleAfter(ids []string) func(map[string]string) any {
	return func(p map[string]string) any {
		var rows []map[string]any
		limit, _ := strconv.Atoi(p["limit"])
		for i, id := range ids {
			last := fmt.Sprintf("2026-10-27 10:%02d:00.000000", i)
			if last < p["after_last"] || (last == p["after_last"] && id <= p["after_id"]) {
				continue
			}
			if len(rows) < limit {
				rows = append(rows, map[string]any{"session_id": id, "last": last})
			}
		}
		return rows
	}
}

// Labelled sessions must not fill the window: with more idle sessions than one
// lookup returns, every pass still makes progress, and a session that keeps
// failing is given up after a few passes instead of blocking the rest.
func TestPassAdvancesPastLabelledAndPoisonSessions(t *testing.T) {
	ids := make([]string, 12)
	for i := range ids {
		ids[i] = fmt.Sprintf("s%02d", i)
	}
	ch := &fakeCH{answers: map[string]any{
		"GROUP BY session_id": idleAfter(ids),
		"tool_call_names": func(p map[string]string) any {
			if p["session"] == "s01" {
				return errors.New("read failed")
			}
			return []map[string]any{{"span_id": "sp", "trace_id": "tr", "start_time": "2026-10-27 10:00:00.000000",
				"input_length": 50, "tool_call_names": []string{}, "tool_calls": []string{}, "user_id": "me@x"}}
		},
	}}
	m, _ := modelServer(t, `{"category":"other"}`)
	w := &memWriter{got: map[string]string{}}
	j := &Job{Tax: Builtin, Reader: Reader{CH: ch, Project: "default"}, Model: m, Writer: w, Batch: 2,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for range 10 {
		j.Pass(context.Background())
	}
	for _, id := range ids {
		if _, ok := w.got[id]; ok == (id == "s01") {
			t.Errorf("%s labelled = %v", id, ok)
		}
	}
	if j.after.ID != "s11" {
		t.Errorf("cursor = %+v, want past s11", j.after)
	}
}

func writeTaxonomy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "taxonomy.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const customYAML = `rules:
  - Classify by what was done for the client, not by the client.
categories:
  - id: operations.fleet_planning
    fits: Routes, loads, delivery schedules.
    notFits: Planning software -> technology.software_engineering.
  - id: technology.software_engineering
    fits: Code and scripts.
`

func TestLoadBuildsTheFixedPromptAroundCustomCategories(t *testing.T) {
	tax, err := Load(writeTaxonomy(t, customYAML))
	if err != nil {
		t.Fatal(err)
	}
	p := tax.Prompt()
	for _, must := range []string{promptIntro,
		"4. Classify by what was done for the client, not by the client.\n",
		"- operations.fleet_planning: Routes, loads, delivery schedules. Does not fit: Planning software -> technology.software_engineering.\n",
		"- technology.software_engineering: Code and scripts.\n",
		"- other: ", promptOutput} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt lacks %q", must)
		}
	}
	if strings.Contains(p, "- trivial:") || strings.Contains(p, "sales.proposals") {
		t.Error("trivial or a built-in category leaked into a custom prompt")
	}
	if !strings.HasPrefix(tax.Version, "custom-") || tax.Index("operations.fleet_planning") != 0 ||
		tax.Index(Other) != 2 || tax.Index(Trivial) != 3 {
		t.Errorf("version %q, indexes %d %d %d", tax.Version,
			tax.Index("operations.fleet_planning"), tax.Index(Other), tax.Index(Trivial))
	}
	again, err := Load(writeTaxonomy(t, "# a comment\n"+customYAML))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := Load(writeTaxonomy(t, strings.Replace(customYAML, "Code and scripts.", "Code.", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != tax.Version || edited.Version == tax.Version {
		t.Errorf("version must follow the prompt: same %q/%q, edited %q", tax.Version, again.Version, edited.Version)
	}
}

func TestLoadFlattensMultiLineText(t *testing.T) {
	folded, err := Load(writeTaxonomy(t, `rules:
  - |
    Classify by what was done
    for the client.
categories:
  - id: a.b
    fits: >
      Routes and
      loads.
`))
	if err != nil {
		t.Fatal(err)
	}
	inline, err := Load(writeTaxonomy(t,
		"rules: [Classify by what was done for the client.]\ncategories:\n  - {id: a.b, fits: Routes and loads.}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := folded.Prompt()
	for _, must := range []string{"4. Classify by what was done for the client.\n", "- a.b: Routes and loads.\n"} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt lacks %q", must)
		}
	}
	if folded.Version != inline.Version {
		t.Errorf("YAML style changed the version: %q vs %q", folded.Version, inline.Version)
	}
}

func TestLoadRejectsBadTaxonomies(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"no categories": {"rules: [x]\n", "no categories"},
		"bad id":        {"categories:\n  - {id: Fleet, fits: x}\n", "must be domain.name"},
		"reserved id":   {"categories:\n  - {id: other, fits: x}\n", "added by the classifier"},
		"trivial id":    {"categories:\n  - {id: trivial, fits: x}\n", "added by the classifier"},
		"duplicate":     {"categories:\n  - {id: a.b, fits: x}\n  - {id: a.b, fits: y}\n", "duplicate id"},
		"empty fits":    {"categories:\n  - {id: a.b, fits: ' '}\n", "has no fits"},
		"unknown field": {"categories:\n  - {id: a.b, fits: x, colour: y}\n", "unknown field"},
		"empty rule":    {"rules: ['']\ncategories:\n  - {id: a.b, fits: x}\n", "empty rule"},
	} {
		_, err := Load(writeTaxonomy(t, tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file: accepted")
	}
}
